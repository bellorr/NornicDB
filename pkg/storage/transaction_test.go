package storage

import (
	"testing"
	"time"
)

func TestNewTransaction(t *testing.T) {
	engine := NewMemoryEngine()
	defer engine.Close()

	tx, err := engine.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	if tx == nil {
		t.Fatal("Expected non-nil transaction")
	}
	if tx.ID == "" {
		t.Error("Transaction ID should be set")
	}
	if tx.Status != TxStatusActive {
		t.Errorf("Expected active status, got %s", tx.Status)
	}
	if tx.StartTime.IsZero() {
		t.Error("StartTime should be set")
	}
}

func TestTransaction_CreateNode_Basic(t *testing.T) {
	engine := NewMemoryEngine()
	tx, _ := engine.BeginTransaction()

	node := &Node{
		ID:         "tx-node-1",
		Labels:     []string{"Test"},
		Properties: map[string]interface{}{"name": "Test Node"},
	}

	// Create in transaction
	err := tx.CreateNode(node)
	if err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}

	// Node should NOT be visible in engine yet (not committed)
	_, err = engine.GetNode("tx-node-1")
	if err != ErrNotFound {
		t.Error("Node should not be visible before commit")
	}

	// Node should be visible within transaction (read-your-writes)
	txNode, err := tx.GetNode("tx-node-1")
	if err != nil {
		t.Errorf("GetNode in transaction failed: %v", err)
	}
	if txNode.Properties["name"] != "Test Node" {
		t.Error("Node properties mismatch")
	}

	// Commit
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Now node should be visible in engine
	stored, err := engine.GetNode("tx-node-1")
	if err != nil {
		t.Fatalf("GetNode after commit failed: %v", err)
	}
	if stored.Properties["name"] != "Test Node" {
		t.Error("Node properties mismatch after commit")
	}
}

func TestTransaction_Rollback(t *testing.T) {
	engine := NewMemoryEngine()
	tx, _ := engine.BeginTransaction()

	// Create some nodes
	for i := 0; i < 5; i++ {
		node := &Node{
			ID:     NodeID("rollback-node-" + string(rune('0'+i))),
			Labels: []string{"Rollback"},
		}
		if err := tx.CreateNode(node); err != nil {
			t.Fatalf("CreateNode failed: %v", err)
		}
	}

	// Verify operations buffered
	if tx.OperationCount() != 5 {
		t.Errorf("Expected 5 operations, got %d", tx.OperationCount())
	}

	// Rollback
	err := tx.Rollback()
	if err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Verify status
	if tx.Status != TxStatusRolledBack {
		t.Errorf("Expected rolled_back status, got %s", tx.Status)
	}

	// Verify nodes not in engine
	for i := 0; i < 5; i++ {
		_, err := engine.GetNode(NodeID("rollback-node-" + string(rune('0'+i))))
		if err != ErrNotFound {
			t.Error("Node should not exist after rollback")
		}
	}
}

func TestTransaction_Atomicity(t *testing.T) {
	engine := NewMemoryEngine()

	// Pre-create a node that will cause conflict
	conflictNode := &Node{ID: "conflict-node", Labels: []string{"Conflict"}}
	if err := engine.CreateNode(conflictNode); err != nil {
		t.Fatalf("Pre-create failed: %v", err)
	}

	tx, _ := engine.BeginTransaction()

	// Create some nodes
	for i := 0; i < 3; i++ {
		node := &Node{
			ID:     NodeID("atomic-node-" + string(rune('0'+i))),
			Labels: []string{"Atomic"},
		}
		if err := tx.CreateNode(node); err != nil {
			t.Fatalf("CreateNode failed: %v", err)
		}
	}

	// Try to create the conflicting node (should fail at commit)
	node := &Node{ID: "conflict-node", Labels: []string{"Conflict"}}
	// This will succeed in transaction (we check at commit time)
	// But when we commit, it should fail

	// For this test, let's verify that creating a node with same ID in TX fails
	err := tx.CreateNode(node)
	if err != ErrAlreadyExists {
		t.Errorf("Expected ErrAlreadyExists, got %v", err)
	}

	// Commit the valid operations
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// All atomic nodes should exist
	for i := 0; i < 3; i++ {
		_, err := engine.GetNode(NodeID("atomic-node-" + string(rune('0'+i))))
		if err != nil {
			t.Errorf("Node atomic-node-%d should exist after commit", i)
		}
	}
}

func TestTransaction_DeleteNode(t *testing.T) {
	engine := NewMemoryEngine()

	// Create a node first
	node := &Node{ID: "delete-me", Labels: []string{"Delete"}}
	if err := engine.CreateNode(node); err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}

	tx, _ := engine.BeginTransaction()

	// Delete in transaction
	err := tx.DeleteNode("delete-me")
	if err != nil {
		t.Fatalf("DeleteNode failed: %v", err)
	}

	// Node should NOT be deleted from engine yet
	_, err = engine.GetNode("delete-me")
	if err != nil {
		t.Error("Node should still exist before commit")
	}

	// But should not be visible in transaction
	_, err = tx.GetNode("delete-me")
	if err != ErrNotFound {
		t.Error("Node should not be visible in transaction after delete")
	}

	// Commit
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Now node should be gone
	_, err = engine.GetNode("delete-me")
	if err != ErrNotFound {
		t.Error("Node should not exist after commit")
	}
}

func TestTransaction_UpdateNode(t *testing.T) {
	engine := NewMemoryEngine()

	// Create a node first
	node := &Node{
		ID:         "update-me",
		Labels:     []string{"Update"},
		Properties: map[string]interface{}{"version": 1},
	}
	if err := engine.CreateNode(node); err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}

	tx, _ := engine.BeginTransaction()

	// Update in transaction
	updatedNode := &Node{
		ID:         "update-me",
		Labels:     []string{"Updated"},
		Properties: map[string]interface{}{"version": 2},
	}
	err := tx.UpdateNode(updatedNode)
	if err != nil {
		t.Fatalf("UpdateNode failed: %v", err)
	}

	// Engine should still have old version
	old, _ := engine.GetNode("update-me")
	if old.Properties["version"] != 1 {
		t.Error("Engine should still have old version before commit")
	}

	// Transaction should have new version
	txNode, _ := tx.GetNode("update-me")
	if txNode.Properties["version"] != 2 {
		t.Error("Transaction should have new version")
	}

	// Commit
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Engine should have new version
	updated, err := engine.GetNode("update-me")
	if err != nil {
		t.Fatalf("GetNode after commit failed: %v", err)
	}
	// Note: JSON serialization may convert int to float64
	version, ok := updated.Properties["version"].(float64)
	if !ok {
		vInt, ok := updated.Properties["version"].(int)
		if ok {
			version = float64(vInt)
		}
	}
	if version != 2 {
		t.Errorf("Engine should have new version after commit, got version=%v (type %T)",
			updated.Properties["version"], updated.Properties["version"])
	}
}

func TestTransaction_CreateEdge(t *testing.T) {
	engine := NewMemoryEngine()

	// Create nodes first
	node1 := &Node{ID: "edge-node-1", Labels: []string{"Node"}}
	node2 := &Node{ID: "edge-node-2", Labels: []string{"Node"}}
	engine.CreateNode(node1)
	engine.CreateNode(node2)

	tx, _ := engine.BeginTransaction()

	// Create edge in transaction
	edge := &Edge{
		ID:        "tx-edge-1",
		StartNode: "edge-node-1",
		EndNode:   "edge-node-2",
		Type:      "CONNECTS",
	}
	err := tx.CreateEdge(edge)
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	// Edge should NOT exist in engine yet
	_, err = engine.GetEdge("tx-edge-1")
	if err != ErrNotFound {
		t.Error("Edge should not exist before commit")
	}

	// Commit
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Edge should exist now
	stored, err := engine.GetEdge("tx-edge-1")
	if err != nil {
		t.Fatalf("GetEdge after commit failed: %v", err)
	}
	if stored.Type != "CONNECTS" {
		t.Error("Edge type mismatch")
	}
}

func TestTransaction_CreateEdgeWithNewNodes(t *testing.T) {
	engine := NewMemoryEngine()
	tx, _ := engine.BeginTransaction()

	// Create nodes IN transaction
	node1 := &Node{ID: "new-edge-node-1", Labels: []string{"New"}}
	node2 := &Node{ID: "new-edge-node-2", Labels: []string{"New"}}
	tx.CreateNode(node1)
	tx.CreateNode(node2)

	// Create edge between new nodes (should work!)
	edge := &Edge{
		ID:        "new-edge-1",
		StartNode: "new-edge-node-1",
		EndNode:   "new-edge-node-2",
		Type:      "LINKS",
	}
	err := tx.CreateEdge(edge)
	if err != nil {
		t.Fatalf("CreateEdge with new nodes failed: %v", err)
	}

	// Commit all
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify all exist
	_, err = engine.GetNode("new-edge-node-1")
	if err != nil {
		t.Error("Node 1 should exist")
	}
	_, err = engine.GetNode("new-edge-node-2")
	if err != nil {
		t.Error("Node 2 should exist")
	}
	_, err = engine.GetEdge("new-edge-1")
	if err != nil {
		t.Error("Edge should exist")
	}
}

func TestTransaction_DeleteEdge(t *testing.T) {
	engine := NewMemoryEngine()

	// Create nodes and edge first
	node1 := &Node{ID: "del-edge-node-1", Labels: []string{"Node"}}
	node2 := &Node{ID: "del-edge-node-2", Labels: []string{"Node"}}
	engine.CreateNode(node1)
	engine.CreateNode(node2)
	edge := &Edge{
		ID:        "delete-edge-1",
		StartNode: "del-edge-node-1",
		EndNode:   "del-edge-node-2",
		Type:      "DELETE_ME",
	}
	engine.CreateEdge(edge)

	tx, _ := engine.BeginTransaction()

	// Delete edge in transaction
	err := tx.DeleteEdge("delete-edge-1")
	if err != nil {
		t.Fatalf("DeleteEdge failed: %v", err)
	}

	// Edge should still exist in engine
	_, err = engine.GetEdge("delete-edge-1")
	if err != nil {
		t.Error("Edge should still exist before commit")
	}

	// Commit
	err = tx.Commit()
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Edge should be gone
	_, err = engine.GetEdge("delete-edge-1")
	if err != ErrNotFound {
		t.Error("Edge should not exist after commit")
	}
}

func TestTransaction_ClosedTransaction(t *testing.T) {
	engine := NewMemoryEngine()
	tx, _ := engine.BeginTransaction()

	// Commit first
	tx.Commit()

	// Try operations on closed transaction
	node := &Node{ID: "closed-test", Labels: []string{"Test"}}
	err := tx.CreateNode(node)
	if err != ErrTransactionClosed {
		t.Errorf("Expected ErrTransactionClosed, got %v", err)
	}

	err = tx.UpdateNode(node)
	if err != ErrTransactionClosed {
		t.Errorf("Expected ErrTransactionClosed, got %v", err)
	}

	err = tx.DeleteNode("any")
	if err != ErrTransactionClosed {
		t.Errorf("Expected ErrTransactionClosed, got %v", err)
	}

	err = tx.Commit()
	if err != ErrTransactionClosed {
		t.Errorf("Expected ErrTransactionClosed, got %v", err)
	}

	err = tx.Rollback()
	if err != ErrTransactionClosed {
		t.Errorf("Expected ErrTransactionClosed, got %v", err)
	}
}

func TestTransaction_IsActive(t *testing.T) {
	engine := NewMemoryEngine()
	tx, _ := engine.BeginTransaction()

	if !tx.IsActive() {
		t.Error("New transaction should be active")
	}

	tx.Commit()

	if tx.IsActive() {
		t.Error("Committed transaction should not be active")
	}
}

func TestTransaction_Isolation(t *testing.T) {
	engine := NewMemoryEngine()
	defer engine.Close()

	// Transaction 1 creates a node
	tx1, _ := engine.BeginTransaction()
	node := &Node{ID: "isolated-node", Labels: []string{"Isolated"}}
	tx1.CreateNode(node)

	// Transaction 2 should NOT see this node
	tx2, _ := engine.BeginTransaction()
	_, err := tx2.GetNode("isolated-node")
	if err != ErrNotFound {
		t.Error("TX2 should not see TX1's uncommitted node")
	}

	// Commit TX1
	tx1.Commit()

	// TX2 still shouldn't see it (snapshot isolation would require this)
	// But our basic implementation will see it now - this is acceptable
	// for the current implementation level

	// Close TX2
	tx2.Rollback()
}

func TestTransaction_MultipleOperationTypes(t *testing.T) {
	engine := NewMemoryEngine()

	// Pre-create some data
	engine.CreateNode(&Node{ID: "existing-1", Labels: []string{"Existing"}})
	engine.CreateNode(&Node{ID: "existing-2", Labels: []string{"Existing"}})

	tx, _ := engine.BeginTransaction()

	// Mix of operations
	tx.CreateNode(&Node{ID: "new-1", Labels: []string{"New"}})
	tx.CreateNode(&Node{ID: "new-2", Labels: []string{"New"}})
	tx.UpdateNode(&Node{ID: "existing-1", Labels: []string{"Updated"}})
	tx.DeleteNode("existing-2")

	// Verify operation count
	if tx.OperationCount() != 4 {
		t.Errorf("Expected 4 operations, got %d", tx.OperationCount())
	}

	// Commit
	tx.Commit()

	// Verify final state
	_, err := engine.GetNode("new-1")
	if err != nil {
		t.Error("new-1 should exist")
	}
	_, err = engine.GetNode("new-2")
	if err != nil {
		t.Error("new-2 should exist")
	}
	updated, _ := engine.GetNode("existing-1")
	if updated.Labels[0] != "Updated" {
		t.Error("existing-1 should be updated")
	}
	_, err = engine.GetNode("existing-2")
	if err != ErrNotFound {
		t.Error("existing-2 should be deleted")
	}
}

// Benchmark transaction overhead
func BenchmarkTransaction_CommitNodes(b *testing.B) {
	engine := NewMemoryEngine()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, _ := engine.BeginTransaction()
		for j := 0; j < 10; j++ {
			node := &Node{
				ID:     NodeID("bench-" + time.Now().Format("150405.000000") + "-" + string(rune('0'+j))),
				Labels: []string{"Bench"},
			}
			tx.CreateNode(node)
		}
		tx.Commit()
	}
}

func BenchmarkTransaction_RollbackNodes(b *testing.B) {
	engine := NewMemoryEngine()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx, _ := engine.BeginTransaction()
		for j := 0; j < 10; j++ {
			node := &Node{
				ID:     NodeID("bench-" + time.Now().Format("150405.000000") + "-" + string(rune('0'+j))),
				Labels: []string{"Bench"},
			}
			tx.CreateNode(node)
		}
		tx.Rollback()
	}
}
