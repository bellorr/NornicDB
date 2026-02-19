package storage_test

import (
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestBadgerEngine_UpdateNodeEmbedding_LargeChunkCount(t *testing.T) {
	engine, err := storage.NewBadgerEngineInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })

	const nodeID = storage.NodeID("nornic:large-embed")
	_, err = engine.CreateNode(&storage.Node{
		ID:     nodeID,
		Labels: []string{"Doc"},
		Properties: map[string]any{
			"path":    "/tmp/large.md",
			"content": "large embedding update regression test",
		},
	})
	require.NoError(t, err)

	const (
		chunkCount = 9000
		dims       = 192
	)
	chunks := make([][]float32, chunkCount)
	for i := 0; i < chunkCount; i++ {
		emb := make([]float32, dims)
		emb[0] = float32(i)
		emb[dims-1] = 0.5
		chunks[i] = emb
	}

	err = engine.UpdateNodeEmbedding(&storage.Node{
		ID:              nodeID,
		ChunkEmbeddings: chunks,
		Properties: map[string]any{
			"has_embedding":        true,
			"embedding_model":      "test-model",
			"embedding_dimensions": int64(dims),
		},
	})
	require.NoError(t, err)

	got, err := engine.GetNode(nodeID)
	require.NoError(t, err)
	require.Len(t, got.ChunkEmbeddings, chunkCount)
	require.Len(t, got.ChunkEmbeddings[42], dims)
	require.Equal(t, float32(42), got.ChunkEmbeddings[42][0])
}
