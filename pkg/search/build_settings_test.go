package search

import (
	"path/filepath"
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
