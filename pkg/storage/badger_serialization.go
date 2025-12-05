// Package storage - Serialization helpers for BadgerDB.
package storage

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"time"
)

// init registers types with gob for proper encoding/decoding of property values.
// gob requires type registration for interface{} values in maps.
func init() {
	// Register primitive types that can appear in Properties map
	gob.Register(int(0))
	gob.Register(int64(0))
	gob.Register(float64(0))
	gob.Register("")
	gob.Register(true)
	gob.Register(time.Time{})

	// Register slice types for list properties
	gob.Register([]interface{}{})
	gob.Register([]string{})
	gob.Register([]int{})
	gob.Register([]int64{})
	gob.Register([]float64{})

	// Register map types for nested properties
	gob.Register(map[string]interface{}{})
}

// serializeNode converts a Node to gob bytes for BadgerDB storage.
// gob preserves Go types (int64 vs float64) unlike JSON.
func serializeNode(node *Node) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(node); err != nil {
		return nil, fmt.Errorf("encoding node: %w", err)
	}
	return buf.Bytes(), nil
}

// deserializeNode converts gob bytes back to a Node.
func deserializeNode(data []byte) (*Node, error) {
	var node Node
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&node); err != nil {
		return nil, fmt.Errorf("decoding node: %w", err)
	}
	return &node, nil
}

// serializeEdge converts an Edge to gob bytes for BadgerDB storage.
func serializeEdge(edge *Edge) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(edge); err != nil {
		return nil, fmt.Errorf("encoding edge: %w", err)
	}
	return buf.Bytes(), nil
}

// deserializeEdge converts gob bytes back to an Edge.
func deserializeEdge(data []byte) (*Edge, error) {
	var edge Edge
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&edge); err != nil {
		return nil, fmt.Errorf("decoding edge: %w", err)
	}
	return &edge, nil
}
