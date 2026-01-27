package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFirstNodeIDByLabel_NamespaceFiltering(t *testing.T) {
	base := NewMemoryEngine()
	engineA := NewNamespacedEngine(base, "a")
	engineB := NewNamespacedEngine(base, "b")

	_, err := engineA.CreateNode(&Node{
		ID:     NodeID("node-a"),
		Labels: []string{"Person"},
	})
	require.NoError(t, err)

	_, err = engineB.CreateNode(&Node{
		ID:     NodeID("node-b"),
		Labels: []string{"Person"},
	})
	require.NoError(t, err)

	id, err := FirstNodeIDByLabel(engineA, "Person")
	require.NoError(t, err)
	require.Equal(t, NodeID("node-a"), id)
}

func TestFirstNodeIDByLabel_InvalidatesOnDelete(t *testing.T) {
	base := NewMemoryEngine()
	engine := NewNamespacedEngine(base, "test")

	_, err := engine.CreateNode(&Node{
		ID:     NodeID("node-1"),
		Labels: []string{"Person"},
	})
	require.NoError(t, err)

	_, err = engine.CreateNode(&Node{
		ID:     NodeID("node-2"),
		Labels: []string{"Person"},
	})
	require.NoError(t, err)

	id1, err := FirstNodeIDByLabel(engine, "Person")
	require.NoError(t, err)

	require.NoError(t, engine.DeleteNode(id1))

	id2, err := FirstNodeIDByLabel(engine, "Person")
	require.NoError(t, err)

	if id1 == "node-1" {
		require.Equal(t, NodeID("node-2"), id2)
	} else {
		require.Equal(t, NodeID("node-1"), id2)
	}
}
