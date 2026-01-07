package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/orneryd/nornicdb/pkg/auth"
	qpb "github.com/qdrant/go-client/qdrant"
)

// =============================================================================
// Collections HTTP API Endpoints (Qdrant-compatible)
// =============================================================================

// registerCollectionsRoutes registers HTTP endpoints for collection management
func (s *Server) registerCollectionsRoutes(mux *http.ServeMux) {
	// Collections API endpoints (Qdrant-compatible REST API)
	// GET    /api/collections           - List all collections
	// GET    /api/collections/{name}    - Get collection info
	// PUT    /api/collections/{name}    - Update collection (not implemented)
	// DELETE /api/collections/{name}    - Delete collection
	// POST   /api/collections           - Create collection

	// Collections list/create - single handler routes by method
	mux.HandleFunc("/api/collections", s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListCollections(w, r)
		case http.MethodPost:
			// Create requires write permission - check here
			claims := getClaims(r)
			if claims == nil || !hasPermission(claims.Roles, auth.PermWrite) {
				s.writeJSONError(w, http.StatusForbidden, "Write permission required to create collections")
				return
			}
			s.handleCreateCollection(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}, auth.PermRead))

	// Collection get/delete - single handler routes by method
	mux.HandleFunc("/api/collections/", s.withAuth(func(w http.ResponseWriter, r *http.Request) {
		// Extract collection name from path: /api/collections/{name}
		path := strings.TrimPrefix(r.URL.Path, "/api/collections/")
		if path == "" {
			http.Error(w, "Collection name required", http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			s.handleGetCollection(w, r, path)
		case http.MethodDelete:
			// Delete requires write permission - check here
			claims := getClaims(r)
			if claims == nil || !hasPermission(claims.Roles, auth.PermWrite) {
				s.writeJSONError(w, http.StatusForbidden, "Write permission required to delete collections")
				return
			}
			s.handleDeleteCollection(w, r, path)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}, auth.PermRead))
}

// handleListCollections returns a list of all collections
func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	if s.qdrantGRPCServer == nil || !s.qdrantGRPCServer.IsRunning() {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Qdrant gRPC service not available")
		return
	}

	registry := s.qdrantGRPCRegistry
	if registry == nil {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Collection registry not available")
		return
	}

	ctx := r.Context()
	names, err := registry.ListCollections(ctx)
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list collections: %v", err))
		return
	}

	// Build collection descriptions
	collections := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		meta, err := registry.GetCollection(ctx, name)
		if err != nil {
			// Skip collections that can't be retrieved
			continue
		}

		pointCount, _ := registry.GetPointCount(ctx, name)

		collections = append(collections, map[string]interface{}{
			"name": name,
			"info": map[string]interface{}{
				"status":        meta.Status.String(),
				"points_count":  pointCount,
				"vectors_count": pointCount, // Same as points for single-vector collections
				"config": map[string]interface{}{
					"params": map[string]interface{}{
						"vectors": map[string]interface{}{
							"size":     meta.Dimensions,
							"distance": meta.Distance.String(),
						},
					},
				},
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": map[string]interface{}{
			"collections": collections,
		},
	})
}

// handleGetCollection returns detailed information about a specific collection
func (s *Server) handleGetCollection(w http.ResponseWriter, r *http.Request, collectionName string) {
	if s.qdrantGRPCServer == nil || !s.qdrantGRPCServer.IsRunning() {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Qdrant gRPC service not available")
		return
	}

	registry := s.qdrantGRPCRegistry
	if registry == nil {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Collection registry not available")
		return
	}

	ctx := r.Context()
	meta, err := registry.GetCollection(ctx, collectionName)
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Collection %q not found", collectionName))
		return
	}

	pointCount, _ := registry.GetPointCount(ctx, collectionName)

	// Build Qdrant-compatible collection info response
	info := map[string]interface{}{
		"status":                meta.Status.String(),
		"points_count":          pointCount,
		"indexed_vectors_count": pointCount,
		"config": map[string]interface{}{
			"params": map[string]interface{}{
				"vectors": map[string]interface{}{
					"size":     meta.Dimensions,
					"distance": meta.Distance.String(),
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": info,
	})
}

// handleCreateCollection creates a new collection
func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	if s.qdrantGRPCServer == nil || !s.qdrantGRPCServer.IsRunning() {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Qdrant gRPC service not available")
		return
	}

	registry := s.qdrantGRPCRegistry
	if registry == nil {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Collection registry not available")
		return
	}

	var req struct {
		Vectors struct {
			Size     int    `json:"size"`
			Distance string `json:"distance"`
		} `json:"vectors"`
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	if req.Name == "" {
		s.writeJSONError(w, http.StatusBadRequest, "Collection name is required")
		return
	}

	if req.Vectors.Size <= 0 {
		s.writeJSONError(w, http.StatusBadRequest, "Vector size must be positive")
		return
	}

	// Parse distance
	var distance qpb.Distance
	switch strings.ToUpper(req.Vectors.Distance) {
	case "COSINE", "Cosine":
		distance = qpb.Distance_Cosine
	case "EUCLIDEAN", "Euclidean":
		distance = qpb.Distance_Euclid
	case "DOT", "Dot":
		distance = qpb.Distance_Dot
	default:
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid distance metric: %s (supported: Cosine, Euclidean, Dot)", req.Vectors.Distance))
		return
	}

	ctx := r.Context()
	if err := registry.CreateCollection(ctx, req.Name, req.Vectors.Size, distance); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			s.writeJSONError(w, http.StatusConflict, fmt.Sprintf("Collection %q already exists", req.Name))
		} else {
			s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create collection: %v", err))
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": true,
	})
}

// handleDeleteCollection deletes a collection
func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request, collectionName string) {
	if s.qdrantGRPCServer == nil || !s.qdrantGRPCServer.IsRunning() {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Qdrant gRPC service not available")
		return
	}

	registry := s.qdrantGRPCRegistry
	if registry == nil {
		s.writeJSONError(w, http.StatusServiceUnavailable, "Collection registry not available")
		return
	}

	ctx := r.Context()
	if err := registry.DeleteCollection(ctx, collectionName); err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Collection %q not found", collectionName))
		} else {
			s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete collection: %v", err))
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result": true,
	})
}

// writeJSONError writes a JSON error response
func (s *Server) writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": map[string]interface{}{
			"error": message,
		},
	})
}
