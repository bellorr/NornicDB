package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBadgerEngine_DeleteByPrefix(t *testing.T) {
	engine, err := NewBadgerEngineInMemory()
	require.NoError(t, err)
	defer engine.Close()

	// Create nodes with different prefixes
	prefixes := []string{"tenant_a:", "tenant_b:", "tenant_c:"}
	for _, prefix := range prefixes {
		for i := 0; i < 3; i++ {
			node := &Node{
				ID:     NodeID(prefix + "node-" + string(rune('0'+i))),
				Labels: []string{"Test"},
				Properties: map[string]any{
					"prefix": prefix,
				},
			}
			err := engine.CreateNode(node)
			require.NoError(t, err)
		}
	}

	// Verify all nodes exist
	allNodes, err := engine.AllNodes()
	require.NoError(t, err)
	assert.Len(t, allNodes, 9)

	// Delete tenant_a prefix
	nodesDeleted, edgesDeleted, err := engine.DeleteByPrefix("tenant_a:")
	require.NoError(t, err)
	assert.Equal(t, int64(3), nodesDeleted)
	assert.Equal(t, int64(0), edgesDeleted) // No edges

	// Verify tenant_a nodes are gone
	allNodes, err = engine.AllNodes()
	require.NoError(t, err)
	assert.Len(t, allNodes, 6)

	// Verify other tenants still exist
	for _, node := range allNodes {
		prefix := node.Properties["prefix"].(string)
		assert.NotEqual(t, "tenant_a:", prefix)
	}

	// Delete tenant_b prefix
	nodesDeleted, edgesDeleted, err = engine.DeleteByPrefix("tenant_b:")
	require.NoError(t, err)
	assert.Equal(t, int64(3), nodesDeleted)

	// Only tenant_c should remain
	allNodes, err = engine.AllNodes()
	require.NoError(t, err)
	assert.Len(t, allNodes, 3)
}

func TestBadgerEngine_DeleteByPrefix_WithEdges(t *testing.T) {
	engine, err := NewBadgerEngineInMemory()
	require.NoError(t, err)
	defer engine.Close()

	// Create nodes and edges with prefix
	prefix := "tenant_a:"
	node1 := &Node{ID: NodeID(prefix + "n1"), Labels: []string{"Person"}}
	node2 := &Node{ID: NodeID(prefix + "n2"), Labels: []string{"Person"}}
	err = engine.CreateNode(node1)
	require.NoError(t, err)
	err = engine.CreateNode(node2)
	require.NoError(t, err)

	edge := &Edge{
		ID:        EdgeID(prefix + "e1"),
		StartNode: NodeID(prefix + "n1"),
		EndNode:   NodeID(prefix + "n2"),
		Type:      "KNOWS",
	}
	err = engine.CreateEdge(edge)
	require.NoError(t, err)

	// Delete by prefix
	nodesDeleted, edgesDeleted, err := engine.DeleteByPrefix(prefix)
	require.NoError(t, err)
	assert.Equal(t, int64(2), nodesDeleted)
	assert.Equal(t, int64(1), edgesDeleted) // Edge deleted with nodes

	// Verify everything is gone
	allNodes, err := engine.AllNodes()
	require.NoError(t, err)
	assert.Len(t, allNodes, 0)

	allEdges, err := engine.AllEdges()
	require.NoError(t, err)
	assert.Len(t, allEdges, 0)
}

func TestBadgerEngine_DeleteByPrefix_EmptyPrefix(t *testing.T) {
	engine, err := NewBadgerEngineInMemory()
	require.NoError(t, err)
	defer engine.Close()

	_, _, err = engine.DeleteByPrefix("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prefix cannot be empty")
}

func TestBadgerEngine_DeleteByPrefix_NoMatches(t *testing.T) {
	engine, err := NewBadgerEngineInMemory()
	require.NoError(t, err)
	defer engine.Close()

	// Create node with different prefix
	node := &Node{ID: NodeID("other:node"), Labels: []string{"Test"}}
	err = engine.CreateNode(node)
	require.NoError(t, err)

	// Delete non-matching prefix
	nodesDeleted, edgesDeleted, err := engine.DeleteByPrefix("tenant_a:")
	require.NoError(t, err)
	assert.Equal(t, int64(0), nodesDeleted)
	assert.Equal(t, int64(0), edgesDeleted)

	// Original node should still exist
	allNodes, err := engine.AllNodes()
	require.NoError(t, err)
	assert.Len(t, allNodes, 1)
}

func TestMemoryEngine_DeleteByPrefix(t *testing.T) {
	engine := NewMemoryEngine()
	defer engine.Close()

	// Create nodes with prefix
	for i := 0; i < 3; i++ {
		node := &Node{
			ID:     NodeID("tenant_a:node-" + string(rune('0'+i))),
			Labels: []string{"Test"},
		}
		err := engine.CreateNode(node)
		require.NoError(t, err)
	}

	// Delete by prefix
	nodesDeleted, edgesDeleted, err := engine.DeleteByPrefix("tenant_a:")
	require.NoError(t, err)
	assert.Equal(t, int64(3), nodesDeleted)
	assert.Equal(t, int64(0), edgesDeleted)

	// Verify deleted
	allNodes, err := engine.AllNodes()
	require.NoError(t, err)
	assert.Len(t, allNodes, 0)
}

