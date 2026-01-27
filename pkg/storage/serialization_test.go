package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStorageSerializerMsgpackRoundTrip(t *testing.T) {
	prev := currentStorageSerializer()
	require.NoError(t, SetStorageSerializer(StorageSerializerMsgpack))
	t.Cleanup(func() {
		_ = SetStorageSerializer(prev)
	})

	node := &Node{
		ID:         NodeID("node-1"),
		Labels:     []string{"Person"},
		Properties: map[string]any{"age": int64(42), "name": "Alice"},
		CreatedAt:  time.Unix(1700000000, 0).UTC(),
	}

	data, _, err := encodeNode(node)
	require.NoError(t, err)

	decoded, err := decodeNode(data)
	require.NoError(t, err)
	require.Equal(t, node.ID, decoded.ID)
	require.Equal(t, node.Labels, decoded.Labels)
	require.Equal(t, node.Properties, decoded.Properties)
	require.True(t, decoded.CreatedAt.Equal(node.CreatedAt))
}

func TestDecodeNode_LegacyGobFallback(t *testing.T) {
	prev := currentStorageSerializer()
	require.NoError(t, SetStorageSerializer(StorageSerializerMsgpack))
	t.Cleanup(func() {
		_ = SetStorageSerializer(prev)
	})

	node := &Node{
		ID:         NodeID("legacy-node"),
		Labels:     []string{"Legacy"},
		Properties: map[string]any{"count": int64(7)},
	}

	legacyData, err := encodeWithSerializer(StorageSerializerGob, node)
	require.NoError(t, err)

	decoded, err := decodeNode(legacyData)
	require.NoError(t, err)
	require.Equal(t, node.ID, decoded.ID)
	require.Equal(t, node.Labels, decoded.Labels)
	require.Equal(t, node.Properties, decoded.Properties)
}

func TestDetectStoredSerializerMismatchUsesDetected(t *testing.T) {
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
	})
	require.NoError(t, err)
	require.NoError(t, base.Close())

	base2, err := NewBadgerEngineWithOptions(BadgerOptions{
		DataDir:    dir,
		Serializer: StorageSerializerMsgpack,
	})
	require.NoError(t, err)
	defer base2.Close()

	require.Equal(t, StorageSerializerGob, currentStorageSerializer())
}
