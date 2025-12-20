// Package storage provides namespaced storage engine wrapper for multi-database support.
//
// NamespacedEngine wraps any storage.Engine with automatic key prefixing for database isolation.
// This enables multiple logical databases (tenants) to share a single physical storage backend
// while maintaining complete data isolation.
//
// Key Design:
//   - All node and edge IDs are prefixed with the namespace: "tenant_a:123" instead of "123"
//   - Queries only see data in the current namespace
//   - DROP DATABASE = delete all keys with namespace prefix
//
// Thread Safety:
//
//	Delegates to underlying engine's thread safety guarantees.
//
// Example:
//
//	inner := storage.NewBadgerEngine("./data")
//	tenantA := storage.NewNamespacedEngine(inner, "tenant_a")
//
//	// Creates node with ID "tenant_a:123" in BadgerDB
//	tenantA.CreateNode(&Node{ID: "123", Labels: []string{"Person"}})
//
//	// Only sees nodes with "tenant_a:" prefix
//	nodes, _ := tenantA.AllNodes()
package storage

import (
	"context"
	"fmt"
	"strings"
)

// NamespacedEngine wraps a storage engine with database namespace isolation.
// All node and edge IDs are automatically prefixed with the namespace.
//
// This provides logical database separation within a single physical storage:
//   - Keys are prefixed: "tenant_a:node:123" instead of "node:123"
//   - Queries only see data in the current namespace
//   - DROP DATABASE = delete all keys with prefix
//
// Thread-safe: delegates to underlying engine's thread safety.
type NamespacedEngine struct {
	inner     Engine
	namespace string
	separator string // Default ":"
}

// NewNamespacedEngine creates a namespaced view of the storage engine.
//
// Parameters:
//   - inner: The underlying storage engine (shared across all namespaces)
//   - namespace: The database name (e.g., "tenant_a", "neo4j")
//
// The namespace is used as a key prefix for all operations.
func NewNamespacedEngine(inner Engine, namespace string) *NamespacedEngine {
	return &NamespacedEngine{
		inner:     inner,
		namespace: namespace,
		separator: ":",
	}
}

// Namespace returns the current database namespace.
func (n *NamespacedEngine) Namespace() string {
	return n.namespace
}

// prefixNodeID adds namespace prefix to a node ID.
// "123" → "tenant_a:123"
func (n *NamespacedEngine) prefixNodeID(id NodeID) NodeID {
	return NodeID(n.namespace + n.separator + string(id))
}

// unprefixNodeID removes namespace prefix from a node ID.
// "tenant_a:123" → "123"
func (n *NamespacedEngine) unprefixNodeID(id NodeID) NodeID {
	prefix := n.namespace + n.separator
	s := string(id)
	if strings.HasPrefix(s, prefix) {
		return NodeID(s[len(prefix):])
	}
	return id
}

// prefixEdgeID adds namespace prefix to an edge ID.
func (n *NamespacedEngine) prefixEdgeID(id EdgeID) EdgeID {
	return EdgeID(n.namespace + n.separator + string(id))
}

// unprefixEdgeID removes namespace prefix from an edge ID.
func (n *NamespacedEngine) unprefixEdgeID(id EdgeID) EdgeID {
	prefix := n.namespace + n.separator
	s := string(id)
	if strings.HasPrefix(s, prefix) {
		return EdgeID(s[len(prefix):])
	}
	return id
}

// hasNodePrefix checks if an ID belongs to this namespace.
func (n *NamespacedEngine) hasNodePrefix(id NodeID) bool {
	return strings.HasPrefix(string(id), n.namespace+n.separator)
}

func (n *NamespacedEngine) hasEdgePrefix(id EdgeID) bool {
	return strings.HasPrefix(string(id), n.namespace+n.separator)
}

// ============================================================================
// Node Operations
// ============================================================================

func (n *NamespacedEngine) CreateNode(node *Node) (NodeID, error) {
	// Create a copy with namespaced ID
	namespacedID := n.prefixNodeID(node.ID)
	namespacedNode := &Node{
		ID:           namespacedID,
		Labels:       node.Labels,
		Properties:   node.Properties,
		CreatedAt:    node.CreatedAt,
		UpdatedAt:    node.UpdatedAt,
		DecayScore:   node.DecayScore,
		LastAccessed: node.LastAccessed,
		AccessCount:  node.AccessCount,
		Embedding:    node.Embedding,
	}
	actualID, err := n.inner.CreateNode(namespacedNode)
	if err != nil {
		return "", err
	}
	// Return unprefixed ID to user (user-facing API)
	// But internally, we track the prefixed ID in nodeToConstituent
	return n.unprefixNodeID(actualID), nil
}

func (n *NamespacedEngine) GetNode(id NodeID) (*Node, error) {
	// If ID is already prefixed with this namespace, use it directly
	// Otherwise, prefix it (for composite engines, nodes may already have prefixed IDs)
	var namespacedID NodeID
	if n.hasNodePrefix(id) {
		namespacedID = id
	} else {
		namespacedID = n.prefixNodeID(id)
	}
	node, err := n.inner.GetNode(namespacedID)
	if err != nil {
		return nil, err
	}

	// Return with unprefixed ID (user sees "123", not "tenant_a:123")
	// This is the user-facing API - hide implementation details
	node.ID = n.unprefixNodeID(node.ID)
	return node, nil
}

func (n *NamespacedEngine) UpdateNode(node *Node) error {
	// Handle already-prefixed node IDs (from composite engines)
	var namespacedID NodeID
	if n.hasNodePrefix(node.ID) {
		namespacedID = node.ID
	} else {
		namespacedID = n.prefixNodeID(node.ID)
	}
	namespacedNode := &Node{
		ID:           namespacedID,
		Labels:       node.Labels,
		Properties:   node.Properties,
		CreatedAt:    node.CreatedAt,
		UpdatedAt:    node.UpdatedAt,
		DecayScore:   node.DecayScore,
		LastAccessed: node.LastAccessed,
		AccessCount:  node.AccessCount,
		Embedding:    node.Embedding,
	}
	return n.inner.UpdateNode(namespacedNode)
}

func (n *NamespacedEngine) DeleteNode(id NodeID) error {
	return n.inner.DeleteNode(n.prefixNodeID(id))
}

// ============================================================================
// Edge Operations
// ============================================================================

func (n *NamespacedEngine) CreateEdge(edge *Edge) error {
	// Handle already-prefixed node IDs (from composite engines)
	var startNodeID, endNodeID NodeID
	if n.hasNodePrefix(edge.StartNode) {
		startNodeID = edge.StartNode
	} else {
		startNodeID = n.prefixNodeID(edge.StartNode)
	}
	if n.hasNodePrefix(edge.EndNode) {
		endNodeID = edge.EndNode
	} else {
		endNodeID = n.prefixNodeID(edge.EndNode)
	}

	namespacedEdge := &Edge{
		ID:            n.prefixEdgeID(edge.ID),
		Type:          edge.Type,
		StartNode:     startNodeID,
		EndNode:       endNodeID,
		Properties:    edge.Properties,
		CreatedAt:     edge.CreatedAt,
		UpdatedAt:     edge.UpdatedAt,
		Confidence:    edge.Confidence,
		AutoGenerated: edge.AutoGenerated,
	}
	return n.inner.CreateEdge(namespacedEdge)
}

func (n *NamespacedEngine) GetEdge(id EdgeID) (*Edge, error) {
	namespacedID := n.prefixEdgeID(id)
	edge, err := n.inner.GetEdge(namespacedID)
	if err != nil {
		return nil, err
	}

	// Unprefix all IDs (user-facing API)
	edge.ID = n.unprefixEdgeID(edge.ID)
	edge.StartNode = n.unprefixNodeID(edge.StartNode)
	edge.EndNode = n.unprefixNodeID(edge.EndNode)
	return edge, nil
}

func (n *NamespacedEngine) UpdateEdge(edge *Edge) error {
	// Handle already-prefixed IDs (from composite engines)
	var startNodeID, endNodeID NodeID
	if n.hasNodePrefix(edge.StartNode) {
		startNodeID = edge.StartNode
	} else {
		startNodeID = n.prefixNodeID(edge.StartNode)
	}
	if n.hasNodePrefix(edge.EndNode) {
		endNodeID = edge.EndNode
	} else {
		endNodeID = n.prefixNodeID(edge.EndNode)
	}
	var edgeID EdgeID
	if n.hasEdgePrefix(edge.ID) {
		edgeID = edge.ID
	} else {
		edgeID = n.prefixEdgeID(edge.ID)
	}
	namespacedEdge := &Edge{
		ID:            edgeID,
		Type:          edge.Type,
		StartNode:     startNodeID,
		EndNode:       endNodeID,
		Properties:    edge.Properties,
		CreatedAt:     edge.CreatedAt,
		UpdatedAt:     edge.UpdatedAt,
		Confidence:    edge.Confidence,
		AutoGenerated: edge.AutoGenerated,
	}
	return n.inner.UpdateEdge(namespacedEdge)
}

func (n *NamespacedEngine) DeleteEdge(id EdgeID) error {
	return n.inner.DeleteEdge(n.prefixEdgeID(id))
}

// ============================================================================
// Query Operations - Filter to namespace
// ============================================================================

func (n *NamespacedEngine) GetNodesByLabel(label string) ([]*Node, error) {
	// Get all nodes with label, then filter to our namespace
	allNodes, err := n.inner.GetNodesByLabel(label)
	if err != nil {
		return nil, err
	}

	var filtered []*Node
	for _, node := range allNodes {
		if n.hasNodePrefix(node.ID) {
			node.ID = n.unprefixNodeID(node.ID)
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (n *NamespacedEngine) GetFirstNodeByLabel(label string) (*Node, error) {
	nodes, err := n.GetNodesByLabel(label)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrNotFound
	}
	return nodes[0], nil
}

func (n *NamespacedEngine) GetOutgoingEdges(nodeID NodeID) ([]*Edge, error) {
	// Handle already-prefixed node IDs (from composite engines)
	var namespacedID NodeID
	if n.hasNodePrefix(nodeID) {
		namespacedID = nodeID
	} else {
		namespacedID = n.prefixNodeID(nodeID)
	}
	edges, err := n.inner.GetOutgoingEdges(namespacedID)
	if err != nil {
		return nil, err
	}

	// Filter to edges in our namespace and unprefix
	var filtered []*Edge
	for _, edge := range edges {
		if n.hasEdgePrefix(edge.ID) {
			edge.ID = n.unprefixEdgeID(edge.ID)
			edge.StartNode = n.unprefixNodeID(edge.StartNode)
			edge.EndNode = n.unprefixNodeID(edge.EndNode)
			filtered = append(filtered, edge)
		}
	}
	return filtered, nil
}

func (n *NamespacedEngine) GetIncomingEdges(nodeID NodeID) ([]*Edge, error) {
	// Handle already-prefixed node IDs (from composite engines)
	var namespacedID NodeID
	if n.hasNodePrefix(nodeID) {
		namespacedID = nodeID
	} else {
		namespacedID = n.prefixNodeID(nodeID)
	}
	edges, err := n.inner.GetIncomingEdges(namespacedID)
	if err != nil {
		return nil, err
	}

	// Filter to edges in our namespace and unprefix
	var filtered []*Edge
	for _, edge := range edges {
		if n.hasEdgePrefix(edge.ID) {
			edge.ID = n.unprefixEdgeID(edge.ID)
			edge.StartNode = n.unprefixNodeID(edge.StartNode)
			edge.EndNode = n.unprefixNodeID(edge.EndNode)
			filtered = append(filtered, edge)
		}
	}
	return filtered, nil
}

func (n *NamespacedEngine) GetEdgesBetween(startID, endID NodeID) ([]*Edge, error) {
	// Handle already-prefixed node IDs (from composite engines)
	var startNamespacedID, endNamespacedID NodeID
	if n.hasNodePrefix(startID) {
		startNamespacedID = startID
	} else {
		startNamespacedID = n.prefixNodeID(startID)
	}
	if n.hasNodePrefix(endID) {
		endNamespacedID = endID
	} else {
		endNamespacedID = n.prefixNodeID(endID)
	}
	edges, err := n.inner.GetEdgesBetween(startNamespacedID, endNamespacedID)
	if err != nil {
		return nil, err
	}

	// Filter to edges in our namespace and unprefix
	var filtered []*Edge
	for _, edge := range edges {
		if n.hasEdgePrefix(edge.ID) {
			edge.ID = n.unprefixEdgeID(edge.ID)
			edge.StartNode = n.unprefixNodeID(edge.StartNode)
			edge.EndNode = n.unprefixNodeID(edge.EndNode)
			filtered = append(filtered, edge)
		}
	}
	return filtered, nil
}

func (n *NamespacedEngine) GetEdgeBetween(startID, endID NodeID, edgeType string) *Edge {
	// Handle already-prefixed node IDs (from composite engines)
	var startNamespacedID, endNamespacedID NodeID
	if n.hasNodePrefix(startID) {
		startNamespacedID = startID
	} else {
		startNamespacedID = n.prefixNodeID(startID)
	}
	if n.hasNodePrefix(endID) {
		endNamespacedID = endID
	} else {
		endNamespacedID = n.prefixNodeID(endID)
	}
	edge := n.inner.GetEdgeBetween(startNamespacedID, endNamespacedID, edgeType)
	if edge == nil {
		return nil
	}
	if !n.hasEdgePrefix(edge.ID) {
		return nil
	}
	// Unprefix all IDs (user-facing API)
	edge.ID = n.unprefixEdgeID(edge.ID)
	edge.StartNode = n.unprefixNodeID(edge.StartNode)
	edge.EndNode = n.unprefixNodeID(edge.EndNode)
	return edge
}

func (n *NamespacedEngine) GetEdgesByType(edgeType string) ([]*Edge, error) {
	allEdges, err := n.inner.GetEdgesByType(edgeType)
	if err != nil {
		return nil, err
	}

	var filtered []*Edge
	for _, edge := range allEdges {
		if n.hasEdgePrefix(edge.ID) {
			edge.ID = n.unprefixEdgeID(edge.ID)
			edge.StartNode = n.unprefixNodeID(edge.StartNode)
			edge.EndNode = n.unprefixNodeID(edge.EndNode)
			filtered = append(filtered, edge)
		}
	}
	return filtered, nil
}

func (n *NamespacedEngine) AllNodes() ([]*Node, error) {
	allNodes, err := n.inner.AllNodes()
	if err != nil {
		return nil, err
	}

	var filtered []*Node
	for _, node := range allNodes {
		if n.hasNodePrefix(node.ID) {
			node.ID = n.unprefixNodeID(node.ID)
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (n *NamespacedEngine) AllEdges() ([]*Edge, error) {
	allEdges, err := n.inner.AllEdges()
	if err != nil {
		return nil, err
	}

	var filtered []*Edge
	for _, edge := range allEdges {
		if n.hasEdgePrefix(edge.ID) {
			edge.ID = n.unprefixEdgeID(edge.ID)
			edge.StartNode = n.unprefixNodeID(edge.StartNode)
			edge.EndNode = n.unprefixNodeID(edge.EndNode)
			filtered = append(filtered, edge)
		}
	}
	return filtered, nil
}

func (n *NamespacedEngine) GetAllNodes() []*Node {
	allNodes := n.inner.GetAllNodes()
	var filtered []*Node
	for _, node := range allNodes {
		if n.hasNodePrefix(node.ID) {
			// Keep prefixed ID - no unprefixing needed
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// ============================================================================
// Degree Operations
// ============================================================================

func (n *NamespacedEngine) GetInDegree(nodeID NodeID) int {
	return n.inner.GetInDegree(n.prefixNodeID(nodeID))
}

func (n *NamespacedEngine) GetOutDegree(nodeID NodeID) int {
	return n.inner.GetOutDegree(n.prefixNodeID(nodeID))
}

// ============================================================================
// Schema Operations
// ============================================================================

func (n *NamespacedEngine) GetSchema() *SchemaManager {
	// Schema is shared across namespaces (labels/types are global concepts)
	// But we could namespace this in the future if needed
	return n.inner.GetSchema()
}

// ============================================================================
// Bulk Operations
// ============================================================================

func (n *NamespacedEngine) BulkCreateNodes(nodes []*Node) error {
	namespacedNodes := make([]*Node, len(nodes))
	for i, node := range nodes {
		namespacedNode := *node
		namespacedNode.ID = n.prefixNodeID(node.ID)
		namespacedNodes[i] = &namespacedNode
	}
	return n.inner.BulkCreateNodes(namespacedNodes)
}

func (n *NamespacedEngine) BulkCreateEdges(edges []*Edge) error {
	namespacedEdges := make([]*Edge, len(edges))
	for i, edge := range edges {
		namespacedEdge := *edge
		namespacedEdge.ID = n.prefixEdgeID(edge.ID)
		namespacedEdge.StartNode = n.prefixNodeID(edge.StartNode)
		namespacedEdge.EndNode = n.prefixNodeID(edge.EndNode)
		namespacedEdges[i] = &namespacedEdge
	}
	return n.inner.BulkCreateEdges(namespacedEdges)
}

func (n *NamespacedEngine) BulkDeleteNodes(ids []NodeID) error {
	namespacedIDs := make([]NodeID, len(ids))
	for i, id := range ids {
		namespacedIDs[i] = n.prefixNodeID(id)
	}
	return n.inner.BulkDeleteNodes(namespacedIDs)
}

func (n *NamespacedEngine) BulkDeleteEdges(ids []EdgeID) error {
	namespacedIDs := make([]EdgeID, len(ids))
	for i, id := range ids {
		namespacedIDs[i] = n.prefixEdgeID(id)
	}
	return n.inner.BulkDeleteEdges(namespacedIDs)
}

// ============================================================================
// Batch Operations
// ============================================================================

func (n *NamespacedEngine) BatchGetNodes(ids []NodeID) (map[NodeID]*Node, error) {
	namespacedIDs := make([]NodeID, len(ids))
	for i, id := range ids {
		namespacedIDs[i] = n.prefixNodeID(id)
	}

	result, err := n.inner.BatchGetNodes(namespacedIDs)
	if err != nil {
		return nil, err
	}

	// Unprefix all returned nodes (user-facing API)
	unprefixed := make(map[NodeID]*Node, len(result))
	for namespacedID, node := range result {
		unprefixedID := n.unprefixNodeID(namespacedID)
		node.ID = unprefixedID
		unprefixed[unprefixedID] = node
	}
	return unprefixed, nil
}

// ============================================================================
// Lifecycle
// ============================================================================

func (n *NamespacedEngine) Close() error {
	// Don't close the inner engine - it's shared across namespaces
	// The DatabaseManager will handle closing the underlying engine
	return nil
}

// ============================================================================
// Stats
// ============================================================================

func (n *NamespacedEngine) NodeCount() (int64, error) {
	// Count nodes in our namespace
	nodes, err := n.AllNodes()
	if err != nil {
		return 0, err
	}
	return int64(len(nodes)), nil
}

func (n *NamespacedEngine) EdgeCount() (int64, error) {
	// Count edges in our namespace
	edges, err := n.AllEdges()
	if err != nil {
		return 0, err
	}
	return int64(len(edges)), nil
}

// ============================================================================
// Streaming Support (if underlying engine supports it)
// ============================================================================

// StreamNodes streams nodes in the namespace.
func (n *NamespacedEngine) StreamNodes(ctx context.Context, fn func(node *Node) error) error {
	if streamer, ok := n.inner.(StreamingEngine); ok {
		return streamer.StreamNodes(ctx, func(node *Node) error {
			if n.hasNodePrefix(node.ID) {
				node.ID = n.unprefixNodeID(node.ID)
				return fn(node)
			}
			return nil // Skip nodes not in our namespace
		})
	}
	// Fallback to AllNodes
	nodes, err := n.AllNodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if err := fn(node); err != nil {
			return err
		}
	}
	return nil
}

// StreamEdges streams edges in the namespace.
func (n *NamespacedEngine) StreamEdges(ctx context.Context, fn func(edge *Edge) error) error {
	if streamer, ok := n.inner.(StreamingEngine); ok {
		return streamer.StreamEdges(ctx, func(edge *Edge) error {
			if n.hasEdgePrefix(edge.ID) {
				edge.ID = n.unprefixEdgeID(edge.ID)
				edge.StartNode = n.unprefixNodeID(edge.StartNode)
				edge.EndNode = n.unprefixNodeID(edge.EndNode)
				return fn(edge)
			}
			return nil // Skip edges not in our namespace
		})
	}
	// Fallback to AllEdges
	edges, err := n.AllEdges()
	if err != nil {
		return err
	}
	for _, edge := range edges {
		if err := fn(edge); err != nil {
			return err
		}
	}
	return nil
}

// StreamNodeChunks streams nodes in chunks.
func (n *NamespacedEngine) StreamNodeChunks(ctx context.Context, chunkSize int, fn func(nodes []*Node) error) error {
	if streamer, ok := n.inner.(StreamingEngine); ok {
		return streamer.StreamNodeChunks(ctx, chunkSize, func(nodes []*Node) error {
			var filtered []*Node
			for _, node := range nodes {
				if n.hasNodePrefix(node.ID) {
					node.ID = n.unprefixNodeID(node.ID)
					filtered = append(filtered, node)
				}
			}
			if len(filtered) > 0 {
				return fn(filtered)
			}
			return nil
		})
	}
	// Fallback
	nodes, err := n.AllNodes()
	if err != nil {
		return err
	}
	for i := 0; i < len(nodes); i += chunkSize {
		end := i + chunkSize
		if end > len(nodes) {
			end = len(nodes)
		}
		if err := fn(nodes[i:end]); err != nil {
			return err
		}
	}
	return nil
}

// DeleteByPrefix is not supported for NamespacedEngine.
// Use the underlying engine's DeleteByPrefix with the namespace prefix instead.
func (n *NamespacedEngine) DeleteByPrefix(prefix string) (nodesDeleted int64, edgesDeleted int64, err error) {
	// NamespacedEngine doesn't support DeleteByPrefix directly.
	// The DatabaseManager should call DeleteByPrefix on the underlying engine
	// with the full namespace prefix (e.g., "tenant_a:").
	return 0, 0, fmt.Errorf("DeleteByPrefix not supported on NamespacedEngine - use underlying engine with namespace prefix")
}

// Ensure NamespacedEngine implements Engine interface
var _ Engine = (*NamespacedEngine)(nil)

// Ensure NamespacedEngine implements StreamingEngine if inner does
var _ StreamingEngine = (*NamespacedEngine)(nil)
