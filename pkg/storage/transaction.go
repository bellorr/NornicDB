// Package storage - Transaction support for atomic operations.
//
// This file implements transaction semantics for NornicDB storage operations,
// enabling ACID-like behavior for graph modifications.
//
// # Transaction Semantics
//
// Transactions provide:
//   - Atomicity: All operations commit together or none do
//   - Isolation: Changes are invisible until commit
//   - Durability: Committed changes are persisted (for persistent engines)
//
// # Implementation Strategy
//
// We use a Write-Ahead Log (WAL) pattern:
//  1. BEGIN: Create transaction, record starting state
//  2. Operations: Buffer all writes, track old values for rollback
//  3. COMMIT: Apply all buffered operations atomically
//  4. ROLLBACK: Discard buffer, restore any partial changes
//
// # ELI12 (Explain Like I'm 12)
//
// Imagine you're moving furniture in your room:
//
//	BEGIN = "I'm going to rearrange my room"
//	OPERATIONS = Moving furniture around (but not committing yet)
//	COMMIT = "Yes! I like this arrangement, keep it!"
//	ROLLBACK = "Nope, put everything back where it was"
//
// The transaction remembers where everything was before, so if you
// change your mind (ROLLBACK), everything goes back to the original spots!
package storage

import (
	"errors"
	"sync"
	"time"
)

// Transaction errors
var (
	ErrNoTransaction       = errors.New("no active transaction")
	ErrTransactionActive   = errors.New("transaction already active")
	ErrTransactionClosed   = errors.New("transaction already closed")
	ErrTransactionRollback = errors.New("transaction rolled back")
)

// TransactionStatus represents the current state of a transaction.
type TransactionStatus string

const (
	TxStatusActive    TransactionStatus = "active"
	TxStatusCommitted TransactionStatus = "committed"
	TxStatusRolledBack TransactionStatus = "rolled_back"
)

// OperationType represents the type of operation in a transaction.
type OperationType string

const (
	OpCreateNode OperationType = "create_node"
	OpUpdateNode OperationType = "update_node"
	OpDeleteNode OperationType = "delete_node"
	OpCreateEdge OperationType = "create_edge"
	OpUpdateEdge OperationType = "update_edge"
	OpDeleteEdge OperationType = "delete_edge"
)

// Operation represents a single operation within a transaction.
type Operation struct {
	Type      OperationType
	Timestamp time.Time

	// For node operations
	NodeID   NodeID
	Node     *Node // New state (for create/update) or nil
	OldNode  *Node // Old state (for update/delete rollback)

	// For edge operations
	EdgeID   EdgeID
	Edge     *Edge // New state (for create/update) or nil
	OldEdge  *Edge // Old state (for update/delete rollback)
}

// Transaction represents an atomic unit of work.
//
// All operations within a transaction are buffered and only applied
// to the underlying storage on commit. If rollback is called, all
// buffered operations are discarded.
type Transaction struct {
	mu sync.Mutex

	// Transaction identity
	ID        string
	StartTime time.Time
	Status    TransactionStatus

	// Buffered operations (applied on commit)
	operations []Operation

	// Reference to storage engine
	engine *MemoryEngine

	// Pending node/edge states for read-your-writes
	pendingNodes map[NodeID]*Node
	pendingEdges map[EdgeID]*Edge
	deletedNodes map[NodeID]struct{}
	deletedEdges map[EdgeID]struct{}
}

// NewTransaction creates a new transaction bound to a storage engine.
func NewTransaction(engine *MemoryEngine) *Transaction {
	return &Transaction{
		ID:           generateTxID(),
		StartTime:    time.Now(),
		Status:       TxStatusActive,
		engine:       engine,
		operations:   make([]Operation, 0),
		pendingNodes: make(map[NodeID]*Node),
		pendingEdges: make(map[EdgeID]*Edge),
		deletedNodes: make(map[NodeID]struct{}),
		deletedEdges: make(map[EdgeID]struct{}),
	}
}

// generateTxID generates a unique transaction ID.
func generateTxID() string {
	return "tx-" + time.Now().Format("20060102150405.000000")
}

// IsActive returns true if the transaction is still active.
func (tx *Transaction) IsActive() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.Status == TxStatusActive
}

// CreateNode buffers a node creation operation.
func (tx *Transaction) CreateNode(node *Node) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return ErrTransactionClosed
	}

	// Check if node already exists in storage or pending
	if _, exists := tx.pendingNodes[node.ID]; exists {
		return ErrAlreadyExists
	}
	if _, deleted := tx.deletedNodes[node.ID]; !deleted {
		// Check underlying storage
		tx.engine.mu.RLock()
		_, exists := tx.engine.nodes[node.ID]
		tx.engine.mu.RUnlock()
		if exists {
			return ErrAlreadyExists
		}
	}

	// Deep copy and buffer
	nodeCopy := copyNode(node)
	tx.pendingNodes[node.ID] = nodeCopy
	delete(tx.deletedNodes, node.ID) // In case it was previously deleted in this tx

	tx.operations = append(tx.operations, Operation{
		Type:      OpCreateNode,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Node:      nodeCopy,
	})

	return nil
}

// UpdateNode buffers a node update operation.
func (tx *Transaction) UpdateNode(node *Node) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return ErrTransactionClosed
	}

	// Get old state for rollback
	var oldNode *Node

	// Check pending first (read-your-writes)
	if pending, exists := tx.pendingNodes[node.ID]; exists {
		oldNode = copyNode(pending)
	} else {
		// Check underlying storage
		tx.engine.mu.RLock()
		existing, exists := tx.engine.nodes[node.ID]
		tx.engine.mu.RUnlock()

		if !exists {
			return ErrNotFound
		}
		oldNode = copyNode(existing)
	}

	// Deep copy and buffer
	nodeCopy := copyNode(node)
	tx.pendingNodes[node.ID] = nodeCopy

	tx.operations = append(tx.operations, Operation{
		Type:      OpUpdateNode,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Node:      nodeCopy,
		OldNode:   oldNode,
	})

	return nil
}

// DeleteNode buffers a node deletion operation.
func (tx *Transaction) DeleteNode(nodeID NodeID) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return ErrTransactionClosed
	}

	// Get old state for rollback
	var oldNode *Node

	// Check pending first
	if pending, exists := tx.pendingNodes[nodeID]; exists {
		oldNode = copyNode(pending)
		delete(tx.pendingNodes, nodeID)
	} else {
		// Check underlying storage
		tx.engine.mu.RLock()
		existing, exists := tx.engine.nodes[nodeID]
		tx.engine.mu.RUnlock()

		if !exists {
			return ErrNotFound
		}
		oldNode = copyNode(existing)
	}

	tx.deletedNodes[nodeID] = struct{}{}

	tx.operations = append(tx.operations, Operation{
		Type:      OpDeleteNode,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		OldNode:   oldNode,
	})

	return nil
}

// CreateEdge buffers an edge creation operation.
func (tx *Transaction) CreateEdge(edge *Edge) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return ErrTransactionClosed
	}

	// Check if edge already exists
	if _, exists := tx.pendingEdges[edge.ID]; exists {
		return ErrAlreadyExists
	}
	if _, deleted := tx.deletedEdges[edge.ID]; !deleted {
		tx.engine.mu.RLock()
		_, exists := tx.engine.edges[edge.ID]
		tx.engine.mu.RUnlock()
		if exists {
			return ErrAlreadyExists
		}
	}

	// Verify start/end nodes exist (in pending or storage)
	if !tx.nodeExists(edge.StartNode) {
		return ErrInvalidEdge
	}
	if !tx.nodeExists(edge.EndNode) {
		return ErrInvalidEdge
	}

	// Deep copy and buffer
	edgeCopy := copyEdge(edge)
	tx.pendingEdges[edge.ID] = edgeCopy
	delete(tx.deletedEdges, edge.ID)

	tx.operations = append(tx.operations, Operation{
		Type:      OpCreateEdge,
		Timestamp: time.Now(),
		EdgeID:    edge.ID,
		Edge:      edgeCopy,
	})

	return nil
}

// DeleteEdge buffers an edge deletion operation.
func (tx *Transaction) DeleteEdge(edgeID EdgeID) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return ErrTransactionClosed
	}

	// Get old state for rollback
	var oldEdge *Edge

	// Check pending first
	if pending, exists := tx.pendingEdges[edgeID]; exists {
		oldEdge = copyEdge(pending)
		delete(tx.pendingEdges, edgeID)
	} else {
		// Check underlying storage
		tx.engine.mu.RLock()
		existing, exists := tx.engine.edges[edgeID]
		tx.engine.mu.RUnlock()

		if !exists {
			return ErrNotFound
		}
		oldEdge = copyEdge(existing)
	}

	tx.deletedEdges[edgeID] = struct{}{}

	tx.operations = append(tx.operations, Operation{
		Type:      OpDeleteEdge,
		Timestamp: time.Now(),
		EdgeID:    edgeID,
		OldEdge:   oldEdge,
	})

	return nil
}

// nodeExists checks if a node exists in pending or storage.
// Must be called with tx.mu held.
func (tx *Transaction) nodeExists(nodeID NodeID) bool {
	// Deleted in this transaction?
	if _, deleted := tx.deletedNodes[nodeID]; deleted {
		return false
	}

	// Created in this transaction?
	if _, exists := tx.pendingNodes[nodeID]; exists {
		return true
	}

	// Exists in storage?
	tx.engine.mu.RLock()
	_, exists := tx.engine.nodes[nodeID]
	tx.engine.mu.RUnlock()

	return exists
}

// GetNode retrieves a node, checking pending changes first (read-your-writes).
func (tx *Transaction) GetNode(nodeID NodeID) (*Node, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return nil, ErrTransactionClosed
	}

	// Deleted in this transaction?
	if _, deleted := tx.deletedNodes[nodeID]; deleted {
		return nil, ErrNotFound
	}

	// Check pending (read-your-writes)
	if pending, exists := tx.pendingNodes[nodeID]; exists {
		return copyNode(pending), nil
	}

	// Fall through to storage
	tx.engine.mu.RLock()
	node, exists := tx.engine.nodes[nodeID]
	tx.engine.mu.RUnlock()

	if !exists {
		return nil, ErrNotFound
	}

	return copyNode(node), nil
}

// Commit applies all buffered operations to the storage engine atomically.
func (tx *Transaction) Commit() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return ErrTransactionClosed
	}

	// Apply all operations to storage
	tx.engine.mu.Lock()
	defer tx.engine.mu.Unlock()

	// Pre-validation: Check all constraints before applying any changes
	for _, op := range tx.operations {
		switch op.Type {
		case OpCreateNode:
			if _, exists := tx.engine.nodes[op.NodeID]; exists {
				return ErrAlreadyExists
			}
		case OpCreateEdge:
			if _, exists := tx.engine.edges[op.EdgeID]; exists {
				return ErrAlreadyExists
			}
		}
	}

	// Apply all operations
	for _, op := range tx.operations {
		switch op.Type {
		case OpCreateNode:
			tx.engine.createNodeUnlocked(op.Node)
		case OpUpdateNode:
			tx.engine.updateNodeUnlocked(op.Node)
		case OpDeleteNode:
			tx.engine.deleteNodeUnlocked(op.NodeID)
		case OpCreateEdge:
			tx.engine.createEdgeUnlocked(op.Edge)
		case OpDeleteEdge:
			tx.engine.deleteEdgeUnlocked(op.EdgeID)
		}
	}

	tx.Status = TxStatusCommitted
	return nil
}

// Rollback discards all buffered operations.
func (tx *Transaction) Rollback() error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.Status != TxStatusActive {
		return ErrTransactionClosed
	}

	// Simply discard all pending state
	tx.operations = nil
	tx.pendingNodes = nil
	tx.pendingEdges = nil
	tx.deletedNodes = nil
	tx.deletedEdges = nil

	tx.Status = TxStatusRolledBack
	return nil
}

// OperationCount returns the number of buffered operations.
func (tx *Transaction) OperationCount() int {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return len(tx.operations)
}

// copyNode creates a deep copy of a node.
func copyNode(node *Node) *Node {
	if node == nil {
		return nil
	}

	nodeCopy := &Node{
		ID:        node.ID,
		Labels:    make([]string, 0, len(node.Labels)),
		CreatedAt: node.CreatedAt,
		UpdatedAt: node.UpdatedAt,
	}
	nodeCopy.Labels = append(nodeCopy.Labels, node.Labels...)

	if node.Properties != nil {
		nodeCopy.Properties = make(map[string]interface{})
		for k, v := range node.Properties {
			nodeCopy.Properties[k] = v
		}
	}

	if node.Embedding != nil {
		nodeCopy.Embedding = make([]float32, len(node.Embedding))
		copy(nodeCopy.Embedding, node.Embedding)
	}

	return nodeCopy
}

// copyEdge creates a deep copy of an edge.
func copyEdge(edge *Edge) *Edge {
	if edge == nil {
		return nil
	}

	copy := &Edge{
		ID:        edge.ID,
		StartNode: edge.StartNode,
		EndNode:   edge.EndNode,
		Type:      edge.Type,
		CreatedAt: edge.CreatedAt,
		UpdatedAt: edge.UpdatedAt,
	}

	if edge.Properties != nil {
		copy.Properties = make(map[string]interface{})
		for k, v := range edge.Properties {
			copy.Properties[k] = v
		}
	}

	return copy
}
