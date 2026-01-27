package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrateBadgerSerializer_GobToMsgpack(t *testing.T) {
	prev := currentStorageSerializer()
	t.Cleanup(func() {
		_ = SetStorageSerializer(prev)
	})

	dir := t.TempDir()
	base, err := NewBadgerEngineWithOptions(BadgerOptions{
		DataDir:    dir,
		Serializer: StorageSerializerGob,
	})
	require.NoError(t, err)

	engine := NewNamespacedEngine(base, "test")
	_, err = engine.CreateNode(&Node{
		ID:     NodeID("node-1"),
		Labels: []string{"Person"},
		Properties: map[string]any{
			"name": "Alice",
		},
	})
	require.NoError(t, err)
	_, err = engine.CreateNode(&Node{
		ID:     NodeID("node-2"),
		Labels: []string{"Person"},
		Properties: map[string]any{
			"name": "Bob",
		},
	})
	require.NoError(t, err)
	err = engine.CreateEdge(&Edge{
		ID:        EdgeID("edge-1"),
		StartNode: NodeID("node-1"),
		EndNode:   NodeID("node-2"),
		Type:      "KNOWS",
	})
	require.NoError(t, err)

	_, err = engine.CreateNode(&Node{
		ID:              NodeID("embed-1"),
		Labels:          []string{"Doc"},
		ChunkEmbeddings: [][]float32{make([]float32, 20000)},
	})
	require.NoError(t, err)

	stats, err := MigrateBadgerSerializerWithDB(base.db, dir, StorageSerializerMsgpack, SerializerMigrationOptions{
		BatchSize: 10,
	})
	require.NoError(t, err)
	require.True(t, stats.HasData)
	require.Equal(t, StorageSerializerGob, stats.Source)
	require.Equal(t, StorageSerializerMsgpack, stats.Target)
	require.Greater(t, stats.NodesConverted+stats.EdgesConverted, 0)

	require.NoError(t, base.Close())

	base2, err := NewBadgerEngineWithOptions(BadgerOptions{
		DataDir:    dir,
		Serializer: StorageSerializerMsgpack,
	})
	require.NoError(t, err)

	engine2 := NewNamespacedEngine(base2, "test")
	node, err := engine2.GetNode(NodeID("node-1"))
	require.NoError(t, err)
	require.Equal(t, NodeID("node-1"), node.ID)

	edge, err := engine2.GetEdge(EdgeID("edge-1"))
	require.NoError(t, err)
	require.Equal(t, "KNOWS", edge.Type)

	require.NoError(t, base2.Close())

	stats2, err := MigrateBadgerSerializer(dir, StorageSerializerMsgpack, SerializerMigrationOptions{
		BatchSize: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 0, stats2.NodesConverted+stats2.EdgesConverted+stats2.EmbeddingsConverted)
	require.Greater(t, stats2.SkippedExisting, 0)
}
