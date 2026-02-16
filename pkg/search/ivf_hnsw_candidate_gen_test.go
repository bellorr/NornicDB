package search

import (
	"context"
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestIVFHNSW_UsedAfterClustering_CPUOnly(t *testing.T) {
	t.Setenv("NORNICDB_VECTOR_IVF_HNSW_ENABLED", "true")
	t.Setenv("NORNICDB_VECTOR_IVF_HNSW_MIN_CLUSTER_SIZE", "1")

	engine := storage.NewMemoryEngine()
	svc := NewServiceWithDimensions(engine, 4)
	svc.EnableClustering(nil, 2)
	svc.SetMinEmbeddingsForClustering(1)

	// Two obvious groups, large enough to use an ANN pipeline (>= NSmallMax).
	for i := 0; i < 3000; i++ {
		n := &storage.Node{
			ID:              storage.NodeID("a-" + itoa(i)),
			Labels:          []string{"Doc"},
			ChunkEmbeddings: [][]float32{{1, 0, 0, 0}},
		}
		require.NoError(t, svc.IndexNode(n))
	}
	for i := 0; i < 3000; i++ {
		n := &storage.Node{
			ID:              storage.NodeID("b-" + itoa(i)),
			Labels:          []string{"Doc"},
			ChunkEmbeddings: [][]float32{{0, 1, 0, 0}},
		}
		require.NoError(t, svc.IndexNode(n))
	}

	require.NoError(t, svc.TriggerClustering(context.Background()))

	pipeline, err := svc.getOrCreateVectorPipeline(context.Background())
	require.NoError(t, err)

	// In CPU-only mode, centroid routing should select IVF-HNSW once per-cluster
	// indexes have been built.
	_, ok := pipeline.candidateGen.(*IVFHNSWCandidateGen)
	require.True(t, ok)

	got, err := pipeline.Search(context.Background(), []float32{1, 0, 0, 0}, 5, 0.0)
	require.NoError(t, err)
	require.NotEmpty(t, got)
}
