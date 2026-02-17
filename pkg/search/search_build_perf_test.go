package search

import (
	"context"
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestPropertyToString_SkipsDenseNumericVectors(t *testing.T) {
	got := propertyToString([]float32{0.1, 0.2, 0.3})
	require.Equal(t, "", got)

	got = propertyToString([]float64{0.1, 0.2, 0.3})
	require.Equal(t, "", got)
}

func TestVectorFromPropertyValue_DimensionAware(t *testing.T) {
	vec, ok := vectorFromPropertyValue([]float32{1, 2, 3}, 3)
	require.True(t, ok)
	require.Equal(t, []float32{1, 2, 3}, vec)

	_, ok = vectorFromPropertyValue([]float32{1, 2}, 3)
	require.False(t, ok)

	vec, ok = vectorFromPropertyValue([]any{1.0, 2.0, 3.0}, 3)
	require.True(t, ok)
	require.Equal(t, []float32{1, 2, 3}, vec)
}

func TestBuildIndexes_IndexesNamedChunkAndPropertyVectors(t *testing.T) {
	t.Parallel()

	engine := storage.NewMemoryEngine()
	svc := NewServiceWithDimensions(engine, 3)

	node := &storage.Node{
		ID:     "nornic:doc-vectors",
		Labels: []string{"Doc"},
		Properties: map[string]any{
			"title":     "vectorized doc",
			"customVec": []float32{0, 1, 0},
		},
		NamedEmbeddings: map[string][]float32{
			"titleVec": {1, 0, 0},
		},
		ChunkEmbeddings: [][]float32{
			{0, 0, 1},
			{0, 1, 0},
		},
	}
	_, err := engine.CreateNode(node)
	require.NoError(t, err)

	require.NoError(t, svc.BuildIndexes(context.Background()))

	// Expected vectors:
	// - named: "nornic:doc-vectors-named-titleVec"
	// - chunks: main id + chunk-0 + chunk-1
	// - custom property vector: "nornic:doc-vectors-prop-customVec"
	require.Equal(t, 5, svc.EmbeddingCount())

	named := svc.nodeNamedVector["nornic:doc-vectors"]
	require.NotNil(t, named)
	require.Equal(t, "nornic:doc-vectors-named-titleVec", named["titleVec"])

	props := svc.nodePropVector["nornic:doc-vectors"]
	require.NotNil(t, props)
	require.Equal(t, "nornic:doc-vectors-prop-customVec", props["customVec"])

	chunks := svc.nodeChunkVectors["nornic:doc-vectors"]
	require.Contains(t, chunks, "nornic:doc-vectors")
	require.Contains(t, chunks, "nornic:doc-vectors-chunk-0")
	require.Contains(t, chunks, "nornic:doc-vectors-chunk-1")
}
