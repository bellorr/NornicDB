package nornicdb

import (
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestInferenceServices_PerDatabaseIsolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Memory.AutoLinksEnabled = true
	cfg.Memory.EmbeddingDimensions = 3

	db, err := Open("", cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	defaultInfer, err := db.GetOrCreateInferenceService(db.defaultDatabaseName(), db.storage)
	require.NoError(t, err)
	require.NotNil(t, defaultInfer)

	db2Storage := storage.NewNamespacedEngine(db.baseStorage, "db2")
	db2Infer, err := db.GetOrCreateInferenceService("db2", db2Storage)
	require.NoError(t, err)
	require.NotNil(t, db2Infer)
	require.NotSame(t, defaultInfer, db2Infer)

	// Reset and ensure a new instance is created on next request.
	db.ResetInferenceService("db2")
	db2Infer2, err := db.GetOrCreateInferenceService("db2", db2Storage)
	require.NoError(t, err)
	require.NotNil(t, db2Infer2)
	require.NotSame(t, db2Infer, db2Infer2)
}
