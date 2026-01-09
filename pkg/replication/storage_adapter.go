// Package replication provides distributed replication for NornicDB.
package replication

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orneryd/nornicdb/pkg/cypher"
	"github.com/orneryd/nornicdb/pkg/storage"
)

// StorageAdapter bridges the replication.Storage interface to storage.Engine.
// It translates replication commands into storage operations and maintains WAL state.
type StorageAdapter struct {
	engine   storage.Engine
	executor *cypher.StorageExecutor // Cypher executor for executing replicated Cypher queries

	// Persistent WAL for replication commands
	wal         *storage.WAL
	walDir      string
	walMu       sync.RWMutex // Protects wal and walPosition
	walPosition atomic.Uint64

	// In-memory WAL for fast streaming (avoids re-reading wal.log continuously).
	memWALMu sync.RWMutex
	memWAL   []*WALEntry // sorted by Position asc
}

type replicationWALRecord struct {
	// Timestamp when the entry was created.
	Timestamp int64 `json:"ts"`

	// Command is the replicated command.
	Command *Command `json:"cmd"`
}

// NewStorageAdapter creates a new storage adapter wrapping the given engine.
// The WAL directory defaults to "data/replication/wal" if not specified.
func NewStorageAdapter(engine storage.Engine) (*StorageAdapter, error) {
	return NewStorageAdapterWithWAL(engine, "")
}

// NewStorageAdapterWithWAL creates a new storage adapter with a custom WAL directory.
// If walDir is empty, defaults to "data/replication/wal".
func NewStorageAdapterWithWAL(engine storage.Engine, walDir string) (*StorageAdapter, error) {
	if walDir == "" {
		walDir = "data/replication/wal"
	}

	// Create WAL directory if needed
	if err := os.MkdirAll(walDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	// Create persistent WAL
	walConfig := storage.DefaultWALConfig()
	walConfig.Dir = walDir
	walConfig.SyncMode = "batch" // Batch sync for performance
	walConfig.BatchSyncInterval = 100 * time.Millisecond

	wal, err := storage.NewWAL(walDir, walConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create WAL: %w", err)
	}

	adapter := &StorageAdapter{
		engine:   engine,
		executor: cypher.NewStorageExecutor(engine),
		wal:      wal,
		walDir:   walDir,
	}

	// Load existing WAL position
	_ = adapter.loadWALPosition()

	return adapter, nil
}

// loadWALPosition loads the last WAL position from persistent storage.
func (a *StorageAdapter) loadWALPosition() error {
	// storage.WAL already recovers its own sequence on startup.
	// Use that as the authoritative replication WAL position.
	if a.wal != nil {
		a.walPosition.Store(a.wal.Sequence())
	}
	return nil
}

// SetExecutor sets a custom Cypher executor for the adapter.
// This allows using an executor with additional configuration (e.g., database manager, embedder).
func (a *StorageAdapter) SetExecutor(executor *cypher.StorageExecutor) {
	a.executor = executor
}

// ApplyCommand applies a replicated command to storage.
func (a *StorageAdapter) ApplyCommand(cmd *Command) error {
	if cmd == nil {
		return fmt.Errorf("nil command")
	}

	// Record in persistent WAL first (write-ahead logging)
	record := replicationWALRecord{
		Timestamp: cmd.Timestamp.UnixNano(),
		Command:   cmd,
	}

	// Append to persistent WAL.
	// Note: Do not read wal.log to stream; use the in-memory WAL below. Reading
	// wal.log races with buffered writes and is also O(N) per batch.
	if err := a.wal.Append(storage.OperationType("replication_command"), record); err != nil {
		return fmt.Errorf("failed to append to WAL: %w", err)
	}

	// Use the WAL's recovered/monotonic sequence as the replication position.
	pos := a.wal.Sequence()
	a.walPosition.Store(pos)

	// Append to in-memory WAL for fast streaming.
	a.memWALMu.Lock()
	a.memWAL = append(a.memWAL, &WALEntry{
		Position:  pos,
		Timestamp: record.Timestamp,
		Command:   cmd,
	})
	a.memWALMu.Unlock()

	// Execute the command
	switch cmd.Type {
	case CmdCreateNode:
		return a.applyCreateNode(cmd.Data)
	case CmdUpdateNode:
		return a.applyUpdateNode(cmd.Data)
	case CmdDeleteNode:
		return a.applyDeleteNode(cmd.Data)
	case CmdCreateEdge:
		return a.applyCreateEdge(cmd.Data)
	case CmdUpdateEdge:
		return a.applyUpdateEdge(cmd.Data)
	case CmdDeleteEdge:
		return a.applyDeleteEdge(cmd.Data)
	case CmdSetProperty:
		return a.applySetProperty(cmd.Data)
	case CmdBatchWrite:
		return a.applyBatchWrite(cmd.Data)
	case CmdCypher:
		return a.applyCypher(cmd.Data)
	case CmdDeleteByPrefix:
		return a.applyDeleteByPrefix(cmd.Data)
	case CmdBulkCreateNodes:
		return a.applyBulkCreateNodes(cmd.Data)
	case CmdBulkCreateEdges:
		return a.applyBulkCreateEdges(cmd.Data)
	case CmdBulkDeleteNodes:
		return a.applyBulkDeleteNodes(cmd.Data)
	case CmdBulkDeleteEdges:
		return a.applyBulkDeleteEdges(cmd.Data)
	default:
		return fmt.Errorf("unknown command type: %d", cmd.Type)
	}
}

// applyCreateNode creates a node from command data.
func (a *StorageAdapter) applyCreateNode(data []byte) error {
	var node storage.Node
	if err := json.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("unmarshal node: %w", err)
	}
	_, err := a.engine.CreateNode(&node)
	return err
}

// applyUpdateNode updates a node from command data.
func (a *StorageAdapter) applyUpdateNode(data []byte) error {
	var node storage.Node
	if err := json.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("unmarshal node: %w", err)
	}
	return a.engine.UpdateNode(&node)
}

// applyDeleteNode deletes a node.
func (a *StorageAdapter) applyDeleteNode(data []byte) error {
	nodeID := string(data)
	return a.engine.DeleteNode(storage.NodeID(nodeID))
}

// applyCreateEdge creates an edge from command data.
func (a *StorageAdapter) applyCreateEdge(data []byte) error {
	var edge storage.Edge
	if err := json.Unmarshal(data, &edge); err != nil {
		return fmt.Errorf("unmarshal edge: %w", err)
	}
	return a.engine.CreateEdge(&edge)
}

func (a *StorageAdapter) applyUpdateEdge(data []byte) error {
	var edge storage.Edge
	if err := json.Unmarshal(data, &edge); err != nil {
		return fmt.Errorf("unmarshal edge: %w", err)
	}
	return a.engine.UpdateEdge(&edge)
}

// applyDeleteEdge deletes an edge.
func (a *StorageAdapter) applyDeleteEdge(data []byte) error {
	var req struct {
		EdgeID string `json:"edge_id"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal delete edge request: %w", err)
	}
	return a.engine.DeleteEdge(storage.EdgeID(req.EdgeID))
}

// applySetProperty sets a property on a node.
func (a *StorageAdapter) applySetProperty(data []byte) error {
	var req struct {
		NodeID string      `json:"node_id"`
		Key    string      `json:"key"`
		Value  interface{} `json:"value"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal set property request: %w", err)
	}

	// Get node, update property, save
	node, err := a.engine.GetNode(storage.NodeID(req.NodeID))
	if err != nil {
		return err
	}
	if node.Properties == nil {
		node.Properties = make(map[string]interface{})
	}
	node.Properties[req.Key] = req.Value
	return a.engine.UpdateNode(node)
}

// applyBatchWrite applies a batch of operations.
func (a *StorageAdapter) applyBatchWrite(data []byte) error {
	var batch struct {
		Nodes []*storage.Node `json:"nodes"`
		Edges []*storage.Edge `json:"edges"`
	}
	if err := json.Unmarshal(data, &batch); err != nil {
		return fmt.Errorf("unmarshal batch: %w", err)
	}

	for _, node := range batch.Nodes {
		if _, err := a.engine.CreateNode(node); err != nil {
			return err
		}
	}
	for _, edge := range batch.Edges {
		if err := a.engine.CreateEdge(edge); err != nil {
			return err
		}
	}
	return nil
}

func (a *StorageAdapter) applyDeleteByPrefix(data []byte) error {
	var req struct {
		Prefix string `json:"prefix"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal delete by prefix request: %w", err)
	}
	if req.Prefix == "" {
		return fmt.Errorf("prefix is required")
	}
	_, _, err := a.engine.DeleteByPrefix(req.Prefix)
	return err
}

func (a *StorageAdapter) applyBulkCreateNodes(data []byte) error {
	var req struct {
		Nodes []*storage.Node `json:"nodes"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal bulk create nodes: %w", err)
	}
	return a.engine.BulkCreateNodes(req.Nodes)
}

func (a *StorageAdapter) applyBulkCreateEdges(data []byte) error {
	var req struct {
		Edges []*storage.Edge `json:"edges"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal bulk create edges: %w", err)
	}
	return a.engine.BulkCreateEdges(req.Edges)
}

func (a *StorageAdapter) applyBulkDeleteNodes(data []byte) error {
	var req struct {
		IDs []storage.NodeID `json:"ids"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal bulk delete nodes: %w", err)
	}
	return a.engine.BulkDeleteNodes(req.IDs)
}

func (a *StorageAdapter) applyBulkDeleteEdges(data []byte) error {
	var req struct {
		IDs []storage.EdgeID `json:"ids"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return fmt.Errorf("unmarshal bulk delete edges: %w", err)
	}
	return a.engine.BulkDeleteEdges(req.IDs)
}

// applyCypher executes a Cypher command (for write queries).
// The data should be a JSON object with "query" (string) and optional "params" (map[string]interface{}).
func (a *StorageAdapter) applyCypher(data []byte) error {
	if a.executor == nil {
		return fmt.Errorf("cypher executor not available - cannot execute Cypher command")
	}

	// Parse Cypher command data
	var cypherCmd struct {
		Query  string                 `json:"query"`
		Params map[string]interface{} `json:"params,omitempty"`
	}

	if err := json.Unmarshal(data, &cypherCmd); err != nil {
		return fmt.Errorf("unmarshal cypher command: %w", err)
	}

	if cypherCmd.Query == "" {
		return fmt.Errorf("cypher query is empty")
	}

	// Execute the Cypher query
	// Use background context since this is a replicated command (no user context)
	ctx := context.Background()
	_, err := a.executor.Execute(ctx, cypherCmd.Query, cypherCmd.Params)
	if err != nil {
		return fmt.Errorf("execute cypher query: %w", err)
	}

	return nil
}

// Close releases replication resources (WAL file handles/background goroutines).
func (a *StorageAdapter) Close() error {
	a.walMu.Lock()
	defer a.walMu.Unlock()
	if a.wal != nil {
		err := a.wal.Close()
		a.wal = nil
		return err
	}
	return nil
}

// GetWALPosition returns the current WAL position.
func (a *StorageAdapter) GetWALPosition() (uint64, error) {
	return a.walPosition.Load(), nil
}

// GetWALEntries returns WAL entries starting from the given position.
func (a *StorageAdapter) GetWALEntries(fromPosition uint64, maxEntries int) ([]*WALEntry, error) {
	// Fast path: serve from in-memory WAL.
	a.memWALMu.RLock()
	mem := a.memWAL
	a.memWALMu.RUnlock()

	if len(mem) > 0 && fromPosition >= mem[0].Position {
		// Binary search first entry with Position > fromPosition
		lo, hi := 0, len(mem)
		for lo < hi {
			mid := (lo + hi) / 2
			if mem[mid].Position <= fromPosition {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo >= len(mem) {
			return []*WALEntry{}, nil
		}
		end := lo + maxEntries
		if end > len(mem) {
			end = len(mem)
		}
		entries := make([]*WALEntry, end-lo)
		copy(entries, mem[lo:end])
		return entries, nil
	}

	// Read from persistent WAL
	a.walMu.RLock()
	defer a.walMu.RUnlock()

	walPath := filepath.Join(a.walDir, "wal.log")
	storageEntries, err := storage.ReadWALEntries(walPath)
	if err != nil {
		// Handle missing WAL file gracefully (may not exist yet)
		if os.IsNotExist(err) {
			return []*WALEntry{}, nil
		}
		// Check if error message indicates file not found (storage.ReadWALEntries may wrap the error)
		errStr := err.Error()
		if strings.Contains(errStr, "no such file") || strings.Contains(errStr, "not found") {
			return []*WALEntry{}, nil
		}
		return nil, fmt.Errorf("failed to read WAL entries: %w", err)
	}

	var entries []*WALEntry
	for _, storageEntry := range storageEntries {
		// Only process replication_command entries
		if storageEntry.Operation != storage.OperationType("replication_command") {
			continue
		}

		// Prefer the current record format, but tolerate older WALs.
		var (
			ts  int64
			cmd *Command
		)
		var rec replicationWALRecord
		if err := json.Unmarshal(storageEntry.Data, &rec); err == nil && rec.Command != nil {
			ts = rec.Timestamp
			cmd = rec.Command
		} else {
			var old WALEntry
			if err := json.Unmarshal(storageEntry.Data, &old); err != nil || old.Command == nil {
				continue // Skip corrupted entries
			}
			ts = old.Timestamp
			cmd = old.Command
		}

		pos := storageEntry.Sequence
		if pos > fromPosition {
			entries = append(entries, &WALEntry{
				Position:  pos,
				Timestamp: ts,
				Command:   cmd,
			})
			if len(entries) >= maxEntries {
				break
			}
		}
	}

	return entries, nil
}

// PruneWALEntries drops in-memory WAL entries up to (and including) uptoPosition.
// This keeps memory bounded while streaming and does not affect the persistent WAL.
func (a *StorageAdapter) PruneWALEntries(uptoPosition uint64) {
	a.memWALMu.Lock()
	defer a.memWALMu.Unlock()
	if len(a.memWAL) == 0 {
		return
	}
	// Find first entry with Position > uptoPosition.
	lo, hi := 0, len(a.memWAL)
	for lo < hi {
		mid := (lo + hi) / 2
		if a.memWAL[mid].Position <= uptoPosition {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return
	}
	// Drop prefix in-place to avoid allocating a new backing array.
	copy(a.memWAL, a.memWAL[lo:])
	a.memWAL = a.memWAL[:len(a.memWAL)-lo]
}

// WriteSnapshot writes a full snapshot to the given writer.
func (a *StorageAdapter) WriteSnapshot(w SnapshotWriter) error {
	// Get all nodes and edges
	nodes, err := a.engine.AllNodes()
	if err != nil {
		return fmt.Errorf("get all nodes: %w", err)
	}

	edges, err := a.engine.AllEdges()
	if err != nil {
		return fmt.Errorf("get all edges: %w", err)
	}

	snapshot := struct {
		WALPosition uint64          `json:"wal_position"`
		Nodes       []*storage.Node `json:"nodes"`
		Edges       []*storage.Edge `json:"edges"`
	}{
		WALPosition: a.walPosition.Load(),
		Nodes:       nodes,
		Edges:       edges,
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	_, err = w.Write(data)
	return err
}

// RestoreSnapshot restores state from a snapshot.
func (a *StorageAdapter) RestoreSnapshot(r SnapshotReader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}

	var snapshot struct {
		WALPosition uint64          `json:"wal_position"`
		Nodes       []*storage.Node `json:"nodes"`
		Edges       []*storage.Edge `json:"edges"`
	}

	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("unmarshal snapshot: %w", err)
	}

	// Restore nodes
	for _, node := range snapshot.Nodes {
		if _, err := a.engine.CreateNode(node); err != nil {
			return fmt.Errorf("restore node: %w", err)
		}
	}

	// Restore edges
	for _, edge := range snapshot.Edges {
		if err := a.engine.CreateEdge(edge); err != nil {
			return fmt.Errorf("restore edge: %w", err)
		}
	}

	// Restore WAL position
	a.walPosition.Store(snapshot.WALPosition)

	return nil
}

// Engine returns the underlying storage engine.
func (a *StorageAdapter) Engine() storage.Engine {
	return a.engine
}

// Verify StorageAdapter implements Storage interface.
var _ Storage = (*StorageAdapter)(nil)
