package search

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestSearchBuildSettingsPath(t *testing.T) {
	p := searchBuildSettingsPath("/tmp/a/bm25", "", "")
	require.Equal(t, filepath.Join("/tmp/a", "build_settings"), p)

	p = searchBuildSettingsPath("", "/tmp/a/vectors", "")
	require.Equal(t, filepath.Join("/tmp/a", "build_settings"), p)

	p = searchBuildSettingsPath("", "", "/tmp/a/hnsw")
	require.Equal(t, filepath.Join("/tmp/a", "build_settings"), p)
}

func TestSearchBuildSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "build_settings")

	engine := storage.NewMemoryEngine()
	svc := NewServiceWithDimensions(engine, 384)
	snap := svc.currentSearchBuildSettings()

	require.NoError(t, saveSearchBuildSettings(path, snap))
	got, err := loadSearchBuildSettings(path)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, snap.FormatVersion, got.FormatVersion)
	require.Equal(t, snap.BM25, got.BM25)
	require.Equal(t, snap.Vector, got.Vector)
	require.Equal(t, snap.HNSW, got.HNSW)
	require.Equal(t, snap.Routing, got.Routing)
}

func TestComposeRoutingBuildSettings_DoesNotEncodeSeedMode(t *testing.T) {
	t.Setenv("NORNICDB_VECTOR_ROUTING_MODE", "hybrid")
	t.Setenv("NORNICDB_KMEANS_MAX_ITERATIONS", "9")
	t.Setenv("NORNICDB_KMEANS_SEED_MODE", "none")

	svc := NewServiceWithDimensions(storage.NewMemoryEngine(), 2)
	routing := svc.composeRoutingBuildSettings()

	require.NotEmpty(t, routing)
	require.True(t, strings.Contains(routing, "kmeans_max_iter=9"))
	require.False(t, strings.Contains(routing, "kmeans_seed="))
}

func TestComposeStrategyBuildSettings_CompressedIncludesFingerprint(t *testing.T) {
	t.Setenv("NORNICDB_VECTOR_ANN_QUALITY", "compressed")
	t.Setenv("NORNICDB_VECTOR_IVF_LISTS", "64")
	t.Setenv("NORNICDB_VECTOR_PQ_SEGMENTS", "8")
	t.Setenv("NORNICDB_VECTOR_PQ_BITS", "4")

	svc := NewServiceWithDimensions(storage.NewMemoryEngine(), 64)
	strategy := svc.composeStrategyBuildSettings()
	require.True(t, strings.Contains(strategy, "quality=compressed"))
	require.True(t, strings.Contains(strategy, "lists=64"))
	require.True(t, strings.Contains(strategy, "segments=8"))
}
