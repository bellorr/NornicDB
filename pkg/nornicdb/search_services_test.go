package nornicdb

import (
	"context"
	"testing"
	"time"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestSearchServices_PerDatabaseIsolation_EventRouting(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingDimensions = 3
	db, err := Open("", cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create and index a node in the default database (nornic).
	alpha := &storage.Node{
		ID:     storage.NodeID("alpha"),
		Labels: []string{"Doc"},
		Properties: map[string]any{
			"content": "hello alpha",
		},
		ChunkEmbeddings: [][]float32{{0.1, 0.2, 0.3}},
	}
	_, err = db.storage.CreateNode(alpha)
	require.NoError(t, err)
	db.indexNodeFromEvent(&storage.Node{
		ID:             storage.NodeID("nornic:alpha"),
		Labels:         alpha.Labels,
		Properties:     alpha.Properties,
		ChunkEmbeddings: alpha.ChunkEmbeddings,
	})

	// Create and index a node in another database.
	db2Storage := storage.NewNamespacedEngine(db.baseStorage, "db2")
	beta := &storage.Node{
		ID:     storage.NodeID("beta"),
		Labels: []string{"Doc"},
		Properties: map[string]any{
			"content": "world beta",
		},
		ChunkEmbeddings: [][]float32{{0.4, 0.5, 0.6}},
	}
	_, err = db2Storage.CreateNode(beta)
	require.NoError(t, err)
	db.indexNodeFromEvent(&storage.Node{
		ID:             storage.NodeID("db2:beta"),
		Labels:         beta.Labels,
		Properties:     beta.Properties,
		ChunkEmbeddings: beta.ChunkEmbeddings,
	})

	// Default DB service should only contain default DB embedding.
	defaultSvc, err := db.GetOrCreateSearchService(db.defaultDatabaseName(), db.storage)
	require.NoError(t, err)
	require.Equal(t, 1, defaultSvc.EmbeddingCount())

	// db2 service should exist and contain only db2 embedding.
	db2Svc, err := db.GetOrCreateSearchService("db2", nil)
	require.NoError(t, err)
	require.Equal(t, 1, db2Svc.EmbeddingCount())

	// Verify text-only search does not cross-contaminate.
	// Default DB should find alpha, not beta.
	resp, err := defaultSvc.Search(ctx, "world", nil, nil)
	require.NoError(t, err)
	require.Len(t, resp.Results, 0)

	resp, err = defaultSvc.Search(ctx, "hello", nil, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(resp.Results), 1)

	// db2 should find beta, not alpha.
	resp, err = db2Svc.Search(ctx, "hello", nil, nil)
	require.NoError(t, err)
	require.Len(t, resp.Results, 0)

	resp, err = db2Svc.Search(ctx, "world", nil, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(resp.Results), 1)
}

func TestSearchServices_ResetDropsCache(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingDimensions = 3
	db, err := Open("", cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.GetOrCreateSearchService("db2", nil)
	require.NoError(t, err)

	db.searchServicesMu.RLock()
	_, exists := db.searchServices["db2"]
	db.searchServicesMu.RUnlock()
	require.True(t, exists)

	db.ResetSearchService("db2")

	db.searchServicesMu.RLock()
	_, exists = db.searchServices["db2"]
	db.searchServicesMu.RUnlock()
	require.False(t, exists)
}
