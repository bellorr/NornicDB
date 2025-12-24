// Package storage provides storage engine implementations for NornicDB.
package storage

import (
	"bytes"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

// Query Operations
// ============================================================================

// GetFirstNodeByLabel returns the first node with the specified label.
// This is optimized for MATCH...LIMIT 1 patterns - stops after first match.
func (b *BadgerEngine) GetFirstNodeByLabel(label string) (*Node, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	var node *Node
	err := b.db.View(func(txn *badger.Txn) error {
		prefix := labelIndexPrefix(label)
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			nodeID := extractNodeIDFromLabelIndex(it.Item().Key(), len(label))
			if nodeID == "" {
				continue
			}

			item, err := txn.Get(nodeKey(nodeID))
			if err != nil {
				continue
			}

			if err := item.Value(func(val []byte) error {
				var decodeErr error
				node, decodeErr = decodeNodeWithEmbeddings(txn, val, nodeID)
				return decodeErr
			}); err != nil {
				continue
			}

			return nil // Found first node, stop
		}
		return nil
	})

	return node, err
}

// GetNodesByLabel returns all nodes with the specified label.
func (b *BadgerEngine) GetNodesByLabel(label string) ([]*Node, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	// Single-pass: iterate label index and fetch nodes in same transaction
	// This reduces transaction overhead compared to two-phase approach
	var nodes []*Node
	err := b.db.View(func(txn *badger.Txn) error {
		prefix := labelIndexPrefix(label)
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false // Don't need values from index keys
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			nodeID := extractNodeIDFromLabelIndex(it.Item().Key(), len(label))
			if nodeID == "" {
				continue
			}

			// Fetch node data in same transaction
			item, err := txn.Get(nodeKey(nodeID))
			if err != nil {
				continue // Skip if node was deleted
			}

			var node *Node
			if err := item.Value(func(val []byte) error {
				var decodeErr error
				node, decodeErr = decodeNodeWithEmbeddings(txn, val, nodeID)
				return decodeErr
			}); err != nil {
				continue
			}

			nodes = append(nodes, node)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return nodes, nil
}

// GetAllNodes returns all nodes in the storage.
func (b *BadgerEngine) GetAllNodes() []*Node {
	nodes, _ := b.AllNodes()
	return nodes
}

// AllNodes returns all nodes (implements Engine interface).
func (b *BadgerEngine) AllNodes() ([]*Node, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	var nodes []*Node
	err := b.db.View(func(txn *badger.Txn) error {
		prefix := []byte{prefixNode}
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			// Extract nodeID from key (skip prefix byte)
			key := it.Item().Key()
			if len(key) <= 1 {
				continue
			}
			nodeID := NodeID(key[1:])

			var node *Node
			if err := it.Item().Value(func(val []byte) error {
				var decodeErr error
				node, decodeErr = decodeNodeWithEmbeddings(txn, val, nodeID)
				return decodeErr
			}); err != nil {
				continue
			}

			nodes = append(nodes, node)
		}

		return nil
	})

	return nodes, err
}

// AllEdges returns all edges (implements Engine interface).
func (b *BadgerEngine) AllEdges() ([]*Edge, error) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	var edges []*Edge
	err := b.db.View(func(txn *badger.Txn) error {
		prefix := []byte{prefixEdge}
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			var edge *Edge
			if err := it.Item().Value(func(val []byte) error {
				var decodeErr error
				edge, decodeErr = decodeEdge(val)
				return decodeErr
			}); err != nil {
				continue
			}

			edges = append(edges, edge)
		}

		return nil
	})

	return edges, err
}

// GetEdgesByType returns all edges of a specific type using the edge type index.
// This is MUCH faster than AllEdges() for queries like mutual follows.
// Edge types are matched case-insensitively (Neo4j compatible).
// Results are cached per type to speed up repeated queries.
func (b *BadgerEngine) GetEdgesByType(edgeType string) ([]*Edge, error) {
	if edgeType == "" {
		return b.AllEdges() // No type filter = all edges
	}

	normalizedType := strings.ToLower(edgeType)

	// Check cache first
	b.edgeTypeCacheMu.RLock()
	if cached, ok := b.edgeTypeCache[normalizedType]; ok {
		b.edgeTypeCacheMu.RUnlock()
		return cached, nil
	}
	b.edgeTypeCacheMu.RUnlock()

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	var edges []*Edge
	err := b.db.View(func(txn *badger.Txn) error {
		prefix := edgeTypeIndexPrefix(edgeType)
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false // We only need the key to get edgeID
		it := txn.NewIterator(opts)
		defer it.Close()

		// Collect edge IDs from index
		var edgeIDs []EdgeID
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := it.Item().Key()
			// Extract edgeID from key: prefix + type + 0x00 + edgeID
			sepIdx := bytes.LastIndexByte(key, 0x00)
			if sepIdx >= 0 && sepIdx < len(key)-1 {
				edgeIDs = append(edgeIDs, EdgeID(key[sepIdx+1:]))
			}
		}

		// Batch fetch edges
		edges = make([]*Edge, 0, len(edgeIDs))
		for _, edgeID := range edgeIDs {
			item, err := txn.Get(edgeKey(edgeID))
			if err != nil {
				continue
			}

			var edge *Edge
			if err := item.Value(func(val []byte) error {
				var decodeErr error
				edge, decodeErr = decodeEdge(val)
				return decodeErr
			}); err != nil {
				continue
			}

			edges = append(edges, edge)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Cache the result (simple LRU-style: clear if too many types)
	b.edgeTypeCacheMu.Lock()
	if len(b.edgeTypeCache) > 50 {
		b.edgeTypeCache = make(map[string][]*Edge, 50)
	}
	b.edgeTypeCache[normalizedType] = edges
	b.edgeTypeCacheMu.Unlock()

	return edges, nil
}

// InvalidateEdgeTypeCache clears the entire edge type cache.
// Called after bulk edge mutations to ensure cache consistency.
func (b *BadgerEngine) InvalidateEdgeTypeCache() {
	b.edgeTypeCacheMu.Lock()
	b.edgeTypeCache = make(map[string][]*Edge, 50)
	b.edgeTypeCacheMu.Unlock()
}

// InvalidateEdgeTypeCacheForType removes only the specified edge type from cache.
// Much faster than full invalidation for single-edge operations.
func (b *BadgerEngine) InvalidateEdgeTypeCacheForType(edgeType string) {
	if edgeType == "" {
		return
	}
	normalizedType := strings.ToLower(edgeType)
	b.edgeTypeCacheMu.Lock()
	delete(b.edgeTypeCache, normalizedType)
	b.edgeTypeCacheMu.Unlock()
}

// BatchGetNodes fetches multiple nodes in a single transaction.
// Returns a map for O(1) lookup by ID. Missing nodes are not included in the result.
// This is optimized for traversal operations that need to fetch many nodes.
func (b *BadgerEngine) BatchGetNodes(ids []NodeID) (map[NodeID]*Node, error) {
	if len(ids) == 0 {
		return make(map[NodeID]*Node), nil
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	result := make(map[NodeID]*Node, len(ids))
	err := b.db.View(func(txn *badger.Txn) error {
		for _, id := range ids {
			if id == "" {
				continue
			}

			item, err := txn.Get(nodeKey(id))
			if err != nil {
				continue // Skip missing nodes
			}

			var node *Node
			if err := item.Value(func(val []byte) error {
				var decodeErr error
				node, decodeErr = decodeNodeWithEmbeddings(txn, val, id)
				return decodeErr
			}); err != nil {
				continue
			}

			result[id] = node
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetOutgoingEdges returns all edges where the given node is the source.
func (b *BadgerEngine) GetOutgoingEdges(nodeID NodeID) ([]*Edge, error) {
	if nodeID == "" {
		return nil, ErrInvalidID
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	var edges []*Edge
	err := b.db.View(func(txn *badger.Txn) error {
		prefix := outgoingIndexPrefix(nodeID)
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			edgeID := extractEdgeIDFromIndexKey(it.Item().Key())
			if edgeID == "" {
				continue
			}

			// Get the edge
			item, err := txn.Get(edgeKey(edgeID))
			if err != nil {
				continue
			}

			var edge *Edge
			if err := item.Value(func(val []byte) error {
				var decodeErr error
				edge, decodeErr = decodeEdge(val)
				return decodeErr
			}); err != nil {
				continue
			}

			edges = append(edges, edge)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return edges, nil
}

// GetIncomingEdges returns all edges where the given node is the target.
func (b *BadgerEngine) GetIncomingEdges(nodeID NodeID) ([]*Edge, error) {
	if nodeID == "" {
		return nil, ErrInvalidID
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	var edges []*Edge
	err := b.db.View(func(txn *badger.Txn) error {
		prefix := incomingIndexPrefix(nodeID)
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			edgeID := extractEdgeIDFromIndexKey(it.Item().Key())
			if edgeID == "" {
				continue
			}

			// Get the edge
			item, err := txn.Get(edgeKey(edgeID))
			if err != nil {
				continue
			}

			var edge *Edge
			if err := item.Value(func(val []byte) error {
				var decodeErr error
				edge, decodeErr = decodeEdge(val)
				return decodeErr
			}); err != nil {
				continue
			}

			edges = append(edges, edge)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return edges, nil
}

// GetEdgesBetween returns all edges between two nodes.
func (b *BadgerEngine) GetEdgesBetween(startID, endID NodeID) ([]*Edge, error) {
	if startID == "" || endID == "" {
		return nil, ErrInvalidID
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil, ErrStorageClosed
	}
	b.mu.RUnlock()

	outgoing, err := b.GetOutgoingEdges(startID)
	if err != nil {
		return nil, err
	}

	var result []*Edge
	for _, edge := range outgoing {
		if edge.EndNode == endID {
			result = append(result, edge)
		}
	}

	return result, nil
}

// GetEdgeBetween returns an edge between two nodes with the given type.
func (b *BadgerEngine) GetEdgeBetween(source, target NodeID, edgeType string) *Edge {
	edges, err := b.GetEdgesBetween(source, target)
	if err != nil {
		return nil
	}

	for _, edge := range edges {
		if edgeType == "" || edge.Type == edgeType {
			return edge
		}
	}

	return nil
}

// ============================================================================
