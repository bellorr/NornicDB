// Package storage schema management for constraints and indexes.
//
// This file implements Neo4j-compatible schema management including:
//   - Unique constraints
//   - Property indexes (single and composite)
//   - Full-text indexes
//   - Vector indexes
//
// Schema definitions are stored in memory and enforced during node operations.
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

// SchemaManager manages database schema including constraints and indexes.
type SchemaManager struct {
	mu sync.RWMutex

	// Constraints
	uniqueConstraints map[string]*UniqueConstraint // key: "Label:property"

	// Indexes
	propertyIndexes   map[string]*PropertyIndex   // key: "Label:property" (single property)
	compositeIndexes  map[string]*CompositeIndex  // key: index name
	fulltextIndexes   map[string]*FulltextIndex   // key: index_name
	vectorIndexes     map[string]*VectorIndex     // key: index_name
}

// NewSchemaManager creates a new schema manager.
func NewSchemaManager() *SchemaManager {
	return &SchemaManager{
		uniqueConstraints: make(map[string]*UniqueConstraint),
		propertyIndexes:   make(map[string]*PropertyIndex),
		compositeIndexes:  make(map[string]*CompositeIndex),
		fulltextIndexes:   make(map[string]*FulltextIndex),
		vectorIndexes:     make(map[string]*VectorIndex),
	}
}

// UniqueConstraint represents a unique constraint on a label and property.
type UniqueConstraint struct {
	Name     string
	Label    string
	Property string
	values   map[interface{}]NodeID // Track unique values
	mu       sync.RWMutex
}

// PropertyIndex represents a property index for faster lookups.
type PropertyIndex struct {
	Name       string
	Label      string
	Properties []string
	values     map[interface{}][]NodeID // Property value -> node IDs
	mu         sync.RWMutex
}

// CompositeKey represents a key composed of multiple property values.
// The key is a hash of all property values in order for efficient lookup.
type CompositeKey struct {
	Hash   string        // SHA256 hash of encoded values (for map lookup)
	Values []interface{} // Original values (for debugging/display)
}

// NewCompositeKey creates a composite key from multiple property values.
func NewCompositeKey(values ...interface{}) CompositeKey {
	// Create deterministic string representation
	var parts []string
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%T:%v", v, v))
	}
	encoded := strings.Join(parts, "|")

	// Hash for efficient map lookup
	hash := sha256.Sum256([]byte(encoded))

	return CompositeKey{
		Hash:   hex.EncodeToString(hash[:]),
		Values: values,
	}
}

// String returns a human-readable representation of the composite key.
func (ck CompositeKey) String() string {
	var parts []string
	for _, v := range ck.Values {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, ", ")
}

// CompositeIndex represents an index on multiple properties for efficient
// multi-property queries. This is Neo4j's composite index equivalent.
//
// Composite indexes support:
//   - Full key lookups (all properties specified)
//   - Prefix lookups (leading properties specified, for ordered access)
//   - Range queries on the last property in a prefix
type CompositeIndex struct {
	Name       string
	Label      string
	Properties []string // Ordered list of property names

	// Primary index: full composite key -> node IDs
	fullIndex map[string][]NodeID

	// Prefix indexes for partial key lookups
	// Key format: "prop1Value|prop2Value|..." -> node IDs
	prefixIndex map[string][]NodeID

	// Individual property value tracking for range queries
	// propertyValues[propIndex][value] = sorted list of (otherValues, nodeID)
	// This enables efficient range queries on any property

	mu sync.RWMutex
}

// FulltextIndex represents a full-text search index.
type FulltextIndex struct {
	Name       string
	Labels     []string
	Properties []string
}

// VectorIndex represents a vector similarity index.
type VectorIndex struct {
	Name             string
	Label            string
	Property         string
	Dimensions       int
	SimilarityFunc   string // "cosine", "euclidean", "dot"
}

// AddUniqueConstraint adds a unique constraint.
func (sm *SchemaManager) AddUniqueConstraint(name, label, property string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := fmt.Sprintf("%s:%s", label, property)
	if _, exists := sm.uniqueConstraints[key]; exists {
		// Constraint already exists - this is fine with IF NOT EXISTS
		return nil
	}

	sm.uniqueConstraints[key] = &UniqueConstraint{
		Name:     name,
		Label:    label,
		Property: property,
		values:   make(map[interface{}]NodeID),
	}

	return nil
}

// CheckUniqueConstraint checks if a value violates a unique constraint.
// Returns error if constraint is violated.
func (sm *SchemaManager) CheckUniqueConstraint(label, property string, value interface{}, excludeNode NodeID) error {
	sm.mu.RLock()
	key := fmt.Sprintf("%s:%s", label, property)
	constraint, exists := sm.uniqueConstraints[key]
	sm.mu.RUnlock()

	if !exists {
		return nil // No constraint
	}

	constraint.mu.RLock()
	defer constraint.mu.RUnlock()

	if existingNode, found := constraint.values[value]; found {
		if existingNode != excludeNode {
			return fmt.Errorf("Node(%s) already exists with %s = %v", label, property, value)
		}
	}

	return nil
}

// RegisterUniqueValue registers a value for a unique constraint.
func (sm *SchemaManager) RegisterUniqueValue(label, property string, value interface{}, nodeID NodeID) {
	sm.mu.RLock()
	key := fmt.Sprintf("%s:%s", label, property)
	constraint, exists := sm.uniqueConstraints[key]
	sm.mu.RUnlock()

	if !exists {
		return
	}

	constraint.mu.Lock()
	constraint.values[value] = nodeID
	constraint.mu.Unlock()
}

// UnregisterUniqueValue removes a value from a unique constraint.
func (sm *SchemaManager) UnregisterUniqueValue(label, property string, value interface{}) {
	sm.mu.RLock()
	key := fmt.Sprintf("%s:%s", label, property)
	constraint, exists := sm.uniqueConstraints[key]
	sm.mu.RUnlock()

	if !exists {
		return
	}

	constraint.mu.Lock()
	delete(constraint.values, value)
	constraint.mu.Unlock()
}

// AddPropertyIndex adds a property index.
func (sm *SchemaManager) AddPropertyIndex(name, label string, properties []string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := fmt.Sprintf("%s:%s", label, properties[0]) // Use first property as key
	if _, exists := sm.propertyIndexes[key]; exists {
		return nil // Already exists
	}

	sm.propertyIndexes[key] = &PropertyIndex{
		Name:       name,
		Label:      label,
		Properties: properties,
		values:     make(map[interface{}][]NodeID),
	}

	return nil
}

// AddCompositeIndex creates a composite index on multiple properties.
// Composite indexes enable efficient queries that filter on multiple properties.
//
// Example usage:
//
//	sm.AddCompositeIndex("user_location_idx", "User", []string{"country", "city", "zipcode"})
//
// This enables efficient queries like:
//   - WHERE country = 'US' AND city = 'NYC' AND zipcode = '10001' (full match)
//   - WHERE country = 'US' AND city = 'NYC' (prefix match)
//   - WHERE country = 'US' (prefix match, uses first property only)
func (sm *SchemaManager) AddCompositeIndex(name, label string, properties []string) error {
	if len(properties) < 2 {
		return fmt.Errorf("composite index requires at least 2 properties, got %d", len(properties))
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.compositeIndexes[name]; exists {
		return nil // Already exists (idempotent)
	}

	sm.compositeIndexes[name] = &CompositeIndex{
		Name:        name,
		Label:       label,
		Properties:  properties,
		fullIndex:   make(map[string][]NodeID),
		prefixIndex: make(map[string][]NodeID),
	}

	return nil
}

// GetCompositeIndex returns a composite index by name.
func (sm *SchemaManager) GetCompositeIndex(name string) (*CompositeIndex, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	idx, exists := sm.compositeIndexes[name]
	return idx, exists
}

// GetCompositeIndexForLabel returns all composite indexes for a label.
func (sm *SchemaManager) GetCompositeIndexesForLabel(label string) []*CompositeIndex {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var indexes []*CompositeIndex
	for _, idx := range sm.compositeIndexes {
		if idx.Label == label {
			indexes = append(indexes, idx)
		}
	}
	return indexes
}

// IndexNodeComposite indexes a node in a composite index.
// Call this when creating or updating a node with the indexed properties.
func (idx *CompositeIndex) IndexNode(nodeID NodeID, properties map[string]interface{}) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Extract values in property order
	values := make([]interface{}, len(idx.Properties))
	for i, propName := range idx.Properties {
		val, exists := properties[propName]
		if !exists {
			// Node doesn't have all properties - can't be fully indexed
			// But we can still index prefixes
			values = values[:i]
			break
		}
		values[i] = val
	}

	// Index full key if all properties present
	if len(values) == len(idx.Properties) {
		key := NewCompositeKey(values...)
		idx.fullIndex[key.Hash] = appendUnique(idx.fullIndex[key.Hash], nodeID)
	}

	// Index all prefixes for partial lookups
	for i := 1; i <= len(values); i++ {
		prefixKey := NewCompositeKey(values[:i]...)
		idx.prefixIndex[prefixKey.Hash] = appendUnique(idx.prefixIndex[prefixKey.Hash], nodeID)
	}

	return nil
}

// RemoveNode removes a node from the composite index.
// Call this when deleting a node or updating its indexed properties.
func (idx *CompositeIndex) RemoveNode(nodeID NodeID, properties map[string]interface{}) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Extract values in property order
	values := make([]interface{}, 0, len(idx.Properties))
	for _, propName := range idx.Properties {
		val, exists := properties[propName]
		if !exists {
			break
		}
		values = append(values, val)
	}

	// Remove from full index
	if len(values) == len(idx.Properties) {
		key := NewCompositeKey(values...)
		idx.fullIndex[key.Hash] = removeNodeID(idx.fullIndex[key.Hash], nodeID)
		if len(idx.fullIndex[key.Hash]) == 0 {
			delete(idx.fullIndex, key.Hash)
		}
	}

	// Remove from all prefix indexes
	for i := 1; i <= len(values); i++ {
		prefixKey := NewCompositeKey(values[:i]...)
		idx.prefixIndex[prefixKey.Hash] = removeNodeID(idx.prefixIndex[prefixKey.Hash], nodeID)
		if len(idx.prefixIndex[prefixKey.Hash]) == 0 {
			delete(idx.prefixIndex, prefixKey.Hash)
		}
	}
}

// LookupFull finds nodes matching all property values exactly.
// All properties in the composite index must be specified.
func (idx *CompositeIndex) LookupFull(values ...interface{}) []NodeID {
	if len(values) != len(idx.Properties) {
		return nil // Must specify all properties for full lookup
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	key := NewCompositeKey(values...)
	if nodes, exists := idx.fullIndex[key.Hash]; exists {
		// Return a copy to avoid race conditions
		result := make([]NodeID, len(nodes))
		copy(result, nodes)
		return result
	}
	return nil
}

// LookupPrefix finds nodes matching a prefix of property values.
// Specify 1 to N-1 property values (where N is total properties in index).
// Returns all nodes that match the prefix.
//
// Example: For index on (country, city, zipcode)
//   - LookupPrefix("US") returns all nodes in the US
//   - LookupPrefix("US", "NYC") returns all nodes in NYC, US
func (idx *CompositeIndex) LookupPrefix(values ...interface{}) []NodeID {
	if len(values) == 0 || len(values) > len(idx.Properties) {
		return nil
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Check if this is a full match (not a prefix)
	if len(values) == len(idx.Properties) {
		key := NewCompositeKey(values...)
		if nodes, exists := idx.fullIndex[key.Hash]; exists {
			result := make([]NodeID, len(nodes))
			copy(result, nodes)
			return result
		}
		return nil
	}

	// Prefix lookup
	key := NewCompositeKey(values...)
	if nodes, exists := idx.prefixIndex[key.Hash]; exists {
		result := make([]NodeID, len(nodes))
		copy(result, nodes)
		return result
	}
	return nil
}

// LookupWithFilter finds nodes using a prefix and applies a filter function.
// This enables more complex queries like range queries on the last property.
//
// Example: Find all users in "US", "NYC" with zipcode > "10000"
//
//	idx.LookupWithFilter(func(n NodeID, props map[string]interface{}) bool {
//	    zip := props["zipcode"].(string)
//	    return zip > "10000"
//	}, "US", "NYC")
func (idx *CompositeIndex) LookupWithFilter(filter func(NodeID) bool, values ...interface{}) []NodeID {
	candidates := idx.LookupPrefix(values...)
	if candidates == nil {
		return nil
	}

	var result []NodeID
	for _, nodeID := range candidates {
		if filter(nodeID) {
			result = append(result, nodeID)
		}
	}
	return result
}

// Stats returns statistics about the composite index.
func (idx *CompositeIndex) Stats() map[string]interface{} {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	return map[string]interface{}{
		"name":             idx.Name,
		"label":            idx.Label,
		"properties":       idx.Properties,
		"fullIndexEntries": len(idx.fullIndex),
		"prefixEntries":    len(idx.prefixIndex),
	}
}

// appendUnique appends a nodeID to a slice if not already present.
func appendUnique(slice []NodeID, nodeID NodeID) []NodeID {
	for _, existing := range slice {
		if existing == nodeID {
			return slice
		}
	}
	return append(slice, nodeID)
}

// removeNodeID removes a nodeID from a slice.
func removeNodeID(slice []NodeID, nodeID NodeID) []NodeID {
	for i, existing := range slice {
		if existing == nodeID {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// AddFulltextIndex adds a full-text index.
func (sm *SchemaManager) AddFulltextIndex(name string, labels, properties []string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.fulltextIndexes[name]; exists {
		return nil // Already exists
	}

	sm.fulltextIndexes[name] = &FulltextIndex{
		Name:       name,
		Labels:     labels,
		Properties: properties,
	}

	return nil
}

// AddVectorIndex adds a vector index.
func (sm *SchemaManager) AddVectorIndex(name, label, property string, dimensions int, similarityFunc string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.vectorIndexes[name]; exists {
		return nil // Already exists
	}

	sm.vectorIndexes[name] = &VectorIndex{
		Name:           name,
		Label:          label,
		Property:       property,
		Dimensions:     dimensions,
		SimilarityFunc: similarityFunc,
	}

	return nil
}

// GetConstraints returns all unique constraints.
func (sm *SchemaManager) GetConstraints() []UniqueConstraint {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	constraints := make([]UniqueConstraint, 0, len(sm.uniqueConstraints))
	for _, c := range sm.uniqueConstraints {
		constraints = append(constraints, UniqueConstraint{
			Name:     c.Name,
			Label:    c.Label,
			Property: c.Property,
		})
	}

	return constraints
}

// GetIndexes returns all indexes.
func (sm *SchemaManager) GetIndexes() []interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	indexes := make([]interface{}, 0)

	for _, idx := range sm.propertyIndexes {
		indexes = append(indexes, map[string]interface{}{
			"name":       idx.Name,
			"type":       "PROPERTY",
			"label":      idx.Label,
			"properties": idx.Properties,
		})
	}

	for _, idx := range sm.compositeIndexes {
		indexes = append(indexes, map[string]interface{}{
			"name":       idx.Name,
			"type":       "COMPOSITE",
			"label":      idx.Label,
			"properties": idx.Properties,
		})
	}

	for _, idx := range sm.fulltextIndexes {
		indexes = append(indexes, map[string]interface{}{
			"name":       idx.Name,
			"type":       "FULLTEXT",
			"labels":     idx.Labels,
			"properties": idx.Properties,
		})
	}

	for _, idx := range sm.vectorIndexes {
		indexes = append(indexes, map[string]interface{}{
			"name":            idx.Name,
			"type":            "VECTOR",
			"label":           idx.Label,
			"property":        idx.Property,
			"dimensions":      idx.Dimensions,
			"similarityFunc":  idx.SimilarityFunc,
		})
	}

	return indexes
}

// GetVectorIndex returns a vector index by name.
func (sm *SchemaManager) GetVectorIndex(name string) (*VectorIndex, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	idx, exists := sm.vectorIndexes[name]
	return idx, exists
}

// GetFulltextIndex returns a fulltext index by name.
func (sm *SchemaManager) GetFulltextIndex(name string) (*FulltextIndex, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	idx, exists := sm.fulltextIndexes[name]
	return idx, exists
}
