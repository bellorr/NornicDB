package dbconfig

import (
	"testing"

	"github.com/orneryd/nornicdb/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_GlobalOnly(t *testing.T) {
	t.Setenv("NORNICDB_SEARCH_BM25_ENGINE", "v2")
	global := config.LoadDefaults()
	global.Memory.EmbeddingDimensions = 1536
	global.Memory.SearchMinSimilarity = 0.6
	r := Resolve(global, nil)
	require.NotNil(t, r)
	assert.Equal(t, 1536, r.EmbeddingDimensions)
	assert.Equal(t, 0.6, r.SearchMinSimilarity)
	assert.Equal(t, "v2", r.BM25Engine)
	assert.NotEmpty(t, r.Effective["NORNICDB_EMBEDDING_DIMENSIONS"])
}

func TestResolve_Overrides(t *testing.T) {
	t.Setenv("NORNICDB_SEARCH_BM25_ENGINE", "v2")
	global := config.LoadDefaults()
	global.Memory.EmbeddingDimensions = 1024
	global.Memory.SearchMinSimilarity = 0.5
	overrides := map[string]string{
		"NORNICDB_EMBEDDING_DIMENSIONS":  "768",
		"NORNICDB_SEARCH_MIN_SIMILARITY": "0.8",
		"NORNICDB_SEARCH_BM25_ENGINE":    "v2",
	}
	r := Resolve(global, overrides)
	require.NotNil(t, r)
	assert.Equal(t, 768, r.EmbeddingDimensions)
	assert.Equal(t, 0.8, r.SearchMinSimilarity)
	assert.Equal(t, "v2", r.BM25Engine)
	assert.Equal(t, "768", r.Effective["NORNICDB_EMBEDDING_DIMENSIONS"])
	assert.Equal(t, "0.8", r.Effective["NORNICDB_SEARCH_MIN_SIMILARITY"])
	assert.Equal(t, "v2", r.Effective["NORNICDB_SEARCH_BM25_ENGINE"])
}

func TestIsAllowedKey(t *testing.T) {
	assert.True(t, IsAllowedKey("NORNICDB_EMBEDDING_MODEL"))
	assert.True(t, IsAllowedKey("NORNICDB_SEARCH_MIN_SIMILARITY"))
	assert.True(t, IsAllowedKey("NORNICDB_EMBEDDING_API_KEY"))
	assert.False(t, IsAllowedKey("UNKNOWN_KEY"))
}
