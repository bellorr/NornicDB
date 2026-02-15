package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"

	"github.com/orneryd/nornicdb/pkg/math/vector"
	"github.com/orneryd/nornicdb/pkg/nornicdb"
	"github.com/orneryd/nornicdb/pkg/search"
	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/orneryd/nornicdb/pkg/util"
)

// =============================================================================
// NornicDB-Specific Handlers (Memory OS for LLMs)
// =============================================================================

// Search Handlers
// =============================================================================

// handleDecay returns memory decay information (NornicDB-specific)
func (s *Server) handleDecay(w http.ResponseWriter, r *http.Request) {
	info := s.db.GetDecayInfo()

	response := map[string]interface{}{
		"enabled":          info.Enabled,
		"archiveThreshold": info.ArchiveThreshold,
		"interval":         info.RecalcInterval.String(),
		"weights": map[string]interface{}{
			"recency":    info.RecencyWeight,
			"frequency":  info.FrequencyWeight,
			"importance": info.ImportanceWeight,
		},
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleEmbedTrigger triggers the embedding worker to process nodes without embeddings.
// Query params:
//   - regenerate=true: Clear all existing embeddings first, then regenerate (async)
func (s *Server) handleEmbedTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeNeo4jError(w, http.StatusMethodNotAllowed, "Neo.ClientError.Request.Invalid", "POST required")
		return
	}

	stats := s.db.EmbedQueueStats()
	if stats == nil {
		s.writeNeo4jError(w, http.StatusServiceUnavailable, "Neo.DatabaseError.General.UnknownError", "Auto-embed not enabled")
		return
	}

	// Check if regenerate=true to clear existing embeddings first
	regenerate := r.URL.Query().Get("regenerate") == "true"

	if regenerate {
		// Return 202 Accepted immediately - clearing happens in background
		response := map[string]interface{}{
			"accepted":   true,
			"regenerate": true,
			"message":    "Regeneration started - clearing embeddings and regenerating in background. Check /nornicdb/embed/stats for progress.",
		}
		s.writeJSON(w, http.StatusAccepted, response)

		// Start background clearing and regeneration
		go func() {
			log.Printf("[EMBED] Starting background regeneration - stopping worker and clearing embeddings...")

			// First, reset the embed worker to stop any in-progress work and clear its state
			if err := s.db.ResetEmbedWorker(); err != nil {
				log.Printf("[EMBED] ⚠️ Failed to reset embed worker: %v", err)
			}

			// Now clear all embeddings
			cleared, err := s.db.ClearAllEmbeddings()
			if err != nil {
				log.Printf("[EMBED] ❌ Failed to clear embeddings: %v", err)
				return
			}
			log.Printf("[EMBED] ✅ Cleared %d embeddings - triggering regeneration", cleared)

			// Trigger embedding worker to regenerate (worker was already restarted by Reset)
			ctx := context.Background()
			if _, err := s.db.EmbedExisting(ctx); err != nil {
				log.Printf("[EMBED] ❌ Failed to trigger embedding worker: %v", err)
				return
			}
			log.Printf("[EMBED] 🚀 Embedding worker triggered for regeneration")
		}()
		return
	}

	// Non-regenerate case: just trigger the worker (fast, synchronous is fine)
	wasRunning := stats.Running

	// Trigger (safe to call even if already running - just wakes up worker)
	_, err := s.db.EmbedExisting(r.Context())
	if err != nil {
		s.writeNeo4jError(w, http.StatusInternalServerError, "Neo.DatabaseError.General.UnknownError", err.Error())
		return
	}

	// Get updated stats
	stats = s.db.EmbedQueueStats()

	var message string
	if wasRunning {
		message = "Embedding worker already running - will continue processing"
	} else {
		message = "Embedding worker triggered - processing nodes in background"
	}

	response := map[string]interface{}{
		"triggered":      true,
		"regenerate":     false,
		"already_active": wasRunning,
		"message":        message,
		"stats":          stats,
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleEmbedStats returns embedding worker statistics.
func (s *Server) handleEmbedStats(w http.ResponseWriter, r *http.Request) {
	stats := s.db.EmbedQueueStats()
	totalEmbeddings := s.db.EmbeddingCount()
	vectorIndexDims := s.db.VectorIndexDimensions()

	if stats == nil {
		response := map[string]interface{}{
			"enabled":                 false,
			"message":                 "Auto-embed not enabled",
			"total_embeddings":        totalEmbeddings,
			"configured_model":        s.config.EmbeddingModel,
			"configured_dimensions":   s.config.EmbeddingDimensions,
			"configured_provider":     s.config.EmbeddingProvider,
			"vector_index_dimensions": vectorIndexDims,
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	response := map[string]interface{}{
		"enabled":                 true,
		"stats":                   stats,
		"total_embeddings":        totalEmbeddings,
		"configured_model":        s.config.EmbeddingModel,
		"configured_dimensions":   s.config.EmbeddingDimensions,
		"configured_provider":     s.config.EmbeddingProvider,
		"vector_index_dimensions": vectorIndexDims,
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleEmbedClear clears all embeddings from nodes (admin only).
// This allows regeneration with a new model or fixing corrupted embeddings.
func (s *Server) handleEmbedClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		s.writeNeo4jError(w, http.StatusMethodNotAllowed, "Neo.ClientError.Request.Invalid", "POST or DELETE required")
		return
	}

	cleared, err := s.db.ClearAllEmbeddings()
	if err != nil {
		s.writeNeo4jError(w, http.StatusInternalServerError, "Neo.DatabaseError.General.UnknownError", err.Error())
		return
	}

	response := map[string]interface{}{
		"success": true,
		"cleared": cleared,
		"message": fmt.Sprintf("Cleared embeddings from %d nodes - use /nornicdb/embed/trigger to regenerate", cleared),
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleSearchRebuild rebuilds search indexes from all nodes in the specified database.
func (s *Server) handleSearchRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeNeo4jError(w, http.StatusMethodNotAllowed, "Neo.ClientError.Request.Invalid", "POST required")
		return
	}

	var req struct {
		Database string `json:"database,omitempty"` // Optional: defaults to default database
	}

	if err := s.readJSON(r, &req); err != nil {
		// If no JSON body, use default database
		req.Database = ""
	}

	// Get database name (default to default database if not specified)
	dbName := req.Database
	if dbName == "" {
		dbName = s.dbManager.DefaultDatabaseName()
	}

	// Per-database RBAC: deny if principal may not access this database (Neo4j-aligned).
	claims := getClaims(r)
	if !s.getDatabaseAccessMode(claims).CanAccessDatabase(dbName) {
		s.writeNeo4jError(w, http.StatusForbidden, "Neo.ClientError.Security.Forbidden",
			fmt.Sprintf("Access to database '%s' is not allowed.", dbName))
		return
	}
	// Rebuild is a write to the database; require ResolvedAccess.Write for this DB.
	if !s.getResolvedAccess(claims, dbName).Write {
		s.writeNeo4jError(w, http.StatusForbidden, "Neo.ClientError.Security.Forbidden",
			fmt.Sprintf("Write on database '%s' is not allowed.", dbName))
		return
	}

	// Get namespaced storage for the specified database
	storageEngine, err := s.dbManager.GetStorage(dbName)
	if err != nil {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("Database '%s' not found", dbName), ErrNotFound)
		return
	}

	// Invalidate cache and rebuild from scratch
	s.db.ResetSearchService(dbName)

	searchSvc, err := s.db.GetOrCreateSearchService(dbName, storageEngine)
	if err != nil {
		s.writeNeo4jError(w, http.StatusInternalServerError, "Neo.DatabaseError.General.UnknownError", err.Error())
		return
	}

	if err := searchSvc.BuildIndexes(r.Context()); err != nil {
		s.writeNeo4jError(w, http.StatusInternalServerError, "Neo.DatabaseError.General.UnknownError", err.Error())
		return
	}

	response := map[string]interface{}{
		"success":  true,
		"database": dbName,
		"message":  fmt.Sprintf("Search indexes rebuilt for database '%s'", dbName),
	}
	s.writeJSON(w, http.StatusOK, response)
}

// getOrCreateSearchService returns a cached search service for the database,
// creating and caching it if it doesn't exist. Search services are namespace-aware
// because they're built from NamespacedEngine which automatically filters nodes.
func (s *Server) getOrCreateSearchService(dbName string, storageEngine storage.Engine) (*search.Service, error) {
	return s.db.GetOrCreateSearchService(dbName, storageEngine)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required", ErrMethodNotAllowed)
		return
	}

	var req struct {
		Database string   `json:"database,omitempty"` // Optional: defaults to default database
		Query    string   `json:"query"`
		Labels   []string `json:"labels,omitempty"`
		Limit    int      `json:"limit,omitempty"`
	}

	if err := s.readJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body", ErrBadRequest)
		return
	}

	if req.Limit <= 0 {
		req.Limit = 10
	}

	// Get database name (default to default database if not specified)
	dbName := req.Database
	if dbName == "" {
		dbName = s.dbManager.DefaultDatabaseName()
	}
	log.Printf("🔍 Search request database=%q query=%q", dbName, req.Query)

	// Per-database RBAC: deny if principal may not access this database (Neo4j-aligned).
	if !s.getDatabaseAccessMode(getClaims(r)).CanAccessDatabase(dbName) {
		s.writeNeo4jError(w, http.StatusForbidden, "Neo.ClientError.Security.Forbidden",
			fmt.Sprintf("Access to database '%s' is not allowed.", dbName))
		return
	}

	// Get namespaced storage for the specified database
	storageEngine, err := s.dbManager.GetStorage(dbName)
	if err != nil {
		log.Printf("⚠️ Search database=%q: %v", dbName, err)
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("Database '%s' not found", dbName), ErrNotFound)
		return
	}

	// Get or create search service for this database
	// Search services are cached per database since indexes are namespace-aware
	// (the storage engine already filters to the correct namespace)
	ctx := r.Context()
	searchSvc, err := s.db.EnsureSearchIndexesBuilt(ctx, dbName, storageEngine)
	if err != nil {
		log.Printf("⚠️ Failed to build search indexes for db %s: %v", dbName, err)
	}
	if searchSvc == nil {
		s.writeError(w, http.StatusServiceUnavailable, "search service unavailable", ErrInternalError)
		return
	}

	// If embeddings are available, chunk long queries by length so vector search
	// remains usable for paragraph-sized inputs (no relying on tokenization errors).
	//
	// For short queries (1 chunk), we preserve the legacy behavior and return the
	// search service response directly.
	const (
		queryChunkSize    = 512
		queryChunkOverlap = 50
		maxQueryChunks    = 32
		outerRRFK         = 60
	)

	queryChunks := util.ChunkText(req.Query, queryChunkSize, queryChunkOverlap)
	if len(queryChunks) > maxQueryChunks {
		queryChunks = queryChunks[:maxQueryChunks]
	}

	var searchResponse *search.SearchResponse
	// Helper to build search options consistently.
	buildOpts := func(q string, limit int) *search.SearchOptions {
		opts := search.GetAdaptiveRRFConfig(q)
		opts.Limit = limit
		if len(req.Labels) > 0 {
			opts.Types = req.Labels
		}
		if s.config != nil && s.config.Features != nil {
			opts.RerankEnabled = s.config.Features.SearchRerankEnabled
		}
		return opts
	}

	// Fast path: short query (single chunk). Try hybrid; fall back to BM25.
	// Use per-DB query embedding so vector dims match the index for this database.
	if len(queryChunks) <= 1 {
		emb, embedErr := s.db.EmbedQueryForDB(ctx, dbName, req.Query)
		if embedErr != nil {
			if errors.Is(embedErr, nornicdb.ErrQueryEmbeddingDimensionMismatch) {
				s.writeError(w, http.StatusBadRequest, embedErr.Error(), ErrBadRequest)
				return
			}
			log.Printf("⚠️ Query embedding failed: %v", embedErr)
		}
		if len(emb) > 0 {
			searchResponse, err = searchSvc.Search(ctx, req.Query, emb, buildOpts(req.Query, req.Limit))
		} else {
			searchResponse, err = searchSvc.Search(ctx, req.Query, nil, buildOpts(req.Query, req.Limit))
		}
	} else {
		// Multi-chunk: embed/search each chunk, then fuse results across chunks using RRF.
		perChunkLimit := req.Limit
		if perChunkLimit < 10 {
			perChunkLimit = 10
		}
		if perChunkLimit < req.Limit*3 {
			perChunkLimit = req.Limit * 3
		}
		if perChunkLimit > 100 {
			perChunkLimit = 100
		}

		type fused struct {
			node       *nornicdb.Node
			scoreRRF   float64
			vectorRank int
			bm25Rank   int
		}
		fusedByID := make(map[string]*fused)

		var usedVectorChunks int
		for _, chunkQuery := range queryChunks {
			emb, embedErr := s.db.EmbedQueryForDB(ctx, dbName, chunkQuery)
			if embedErr != nil {
				if errors.Is(embedErr, nornicdb.ErrQueryEmbeddingDimensionMismatch) {
					s.writeError(w, http.StatusBadRequest, embedErr.Error(), ErrBadRequest)
					return
				}
				log.Printf("⚠️ Query embedding failed (chunked): %v", embedErr)
				continue
			}
			if len(emb) == 0 {
				continue
			}
			usedVectorChunks++

			resp, searchErr := searchSvc.Search(ctx, chunkQuery, emb, buildOpts(chunkQuery, perChunkLimit))
			if searchErr != nil {
				if errors.Is(searchErr, search.ErrSearchIndexBuilding) {
					err = searchErr
					break
				}
				continue
			}
			if resp == nil {
				continue
			}

			for rank := range resp.Results {
				r := resp.Results[rank]
				id := string(r.NodeID) // already unprefixed by namespaced storage/search layer
				f := fusedByID[id]
				if f == nil {
					f = &fused{
						node: &nornicdb.Node{
							ID:         id,
							Labels:     r.Labels,
							Properties: r.Properties,
						},
						scoreRRF:   0,
						vectorRank: r.VectorRank,
						bm25Rank:   r.BM25Rank,
					}
					fusedByID[id] = f
				}

				// Outer RRF: 1/(k + rank), rank is 1-based.
				f.scoreRRF += 1.0 / (outerRRFK + float64(rank+1))
			}
		}

		if err == nil {
			if usedVectorChunks == 0 || len(fusedByID) == 0 {
				// Embeddings not available (or all failed): fall back to BM25.
				searchResponse, err = searchSvc.Search(ctx, req.Query, nil, buildOpts(req.Query, req.Limit))
			} else {
				// Materialize fused response.
				fusedList := make([]*fused, 0, len(fusedByID))
				for _, f := range fusedByID {
					fusedList = append(fusedList, f)
				}
				sort.Slice(fusedList, func(i, j int) bool {
					return fusedList[i].scoreRRF > fusedList[j].scoreRRF
				})
				if len(fusedList) > req.Limit {
					fusedList = fusedList[:req.Limit]
				}

				// Build a SearchResponse-like structure to reuse existing conversion code.
				searchResponse = &search.SearchResponse{
					SearchMethod:      "chunked_rrf_hybrid",
					FallbackTriggered: false,
					Results:           make([]search.SearchResult, 0, len(fusedList)),
				}
				for _, f := range fusedList {
					// Preserve vector_rank and bm25_rank from the first chunk where the node appeared.
					searchResponse.Results = append(searchResponse.Results, search.SearchResult{
						ID:         f.node.ID,
						NodeID:     storage.NodeID(f.node.ID),
						Labels:     f.node.Labels,
						Properties: f.node.Properties,
						Score:      f.scoreRRF,
						RRFScore:   f.scoreRRF,
						VectorRank: f.vectorRank,
						BM25Rank:   f.bm25Rank,
					})
				}
			}
		}
	}

	if err != nil {
		if errors.Is(err, search.ErrSearchIndexBuilding) {
			s.writeError(w, http.StatusServiceUnavailable, err.Error(), ErrServiceUnavailable)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err.Error(), ErrInternalError)
		return
	}

	// Convert search results to our format
	// SearchResult.NodeID is already unprefixed (NamespacedEngine.GetNode() unprefixes automatically)
	results := make([]*nornicdb.SearchResult, len(searchResponse.Results))
	for i, r := range searchResponse.Results {
		results[i] = &nornicdb.SearchResult{
			Node: &nornicdb.Node{
				ID:         string(r.NodeID),
				Labels:     r.Labels,
				Properties: r.Properties,
			},
			Score:      r.Score,
			RRFScore:   r.RRFScore,
			VectorRank: r.VectorRank,
			BM25Rank:   r.BM25Rank,
		}
	}

	s.writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleSimilar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required", ErrMethodNotAllowed)
		return
	}

	var req struct {
		Database string `json:"database,omitempty"` // Optional: defaults to default database
		NodeID   string `json:"node_id"`
		Limit    int    `json:"limit,omitempty"`
	}

	if err := s.readJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body", ErrBadRequest)
		return
	}

	if req.Limit <= 0 {
		req.Limit = 10
	}

	// Get database name (default to default database if not specified)
	dbName := req.Database
	if dbName == "" {
		dbName = s.dbManager.DefaultDatabaseName()
	}

	// Per-database RBAC: deny if principal may not access this database (Neo4j-aligned).
	if !s.getDatabaseAccessMode(getClaims(r)).CanAccessDatabase(dbName) {
		s.writeNeo4jError(w, http.StatusForbidden, "Neo.ClientError.Security.Forbidden",
			fmt.Sprintf("Access to database '%s' is not allowed.", dbName))
		return
	}

	// Get namespaced storage for the specified database
	storageEngine, err := s.dbManager.GetStorage(dbName)
	if err != nil {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("Database '%s' not found", dbName), ErrNotFound)
		return
	}

	// Get the target node from namespaced storage
	targetNode, err := storageEngine.GetNode(storage.NodeID(req.NodeID))
	if err != nil {
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("Node '%s' not found", req.NodeID), ErrNotFound)
		return
	}

	if len(targetNode.ChunkEmbeddings) == 0 || len(targetNode.ChunkEmbeddings[0]) == 0 {
		s.writeError(w, http.StatusBadRequest, "Node has no embedding", ErrBadRequest)
		return
	}

	// Find similar nodes using vector similarity search
	type scored struct {
		node  *storage.Node
		score float64
	}
	var results []scored

	ctx := r.Context()
	err = storage.StreamNodesWithFallback(ctx, storageEngine, 1000, func(n *storage.Node) error {
		// Skip self and nodes without embeddings
		if string(n.ID) == req.NodeID || len(n.ChunkEmbeddings) == 0 || len(n.ChunkEmbeddings[0]) == 0 {
			return nil
		}

		// Use first chunk embedding for similarity (always stored in ChunkEmbeddings, even single chunk = array of 1)
		var targetEmb, nEmb []float32
		if len(targetNode.ChunkEmbeddings) > 0 && len(targetNode.ChunkEmbeddings[0]) > 0 {
			targetEmb = targetNode.ChunkEmbeddings[0]
		}
		if len(n.ChunkEmbeddings) > 0 && len(n.ChunkEmbeddings[0]) > 0 {
			nEmb = n.ChunkEmbeddings[0]
		}
		sim := vector.CosineSimilarity(targetEmb, nEmb)

		// Maintain top-k results
		if len(results) < req.Limit {
			results = append(results, scored{node: n, score: sim})
			if len(results) == req.Limit {
				sort.Slice(results, func(i, j int) bool {
					return results[i].score > results[j].score
				})
			}
		} else if sim > results[req.Limit-1].score {
			results[req.Limit-1] = scored{node: n, score: sim}
			sort.Slice(results, func(i, j int) bool {
				return results[i].score > results[j].score
			})
		}
		return nil
	})

	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error(), ErrInternalError)
		return
	}

	// Final sort
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Convert to response format (node IDs are already unprefixed from NamespacedEngine)
	searchResults := make([]*nornicdb.SearchResult, len(results))
	for i, r := range results {
		searchResults[i] = &nornicdb.SearchResult{
			Node: &nornicdb.Node{
				ID:         string(r.node.ID),
				Labels:     r.node.Labels,
				Properties: r.node.Properties,
				CreatedAt:  r.node.CreatedAt,
			},
			Score: r.score,
		}
	}

	s.writeJSON(w, http.StatusOK, searchResults)
}
