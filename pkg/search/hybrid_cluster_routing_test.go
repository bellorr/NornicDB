package search

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestEnableClustering_DefaultMaxIterationsIsFive(t *testing.T) {
	t.Setenv("NORNICDB_KMEANS_MAX_ITERATIONS", "")
	svc := NewServiceWithDimensions(storage.NewMemoryEngine(), 2)
	svc.EnableClustering(nil, 2)
	require.NotNil(t, svc.clusterIndex)
	require.Equal(t, 5, svc.clusterIndex.Config().MaxIterations)
}

func TestSelectHybridClusters_UsesLexicalSignal(t *testing.T) {
	t.Setenv("NORNICDB_VECTOR_HYBRID_ROUTING_W_SEM", "0.1")
	t.Setenv("NORNICDB_VECTOR_HYBRID_ROUTING_W_LEX", "0.9")
	t.Setenv("NORNICDB_KMEANS_MAX_ITERATIONS", "5")

	svc := NewServiceWithDimensions(storage.NewMemoryEngine(), 2)
	svc.EnableClustering(nil, 2)
	svc.SetMinEmbeddingsForClustering(1)

	// Two clear semantic groups.
	require.NoError(t, svc.IndexNode(&storage.Node{ID: storage.NodeID("a1"), Properties: map[string]interface{}{"text": "alpha topic"}, ChunkEmbeddings: [][]float32{{1, 0}}}))
	require.NoError(t, svc.IndexNode(&storage.Node{ID: storage.NodeID("a2"), Properties: map[string]interface{}{"text": "alpha graph"}, ChunkEmbeddings: [][]float32{{0.9, 0.1}}}))
	require.NoError(t, svc.IndexNode(&storage.Node{ID: storage.NodeID("b1"), Properties: map[string]interface{}{"text": "beta api"}, ChunkEmbeddings: [][]float32{{0, 1}}}))
	require.NoError(t, svc.IndexNode(&storage.Node{ID: storage.NodeID("b2"), Properties: map[string]interface{}{"text": "beta auth"}, ChunkEmbeddings: [][]float32{{0.1, 0.9}}}))

	require.NoError(t, svc.TriggerClustering(context.Background()))
	require.True(t, svc.clusterIndex.IsClustered())

	// Semantic prefers first cluster for query [1,0].
	sem := svc.clusterIndex.FindNearestClusters([]float32{1, 0}, 2)
	require.Len(t, sem, 2)
	semFirst, semSecond := sem[0], sem[1]

	// Force lexical preference to second cluster via profile weights.
	svc.clusterLexicalMu.Lock()
	svc.clusterLexicalProfiles = map[int]map[string]float64{
		semFirst:  {"alpha": 1.0},
		semSecond: {"beta": 1.0},
	}
	svc.clusterLexicalMu.Unlock()

	out := svc.selectHybridClusters(withQueryText(context.Background(), "beta auth"), []float32{1, 0}, 1)
	require.Len(t, out, 1)
	require.Equal(t, semSecond, out[0], "lexical signal should override semantic top cluster when weighted higher")
}

func BenchmarkSelectHybridClusters(b *testing.B) {
	_ = os.Setenv("NORNICDB_VECTOR_HYBRID_ROUTING_W_SEM", "0.7")
	_ = os.Setenv("NORNICDB_VECTOR_HYBRID_ROUTING_W_LEX", "0.3")
	defer os.Unsetenv("NORNICDB_VECTOR_HYBRID_ROUTING_W_SEM")
	defer os.Unsetenv("NORNICDB_VECTOR_HYBRID_ROUTING_W_LEX")

	svc := NewServiceWithDimensions(storage.NewMemoryEngine(), 2)
	svc.EnableClustering(nil, 8)
	svc.SetMinEmbeddingsForClustering(1)
	for i := 0; i < 256; i++ {
		id := storage.NodeID(fmt.Sprintf("n-%d", i))
		vec := []float32{float32(i % 2), float32((i + 1) % 2)}
		_ = svc.IndexNode(&storage.Node{ID: id, Properties: map[string]interface{}{"text": "alpha beta gamma"}, ChunkEmbeddings: [][]float32{vec}})
	}
	_ = svc.TriggerClustering(context.Background())

	svc.clusterLexicalMu.Lock()
	if len(svc.clusterLexicalProfiles) == 0 {
		svc.rebuildClusterLexicalProfiles()
	}
	svc.clusterLexicalMu.Unlock()

	ctx := withQueryText(context.Background(), "alpha beta")
	query := []float32{1, 0}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = svc.selectHybridClusters(ctx, query, 3)
	}
}
