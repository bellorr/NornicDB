package nornicdb

import (
	"context"
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

func TestUpdateNode_InvalidatesManagedEmbeddings(t *testing.T) {
	ctx := context.Background()

	db, err := Open(t.TempDir(), nil)
	require.NoError(t, err)
	defer db.Close()

	nodeID := "invalidate-1"
	_, err = db.storage.CreateNode(&storage.Node{
		ID:     storage.NodeID(nodeID),
		Labels: []string{"Test"},
		Properties: map[string]any{
			"id":                   nodeID,
			"name":                 "Alice",
			"has_embedding":        true,
			"embedding_model":      "test-model",
			"embedding_dimensions": 1024,
			"embedded_at":          "2024-01-01T00:00:00Z",
			"chunk_count":          1,
		},
		ChunkEmbeddings: [][]float32{make([]float32, 1024)},
	})
	require.NoError(t, err)

	before, err := db.storage.GetNode(storage.NodeID(nodeID))
	require.NoError(t, err)
	require.NotEmpty(t, before.ChunkEmbeddings, "sanity: node should start with embeddings")

	// Mutate an embeddable property; this should clear managed embeddings + metadata.
	_, err = db.UpdateNode(ctx, nodeID, map[string]interface{}{"name": "Bob"})
	require.NoError(t, err)

	after, err := db.storage.GetNode(storage.NodeID(nodeID))
	require.NoError(t, err)
	require.Empty(t, after.ChunkEmbeddings, "managed embeddings should be cleared on mutation")
	require.NotContains(t, after.Properties, "has_embedding")
	require.NotContains(t, after.Properties, "embedding_model")
	require.NotContains(t, after.Properties, "embedding_dimensions")
	require.NotContains(t, after.Properties, "embedded_at")
	require.NotContains(t, after.Properties, "chunk_count")
}

func TestExecuteCypher_SetInvalidatesManagedEmbeddings(t *testing.T) {
	ctx := context.Background()

	db, err := Open(t.TempDir(), nil)
	require.NoError(t, err)
	defer db.Close()

	nodeID := "invalidate-cypher-1"
	_, err = db.storage.CreateNode(&storage.Node{
		ID:     storage.NodeID(nodeID),
		Labels: []string{"Test"},
		Properties: map[string]any{
			"id":   nodeID,
			"name": "Alice",
		},
		ChunkEmbeddings: [][]float32{make([]float32, 1024)},
	})
	require.NoError(t, err)

	// Cypher SET should invalidate managed embeddings when changing non-metadata properties.
	_, err = db.ExecuteCypher(ctx, "MATCH (n {id: 'invalidate-cypher-1'}) SET n.name = 'Bob' RETURN n", nil)
	require.NoError(t, err)

	after, err := db.storage.GetNode(storage.NodeID(nodeID))
	require.NoError(t, err)
	require.Empty(t, after.ChunkEmbeddings, "managed embeddings should be cleared on Cypher SET mutation")
}

