// Package search provides unified hybrid search with Reciprocal Rank Fusion (RRF).
//
// This package implements the same hybrid search approach used by production systems
// like Azure AI Search, Elasticsearch, and Weaviate: combining vector similarity search
// with BM25 full-text search using Reciprocal Rank Fusion.
//
// Search Capabilities:
//   - Vector similarity search (cosine similarity with HNSW index)
//   - BM25 full-text search (keyword matching with TF-IDF)
//   - RRF hybrid search (fuses vector + BM25 results)
//   - Adaptive weighting based on query characteristics
//   - Automatic fallback when one method fails
//
// Example Usage:
//
//	// Create search service
//	svc := search.NewService(storageEngine)
//
//	// Build indexes from existing nodes
//	if err := svc.BuildIndexes(ctx); err != nil {
//		log.Fatal(err)
//	}
//
//	// Perform hybrid search
//	query := "machine learning algorithms"
//	embedding := embedder.Embed(ctx, query) // Get from embed package
//	opts := search.DefaultSearchOptions()
//
//	response, err := svc.Search(ctx, query, embedding, opts)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	for _, result := range response.Results {
//		fmt.Printf("[%.3f] %s\n", result.RRFScore, result.Title)
//	}
//
// How RRF Works:
//
// RRF (Reciprocal Rank Fusion) combines rankings from multiple search methods.
// Instead of merging scores directly (which can be incomparable), RRF uses rank
// positions to create a unified ranking.
//
// Formula: RRF_score = Σ (weight / (k + rank))
//
// Where:
//   - k is a constant (typically 60) to reduce the impact of high ranks
//   - rank is the position in the result list (1-indexed)
//   - weight allows emphasizing one method over another
//
// Example: A document ranked #1 in vector search and #3 in BM25:
//
//	RRF = (1.0 / (60 + 1)) + (1.0 / (60 + 3))
//	    = (1.0 / 61) + (1.0 / 63)
//	    = 0.0164 + 0.0159
//	    = 0.0323
//
// Documents that appear in both result sets get boosted scores.
//
// ELI12 (Explain Like I'm 12):
//
// Imagine two friends ranking pizza places:
//   - Friend A (vector search) ranks by taste similarity to your favorite
//   - Friend B (BM25) ranks by matching your description "spicy pepperoni"
//
// They might disagree! Friend A says place X is #1 (tastes similar), while
// Friend B says it's #5 (doesn't match keywords well).
//
// RRF solves this by:
// 1. If a place appears in BOTH lists, it gets bonus points
// 2. Higher ranks (being at the top) give more points
// 3. The magic number 60 prevents #1 from completely dominating
//
// This way, a place that's #2 in both lists beats a place that's #1 in one
// but missing from the other!
//
// Index persistence and WAL alignment:
//
// NornicDB storage uses a write-ahead log (WAL) for durability; graph state is recovered
// from WAL (and snapshots) on startup. Search indexes (BM25 and vector) are built or
// loaded after storage recovery, so they always reflect the WAL-consistent state. Index
// files use semver format versioning (Qdrant-style): only indexes with the same format
// version are loaded; older or newer versions are rejected so the caller can rebuild.
// Persisted indexes are saved on a debounced schedule after mutations and on shutdown.
//
// Result caching:
//
// Search() results are cached in-process by query + options (limit, types, rerank, MMR, etc.),
// with the same semantics as the Cypher query cache: LRU eviction (default 1000 entries),
// TTL (default 5 minutes), and full invalidation on IndexNode/RemoveNode so results stay
// correct after index changes. All call paths (HTTP search API, Cypher vector procedures,
// MCP, etc.) share this cache, so repeated identical searches return immediately.
package search

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orneryd/nornicdb/pkg/gpu"
	"github.com/orneryd/nornicdb/pkg/math/vector"
	"github.com/orneryd/nornicdb/pkg/storage"
)

// SearchableProperties defines PRIORITY properties for full-text search ranking.
// These properties are indexed first for better BM25 ranking.
// Note: ALL node properties are indexed, but these get priority weighting.
// These match Mimir's Neo4j fulltext index configuration.
var SearchableProperties = []string{
	"content",
	"text",
	"title",
	"name",
	"description",
	"path",
	"workerRole",
	"requirements",
}

// SearchResult represents a unified search result.
type SearchResult struct {
	ID             string         `json:"id"`
	NodeID         storage.NodeID `json:"nodeId"`
	Type           string         `json:"type"`
	Labels         []string       `json:"labels"`
	Title          string         `json:"title,omitempty"`
	Description    string         `json:"description,omitempty"`
	ContentPreview string         `json:"content_preview,omitempty"`
	Properties     map[string]any `json:"properties,omitempty"`

	// Scoring
	Score      float64 `json:"score"`
	Similarity float64 `json:"similarity,omitempty"`

	// RRF metadata (vector_rank/bm25_rank are always emitted so clients see original
	// ranks even when Stage-2 reranking is applied; 0 means not in that result set)
	RRFScore   float64 `json:"rrf_score,omitempty"`
	VectorRank int     `json:"vector_rank"`
	BM25Rank   int     `json:"bm25_rank"`
}

// SearchResponse is the response from a search operation.
type SearchResponse struct {
	Status            string         `json:"status"`
	Query             string         `json:"query"`
	Results           []SearchResult `json:"results"`
	TotalCandidates   int            `json:"total_candidates"`
	Returned          int            `json:"returned"`
	SearchMethod      string         `json:"search_method"`
	FallbackTriggered bool           `json:"fallback_triggered"`
	Message           string         `json:"message,omitempty"`
	Metrics           *SearchMetrics `json:"metrics,omitempty"`
}

// SearchMetrics contains timing and statistics.
type SearchMetrics struct {
	VectorSearchTimeMs int `json:"vector_search_time_ms"`
	BM25SearchTimeMs   int `json:"bm25_search_time_ms"`
	FusionTimeMs       int `json:"fusion_time_ms"`
	TotalTimeMs        int `json:"total_time_ms"`
	VectorCandidates   int `json:"vector_candidates"`
	BM25Candidates     int `json:"bm25_candidates"`
	FusedCandidates    int `json:"fused_candidates"`
}

// SearchOptions configures the search behavior.
type SearchOptions struct {
	// Limit is the maximum number of results to return
	Limit int

	// MinSimilarity is the minimum similarity threshold for vector search.
	// nil = use service default, otherwise use the provided value.
	MinSimilarity *float64

	// Types filters results by node type (labels)
	Types []string

	// RRF configuration
	RRFK         float64 // RRF constant (default: 60)
	VectorWeight float64 // Weight for vector results (default: 1.0)
	BM25Weight   float64 // Weight for BM25 results (default: 1.0)
	MinRRFScore  float64 // Minimum RRF score threshold (default: 0.01)

	// MMR (Maximal Marginal Relevance) diversification
	// When enabled, results are re-ranked to balance relevance with diversity
	MMREnabled bool    // Enable MMR diversification (default: false)
	MMRLambda  float64 // Balance: 1.0 = pure relevance, 0.0 = pure diversity (default: 0.7)

	// Cross-encoder reranking (Stage 2)
	// When enabled, top candidates are re-scored using a cross-encoder model
	// for higher accuracy at the cost of latency
	RerankEnabled  bool    // Enable cross-encoder reranking (default: false)
	RerankTopK     int     // How many candidates to rerank (default: 100)
	RerankMinScore float64 // Minimum cross-encoder score to include (default: 0)
}

// DefaultSearchOptions returns sensible defaults.
func DefaultSearchOptions() *SearchOptions {
	return &SearchOptions{
		Limit:          50,
		MinSimilarity:  nil, // nil = use service default or 0.5 fallback
		RRFK:           60,
		VectorWeight:   1.0,
		BM25Weight:     1.0,
		MinRRFScore:    0.01,
		MMREnabled:     false,
		MMRLambda:      0.7, // Balanced: 70% relevance, 30% diversity
		RerankEnabled:  false,
		RerankTopK:     100,
		RerankMinScore: 0.0,
	}
}

// GetMinSimilarity returns the MinSimilarity value, or the fallback if nil.
func (o *SearchOptions) GetMinSimilarity(fallback float64) float64 {
	if o.MinSimilarity != nil {
		return *o.MinSimilarity
	}
	return fallback
}

// searchResultCacheEntry holds a cached SearchResponse and expiry (same semantics as Cypher query cache).
type searchResultCacheEntry struct {
	response *SearchResponse
	expires  time.Time
}

// searchResultCache is an LRU cache for search results keyed by query + options.
// Invalidated on IndexNode/RemoveNode so results stay correct after index changes.
type searchResultCache struct {
	mu      sync.RWMutex
	entries map[string]*searchResultCacheEntry
	lru     []string // key order, oldest first for eviction
	maxSize int
	ttl     time.Duration
}

func newSearchResultCache(maxSize int, ttl time.Duration) *searchResultCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &searchResultCache{
		entries: make(map[string]*searchResultCacheEntry, maxSize),
		lru:     make([]string, 0, maxSize),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// searchCacheKey builds a deterministic key for query + options (same inputs => same key).
func searchCacheKey(query string, opts *SearchOptions) string {
	if opts == nil {
		opts = DefaultSearchOptions()
	}
	typesCopy := make([]string, len(opts.Types))
	copy(typesCopy, opts.Types)
	sort.Strings(typesCopy)
	return strings.Join([]string{
		query,
		strconv.Itoa(opts.Limit),
		strings.Join(typesCopy, "|"),
		strconv.FormatBool(opts.RerankEnabled),
		strconv.Itoa(opts.RerankTopK),
		strconv.FormatBool(opts.MMREnabled),
		strconv.FormatFloat(opts.MMRLambda, 'g', -1, 64),
		strconv.FormatFloat(opts.RerankMinScore, 'g', -1, 64),
	}, "\x00")
}

func (c *searchResultCache) Get(key string) *SearchResponse {
	c.mu.RLock()
	ent, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || ent == nil {
		return nil
	}
	if c.ttl > 0 && time.Now().After(ent.expires) {
		c.mu.Lock()
		delete(c.entries, key)
		for i, k := range c.lru {
			if k == key {
				c.lru = append(c.lru[:i], c.lru[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
		return nil
	}
	return ent.response
}

func (c *searchResultCache) Put(key string, response *SearchResponse) {
	if response == nil {
		return
	}
	expires := time.Time{}
	if c.ttl > 0 {
		expires = time.Now().Add(c.ttl)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		for len(c.lru) >= c.maxSize {
			evict := c.lru[0]
			c.lru = c.lru[1:]
			delete(c.entries, evict)
		}
		c.lru = append(c.lru, key)
	}
	c.entries[key] = &searchResultCacheEntry{response: response, expires: expires}
}

func (c *searchResultCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*searchResultCacheEntry, c.maxSize)
	c.lru = c.lru[:0]
}

// Service provides unified hybrid search with automatic index management.
//
// The Service maintains:
//   - Vector index (HNSW for fast approximate nearest neighbor search)
//   - Full-text index (BM25 with inverted index)
//   - Connection to storage engine for node data enrichment
//
// Thread-safe: Multiple goroutines can call Search() concurrently.
//
// Example:
//
//	svc := search.NewService(engine)
//	defer svc.Close()
//
//	// Index existing data
//	if err := svc.BuildIndexes(ctx); err != nil {
//		log.Fatal(err)
//	}
//
//	// Index new nodes as they're created
//	node := &storage.Node{...}
//	if err := svc.IndexNode(node); err != nil {
//		log.Printf("Failed to index: %v", err)
//	}
type Service struct {
	engine        storage.Engine
	vectorIndex   *VectorIndex
	fulltextIndex *FulltextIndex
	reranker      Reranker
	mu            sync.RWMutex

	// cypherMetadata is a small, in-memory view of node embedding availability and labels.
	// It allows Cypher-compatible vector queries to execute without scanning storage.
	nodeLabels       map[string][]string          // nodeID -> labels
	nodeNamedVector  map[string]map[string]string // nodeID -> vectorName -> vectorID
	nodePropVector   map[string]map[string]string // nodeID -> propertyKey -> vectorID
	nodeChunkVectors map[string][]string          // nodeID -> vectorIDs (main + chunks)

	// HNSW index for approximate nearest neighbor search (optional, lazy-initialized)
	hnswIndex           *HNSWIndex
	hnswMu              sync.RWMutex
	hnswMaintOnce       sync.Once
	hnswMaintStop       chan struct{}
	hnswRebuildInFlight atomic.Bool
	hnswLastRebuildUnix atomic.Int64

	// Optional GPU-accelerated brute-force embedding index.
	// When enabled, this is used as an exact vector-search backend.
	gpuManager        *gpu.Manager
	gpuEmbeddingIndex *gpu.EmbeddingIndex

	// GPU k-means clustering for accelerated search (optional)
	clusterIndex      *gpu.ClusterIndex
	clusterEnabled    bool
	clusterUsesGPU    bool
	kmeansInProgress  atomic.Bool

	// Optional IVF-HNSW acceleration: build one HNSW per cluster to use centroids
	// as a routing layer for CPU-only large datasets.
	clusterHNSWMu sync.RWMutex
	clusterHNSW   map[int]*HNSWIndex

	// minEmbeddingsForClustering is the minimum number of embeddings needed
	// before k-means clustering provides any benefit. Configurable via
	// SetMinEmbeddingsForClustering(). Default: 1000
	minEmbeddingsForClustering int

	// defaultMinSimilarity is the minimum cosine similarity threshold for vector search.
	// Apple Intelligence embeddings produce scores in 0.2-0.8 range, bge-m3/mxbai produce 0.7-0.99.
	// Configurable via SetDefaultMinSimilarity(). Default: -1 (not set, use SearchOptions default)
	// Set to 0.0 to let RRF handle relevance filtering.
	defaultMinSimilarity float64

	// Vector search pipeline (lazy-initialized)
	vectorPipeline *VectorSearchPipeline
	pipelineMu     sync.RWMutex

	// Index persistence: when both paths are set, BuildIndexes tries to load BM25 and vector
	// indexes from disk; if both load successfully with count > 0, the full iteration is skipped.
	// When hnswIndexPath is set, the HNSW index is also saved/loaded so it does not need rebuilding.
	fulltextIndexPath string
	vectorIndexPath  string
	hnswIndexPath   string

	// Debounced persist: after IndexNode/RemoveNode we schedule a write to disk after an idle delay.
	persistMu        sync.Mutex
	persistTimer     *time.Timer
	buildInProgress  atomic.Bool

	// resultCache caches Search() results by query+options (same semantics as Cypher query cache).
	// All call paths (HTTP search, Cypher, etc.) benefit. Invalidated on IndexNode/RemoveNode.
	resultCache *searchResultCache
}

// NewService creates a new search Service with empty indexes.
//
// The service is created with:
//   - 1024-dimensional vector index (default for bge-m3)
//   - Empty full-text index
//   - Reference to storage engine for data enrichment
//
// Call BuildIndexes() after creation to populate indexes from existing data.
//
// Example:
//
//	engine, _ := storage.NewMemoryEngine()
//	svc := search.NewService(engine)
//
//	// Build indexes from all nodes
//	if err := svc.BuildIndexes(context.Background()); err != nil {
//		log.Fatal(err)
//	}
//
// Returns a new Service ready for indexing and searching.
//
// Example 1 - Basic Setup:
//
//	engine := storage.NewMemoryEngine()
//	svc := search.NewService(engine)
//	defer svc.Close()
//
//	// Build indexes from existing nodes
//	if err := svc.BuildIndexes(ctx); err != nil {
//		log.Fatal(err)
//	}
//
//	// Now ready to search
//	results, _ := svc.Search(ctx, "machine learning", nil, nil)
//
// Example 2 - With Embedder Integration:
//
//	engine := storage.NewBadgerEngine("./data")
//	svc := search.NewService(engine)
//
//	// Create embedder
//	embedder := embed.NewOllama(embed.DefaultOllamaConfig())
//
//	// Index documents with embeddings
//	for _, doc := range documents {
//		node := &storage.Node{
//			ID: storage.NodeID(doc.ID),
//			Labels: []string{"Document"},
//			Properties: map[string]any{
//				"title":   doc.Title,
//				"content": doc.Content,
//			},
//			Embedding: embedder.Embed(ctx, doc.Content),
//		}
//		engine.CreateNode(node)
//		svc.IndexNode(node)
//	}
//
// Example 3 - Real-time Indexing:
//
//	svc := search.NewService(engine)
//
//	// Index as nodes are created
//	onCreate := func(node *storage.Node) {
//		if err := svc.IndexNode(node); err != nil {
//			log.Printf("Index failed: %v", err)
//		}
//	}
//
//	// Hook into storage engine
//	engine.OnNodeCreate(onCreate)
//
// ELI12:
//
// Think of NewService like building a library with two special catalogs:
//  1. A "similarity catalog" (vector index) - finds books that are LIKE what you want
//  2. A "keyword catalog" (fulltext index) - finds books with specific words
//
// When you search, the library assistant checks BOTH catalogs and shows you
// the books that appear in both lists first. That's hybrid search!
//
// Performance:
//   - Vector index: HNSW algorithm, O(log n) search
//   - Fulltext index: Inverted index, O(k + m) where k = unique terms, m = matches
//   - Memory: ~4KB per 1000-dim embedding + ~500 bytes per document
//
// Thread Safety:
//
//	Safe for concurrent searches from multiple goroutines.
func NewService(engine storage.Engine) *Service {
	return NewServiceWithDimensions(engine, 1024)
}

// NewServiceWithDimensions creates a search Service with the specified embedding dimensions.
// Use this when your embedding model produces vectors of a different size than the default 1024.
//
// Example:
//
//	// For Apple Intelligence embeddings (512 dimensions)
//	svc := search.NewServiceWithDimensions(engine, 512)
//
//	// For OpenAI text-embedding-3-small (1536 dimensions)
//	svc := search.NewServiceWithDimensions(engine, 1536)
func NewServiceWithDimensions(engine storage.Engine, dimensions int) *Service {
	return &Service{
		engine:                     engine,
		vectorIndex:                NewVectorIndex(dimensions),
		fulltextIndex:              NewFulltextIndex(),
		minEmbeddingsForClustering: DefaultMinEmbeddingsForClustering,
		defaultMinSimilarity:       -1, // -1 = not set, use SearchOptions default
		nodeLabels:                 make(map[string][]string, 1024),
		nodeNamedVector:            make(map[string]map[string]string, 1024),
		nodePropVector:             make(map[string]map[string]string, 1024),
		nodeChunkVectors:           make(map[string][]string, 1024),
		resultCache:                newSearchResultCache(1000, 5*time.Minute), // same order as Cypher query cache
	}
}

// SetGPUManager enables GPU acceleration for exact brute-force vector search.
//
// This is independent from k-means clustering. When enabled, the vector pipeline
// may choose GPU brute-force search (exact) for datasets where it outperforms HNSW.
func (s *Service) SetGPUManager(manager *gpu.Manager) {
	s.pipelineMu.Lock()
	s.vectorPipeline = nil
	s.pipelineMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.gpuManager = manager
	if manager == nil || !manager.IsEnabled() {
		s.gpuEmbeddingIndex = nil
		return
	}

	dimensions := 0
	if s.vectorIndex != nil {
		dimensions = s.vectorIndex.GetDimensions()
	}
	if dimensions <= 0 {
		return
	}

	cfg := gpu.DefaultEmbeddingIndexConfig(dimensions)
	cfg.GPUEnabled = true
	cfg.AutoSync = true
	cfg.BatchThreshold = 1000
	gi := gpu.NewEmbeddingIndex(manager, cfg)

	// Snapshot current vectors.
	s.vectorIndex.mu.RLock()
	ids := make([]string, 0, len(s.vectorIndex.vectors))
	embs := make([][]float32, 0, len(s.vectorIndex.vectors))
	for id, vec := range s.vectorIndex.vectors {
		if len(vec) == 0 {
			continue
		}
		ids = append(ids, id)
		embs = append(embs, vec)
	}
	s.vectorIndex.mu.RUnlock()

	_ = gi.AddBatch(ids, embs)
	_ = gi.SyncToGPU()

	s.gpuEmbeddingIndex = gi
}

// EnableClustering enables GPU k-means clustering for accelerated vector search.
//
// Performance Improvements (Real-World Benchmarks):
//   - 2,000 embeddings: ~14% faster (61ms vs 65ms avg)
//   - 4,500 embeddings: ~26% faster (35ms vs 47ms avg)
//   - 10,000+ embeddings: 10-50x faster (scales with dataset size)
//
// The speedup increases with dataset size as the cluster-based search
// avoids comparing against all vectors.
//
// Parameters:
//   - gpuManager: GPU manager for acceleration (can be nil for CPU-only)
//   - numClusters: Number of k-means clusters (0 for auto based on dataset size)
//
// Call this BEFORE BuildIndexes(), then call TriggerClustering() after indexing.
//
// Example:
//
//	gpuManager, _ := gpu.NewManager(nil)
//	svc.EnableClustering(gpuManager, 100) // 100 clusters
//	svc.BuildIndexes(ctx)
//	svc.TriggerClustering(ctx) // Run k-means
func (s *Service) EnableClustering(gpuManager *gpu.Manager, numClusters int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If clustering is already enabled, only rebuild when switching CPU<->GPU.
	if s.clusterEnabled && s.clusterIndex != nil {
		requestGPU := gpuManager != nil && gpuManager.IsEnabled()
		if s.clusterUsesGPU == requestGPU {
			return
		}
	}

	// Cluster count: env override, else 0 = auto from dataset size at trigger time (sqrt(n/2), clamped).
	envK := envInt("NORNICDB_KMEANS_NUM_CLUSTERS", 0)
	if envK > 0 {
		numClusters = envK
	} else if numClusters <= 0 {
		numClusters = 0 // Auto: gpu.optimalK(n) when TriggerClustering runs
	}
	autoK := numClusters <= 0

	// Max iterations is a cap; clustering stops when assignments stop changing (early convergence).
	// Best practice: 20-30 is usually enough with k-means++; 50+ rarely needed. Override via NORNICDB_KMEANS_MAX_ITERATIONS.
	maxIter := envInt("NORNICDB_KMEANS_MAX_ITERATIONS", 25)
	if maxIter < 5 {
		maxIter = 5
	}
	if maxIter > 500 {
		maxIter = 500
	}
	kmeansConfig := &gpu.KMeansConfig{
		NumClusters:   numClusters,
		AutoK:         autoK,
		MaxIterations: maxIter,
		Tolerance:     0.001,
		InitMethod:    "kmeans++",
	}

	// Use the same dimensions as the vector index (no hardcoded fallback)
	if s.vectorIndex == nil {
		log.Printf("[K-MEANS] ⚠️ Cannot enable clustering: vector index not initialized")
		return
	}
	dimensions := s.vectorIndex.dimensions
	embConfig := gpu.DefaultEmbeddingIndexConfig(dimensions)

	s.clusterIndex = gpu.NewClusterIndex(gpuManager, embConfig, kmeansConfig)
	s.clusterEnabled = true
	s.clusterUsesGPU = gpuManager != nil && gpuManager.IsEnabled()

	// Backfill embeddings from the current vector index so clustering can run
	// immediately, even if indexes were built before clustering was enabled.
	if s.vectorIndex != nil {
		s.vectorIndex.mu.RLock()
		ids := make([]string, 0, len(s.vectorIndex.vectors))
		embs := make([][]float32, 0, len(s.vectorIndex.vectors))
		for id, vec := range s.vectorIndex.vectors {
			if len(vec) == 0 {
				continue
			}
			copyVec := make([]float32, len(vec))
			copy(copyVec, vec)
			ids = append(ids, id)
			embs = append(embs, copyVec)
		}
		s.vectorIndex.mu.RUnlock()

		_ = s.clusterIndex.AddBatch(ids, embs) // Best effort
	}

	mode := "CPU"
	if gpuManager != nil {
		mode = "GPU"
	}
	clusterDesc := fmt.Sprintf("%d", numClusters)
	if autoK {
		clusterDesc = "auto"
	}
	log.Printf("[K-MEANS] ✅ Clustering ENABLED | mode=%s clusters=%s max_iter=%d init=%s",
		mode, clusterDesc, kmeansConfig.MaxIterations, kmeansConfig.InitMethod)
}

// DefaultMinEmbeddingsForClustering is the default minimum number of embeddings
// needed before k-means clustering provides any benefit. Below this threshold,
// brute-force search is faster than cluster overhead.
//
// This value can be overridden per-service using SetMinEmbeddingsForClustering().
//
// Performance Scaling (Real-World Benchmarks):
//   - <1000 embeddings: Clustering overhead > speedup benefit
//   - 2,000 embeddings: ~14% faster with clustering
//   - 4,500 embeddings: ~26% faster with clustering
//   - 10,000+ embeddings: 10-50x faster with clustering
//
// Tuning Guidelines:
//   - 1000 (default): Safe for most workloads, proven performance benefit
//   - 500-1000: Use for latency-sensitive apps (14-26% speedup range)
//   - 100-500: Testing or small datasets (verify clustering works)
//   - 2000+: Very large datasets (maximize speedup, delay until more data)
//
// Environment Variable: NORNICDB_KMEANS_MIN_EMBEDDINGS (overrides default)
const DefaultMinEmbeddingsForClustering = 1000

// TriggerClustering runs k-means clustering on all indexed embeddings.
// Stops promptly when ctx is cancelled (e.g. process shutdown).
// Only one run executes at a time per service; if clustering is already in progress,
// returns nil immediately (debounced).
//
// Trigger Policies:
//   - After bulk loads: Automatically called after BuildIndexes() completes
//   - Periodic clustering: Background timer runs clustering at regular intervals
//   - Manual trigger: Call this after bulk data loading to enable k-means routing
//
// Once clustering completes, the vector search pipeline automatically uses
// KMeansCandidateGen for candidate generation, providing significant speedup
// for very large datasets (N > 100K).
//
// Returns nil (not error) if there are too few embeddings - clustering will
// be skipped silently as brute-force search is faster for small datasets.
// Returns error only if clustering is not enabled or fails unexpectedly.
func (s *Service) TriggerClustering(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.kmeansInProgress.CompareAndSwap(false, true) {
		log.Printf("[K-MEANS] ⏭️  SKIPPED | reason=already_running")
		return nil
	}
	defer s.kmeansInProgress.Store(false)

	s.mu.RLock()
	clusterIndex := s.clusterIndex
	threshold := s.minEmbeddingsForClustering
	s.mu.RUnlock()

	if clusterIndex == nil {
		log.Printf("[K-MEANS] ❌ SKIPPED | reason=not_enabled")
		return fmt.Errorf("clustering not enabled - call EnableClustering() first")
	}

	if threshold <= 0 {
		threshold = DefaultMinEmbeddingsForClustering
	}
	embeddingCount := clusterIndex.Count()

	// Skip clustering if too few embeddings - not worth the overhead
	if embeddingCount < threshold {
		log.Printf("[K-MEANS] ⏭️  SKIPPED | embeddings=%d threshold=%d reason=too_few_embeddings",
			embeddingCount, threshold)
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	log.Printf("[K-MEANS] 🔄 STARTING | embeddings=%d", embeddingCount)
	startTime := time.Now()

	if err := clusterIndex.ClusterWithContext(ctx); err != nil {
		if ctx.Err() != nil {
			log.Printf("[K-MEANS] ⏹️  CANCELLED | embeddings=%d (shutdown)", embeddingCount)
			return err
		}
		log.Printf("[K-MEANS] ❌ FAILED | embeddings=%d error=%v", embeddingCount, err)
		return fmt.Errorf("clustering failed: %w", err)
	}

	elapsed := time.Since(startTime)
	stats := clusterIndex.ClusterStats()
	log.Printf("[K-MEANS] ✅ COMPLETE | clusters=%d embeddings=%d iterations=%d duration=%v avg_cluster_size=%.1f",
		stats.NumClusters, stats.EmbeddingCount, stats.Iterations, elapsed, stats.AvgClusterSize)

	s.pipelineMu.Lock()
	s.vectorPipeline = nil
	s.pipelineMu.Unlock()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if envBool("NORNICDB_VECTOR_IVF_HNSW_ENABLED", true) {
		if err := s.rebuildClusterHNSWIndexes(ctx, clusterIndex); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("[IVF-HNSW] ⚠️  build skipped: %v", err)
		}
	}

	return nil
}

func (s *Service) rebuildClusterHNSWIndexes(ctx context.Context, clusterIndex *gpu.ClusterIndex) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if clusterIndex == nil || !clusterIndex.IsClustered() {
		return fmt.Errorf("cluster index not clustered")
	}

	s.mu.RLock()
	vi := s.vectorIndex
	dims := 0
	if vi != nil {
		dims = vi.GetDimensions()
	}
	hnswPath := s.hnswIndexPath
	s.mu.RUnlock()
	if vi == nil || dims <= 0 {
		return fmt.Errorf("vector index unavailable")
	}

	minClusterSize := envInt("NORNICDB_VECTOR_IVF_HNSW_MIN_CLUSTER_SIZE", 200)
	maxClusters := envInt("NORNICDB_VECTOR_IVF_HNSW_MAX_CLUSTERS", 1024)
	numClusters := clusterIndex.NumClusters()
	if numClusters <= 0 {
		return fmt.Errorf("no clusters")
	}
	if numClusters > maxClusters {
		numClusters = maxClusters
	}

	config := HNSWConfigFromEnv()
	rebuilt := make(map[int]*HNSWIndex, numClusters)
	vectorLookup := vi.GetVector

	for cid := 0; cid < numClusters; cid++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		memberIDs := clusterIndex.GetClusterMemberIDsForCluster(cid)
		if len(memberIDs) < minClusterSize {
			continue
		}
		// Try loading persisted IVF-HNSW cluster first (same path convention as SaveIVFHNSW).
		var idx *HNSWIndex
		if hnswPath != "" && vectorLookup != nil {
			loaded, _ := LoadIVFHNSWCluster(hnswPath, cid, vectorLookup)
			if loaded != nil && loaded.GetDimensions() == dims {
				idx = loaded
			}
		}
		if idx == nil {
			idx = NewHNSWIndex(dims, config)
			vi.mu.RLock()
			for _, id := range memberIDs {
				vec, ok := vi.vectors[id]
				if !ok || len(vec) == 0 {
					continue
				}
				_ = idx.Add(id, vec)
			}
			vi.mu.RUnlock()
		}
		rebuilt[cid] = idx
	}

	s.clusterHNSWMu.Lock()
	s.clusterHNSW = rebuilt
	s.clusterHNSWMu.Unlock()

	s.pipelineMu.Lock()
	s.vectorPipeline = nil
	s.pipelineMu.Unlock()
	return nil
}

// ClusteringInProgress returns true if k-means is currently running for this service.
// Used to debounce timer ticks and avoid starting a second run while one is in progress.
func (s *Service) ClusteringInProgress() bool {
	return s.kmeansInProgress.Load()
}

// IsClusteringEnabled returns true if GPU clustering is enabled.
func (s *Service) IsClusteringEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clusterEnabled && s.clusterIndex != nil
}

// SetMinEmbeddingsForClustering sets the minimum number of embeddings required
// before k-means clustering is triggered. Below this threshold, brute-force
// search is used as it's faster for small datasets.
//
// This should be called BEFORE TriggerClustering() to take effect.
//
// Parameters:
//   - threshold: Minimum embeddings (must be > 0, default: 1000)
//
// Tuning Guidelines:
//   - 1000 (default): Safe for most workloads
//   - 500-1000: Latency-sensitive applications with moderate data
//   - 100-500: Testing or small datasets
//   - 2000+: Very large datasets, delay clustering until more data arrives
//
// Example:
//
//	svc := search.NewService(engine)
//	svc.SetMinEmbeddingsForClustering(500) // Lower threshold for faster clustering
//	svc.EnableClustering(gpuManager, 100)
//	svc.BuildIndexes(ctx)
//	svc.TriggerClustering(ctx) // Will cluster if >= 500 embeddings
func (s *Service) SetMinEmbeddingsForClustering(threshold int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if threshold > 0 {
		s.minEmbeddingsForClustering = threshold
	}
}

// GetMinEmbeddingsForClustering returns the current minimum embeddings threshold.
func (s *Service) GetMinEmbeddingsForClustering() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.minEmbeddingsForClustering <= 0 {
		return DefaultMinEmbeddingsForClustering
	}
	return s.minEmbeddingsForClustering
}

// SetDefaultMinSimilarity sets the default minimum cosine similarity threshold for vector search.
// Apple Intelligence embeddings produce scores in 0.2-0.8 range, bge-m3/mxbai produce 0.7-0.99.
// Default: 0.0 (let RRF ranking handle relevance filtering)
func (s *Service) SetDefaultMinSimilarity(threshold float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultMinSimilarity = threshold
}

// GetDefaultMinSimilarity returns the configured minimum similarity threshold.
func (s *Service) GetDefaultMinSimilarity() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaultMinSimilarity
}

// SetFulltextIndexPath sets the path for persisting the BM25 fulltext index.
// When both fulltext and vector paths are set, BuildIndexes() will try to load both;
// if both load with count > 0, the full storage iteration is skipped.
func (s *Service) SetFulltextIndexPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fulltextIndexPath = path
}

// SetVectorIndexPath sets the path for persisting the vector index.
// When both fulltext and vector paths are set, BuildIndexes() will try to load both;
// if both load with count > 0, the full storage iteration is skipped.
func (s *Service) SetVectorIndexPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vectorIndexPath = path
}

// SetHNSWIndexPath sets the path for persisting the HNSW index.
// When set with persist search indexes, the HNSW index is saved after build/warmup and
// loaded on startup so the full graph does not need to be rebuilt from vectors.
func (s *Service) SetHNSWIndexPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hnswIndexPath = path
}

// schedulePersist schedules a write of BM25 and vector indexes to disk after an idle delay.
// Called after IndexNode/RemoveNode when paths are set; resets the timer on each mutation
// so we only write after activity settles. No-ops during BuildIndexes (we save at end there).
// Delay is NORNICDB_SEARCH_INDEX_PERSIST_DELAY_SEC (default 30).
func (s *Service) schedulePersist() {
	if s.buildInProgress.Load() {
		return
	}
	s.mu.RLock()
	ftPath := s.fulltextIndexPath
	vPath := s.vectorIndexPath
	s.mu.RUnlock()
	if ftPath == "" || vPath == "" {
		return
	}

	delay := envDurationSec("NORNICDB_SEARCH_INDEX_PERSIST_DELAY_SEC", 30)
	if delay <= 0 {
		delay = 30 * time.Second
	}

	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if s.persistTimer != nil {
		s.persistTimer.Stop()
		s.persistTimer = nil
	}
	s.persistTimer = time.AfterFunc(delay, func() {
		s.persistMu.Lock()
		s.persistTimer = nil
		s.persistMu.Unlock()
		s.runPersist()
	})
}

// runPersist writes the current in-memory BM25, vector, HNSW, and IVF-HNSW indexes to disk.
// Used both by the debounced background timer and on shutdown (via PersistIndexesToDisk).
// Persistence is strategy-based: vectors is always written; hnsw and/or hnsw_ivf/
// when the service uses global HNSW or IVF-HNSW. Does not hold s.mu across I/O.
// Each index Save() copies data under a short read lock then writes without holding the lock,
// so Search and IndexNode remain responsive during persist.
func (s *Service) runPersist() {
	s.mu.RLock()
	ftPath := s.fulltextIndexPath
	vPath := s.vectorIndexPath
	hnswPath := s.hnswIndexPath
	vi := s.vectorIndex
	s.mu.RUnlock()
	if ftPath == "" && vPath == "" && hnswPath == "" {
		return
	}
	if ftPath != "" {
		if err := s.fulltextIndex.Save(ftPath); err != nil {
			log.Printf("⚠️ Background persist: failed to save BM25 index to %s: %v", ftPath, err)
		} else {
			log.Printf("📇 Background persist: BM25 index saved to %s", ftPath)
		}
	}
	if vPath != "" && vi != nil {
		if err := vi.Save(vPath); err != nil {
			log.Printf("⚠️ Background persist: failed to save vector index to %s: %v", vPath, err)
		} else {
			log.Printf("📇 Background persist: vector index saved to %s", vPath)
		}
	}
	// Only persist HNSW when we use HNSW strategy (N >= NSmallMax). Do not build on shutdown to avoid long hangs.
	if hnswPath != "" {
		vecCount := 0
		if vi != nil {
			vecCount = vi.Count()
		}
		if vi == nil || vecCount < NSmallMax {
			log.Printf("📇 Background persist: HNSW skip (vector count %d < %d)", vecCount, NSmallMax)
		} else {
			s.hnswMu.RLock()
			idx := s.hnswIndex
			s.hnswMu.RUnlock()
			if idx == nil {
				log.Printf("📇 Background persist: HNSW skip (index not built; will rebuild on next search)")
			} else {
				if err := idx.Save(hnswPath); err != nil {
					log.Printf("⚠️ Background persist: failed to save HNSW index to %s: %v", hnswPath, err)
				} else {
					log.Printf("📇 Background persist: HNSW index saved to %s", hnswPath)
				}
			}
		}
	}
	// When IVF-HNSW is the strategy, persist per-cluster HNSW and centroids (same dir as hnsw, in hnsw_ivf/).
	if hnswPath != "" {
		s.clusterHNSWMu.RLock()
		clusterHNSW := s.clusterHNSW
		s.clusterHNSWMu.RUnlock()
		if len(clusterHNSW) > 0 {
			if err := SaveIVFHNSW(hnswPath, clusterHNSW); err != nil {
				log.Printf("⚠️ Background persist: failed to save IVF-HNSW clusters to %s: %v", hnswPath, err)
			} else {
				log.Printf("📇 Background persist: IVF-HNSW clusters saved to %s (%d clusters)", hnswPath, len(clusterHNSW))
			}
		}
	}
}

// PersistIndexesToDisk writes the current BM25, vector, HNSW, and IVF-HNSW (per-cluster) indexes to disk immediately.
// Call this on shutdown so the latest in-memory state is saved before exit, same as every other persisted index.
// Cancels any pending debounced persist so no write runs after shutdown.
func (s *Service) PersistIndexesToDisk() {
	log.Printf("📇 Persisting search indexes (BM25, vector, HNSW/IVF-HNSW)...")
	s.persistMu.Lock()
	if s.persistTimer != nil {
		s.persistTimer.Stop()
		s.persistTimer = nil
	}
	s.persistMu.Unlock()
	s.runPersist()
}

// resolveMinSimilarity returns the MinSimilarity to use for a search.
// Priority: explicit opts value > service default > hardcoded fallback (0.5)
func (s *Service) resolveMinSimilarity(opts *SearchOptions) *float64 {
	fallback := 0.5 // hardcoded fallback
	if opts != nil && opts.MinSimilarity != nil {
		return opts.MinSimilarity
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.defaultMinSimilarity >= 0 {
		return &s.defaultMinSimilarity
	}
	return &fallback
}

// ClusterStats returns k-means clustering statistics.
func (s *Service) ClusterStats() *gpu.ClusterStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.clusterIndex == nil {
		return nil
	}
	stats := s.clusterIndex.ClusterStats()
	return &stats
}

// EmbeddingCount returns the total number of nodes with embeddings in the vector index.
func (s *Service) EmbeddingCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.vectorIndex == nil {
		return 0
	}
	return s.vectorIndex.Count()
}

// VectorIndexDimensions returns the configured dimensions of the vector index.
func (s *Service) VectorIndexDimensions() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.vectorIndex == nil {
		return 0
	}
	return s.vectorIndex.GetDimensions()
}

func firstVectorDimensions(node *storage.Node) int {
	if node == nil {
		return 0
	}
	if node.NamedEmbeddings != nil {
		for _, emb := range node.NamedEmbeddings {
			if len(emb) > 0 {
				return len(emb)
			}
		}
	}
	if len(node.ChunkEmbeddings) > 0 {
		for _, emb := range node.ChunkEmbeddings {
			if len(emb) > 0 {
				return len(emb)
			}
		}
	}
	if node.Properties != nil {
		for _, value := range node.Properties {
			vec := toFloat32SliceAny(value)
			if len(vec) > 0 {
				return len(vec)
			}
		}
	}
	return 0
}

func (s *Service) maybeAutoSetVectorDimensions(dimensions int) {
	if s == nil || dimensions <= 0 {
		return
	}

	s.mu.RLock()
	currentDims := 0
	currentCount := 0
	if s.vectorIndex != nil {
		currentDims = s.vectorIndex.GetDimensions()
		currentCount = s.vectorIndex.Count()
	}
	s.mu.RUnlock()

	// Only auto-adjust when the index is still empty (first embedding wins).
	if currentCount > 0 || currentDims == dimensions {
		return
	}

	// Lock order: pipelineMu -> mu -> hnswMu, matching pipeline construction paths.
	s.pipelineMu.Lock()
	s.vectorPipeline = nil
	s.pipelineMu.Unlock()

	s.mu.Lock()
	if s.vectorIndex == nil {
		s.vectorIndex = NewVectorIndex(dimensions)
	} else if s.vectorIndex.Count() == 0 && s.vectorIndex.GetDimensions() != dimensions {
		s.vectorIndex = NewVectorIndex(dimensions)
	}

	// Dimension changes invalidate all vector backends.
	if s.gpuEmbeddingIndex != nil {
		s.gpuEmbeddingIndex.Clear()
		s.gpuEmbeddingIndex = nil
	}
	if s.clusterIndex != nil {
		s.clusterIndex.Clear()
	}
	s.mu.Unlock()

	s.clusterHNSWMu.Lock()
	s.clusterHNSW = nil
	s.clusterHNSWMu.Unlock()

	s.hnswMu.Lock()
	if s.hnswIndex != nil {
		s.hnswIndex.Clear()
		s.hnswIndex = nil
	}
	s.hnswMu.Unlock()
}

// ClearVectorIndex removes all embeddings from the vector index.
// This is used when regenerating all embeddings to reset the index count.
// Also frees memory from HNSW tombstones which can accumulate over time.
func (s *Service) ClearVectorIndex() {
	// Lock order: pipelineMu -> mu -> hnswMu, matching pipeline construction paths.
	s.pipelineMu.Lock()
	s.vectorPipeline = nil
	s.pipelineMu.Unlock()

	s.mu.Lock()
	if s.vectorIndex != nil {
		s.vectorIndex.Clear()
	}
	if s.gpuEmbeddingIndex != nil {
		s.gpuEmbeddingIndex.Clear()
	}
	if s.clusterIndex != nil {
		s.clusterIndex.Clear()
	}
	clear(s.nodeLabels)
	clear(s.nodeNamedVector)
	clear(s.nodePropVector)
	clear(s.nodeChunkVectors)
	s.mu.Unlock()

	s.clusterHNSWMu.Lock()
	s.clusterHNSW = nil
	s.clusterHNSWMu.Unlock()

	// Also clear HNSW index if it exists (frees memory from tombstones).
	// HNSW uses tombstones for deletes which never free memory, so explicit Clear() is required.
	s.hnswMu.Lock()
	if s.hnswIndex != nil {
		s.hnswIndex.Clear()
		s.hnswIndex = nil // Reset to nil so it can be recreated lazily
	}
	s.hnswMu.Unlock()
}

// IndexNode adds a node to all search indexes.
// All embeddings are stored in ChunkEmbeddings (even single chunk = array of 1).
func (s *Service) IndexNode(node *storage.Node) error {
	defer s.schedulePersist()
	if s.resultCache != nil {
		s.resultCache.Invalidate()
	}
	s.maybeAutoSetVectorDimensions(firstVectorDimensions(node))

	s.mu.Lock()
	defer s.mu.Unlock()

	nodeIDStr := string(node.ID)
	if nodeIDStr != "" {
		// CRITICAL: IndexNode is called for both creates and updates.
		// When a node is re-indexed with fewer chunks or fewer named vectors,
		// we must remove the old vector IDs first, otherwise they become orphaned
		// in the in-memory index and EmbeddingCount() will drift upward over time.
		s.removeNodeLocked(nodeIDStr)
	}
	if nodeIDStr != "" {
		labelsCopy := make([]string, len(node.Labels))
		copy(labelsCopy, node.Labels)
		s.nodeLabels[nodeIDStr] = labelsCopy
	}

	// Index all embeddings: NamedEmbeddings and ChunkEmbeddings
	// Strategy:
	//   - NamedEmbeddings: Index each named vector at "node-id-named-{vectorName}"
	//   - ChunkEmbeddings: Index main at node.ID, chunks at "node-id-chunk-N"
	// This allows efficient indexed search for all embedding types
	// Embeddings are stored in struct fields (opaque to users), not in properties

	// Index NamedEmbeddings (each named vector gets its own index entry)
	if len(node.NamedEmbeddings) > 0 {
		for vectorName, embedding := range node.NamedEmbeddings {
			if len(embedding) == 0 {
				continue
			}

			// Index each named embedding with ID: "node-id-named-{vectorName}"
			namedID := fmt.Sprintf("%s-named-%s", node.ID, vectorName)
			if err := s.vectorIndex.Add(namedID, embedding); err != nil {
				if err == ErrDimensionMismatch {
					log.Printf("⚠️ IndexNode %s named[%s]: embedding dimension mismatch (got %d, expected %d)",
						node.ID, vectorName, len(embedding), s.vectorIndex.dimensions)
				}
				continue
			}

			if s.nodeNamedVector[nodeIDStr] == nil {
				s.nodeNamedVector[nodeIDStr] = make(map[string]string, len(node.NamedEmbeddings))
			}
			s.nodeNamedVector[nodeIDStr][vectorName] = namedID

			if s.gpuEmbeddingIndex != nil {
				_ = s.gpuEmbeddingIndex.Add(namedID, embedding) // Best effort
			}

			// Also add/update to HNSW index if it exists
			s.hnswMu.RLock()
			if s.hnswIndex != nil {
				_ = s.hnswIndex.Update(namedID, embedding) // Best effort
			}
			s.hnswMu.RUnlock()

			// Also add to cluster index if enabled
			if s.clusterIndex != nil {
				_ = s.clusterIndex.Add(namedID, embedding) // Best effort
			}
		}
	}

	// Index ChunkEmbeddings (chunked documents)
	// Strategy:
	//   - Index a "main" embedding at node.ID (currently uses chunk 0 as a representative)
	//   - For multi-chunk nodes, index every chunk separately at "node-id-chunk-N"
	//
	// Vector search uses ALL chunk vectors because we overfetch and then collapse
	// chunk IDs back to a unique node ID. The "main" embedding is an additional
	// node-level entry used by some call paths and for compatibility.
	// Chunk embeddings are stored in struct field (opaque to users), not in properties
	if len(node.ChunkEmbeddings) > 0 && len(node.ChunkEmbeddings[0]) > 0 {
		chunkIDs := make([]string, 0, len(node.ChunkEmbeddings)+1)
		// Always index a main embedding at the node ID (using first chunk)
		mainEmbedding := node.ChunkEmbeddings[0]
		if err := s.vectorIndex.Add(string(node.ID), mainEmbedding); err != nil {
			if err == ErrDimensionMismatch {
				log.Printf("⚠️ IndexNode %s main: embedding dimension mismatch (got %d, expected %d)",
					node.ID, len(mainEmbedding), s.vectorIndex.dimensions)
			}
		} else {
			chunkIDs = append(chunkIDs, nodeIDStr) // main ID
			if s.gpuEmbeddingIndex != nil {
				_ = s.gpuEmbeddingIndex.Add(string(node.ID), mainEmbedding) // Best effort
			}

			// Also add/update to HNSW index if it exists (for large datasets)
			s.hnswMu.RLock()
			if s.hnswIndex != nil {
				// Use Update() to handle both new and existing vectors correctly
				_ = s.hnswIndex.Update(string(node.ID), mainEmbedding) // Best effort
			}
			s.hnswMu.RUnlock()

			// Also add to cluster index if enabled
			if s.clusterIndex != nil {
				_ = s.clusterIndex.Add(string(node.ID), mainEmbedding) // Best effort
			}
		}

		// For multi-chunk nodes, also index each chunk separately with chunk suffix
		// This allows granular search at the chunk level while maintaining node-level search
		// Note: chunk 0 is indexed both as main (node.ID) and as chunk-0 for consistency
		// ALL chunks are indexed in vectorIndex, HNSW, and clusterIndex for complete search coverage
		if len(node.ChunkEmbeddings) > 1 {
			for i, embedding := range node.ChunkEmbeddings {
				if len(embedding) > 0 {
					chunkID := fmt.Sprintf("%s-chunk-%d", node.ID, i)
					if err := s.vectorIndex.Add(chunkID, embedding); err != nil {
						if err == ErrDimensionMismatch {
							log.Printf("⚠️ IndexNode %s chunk %d: embedding dimension mismatch (got %d, expected %d)",
								node.ID, i, len(embedding), s.vectorIndex.dimensions)
						}
						// Continue indexing other chunks even if one fails
						continue
					}
					chunkIDs = append(chunkIDs, chunkID)

					if s.gpuEmbeddingIndex != nil {
						_ = s.gpuEmbeddingIndex.Add(chunkID, embedding) // Best effort
					}

					// Also add to cluster index if enabled
					if s.clusterIndex != nil {
						_ = s.clusterIndex.Add(chunkID, embedding) // Best effort
					}
				}
			}
		}
		if len(chunkIDs) > 0 {
			s.nodeChunkVectors[nodeIDStr] = chunkIDs
		}
	}

	// Index vector-shaped property values for Cypher compatibility.
	// These are indexed under IDs: "node-id-prop-{propertyKey}".
	if node.Properties != nil {
		for key, value := range node.Properties {
			vec := toFloat32SliceAny(value)
			if len(vec) == 0 || len(vec) != s.vectorIndex.dimensions {
				continue
			}
			propID := fmt.Sprintf("%s-prop-%s", nodeIDStr, key)
			if err := s.vectorIndex.Add(propID, vec); err != nil {
				continue
			}
			if s.nodePropVector[nodeIDStr] == nil {
				s.nodePropVector[nodeIDStr] = make(map[string]string, 4)
			}
			s.nodePropVector[nodeIDStr][key] = propID
			if s.gpuEmbeddingIndex != nil {
				_ = s.gpuEmbeddingIndex.Add(propID, vec) // Best effort
			}
			s.hnswMu.RLock()
			if s.hnswIndex != nil {
				_ = s.hnswIndex.Update(propID, vec) // Best effort
			}
			s.hnswMu.RUnlock()
			if s.clusterIndex != nil {
				_ = s.clusterIndex.Add(propID, vec) // Best effort
			}
		}
	}

	// Add to fulltext index
	text := s.extractSearchableText(node)
	if text != "" {
		s.fulltextIndex.Index(string(node.ID), text)
	}

	return nil
}

// removeNodeLocked removes a node from all search indexes.
// Caller MUST hold s.mu (write lock).
//
// This is used by both RemoveNode (delete path) and IndexNode (update path) to ensure
// vector IDs never become orphaned when embeddings change shape over time.
func (s *Service) removeNodeLocked(nodeIDStr string) {
	if nodeIDStr == "" {
		return
	}

	// Remove main embedding
	if s.vectorIndex != nil {
		s.vectorIndex.Remove(nodeIDStr)
	}
	if s.gpuEmbeddingIndex != nil {
		_ = s.gpuEmbeddingIndex.Remove(nodeIDStr)
	}
	if s.fulltextIndex != nil {
		s.fulltextIndex.Remove(nodeIDStr)
	}

	// Also remove from HNSW index if it exists
	s.hnswMu.RLock()
	if s.hnswIndex != nil {
		s.hnswIndex.Remove(nodeIDStr)
	}
	s.hnswMu.RUnlock()

	// Also remove from cluster index if enabled
	if s.clusterIndex != nil {
		s.clusterIndex.Remove(nodeIDStr)
	}

	delete(s.nodeLabels, nodeIDStr)

	// Remove property vectors tracked for Cypher compatibility.
	if props := s.nodePropVector[nodeIDStr]; len(props) > 0 {
		for _, propID := range props {
			if s.vectorIndex != nil {
				s.vectorIndex.Remove(propID)
			}
			if s.gpuEmbeddingIndex != nil {
				_ = s.gpuEmbeddingIndex.Remove(propID)
			}
			s.hnswMu.RLock()
			if s.hnswIndex != nil {
				s.hnswIndex.Remove(propID)
			}
			s.hnswMu.RUnlock()
			if s.clusterIndex != nil {
				s.clusterIndex.Remove(propID)
			}
		}
	}
	delete(s.nodePropVector, nodeIDStr)

	// Remove all named embeddings (they're indexed as "node-id-named-{vectorName}")
	if named := s.nodeNamedVector[nodeIDStr]; len(named) > 0 {
		for _, namedID := range named {
			if s.vectorIndex != nil {
				s.vectorIndex.Remove(namedID)
			}
			if s.gpuEmbeddingIndex != nil {
				_ = s.gpuEmbeddingIndex.Remove(namedID)
			}

			// Also remove from HNSW index if it exists
			s.hnswMu.RLock()
			if s.hnswIndex != nil {
				s.hnswIndex.Remove(namedID)
			}
			s.hnswMu.RUnlock()

			if s.clusterIndex != nil {
				s.clusterIndex.Remove(namedID)
			}
		}
	}
	delete(s.nodeNamedVector, nodeIDStr)

	// Remove all chunk embeddings (they're indexed as "node-id-chunk-0", "node-id-chunk-1", etc.)
	if chunkIDs := s.nodeChunkVectors[nodeIDStr]; len(chunkIDs) > 0 {
		for _, chunkID := range chunkIDs {
			if s.vectorIndex != nil {
				s.vectorIndex.Remove(chunkID)
			}
			if s.gpuEmbeddingIndex != nil {
				_ = s.gpuEmbeddingIndex.Remove(chunkID)
			}

			// Also remove from HNSW index if it exists
			s.hnswMu.RLock()
			if s.hnswIndex != nil {
				s.hnswIndex.Remove(chunkID)
			}
			s.hnswMu.RUnlock()

			if s.clusterIndex != nil {
				s.clusterIndex.Remove(chunkID)
			}
		}
	}
	delete(s.nodeChunkVectors, nodeIDStr)
}

// RemoveNode removes a node from all search indexes.
// Also removes all chunk embeddings (for nodes with multiple chunks).
func (s *Service) RemoveNode(nodeID storage.NodeID) error {
	defer s.schedulePersist()
	if s.resultCache != nil {
		s.resultCache.Invalidate()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeNodeLocked(string(nodeID))

	return nil
}

// handleOrphanedEmbedding handles the case where a vector/index hit refers to a node
// that no longer exists in storage (orphaned embedding). If err is storage.ErrNotFound,
// it logs once per node ID (when seenOrphans is provided), removes all embeddings for
// that node from indexes via RemoveNode, and returns true so the caller can skip the result.
// If seenOrphans is non-nil and the node ID was already seen this request, it returns true
// without logging or removing again. If err is not ErrNotFound, returns false.
func (s *Service) handleOrphanedEmbedding(ctx context.Context, nodeIDStr string, err error, seenOrphans map[string]bool) bool {
	if !errors.Is(err, storage.ErrNotFound) {
		return false
	}
	if seenOrphans != nil && seenOrphans[nodeIDStr] {
		return true // already logged and removed this request
	}
	log.Printf("[search] orphaned embedding detected, removing from indexes: nodeID=%s", nodeIDStr)
	if removeErr := s.RemoveNode(storage.NodeID(nodeIDStr)); removeErr != nil {
		log.Printf("[search] failed to remove orphaned embedding for nodeID=%s: %v", nodeIDStr, removeErr)
	}
	if seenOrphans != nil {
		seenOrphans[nodeIDStr] = true
	}
	return true
}

// NodeIterator is an interface for streaming node iteration.
type NodeIterator interface {
	IterateNodes(fn func(*storage.Node) bool) error
}

// BuildIndexes builds search indexes from all nodes in the engine.
// Call this after storage (and WAL recovery) so indexes reflect durable state.
// When both fulltext and vector index paths are set, tries to load both from disk;
// if both load with count > 0 (and semver format version matches), the full iteration
// is skipped. Otherwise iterates over storage and saves both indexes at the end when paths are set.
func (s *Service) BuildIndexes(ctx context.Context) error {
	s.buildInProgress.Store(true)
	defer s.buildInProgress.Store(false)

	s.mu.RLock()
	fulltextPath := s.fulltextIndexPath
	vectorPath := s.vectorIndexPath
	s.mu.RUnlock()

	skipIteration := false

	if fulltextPath != "" {
		_ = s.fulltextIndex.Load(fulltextPath)
	}
	if vectorPath != "" && s.vectorIndex != nil {
		_ = s.vectorIndex.Load(vectorPath)
	}
	// When both paths are set and both indexes have content, skip the full iteration.
	if fulltextPath != "" && vectorPath != "" && s.fulltextIndex.Count() > 0 && s.vectorIndex != nil && s.vectorIndex.Count() > 0 {
		skipIteration = true
		log.Printf("📇 Search indexes loaded from disk (BM25: %d docs, vector: %d); skipping rebuild",
			s.fulltextIndex.Count(), s.vectorIndex.Count())
	}

	// When skipping iteration and we use HNSW strategy (N >= NSmallMax), load HNSW from disk so warmup does not rebuild it.
	s.mu.RLock()
	hnswPath := s.hnswIndexPath
	s.mu.RUnlock()
	if skipIteration && hnswPath != "" && s.vectorIndex != nil && s.vectorIndex.Count() >= NSmallMax {
		vi := s.vectorIndex
		loaded, err := LoadHNSWIndex(hnswPath, vi.GetVector)
		if err == nil && loaded != nil && loaded.GetDimensions() == vi.GetDimensions() {
			s.hnswMu.Lock()
			s.hnswIndex = loaded
			s.hnswMu.Unlock()
			log.Printf("📇 HNSW index loaded from disk: vectors=%d tombstone_ratio=%.2f", loaded.Size(), loaded.TombstoneRatio())
		}
	}

	if skipIteration {
		s.warmupVectorPipeline(ctx)
		if hnswPath != "" && s.vectorIndex != nil && s.vectorIndex.Count() >= NSmallMax {
			s.hnswMu.RLock()
			idx := s.hnswIndex
			s.hnswMu.RUnlock()
			if idx != nil {
				if err := idx.Save(hnswPath); err != nil {
					log.Printf("⚠️ Failed to save HNSW index to %s: %v", hnswPath, err)
				} else {
					log.Printf("📇 HNSW index saved to %s", hnswPath)
				}
			}
		}
		if hnswPath != "" {
			s.clusterHNSWMu.RLock()
			clusterHNSW := s.clusterHNSW
			s.clusterHNSWMu.RUnlock()
			if len(clusterHNSW) > 0 {
				if err := SaveIVFHNSW(hnswPath, clusterHNSW); err != nil {
					log.Printf("⚠️ Failed to save HNSW index to %s: %v", hnswPath, err)
				} else {
					log.Printf("📇 HNSW index saved to %s", hnswPath)
				}
			}
		}
		return nil
	}

	// Build indexes by iterating over storage.
	if iterator, ok := s.engine.(NodeIterator); ok {
		count := 0
		err := iterator.IterateNodes(func(node *storage.Node) bool {
			select {
			case <-ctx.Done():
				return false
			default:
				_ = s.IndexNode(node)
				count++
				if count%1000 == 0 {
					fmt.Printf("📊 Indexed %d nodes...\n", count)
				}
				return true
			}
		})
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Printf("📊 Indexed %d total nodes\n", count)
		if fulltextPath != "" {
			if err := s.fulltextIndex.Save(fulltextPath); err != nil {
				log.Printf("⚠️ Failed to save BM25 index to %s: %v", fulltextPath, err)
			} else {
				log.Printf("📇 BM25 index saved to %s", fulltextPath)
			}
		}
		if vectorPath != "" && s.vectorIndex != nil {
			if err := s.vectorIndex.Save(vectorPath); err != nil {
				log.Printf("⚠️ Failed to save vector index to %s: %v", vectorPath, err)
			} else {
				log.Printf("📇 Vector index saved to %s", vectorPath)
			}
		}
		s.warmupVectorPipeline(ctx)
		if hnswPath != "" && s.vectorIndex != nil && s.vectorIndex.Count() >= NSmallMax {
			s.hnswMu.RLock()
			idx := s.hnswIndex
			s.hnswMu.RUnlock()
			if idx != nil {
				if err := idx.Save(hnswPath); err != nil {
					log.Printf("⚠️ Failed to save HNSW index to %s: %v", hnswPath, err)
				} else {
					log.Printf("📇 HNSW index saved to %s", hnswPath)
				}
			}
		}
		if hnswPath != "" {
			s.clusterHNSWMu.RLock()
			clusterHNSW := s.clusterHNSW
			s.clusterHNSWMu.RUnlock()
			if len(clusterHNSW) > 0 {
				if err := SaveIVFHNSW(hnswPath, clusterHNSW); err != nil {
					log.Printf("⚠️ Failed to save HNSW index to %s: %v", hnswPath, err)
				} else {
					log.Printf("📇 HNSW index saved to %s", hnswPath)
				}
			}
		}
		return nil
	}

	count := 0
	err := storage.StreamNodesWithFallback(ctx, s.engine, 1000, func(node *storage.Node) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.IndexNode(node); err != nil {
			return nil
		}
		count++
		if count%1000 == 0 {
			fmt.Printf("📊 Indexed %d nodes...\n", count)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("📊 Indexed %d total nodes\n", count)
	if fulltextPath != "" {
		if err := s.fulltextIndex.Save(fulltextPath); err != nil {
			log.Printf("⚠️ Failed to save BM25 index to %s: %v", fulltextPath, err)
		} else {
			log.Printf("📇 BM25 index saved to %s", fulltextPath)
		}
	}
	if vectorPath != "" && s.vectorIndex != nil {
		if err := s.vectorIndex.Save(vectorPath); err != nil {
			log.Printf("⚠️ Failed to save vector index to %s: %v", vectorPath, err)
		} else {
			log.Printf("📇 Vector index saved to %s", vectorPath)
		}
	}
	s.warmupVectorPipeline(ctx)
	if hnswPath != "" && s.vectorIndex != nil && s.vectorIndex.Count() >= NSmallMax {
		s.hnswMu.RLock()
		idx := s.hnswIndex
		s.hnswMu.RUnlock()
		if idx != nil {
			if err := idx.Save(hnswPath); err != nil {
				log.Printf("⚠️ Failed to save HNSW index to %s: %v", hnswPath, err)
			} else {
				log.Printf("📇 HNSW index saved to %s", hnswPath)
			}
		}
	}
	if hnswPath != "" {
		s.clusterHNSWMu.RLock()
		clusterHNSW := s.clusterHNSW
		s.clusterHNSWMu.RUnlock()
		if len(clusterHNSW) > 0 {
			if err := SaveIVFHNSW(hnswPath, clusterHNSW); err != nil {
				log.Printf("⚠️ Failed to save HNSW index to %s: %v", hnswPath, err)
			} else {
				log.Printf("📇 HNSW index saved to %s", hnswPath)
			}
		}
	}
	return nil
}

// warmupVectorPipeline creates the vector search pipeline (and builds HNSW or IVF-HNSW if needed) so that
// the first user search is fast. When clustering is enabled and there are enough embeddings, runs
// k-means and builds per-cluster HNSW (IVF-HNSW) first so the pipeline uses IVF-HNSW instead of global HNSW.
func (s *Service) warmupVectorPipeline(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	n := 0
	if s.vectorIndex != nil {
		n = s.vectorIndex.Count()
	}
	if n == 0 {
		return
	}
	log.Printf("🔍 Warming up vector search pipeline (%d vectors)...", n)
	start := time.Now()

	// When clustering is enabled and we have enough embeddings, restore or run k-means and build IVF-HNSW.
	// If centroids are persisted (from previous IVF-HNSW save), load them and skip k-means so warmup is fast.
	if s.IsClusteringEnabled() && n >= s.GetMinEmbeddingsForClustering() {
		s.mu.RLock()
		clusterIndex := s.clusterIndex
		hnswPath := s.hnswIndexPath
		s.mu.RUnlock()
		if clusterIndex != nil && clusterIndex.Count() < n {
			// Backfill cluster index from vector index (e.g. after loading indexes from disk).
			s.vectorIndex.mu.RLock()
			ids := make([]string, 0, len(s.vectorIndex.vectors))
			embs := make([][]float32, 0, len(s.vectorIndex.vectors))
			for id, vec := range s.vectorIndex.vectors {
				if len(vec) == 0 {
					continue
				}
				copyVec := make([]float32, len(vec))
				copy(copyVec, vec)
				ids = append(ids, id)
				embs = append(embs, copyVec)
			}
			s.vectorIndex.mu.RUnlock()
			_ = clusterIndex.AddBatch(ids, embs)
			log.Printf("🔍 Backfilled cluster index with %d vectors for IVF-HNSW warmup", len(ids))
		}
		// Skip k-means only on initial load when we have persisted IVF-HNSW cluster files.
		// Centroids and idToCluster are derived from hnsw_ivf/ cluster files + vectors (graph-only).
		// The re-cluster timer still runs later and will re-run k-means when enabled.
		restoredFromDisk := false
		if clusterIndex != nil && hnswPath != "" && s.vectorIndex != nil {
			vectorLookup := s.vectorIndex.GetVector
			if vectorLookup != nil {
				centroids, idToCluster, deriveErr := DeriveIVFCentroidsFromClusters(hnswPath, vectorLookup)
				if len(centroids) > 0 && len(idToCluster) > 0 {
					if err := clusterIndex.RestoreClusteringState(centroids, idToCluster); err == nil {
						if err := s.rebuildClusterHNSWIndexes(ctx, clusterIndex); err == nil {
							log.Printf("🔍 IVF-HNSW restored from disk (%d clusters); skipping k-means", len(centroids))
							restoredFromDisk = true
						}
					}
				} else if !restoredFromDisk {
					log.Printf("[IVF-HNSW] ⚠️ Could not restore from hnsw_ivf (path=%q); running k-means. deriveErr=%v centroids=%d idToCluster=%d",
						hnswPath, deriveErr, len(centroids), len(idToCluster))
				}
			}
		}
		if !restoredFromDisk {
			if err := s.TriggerClustering(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("⚠️ Clustering during warmup failed (will use global HNSW): %v", err)
			}
		}
	}

	if _, err := s.getOrCreateVectorPipeline(); err != nil {
		log.Printf("⚠️ Vector pipeline warmup failed (first search may be slow): %v", err)
		return
	}
	log.Printf("🔍 Vector search pipeline ready in %v", time.Since(start))
}

// Search performs hybrid search with automatic fallback.
//
// Search strategy:
//  1. Try RRF hybrid search (vector + BM25) if embedding provided
//  2. Fall back to vector-only if RRF returns no results
//  3. Fall back to BM25-only if vector search fails or no embedding
//
// This ensures you always get results even if one index is empty or fails.
//
// Parameters:
//   - ctx: Context for cancellation
//   - query: Text query for BM25 search
//   - embedding: Vector embedding for similarity search (can be nil)
//   - opts: Search options (use DefaultSearchOptions() if unsure)
//
// Example:
//
//	svc := search.NewService(engine)
//
//	// Hybrid search (best results)
//	query := "graph database memory"
//	embedding, _ := embedder.Embed(ctx, query)
//	opts := search.DefaultSearchOptions()
//	opts.Limit = 10
//
//	resp, err := svc.Search(ctx, query, embedding, opts)
//	if err != nil {
//		return err
//	}
//
//	fmt.Printf("Found %d results using %s\n",
//		resp.Returned, resp.SearchMethod)
//
//	for i, result := range resp.Results {
//		fmt.Printf("%d. [RRF: %.4f] %s\n",
//			i+1, result.RRFScore, result.Title)
//		fmt.Printf("   Vector rank: #%d, BM25 rank: #%d\n",
//			result.VectorRank, result.BM25Rank)
//	}
//
// Returns a SearchResponse with ranked results and metadata about the search method used.
func (s *Service) Search(ctx context.Context, query string, embedding []float32, opts *SearchOptions) (*SearchResponse, error) {
	if opts == nil {
		opts = DefaultSearchOptions()
	}

	// Set resolved value back for downstream use
	opts.MinSimilarity = s.resolveMinSimilarity(opts)

	// Cache key for result cache (same query+options => same key; used for Get and Put).
	cacheKey := searchCacheKey(query, opts)
	if s.resultCache != nil {
		if cached := s.resultCache.Get(cacheKey); cached != nil {
			return cached, nil
		}
	}

	// If no embedding provided, fall back to full-text only
	if len(embedding) == 0 {
		resp, err := s.fullTextSearchOnly(ctx, query, opts)
		if err == nil && s.resultCache != nil {
			s.resultCache.Put(cacheKey, resp)
		}
		return resp, err
	}

	// For vector-only calls (no text query), skip hybrid and go straight to
	// vector search. This avoids unnecessary BM25+RRF overhead and matches the
	// intended semantics of "pure embedding" search.
	if strings.TrimSpace(query) == "" {
		resp, err := s.vectorSearchOnly(ctx, embedding, opts)
		if err == nil && s.resultCache != nil {
			s.resultCache.Put(cacheKey, resp)
		}
		return resp, err
	}

	// Try RRF hybrid search
	response, err := s.rrfHybridSearch(ctx, query, embedding, opts)
	if err == nil && len(response.Results) > 0 {
		if s.resultCache != nil {
			s.resultCache.Put(cacheKey, response)
		}
		return response, nil
	}

	// Fallback to vector-only
	response, err = s.vectorSearchOnly(ctx, embedding, opts)
	if err == nil && len(response.Results) > 0 {
		response.FallbackTriggered = true
		response.Message = "RRF search returned no results, fell back to vector search"
		if s.resultCache != nil {
			s.resultCache.Put(cacheKey, response)
		}
		return response, nil
	}

	// Final fallback to full-text
	resp, err := s.fullTextSearchOnly(ctx, query, opts)
	if err == nil && s.resultCache != nil {
		s.resultCache.Put(cacheKey, resp)
	}
	return resp, err
}

// rrfHybridSearch performs Reciprocal Rank Fusion combining vector and BM25 results.
func (s *Service) rrfHybridSearch(ctx context.Context, query string, embedding []float32, opts *SearchOptions) (*SearchResponse, error) {
	// Avoid holding s.mu while interacting with the vector pipeline (pipelineMu),
	// otherwise we can deadlock with writers that lock pipelineMu and then need s.mu.
	// See: maybeAutoSetVectorDimensions(), ClearVectorIndex(), SetGPUManager().
	s.mu.RLock()
	reranker := s.reranker
	fulltextIndex := s.fulltextIndex
	s.mu.RUnlock()

	// Get more candidates for better fusion
	vectorCandidateLimit := vectorOverfetchLimit(opts.Limit)
	bm25CandidateLimit := opts.Limit * 2
	if bm25CandidateLimit < 20 {
		bm25CandidateLimit = 20
	}

	// Step 1: Vector search
	var vectorResults []indexResult
	pipeline, pipelineErr := s.getOrCreateVectorPipeline()
	if pipelineErr != nil {
		return nil, pipelineErr
	}
	scored, searchErr := pipeline.Search(ctx, embedding, vectorCandidateLimit, opts.GetMinSimilarity(0.5))
	if searchErr != nil {
		return nil, searchErr
	}
	for _, r := range scored {
		vectorResults = append(vectorResults, indexResult{ID: r.ID, Score: r.Score})
	}

	// Step 2: BM25 full-text search (skip if no full-text index; ranks will have vector only)
	var bm25Results []indexResult
	if fulltextIndex != nil {
		bm25Results = fulltextIndex.Search(query, bm25CandidateLimit)
	}

	// Collapse vector IDs back to unique node IDs.
	vectorResults = collapseIndexResultsByNodeID(vectorResults)

	seenOrphans := make(map[string]bool)

	// Step 3: Filter by type if specified
	if len(opts.Types) > 0 {
		vectorResults = s.filterByType(ctx, vectorResults, opts.Types, seenOrphans)
		bm25Results = s.filterByType(ctx, bm25Results, opts.Types, seenOrphans)
	}

	// Step 4: Fuse with RRF
	fusedResults := s.fuseRRF(vectorResults, bm25Results, opts)

	// Step 5: Apply MMR diversification if enabled
	searchMethod := "rrf_hybrid"
	message := "Reciprocal Rank Fusion (Vector + BM25)"
	switch pipeline.candidateGen.(type) {
	case *KMeansCandidateGen:
		searchMethod = "rrf_hybrid_clustered"
		message = "RRF (K-means routed vector + BM25)"
	case *IVFHNSWCandidateGen:
		searchMethod = "rrf_hybrid_ivf_hnsw"
		message = "RRF (IVF-HNSW vector + BM25)"
	}
	if opts.MMREnabled && len(embedding) > 0 {
		fusedResults = s.applyMMR(ctx, fusedResults, embedding, opts.Limit, opts.MMRLambda, seenOrphans)
		searchMethod += "+mmr"
		message = fmt.Sprintf("%s + MMR diversification (λ=%.2f)", message, opts.MMRLambda)
	}

	// Step 6: Stage-2 reranking (optional)
	if opts.RerankEnabled && reranker != nil && reranker.Enabled() {
		fusedResults = s.applyStage2Rerank(ctx, query, fusedResults, opts, seenOrphans, reranker)
		if searchMethod == "rrf_hybrid" {
			searchMethod = "rrf_hybrid+rerank"
			message = fmt.Sprintf("RRF + Reranking (%s)", reranker.Name())
		} else {
			searchMethod += "+rerank"
			message += fmt.Sprintf(" + Reranking (%s)", reranker.Name())
		}
	}

	// Step 7: Convert to SearchResult and enrich with node data
	results := s.enrichResults(ctx, fusedResults, opts.Limit, seenOrphans)

	return &SearchResponse{
		Status:          "success",
		Query:           query,
		Results:         results,
		TotalCandidates: len(fusedResults),
		Returned:        len(results),
		SearchMethod:    searchMethod,
		Message:         message,
		Metrics: &SearchMetrics{
			VectorCandidates: len(vectorResults),
			BM25Candidates:   len(bm25Results),
			FusedCandidates:  len(fusedResults),
		},
	}, nil
}

// SearchCandidate is a lightweight vector-search result: just the ID and score.
//
// This is intended for high-throughput call paths that don’t require node
// enrichment (e.g. Qdrant-compatible gRPC searches that return IDs+scores).
type SearchCandidate struct {
	ID    string
	Score float64
}

// VectorSearchCandidates performs vector-only search and returns lightweight
// candidates without enrichment. It is optimized for throughput: it skips BM25,
// RRF fusion, and storage fetches.
//
// This method uses the unified vector search pipeline (CandidateGen + ExactScore)
// with automatic strategy selection (brute-force for small N, HNSW for large N).
func (s *Service) VectorSearchCandidates(ctx context.Context, embedding []float32, opts *SearchOptions) ([]SearchCandidate, error) {
	if opts == nil {
		opts = DefaultSearchOptions()
	}

	opts.MinSimilarity = s.resolveMinSimilarity(opts)
	if len(embedding) == 0 {
		return nil, fmt.Errorf("vector search requires embedding")
	}

	candidateLimit := vectorOverfetchLimit(opts.Limit)

	// Use unified vector search pipeline
	pipeline, err := s.getOrCreateVectorPipeline()
	if err != nil {
		return nil, fmt.Errorf("failed to get vector pipeline: %w", err)
	}

	scored, err := pipeline.Search(ctx, embedding, candidateLimit, opts.GetMinSimilarity(0.5))
	if err != nil {
		return nil, err
	}

	// Convert to SearchCandidate
	candidates := make([]SearchCandidate, len(scored))
	for i, s := range scored {
		candidates[i] = SearchCandidate{ID: s.ID, Score: s.Score}
	}

	candidates = collapseCandidatesByNodeID(candidates)

	seenOrphans := make(map[string]bool)
	// Apply type filters
	if len(opts.Types) > 0 {
		candidates = s.filterCandidatesByType(ctx, candidates, opts.Types, seenOrphans)
	}

	if len(candidates) > opts.Limit && opts.Limit > 0 {
		candidates = candidates[:opts.Limit]
	}

	return candidates, nil
}

func vectorOverfetchLimit(limit int) int {
	// Vector IDs in the index may represent:
	// - node IDs (main embedding)
	// - chunk IDs ("nodeID-chunk-i")
	// - named embedding IDs ("nodeID-named-name")
	//
	// We overfetch and then collapse back to unique node IDs.
	if limit <= 0 {
		return 20
	}
	over := limit * 10
	if over < limit {
		over = limit
	}
	// Keep worst-case work bounded for brute-force paths.
	if over > 5000 {
		return 5000
	}
	if over < 50 {
		return 50
	}
	return over
}

func normalizeVectorResultIDToNodeID(id string) string {
	// Chunk IDs are formatted as: "{nodeID}-chunk-{i}"
	// Named IDs are formatted as: "{nodeID}-named-{vectorName}"
	// Property-vector IDs are formatted as: "{nodeID}-prop-{propertyKey}"
	//
	// Node IDs can contain '-' (UUIDs etc.), so only treat these as suffixes when they match
	// the known patterns (and for chunks: when the suffix is an integer).
	if idx := strings.LastIndex(id, "-chunk-"); idx >= 0 {
		suffix := id[idx+len("-chunk-"):]
		if suffix != "" {
			if _, err := strconv.Atoi(suffix); err == nil {
				return id[:idx]
			}
		}
	}
	if idx := strings.LastIndex(id, "-named-"); idx >= 0 {
		suffix := id[idx+len("-named-"):]
		if suffix != "" {
			return id[:idx]
		}
	}
	if idx := strings.LastIndex(id, "-prop-"); idx >= 0 {
		suffix := id[idx+len("-prop-"):]
		if suffix != "" {
			return id[:idx]
		}
	}
	return id
}

func collapseCandidatesByNodeID(cands []SearchCandidate) []SearchCandidate {
	if len(cands) == 0 {
		return nil
	}
	best := make(map[string]float64, len(cands))
	for _, c := range cands {
		nodeID := normalizeVectorResultIDToNodeID(c.ID)
		if prev, ok := best[nodeID]; !ok || c.Score > prev {
			best[nodeID] = c.Score
		}
	}
	out := make([]SearchCandidate, 0, len(best))
	for id, score := range best {
		out = append(out, SearchCandidate{ID: id, Score: score})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func collapseIndexResultsByNodeID(results []indexResult) []indexResult {
	if len(results) == 0 {
		return nil
	}
	best := make(map[string]float64, len(results))
	for _, r := range results {
		nodeID := normalizeVectorResultIDToNodeID(r.ID)
		if prev, ok := best[nodeID]; !ok || r.Score > prev {
			best[nodeID] = r.Score
		}
	}
	out := make([]indexResult, 0, len(best))
	for id, score := range best {
		out = append(out, indexResult{ID: id, Score: score})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// getOrCreateVectorPipeline returns the vector search pipeline, creating it if needed.
//
// The pipeline uses auto strategy selection:
//   - GPU brute-force (exact) when enabled and within configured thresholds
//   - Cluster routing when clustered (GPU ScoreSubset when GPU enabled but full brute is out-of-range;
//     otherwise CPU IVF-HNSW when available, else CPU k-means routing)
//   - CPU brute-force for small datasets (N < NSmallMax)
//   - Global HNSW for large datasets (N >= NSmallMax, no clustering)
func (s *Service) getOrCreateVectorPipeline() (*VectorSearchPipeline, error) {
	s.pipelineMu.RLock()
	if s.vectorPipeline != nil {
		s.pipelineMu.RUnlock()
		return s.vectorPipeline, nil
	}
	s.pipelineMu.RUnlock()

	s.pipelineMu.Lock()
	defer s.pipelineMu.Unlock()

	// Double-check after acquiring write lock
	if s.vectorPipeline != nil {
		return s.vectorPipeline, nil
	}

	s.mu.RLock()
	vectorCount := s.vectorIndex.Count()
	dimensions := s.vectorIndex.GetDimensions()
	s.mu.RUnlock()

	// Auto strategy: choose candidate generator based on dataset size and clustering
	var candidateGen CandidateGenerator
	var strategyName string

	gpuEnabled := s.gpuManager != nil && s.gpuManager.IsEnabled() && s.gpuEmbeddingIndex != nil
	gpuMinN := envInt("NORNICDB_VECTOR_GPU_BRUTE_MIN_N", 5000)
	gpuMaxN := envInt("NORNICDB_VECTOR_GPU_BRUTE_MAX_N", 100000)

	// Prefer GPU brute-force (exact) when enabled and within configured thresholds.
	// This path is exact and typically highest-throughput within its tuned N range.
	if gpuEnabled && vectorCount >= gpuMinN && vectorCount <= gpuMaxN {
		candidateGen = NewGPUBruteForceCandidateGen(s.gpuEmbeddingIndex)
		strategyName = "GPU brute-force"
	} else if vectorCount < NSmallMax {
		// Small dataset: use brute-force on CPU (exact).
		// Even if clustering is available, centroid routing reduces recall and adds overhead at small N.
		candidateGen = NewBruteForceCandidateGen(s.vectorIndex)
		strategyName = "CPU brute-force"
	} else if s.clusterIndex != nil && s.clusterIndex.IsClustered() {
		// If clustering is enabled and clusters are built, use centroid routing on CPU
		// (GPU subset scoring when GPU is enabled but full brute is out-of-range, else IVF-HNSW when available,
		// else CPU k-means candidate generation).
		numClustersToSearch := 3 // Default: search 3 nearest clusters

		// When GPU is enabled but full brute-force is not selected (e.g. N too large),
		// still use k-means centroids to route and score only the cluster subset.
		// ScoreSubset uses GPU when possible and falls back to CPU gracefully.
		if gpuEnabled && vectorCount > gpuMaxN {
			candidateGen = NewGPUKMeansCandidateGen(s.clusterIndex, numClustersToSearch)
			strategyName = "GPU k-means (cluster routing)"
		} else {
			if envBool("NORNICDB_VECTOR_IVF_HNSW_ENABLED", true) && !gpuEnabled {
				s.clusterHNSWMu.RLock()
				hasClusterHNSW := len(s.clusterHNSW) > 0
				s.clusterHNSWMu.RUnlock()
				if hasClusterHNSW {
					candidateGen = NewIVFHNSWCandidateGen(s.clusterIndex, func(clusterID int) *HNSWIndex {
						s.clusterHNSWMu.RLock()
						defer s.clusterHNSWMu.RUnlock()
						return s.clusterHNSW[clusterID]
					}, numClustersToSearch)
					strategyName = "IVF-HNSW (per-cluster HNSW)"
				}
			}
			if candidateGen == nil {
				candidateGen = NewKMeansCandidateGen(s.clusterIndex, s.vectorIndex, numClustersToSearch)
				strategyName = "CPU k-means (cluster routing)"
			}
		}
	} else {
		// Large dataset: use HNSW (lazy-initialize if needed)
		hnswIndex, err := s.getOrCreateHNSWIndex(dimensions)
		if err != nil {
			return nil, fmt.Errorf("failed to create HNSW index: %w", err)
		}
		candidateGen = NewHNSWCandidateGen(hnswIndex)
		strategyName = "HNSW"
	}

	log.Printf("🔍 Vector search strategy: %s (N=%d vectors)", strategyName, vectorCount)

	// Exact scoring defaults to SIMD CPU scoring.
	var exactScorer ExactScorer
	switch candidateGen.(type) {
	case *GPUBruteForceCandidateGen, *GPUKMeansCandidateGen:
		exactScorer = &IdentityExactScorer{}
	default:
		exactScorer = NewCPUExactScorer(s.vectorIndex)
	}

	s.vectorPipeline = NewVectorSearchPipeline(candidateGen, exactScorer)
	return s.vectorPipeline, nil
}

// getOrCreateHNSWIndex returns the HNSW index, creating it if needed.
func (s *Service) getOrCreateHNSWIndex(dimensions int) (*HNSWIndex, error) {
	s.hnswMu.RLock()
	if s.hnswIndex != nil {
		s.hnswMu.RUnlock()
		return s.hnswIndex, nil
	}
	s.hnswMu.RUnlock()

	// Snapshot the current vector index contents without holding hnswMu to avoid
	// lock inversion with writers (IndexNode holds s.mu then takes hnswMu).
	type vecPair struct {
		id  string
		vec []float32
	}

	s.mu.RLock()
	vi := s.vectorIndex
	s.mu.RUnlock()
	if vi == nil {
		return nil, fmt.Errorf("vector index is nil")
	}

	vi.mu.RLock()
	pairs := make([]vecPair, 0, len(vi.vectors))
	for id, vec := range vi.vectors {
		pairs = append(pairs, vecPair{id: id, vec: vec})
	}
	vi.mu.RUnlock()

	config := HNSWConfigFromEnv()
	built := NewHNSWIndex(dimensions, config)
	for _, p := range pairs {
		if err := built.Add(p.id, p.vec); err != nil {
			return nil, fmt.Errorf("failed to add vector to HNSW: %w", err)
		}
	}

	// Install unless someone else won the race.
	s.hnswMu.Lock()
	if s.hnswIndex != nil {
		existing := s.hnswIndex
		s.hnswMu.Unlock()
		return existing, nil
	}

	// Log configuration and stats for observability
	quality := os.Getenv("NORNICDB_VECTOR_ANN_QUALITY")
	if quality == "" {
		quality = "balanced"
	}
	log.Printf("🔍 HNSW index created: quality=%s M=%d efConstruction=%d efSearch=%d vectors=%d tombstone_ratio=%.2f",
		quality, config.M, config.EfConstruction, config.EfSearch, built.Size(), built.TombstoneRatio())

	s.hnswIndex = built
	s.hnswMu.Unlock()

	s.ensureHNSWMaintenance()
	return built, nil
}

func (s *Service) ensureHNSWMaintenance() {
	s.hnswMaintOnce.Do(func() {
		s.hnswMaintStop = make(chan struct{})

		interval := envDurationMs("NORNICDB_HNSW_MAINT_INTERVAL_MS", 30_000)
		minRebuildInterval := envDurationSec("NORNICDB_HNSW_MIN_REBUILD_INTERVAL_SEC", 60)
		rebuildRatio := envFloat("NORNICDB_HNSW_TOMBSTONE_REBUILD_RATIO", 0.50)
		maxOverhead := envFloat("NORNICDB_HNSW_MAX_TOMBSTONE_OVERHEAD_FACTOR", 2.0)
		enabled := envBool("NORNICDB_HNSW_REBUILD_ENABLED", true)

		ticker := time.NewTicker(interval)
		go func() {
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if !enabled {
						continue
					}
					_ = s.maybeRebuildHNSW(context.Background(), rebuildRatio, maxOverhead, minRebuildInterval)
				case <-s.hnswMaintStop:
					return
				}
			}
		}()
	})
}

func (s *Service) maybeRebuildHNSW(ctx context.Context, tombstoneRatioThreshold, maxOverheadFactor float64, minInterval time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if minInterval <= 0 {
		minInterval = 60 * time.Second
	}

	last := time.Unix(s.hnswLastRebuildUnix.Load(), 0)
	if !last.IsZero() && time.Since(last) < minInterval {
		return nil
	}

	s.hnswMu.RLock()
	old := s.hnswIndex
	s.hnswMu.RUnlock()
	if old == nil {
		return nil
	}

	// Derive rebuild condition from a single read lock on the index.
	old.mu.RLock()
	total := len(old.nodeLevel)
	live := old.liveCount
	old.mu.RUnlock()
	if total == 0 || live <= 0 {
		return nil
	}

	deleted := total - live
	ratio := float64(deleted) / float64(total)
	overhead := float64(total) / float64(live)
	if ratio <= tombstoneRatioThreshold && overhead <= maxOverheadFactor {
		return nil
	}

	if !s.hnswRebuildInFlight.CompareAndSwap(false, true) {
		return nil
	}
	defer s.hnswRebuildInFlight.Store(false)

	// Snapshot vectors without holding hnswMu to avoid lock inversion with writers.
	type vecPair struct {
		id  string
		vec []float32
	}

	s.mu.RLock()
	vi := s.vectorIndex
	s.mu.RUnlock()
	if vi == nil {
		return nil
	}

	vi.mu.RLock()
	pairs := make([]vecPair, 0, len(vi.vectors))
	for id, vec := range vi.vectors {
		pairs = append(pairs, vecPair{id: id, vec: vec})
	}
	vi.mu.RUnlock()

	rebuilt := NewHNSWIndex(old.dimensions, old.config)
	for _, p := range pairs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_ = rebuilt.Add(p.id, p.vec)
	}

	// Swap only if the index hasn't changed.
	s.hnswMu.Lock()
	if s.hnswIndex == old {
		s.hnswIndex = rebuilt
		s.hnswLastRebuildUnix.Store(time.Now().Unix())

		// Invalidate pipeline so the next query uses the rebuilt index.
		s.pipelineMu.Lock()
		s.vectorPipeline = nil
		s.pipelineMu.Unlock()
	}
	s.hnswMu.Unlock()

	return nil
}

func envBool(key string, fallback bool) bool {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envFloat(key string, fallback float64) float64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envDurationMs(key string, fallbackMs int) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return time.Duration(fallbackMs) * time.Millisecond
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return time.Duration(fallbackMs) * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

func envDurationSec(key string, fallbackSec int) time.Duration {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return time.Duration(fallbackSec) * time.Second
	}
	sec, err := strconv.Atoi(raw)
	if err != nil || sec <= 0 {
		return time.Duration(fallbackSec) * time.Second
	}
	return time.Duration(sec) * time.Second
}

// filterCandidatesByType filters candidates by node type/label.
func (s *Service) filterCandidatesByType(ctx context.Context, candidates []SearchCandidate, types []string, seenOrphans map[string]bool) []SearchCandidate {
	if len(types) == 0 {
		return candidates
	}

	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[strings.ToLower(t)] = true
	}

	filtered := make([]SearchCandidate, 0, len(candidates))
	for _, cand := range candidates {
		node, err := s.engine.GetNode(storage.NodeID(cand.ID))
		if err != nil {
			if s.handleOrphanedEmbedding(ctx, cand.ID, err, seenOrphans) {
				continue
			}
			continue
		}

		// Check if any label matches
		matches := false
		for _, label := range node.Labels {
			if typeSet[strings.ToLower(label)] {
				matches = true
				break
			}
		}

		if matches {
			filtered = append(filtered, cand)
		}
	}

	return filtered
}

// fuseRRF implements the Reciprocal Rank Fusion (RRF) algorithm.
//
// RRF combines multiple ranked lists without requiring score normalization.
// Each ranking method votes for documents using their rank positions.
//
// Formula: RRF_score(doc) = Σ (weight_i / (k + rank_i))
//
// Where:
//   - k = constant (default 60) to smooth rank differences
//   - rank_i = position in list i (1-indexed: 1st place = rank 1)
//   - weight_i = importance weight for list i (default 1.0)
//
// Why k=60?
//   - From research by Cormack et al. (2009)
//   - Balances between giving too much weight to top results vs treating all ranks equally
//   - k=60 means rank #1 gets score 1/61=0.016, rank #2 gets 1/62=0.016
//   - Difference is small, but rank #1 is still slightly better
//
// Example calculation:
//
//	Document appears in:
//	  - Vector results at rank #2
//	  - BM25 results at rank #5
//
//	RRF_score = (1.0 / (60 + 2)) + (1.0 / (60 + 5))
//	          = (1.0 / 62) + (1.0 / 65)
//	          = 0.01613 + 0.01538
//	          = 0.03151
//
//	Document only in vector at rank #1:
//	RRF_score = (1.0 / (60 + 1)) + 0
//	          = 0.01639
//
//	First document wins! Being in both lists beats being #1 in just one.
//
// ELI12:
//
// Think of it like American Idol with two judges:
//   - Judge A ranks singers by vocal technique
//   - Judge B ranks by stage presence
//
// A singer ranked #2 by both judges should beat one ranked #1 by only one judge.
// RRF does this math automatically!
//
// Reference: Cormack, Clarke & Buettcher (2009)
// "Reciprocal Rank Fusion outperforms the best known automatic evaluation
// measures in combining results from multiple text retrieval systems."
func (s *Service) fuseRRF(vectorResults, bm25Results []indexResult, opts *SearchOptions) []rrfResult {
	// Create rank maps (1-indexed per RRF formula)
	vectorRanks := make(map[string]int)
	for i, r := range vectorResults {
		vectorRanks[r.ID] = i + 1
	}

	bm25Ranks := make(map[string]int)
	for i, r := range bm25Results {
		bm25Ranks[r.ID] = i + 1
	}

	// Get all unique document IDs
	allIDs := make(map[string]struct{})
	for _, r := range vectorResults {
		allIDs[r.ID] = struct{}{}
	}
	for _, r := range bm25Results {
		allIDs[r.ID] = struct{}{}
	}

	// Calculate RRF scores
	var results []rrfResult
	k := opts.RRFK
	if k == 0 {
		k = 60 // Default
	}
	vectorWeight := opts.VectorWeight
	if vectorWeight == 0 {
		vectorWeight = 1.0 // Default weight
	}
	bm25Weight := opts.BM25Weight
	if bm25Weight == 0 {
		bm25Weight = 1.0 // Default weight
	}

	for id := range allIDs {
		var vectorComponent, bm25Component float64

		if rank, ok := vectorRanks[id]; ok {
			vectorComponent = vectorWeight / (k + float64(rank))
		}
		if rank, ok := bm25Ranks[id]; ok {
			bm25Component = bm25Weight / (k + float64(rank))
		}

		rrfScore := vectorComponent + bm25Component

		// Skip below threshold
		if rrfScore < opts.MinRRFScore {
			continue
		}

		// Get original score (prefer vector if available)
		var originalScore float64
		if idx := findResultIndex(vectorResults, id); idx >= 0 {
			originalScore = vectorResults[idx].Score
		} else if idx := findResultIndex(bm25Results, id); idx >= 0 {
			originalScore = bm25Results[idx].Score
		}

		results = append(results, rrfResult{
			ID:            id,
			RRFScore:      rrfScore,
			VectorRank:    vectorRanks[id],
			BM25Rank:      bm25Ranks[id],
			OriginalScore: originalScore,
		})
	}

	// Sort by RRF score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].RRFScore > results[j].RRFScore
	})

	return results
}

// applyMMR applies Maximal Marginal Relevance diversification to search results.
//
// MMR re-ranks results to balance relevance with diversity, preventing redundant
// results that are too similar to each other.
//
// Formula: MMR(d) = λ * Sim(d, query) - (1-λ) * max(Sim(d, d_i))
//
// Where:
//   - λ (lambda) controls relevance vs diversity balance (0.0 to 1.0)
//   - λ = 1.0: Pure relevance (no diversity)
//   - λ = 0.0: Pure diversity (ignore relevance)
//   - λ = 0.7: Balanced (default, 70% relevance, 30% diversity)
//   - Sim(d, query) = similarity to the query (RRF score)
//   - max(Sim(d, d_i)) = max similarity to already selected results
//
// Algorithm:
//  1. Select the most relevant document first
//  2. For each remaining position:
//     - Calculate MMR score for all remaining docs
//     - Select doc with highest MMR (balancing relevance + diversity)
//  3. Repeat until limit reached
//
// ELI12:
//
// Imagine picking a playlist from your library. You don't want 5 songs that
// all sound the same! MMR is like saying:
//   - "I want songs I like (relevance)"
//   - "But also songs that are different from what I already picked (diversity)"
//
// Lambda controls how much you care about variety vs. your favorites.
//
// Reference: Carbonell & Goldstein (1998)
// "The Use of MMR, Diversity-Based Reranking for Reordering Documents
// and Producing Summaries"
func (s *Service) applyMMR(ctx context.Context, results []rrfResult, queryEmbedding []float32, limit int, lambda float64, seenOrphans map[string]bool) []rrfResult {
	if len(results) <= 1 || lambda >= 1.0 {
		// No diversification needed
		return results
	}

	// Get embeddings for all candidate documents
	type docWithEmbed struct {
		result    rrfResult
		embedding []float32
	}

	candidates := make([]docWithEmbed, 0, len(results))
	for _, r := range results {
		// Get embedding from storage
		node, err := s.engine.GetNode(storage.NodeID(r.ID))
		if err != nil {
			if s.handleOrphanedEmbedding(ctx, r.ID, err, seenOrphans) {
				continue
			}
			candidates = append(candidates, docWithEmbed{result: r, embedding: nil})
			continue
		}
		if node == nil || len(node.ChunkEmbeddings) == 0 || len(node.ChunkEmbeddings[0]) == 0 {
			// No embedding - use original score only
			candidates = append(candidates, docWithEmbed{
				result:    r,
				embedding: nil,
			})
		} else {
			// Use first chunk embedding (always stored in ChunkEmbeddings, even single chunk = array of 1)
			candidates = append(candidates, docWithEmbed{
				result:    r,
				embedding: node.ChunkEmbeddings[0],
			})
		}
	}

	// MMR selection
	selected := make([]rrfResult, 0, limit)
	remaining := candidates

	for len(selected) < limit && len(remaining) > 0 {
		bestIdx := -1
		bestMMR := math.Inf(-1)

		for i, cand := range remaining {
			// Relevance component: similarity to query (using RRF score as proxy)
			relevance := cand.result.RRFScore

			// Diversity component: max similarity to already selected docs
			maxSimToSelected := 0.0
			if cand.embedding != nil && len(selected) > 0 {
				for _, sel := range selected {
					// Find embedding for selected doc
					for _, c := range candidates {
						if c.result.ID == sel.ID && c.embedding != nil {
							sim := vector.CosineSimilarity(cand.embedding, c.embedding)
							if sim > maxSimToSelected {
								maxSimToSelected = sim
							}
							break
						}
					}
				}
			}

			// MMR formula
			mmrScore := lambda*relevance - (1-lambda)*maxSimToSelected

			if mmrScore > bestMMR {
				bestMMR = mmrScore
				bestIdx = i
			}
		}

		if bestIdx >= 0 {
			selected = append(selected, remaining[bestIdx].result)
			// Remove selected from remaining
			remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
		} else {
			break
		}
	}

	return selected
}

// applyStage2Rerank applies Stage-2 reranking to RRF results.
//
// This is Stage 2 of a two-stage retrieval system:
//   - Stage 1 (fast): Bi-encoder retrieval (vector + BM25 with RRF)
//   - Stage 2 (accurate): Optional reranking of top candidates (LLM or cross-encoder)
//
// Reranking is slower than Stage 1, so it should be used on a bounded TopK.
func (s *Service) applyStage2Rerank(ctx context.Context, query string, results []rrfResult, opts *SearchOptions, seenOrphans map[string]bool, reranker Reranker) []rrfResult {
	if len(results) == 0 {
		return results
	}
	if reranker == nil || !reranker.Enabled() {
		return results
	}

	// Limit to top-K (optional; keeps prompt/service bounded).
	topK := opts.RerankTopK
	if topK <= 0 {
		topK = 100
	}
	if len(results) > topK {
		results = results[:topK]
	}

	// Build candidates with content from storage.
	candidates := make([]RerankCandidate, 0, len(results))
	for _, r := range results {
		node, err := s.engine.GetNode(storage.NodeID(r.ID))
		if err != nil {
			if s.handleOrphanedEmbedding(ctx, r.ID, err, seenOrphans) {
				continue
			}
			continue
		}
		if node == nil {
			continue
		}

		// Extract searchable content
		content := s.extractSearchableText(node)
		if content == "" {
			continue
		}

		candidates = append(candidates, RerankCandidate{
			ID:      r.ID,
			Content: content,
			Score:   r.RRFScore,
		})
	}

	if len(candidates) == 0 {
		return results
	}

	// Log before reranking
	queryPreview := query
	if len(queryPreview) > 60 {
		queryPreview = queryPreview[:57] + "..."
	}
	log.Printf("🔄 Reranking %d candidates for query %q (%s)...", len(candidates), queryPreview, reranker.Name())
	start := time.Now()

	// Apply Stage-2 reranking.
	reranked, err := reranker.Rerank(ctx, query, candidates)
	if err != nil {
		// Fallback to original results on error
		log.Printf("⚠️ Reranking failed (%s): %v; using original order", reranker.Name(), err)
		return results
	}

	// Log after reranking
	log.Printf("✅ Reranking complete: %d results in %v (%s)", len(reranked), time.Since(start), reranker.Name())

	// If reranker produced nearly identical scores (e.g. model not discriminating),
	// keep original RRF order and scores so the user gets the better ranking.
	// This avoids replacing discriminative RRF (0.03, 0.02, ...) with flat 0.49 for all.
	const minScoreRange = 0.05
	var scoreMin, scoreMax float64
	for i, r := range reranked {
		s := r.FinalScore
		if i == 0 {
			scoreMin, scoreMax = s, s
		} else {
			if s < scoreMin {
				scoreMin = s
			}
			if s > scoreMax {
				scoreMax = s
			}
		}
	}
	if scoreMax-scoreMin < minScoreRange {
		log.Printf("ℹ️ Reranking produced nearly identical scores (range=%.4f); using RRF order and scores", scoreMax-scoreMin)
		return results
	}

	// Build map by ID so we can reliably preserve VectorRank/BM25Rank when converting
	// reranker output back to rrfResult. Without this, original ranks can be lost when
	// reranking reorders or filters results.
	resultsByID := make(map[string]*rrfResult, len(results))
	for i := range results {
		resultsByID[results[i].ID] = &results[i]
	}

	// Convert back to rrfResult format
	rerankedResults := make([]rrfResult, 0, len(reranked))
	for _, r := range reranked {
		original := resultsByID[r.ID]
		if original == nil {
			log.Printf("⚠️ Reranker returned ID %q not in pre-rerank results; preserving result with zero ranks", r.ID)
		}
		// Apply per-request MinScore filter if configured. Note that individual rerankers
		// may also apply their own MinScore internally.
		if opts.RerankMinScore > 0 && r.FinalScore < opts.RerankMinScore {
			continue
		}
		var vectorRank, bm25Rank int
		if original != nil {
			vectorRank, bm25Rank = original.VectorRank, original.BM25Rank
		}
		rerankedResults = append(rerankedResults, rrfResult{
			ID:            r.ID,
			RRFScore:      r.FinalScore, // Use cross-encoder score
			VectorRank:    vectorRank,
			BM25Rank:      bm25Rank,
			OriginalScore: r.BiScore,
		})
	}

	return rerankedResults
}

// SetCrossEncoder configures the Stage-2 reranker to use the cross-encoder implementation.
//
// Example:
//
//	svc := search.NewService(engine)
//	svc.SetCrossEncoder(search.NewCrossEncoder(&search.CrossEncoderConfig{
//		Enabled: true,
//		APIURL:  "http://localhost:8081/rerank",
//		Model:   "cross-encoder/ms-marco-MiniLM-L-6-v2",
//	}))
func (s *Service) SetCrossEncoder(ce *CrossEncoder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reranker = ce
}

// SetReranker configures the Stage-2 reranker.
func (s *Service) SetReranker(r Reranker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reranker = r
}

// CrossEncoderAvailable returns true if a cross-encoder reranker is configured and available.
func (s *Service) CrossEncoderAvailable(ctx context.Context) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.reranker == nil {
		return false
	}
	_, ok := s.reranker.(*CrossEncoder)
	if !ok {
		return false
	}
	return s.reranker.IsAvailable(ctx)
}

// RerankerAvailable returns true if Stage-2 reranking is configured and available.
func (s *Service) RerankerAvailable(ctx context.Context) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reranker != nil && s.reranker.IsAvailable(ctx)
}

// vectorSearchOnly performs vector-only search.
func (s *Service) vectorSearchOnly(ctx context.Context, embedding []float32, opts *SearchOptions) (*SearchResponse, error) {
	s.mu.RLock()
	pipeline, pipelineErr := s.getOrCreateVectorPipeline()
	if pipelineErr != nil {
		s.mu.RUnlock()
		return nil, pipelineErr
	}
	scored, searchErr := pipeline.Search(ctx, embedding, vectorOverfetchLimit(opts.Limit), opts.GetMinSimilarity(0.5))
	s.mu.RUnlock()
	if searchErr != nil {
		return nil, searchErr
	}

	var results []indexResult
	for _, r := range scored {
		results = append(results, indexResult{ID: r.ID, Score: r.Score})
	}
	searchMethod := "vector"
	message := "Vector similarity search (cosine)"
	searchStart := time.Now()

	switch pipeline.candidateGen.(type) {
	case *KMeansCandidateGen:
		searchMethod = "vector_clustered"
		message = "K-means routed vector search"
	case *IVFHNSWCandidateGen:
		searchMethod = "vector_ivf_hnsw"
		message = "IVF-HNSW (centroid routing + per-cluster HNSW)"
	case *GPUBruteForceCandidateGen:
		searchMethod = "vector_gpu_brute"
		message = "GPU brute-force vector search (exact)"
	case *HNSWCandidateGen:
		searchMethod = "vector_hnsw"
		message = "HNSW approximate nearest neighbor search"
	case *BruteForceCandidateGen:
		searchMethod = "vector_brute"
		message = "CPU brute-force vector search (exact)"
	}

	s.mu.RLock()
	clusterEnabled := s.clusterEnabled && s.clusterIndex != nil
	s.mu.RUnlock()
	if clusterEnabled {
		log.Printf("[K-MEANS] 🔍 SEARCH | mode=%s candidates=%d duration=%v",
			searchMethod, len(results), time.Since(searchStart))
	}

	// Collapse vector IDs back to unique node IDs.
	results = collapseIndexResultsByNodeID(results)

	seenOrphans := make(map[string]bool)
	if len(opts.Types) > 0 {
		results = s.filterByType(ctx, results, opts.Types, seenOrphans)
	}

	searchResults := s.enrichIndexResults(ctx, results, opts.Limit, seenOrphans)
	// Vector-only: set vector_rank from position (1-based), bm25_rank = 0
	for i := range searchResults {
		searchResults[i].VectorRank = i + 1
		searchResults[i].BM25Rank = 0
	}

	return &SearchResponse{
		Status:          "success",
		Results:         searchResults,
		TotalCandidates: len(results),
		Returned:        len(searchResults),
		SearchMethod:    searchMethod,
		Message:         message,
	}, nil
}

// fullTextSearchOnly performs full-text BM25 search only.
func (s *Service) fullTextSearchOnly(ctx context.Context, query string, opts *SearchOptions) (*SearchResponse, error) {
	s.mu.RLock()
	ft := s.fulltextIndex
	s.mu.RUnlock()
	if ft == nil {
		return &SearchResponse{
			Status:            "success",
			Query:             query,
			Results:           nil,
			TotalCandidates:   0,
			Returned:          0,
			SearchMethod:      "fulltext",
			FallbackTriggered: true,
			Message:           "Full-text index not available",
		}, nil
	}
	results := ft.Search(query, opts.Limit*2)

	seenOrphans := make(map[string]bool)
	if len(opts.Types) > 0 {
		results = s.filterByType(ctx, results, opts.Types, seenOrphans)
	}

	searchResults := s.enrichIndexResults(ctx, results, opts.Limit, seenOrphans)
	// Full-text only: set bm25_rank from position (1-based), vector_rank = 0
	for i := range searchResults {
		searchResults[i].VectorRank = 0
		searchResults[i].BM25Rank = i + 1
	}

	return &SearchResponse{
		Status:            "success",
		Query:             query,
		Results:           searchResults,
		TotalCandidates:   len(results),
		Returned:          len(searchResults),
		SearchMethod:      "fulltext",
		FallbackTriggered: true,
		Message:           "Full-text BM25 search (vector search unavailable or returned no results)",
	}, nil
}

// extractSearchableText extracts text from ALL node properties for full-text indexing.
// This includes:
//   - Node labels (for searching by type)
//   - All string properties
//   - String representations of other property types
//   - Priority properties (content, title, etc.) are included first for better ranking
func (s *Service) extractSearchableText(node *storage.Node) string {
	// Note: We now receive a stable copy of the node from IterateNodes,
	// so we can safely access its properties without additional locking.
	// The storage engine (AsyncEngine) makes copies during iteration to
	// prevent concurrent modification issues.

	var parts []string

	// 1. Add labels first (important for type-based search)
	for _, label := range node.Labels {
		parts = append(parts, label)
	}

	// 2. Add priority searchable properties first (better ranking for these)
	for _, prop := range SearchableProperties {
		if val, ok := node.Properties[prop]; ok {
			if str := propertyToString(val); str != "" {
				parts = append(parts, str)
			}
		}
	}

	// 3. Add ALL other properties (for comprehensive search)
	prioritySet := make(map[string]bool)
	for _, p := range SearchableProperties {
		prioritySet[p] = true
	}

	for key, val := range node.Properties {
		// Skip if already added as priority property
		if prioritySet[key] {
			continue
		}
		// Add property name and value for searchability
		if str := propertyToString(val); str != "" {
			// Include property name to enable searches like "genre:action"
			parts = append(parts, key, str)
		}
	}

	return strings.Join(parts, " ")
}

// propertyToString converts any property value to a searchable string.
func propertyToString(val interface{}) string {
	switch v := val.(type) {
	case string:
		return v
	case []string:
		return strings.Join(v, " ")
	case int, int64, int32, float64, float32:
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []interface{}:
		var strs []string
		for _, item := range v {
			if s := propertyToString(item); s != "" {
				strs = append(strs, s)
			}
		}
		return strings.Join(strs, " ")
	default:
		// For complex types, try to get string representation
		if v != nil {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
}

// filterByType filters results to only include specified node types.
func (s *Service) filterByType(ctx context.Context, results []indexResult, types []string, seenOrphans map[string]bool) []indexResult {
	if len(types) == 0 {
		return results
	}

	typeSet := make(map[string]struct{})
	for _, t := range types {
		typeSet[strings.ToLower(t)] = struct{}{}
	}

	var filtered []indexResult
	for _, r := range results {
		node, err := s.engine.GetNode(storage.NodeID(r.ID))
		if err != nil {
			if s.handleOrphanedEmbedding(ctx, r.ID, err, seenOrphans) {
				continue
			}
			continue
		}

		// Check if any label matches
		for _, label := range node.Labels {
			if _, ok := typeSet[strings.ToLower(label)]; ok {
				filtered = append(filtered, r)
				break
			}
		}

		// Also check type property
		if nodeType, ok := node.Properties["type"].(string); ok {
			if _, ok := typeSet[strings.ToLower(nodeType)]; ok {
				filtered = append(filtered, r)
			}
		}
	}

	return filtered
}

// enrichResults converts RRF results to SearchResult with full node data.
func (s *Service) enrichResults(ctx context.Context, rrfResults []rrfResult, limit int, seenOrphans map[string]bool) []SearchResult {
	var results []SearchResult

	for i, rrf := range rrfResults {
		if i >= limit {
			break
		}

		node, err := s.engine.GetNode(storage.NodeID(rrf.ID))
		if err != nil {
			if s.handleOrphanedEmbedding(ctx, rrf.ID, err, seenOrphans) {
				continue
			}
			continue
		}

		result := SearchResult{
			ID:         rrf.ID,
			NodeID:     node.ID,
			Labels:     node.Labels,
			Properties: node.Properties,
			Score:      rrf.RRFScore,
			Similarity: rrf.OriginalScore,
			RRFScore:   rrf.RRFScore,
			VectorRank: rrf.VectorRank,
			BM25Rank:   rrf.BM25Rank,
		}

		// Extract common fields
		if t, ok := node.Properties["type"].(string); ok {
			result.Type = t
		}
		if title, ok := node.Properties["title"].(string); ok {
			result.Title = title
		}
		if desc, ok := node.Properties["description"].(string); ok {
			result.Description = desc
		}
		if content, ok := node.Properties["content"].(string); ok {
			result.ContentPreview = truncate(content, 200)
		} else if text, ok := node.Properties["text"].(string); ok {
			result.ContentPreview = truncate(text, 200)
		}

		results = append(results, result)
	}

	return results
}

// enrichIndexResults converts raw index results to SearchResult.
// Maps chunk IDs (e.g., "node-id-chunk-0") back to the original node ID.
func (s *Service) enrichIndexResults(ctx context.Context, indexResults []indexResult, limit int, seenOrphans map[string]bool) []SearchResult {
	var results []SearchResult
	seenNodes := make(map[string]bool) // Track nodes we've already added to avoid duplicates

	for _, ir := range indexResults {
		if len(results) >= limit {
			break
		}

		nodeIDStr := normalizeVectorResultIDToNodeID(ir.ID)

		// Skip if we've already added this node (from a different chunk)
		if seenNodes[nodeIDStr] {
			continue
		}

		node, err := s.engine.GetNode(storage.NodeID(nodeIDStr))
		if err != nil {
			if s.handleOrphanedEmbedding(ctx, nodeIDStr, err, seenOrphans) {
				continue
			}
			continue
		}

		seenNodes[nodeIDStr] = true

		result := SearchResult{
			ID:         nodeIDStr, // Use original node ID, not chunk ID
			NodeID:     node.ID,
			Labels:     node.Labels,
			Properties: node.Properties,
			Score:      ir.Score,
			Similarity: ir.Score,
		}

		// Extract common fields
		if t, ok := node.Properties["type"].(string); ok {
			result.Type = t
		}
		if title, ok := node.Properties["title"].(string); ok {
			result.Title = title
		}
		if desc, ok := node.Properties["description"].(string); ok {
			result.Description = desc
		}
		if content, ok := node.Properties["content"].(string); ok {
			result.ContentPreview = truncate(content, 200)
		} else if text, ok := node.Properties["text"].(string); ok {
			result.ContentPreview = truncate(text, 200)
		}

		results = append(results, result)
	}

	return results
}

// GetAdaptiveRRFConfig returns optimized RRF weights based on query characteristics.
//
// This function analyzes the query and adjusts weights to favor the search method
// most likely to perform well:
//
//   - Short queries (1-2 words): Favor BM25 keyword matching
//     Example: "python" or "graph database"
//     Weights: Vector=0.5, BM25=1.5
//
//   - Long queries (6+ words): Favor vector semantic understanding
//     Example: "How do I implement a distributed consensus algorithm?"
//     Weights: Vector=1.5, BM25=0.5
//
//   - Medium queries (3-5 words): Balanced approach
//     Example: "machine learning algorithms"
//     Weights: Vector=1.0, BM25=1.0
//
// Why this works:
//   - Short queries lack context → keywords more reliable
//   - Long queries have semantic meaning → embeddings capture intent better
//
// Example:
//
//	// Automatic adaptation
//	query1 := "database"
//	opts1 := search.GetAdaptiveRRFConfig(query1)
//	fmt.Printf("Short query weights: V=%.1f, B=%.1f\n",
//		opts1.VectorWeight, opts1.BM25Weight)
//	// Output: V=0.5, B=1.5 (favors keywords)
//
//	query2 := "What are the best practices for scaling graph databases?"
//	opts2 := search.GetAdaptiveRRFConfig(query2)
//	fmt.Printf("Long query weights: V=%.1f, B=%.1f\n",
//		opts2.VectorWeight, opts2.BM25Weight)
//	// Output: V=1.5, B=0.5 (favors semantics)
//
// Returns SearchOptions with adapted weights. Other options (Limit, MinSimilarity)
// are set to defaults.
func GetAdaptiveRRFConfig(query string) *SearchOptions {
	words := strings.Fields(query)
	wordCount := len(words)

	opts := DefaultSearchOptions()

	// Short queries (1-2 words): Emphasize keyword matching
	if wordCount <= 2 {
		opts.VectorWeight = 0.5
		opts.BM25Weight = 1.5
		return opts
	}

	// Long queries (6+ words): Emphasize semantic understanding
	if wordCount >= 6 {
		opts.VectorWeight = 1.5
		opts.BM25Weight = 0.5
		return opts
	}

	// Medium queries: Balanced
	return opts
}

// Helper types
type indexResult struct {
	ID    string
	Score float64
}

type rrfResult struct {
	ID            string
	RRFScore      float64
	VectorRank    int
	BM25Rank      int
	OriginalScore float64
}

func findResultIndex(results []indexResult, id string) int {
	for i, r := range results {
		if r.ID == id {
			return i
		}
	}
	return -1
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Handle edge cases where maxLen is too small for ellipsis
	if maxLen <= 3 {
		if maxLen <= 0 {
			return ""
		}
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
