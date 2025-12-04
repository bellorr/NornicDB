// Package storage - Unit tests for atomic WAL write format.
//
// These tests verify:
// 1. Atomic format writing and reading works correctly
// 2. Partial writes are detected and handled gracefully
// 3. Backward compatibility with legacy JSON format
// 4. CRC verification catches corruption
package storage

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// =============================================================================
// ATOMIC FORMAT TESTS
// =============================================================================

// TestAtomicWALWriteFormat verifies the atomic write format structure.
func TestAtomicWALWriteFormat(t *testing.T) {
	dir := t.TempDir()

	cfg := &WALConfig{
		Dir:      dir,
		SyncMode: "immediate",
	}

	wal, err := NewWAL(dir, cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Write an entry
	node := &Node{ID: "test-node", Labels: []string{"Test"}}
	if err := wal.Append(OpCreateNode, WALNodeData{Node: node}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	wal.Close()

	// Read raw file to verify format
	walPath := filepath.Join(dir, "wal.log")
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("Failed to read WAL file: %v", err)
	}

	// Verify magic bytes
	if len(data) < 4 {
		t.Fatal("WAL file too short")
	}
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != walMagic {
		t.Errorf("Expected magic 0x%x, got 0x%x", walMagic, magic)
	}

	// Verify version
	if data[4] != walFormatVersion {
		t.Errorf("Expected version %d, got %d", walFormatVersion, data[4])
	}

	// Verify we can read back the entry
	entries, err := ReadWALEntries(walPath)
	if err != nil {
		t.Fatalf("Failed to read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}
	if entries[0].Operation != OpCreateNode {
		t.Errorf("Expected OpCreateNode, got %s", entries[0].Operation)
	}
}

// TestAtomicWALMultipleEntries verifies multiple entries are written correctly.
func TestAtomicWALMultipleEntries(t *testing.T) {
	dir := t.TempDir()

	cfg := &WALConfig{
		Dir:      dir,
		SyncMode: "immediate",
	}

	wal, err := NewWAL(dir, cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Write multiple entries
	for i := 0; i < 100; i++ {
		node := &Node{ID: NodeID("node-" + string(rune('a'+i%26))), Labels: []string{"Test"}}
		if err := wal.Append(OpCreateNode, WALNodeData{Node: node}); err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}
	wal.Close()

	// Read back and verify
	entries, err := ReadWALEntries(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatalf("Failed to read entries: %v", err)
	}
	if len(entries) != 100 {
		t.Fatalf("Expected 100 entries, got %d", len(entries))
	}

	// Verify sequences are correct
	for i, entry := range entries {
		if entry.Sequence != uint64(i+1) {
			t.Errorf("Entry %d: expected seq %d, got %d", i, i+1, entry.Sequence)
		}
	}
}

// =============================================================================
// PARTIAL WRITE DETECTION TESTS
// =============================================================================

// TestAtomicWALDetectsPartialMagic verifies partial magic write is detected.
func TestAtomicWALDetectsPartialMagic(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Write a valid entry first
	cfg := &WALConfig{Dir: dir, SyncMode: "immediate"}
	wal, _ := NewWAL(dir, cfg)
	wal.Append(OpCreateNode, WALNodeData{Node: &Node{ID: "n1", Labels: []string{"Test"}}})
	wal.Close()

	// Append partial magic bytes (simulate crash)
	f, _ := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.Write([]byte{0x57, 0x41}) // Partial "WALE"
	f.Close()

	// Should recover the first entry and detect partial write
	entries, err := ReadWALEntries(walPath)
	if err != nil {
		t.Fatalf("Should recover valid entries, got error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Expected 1 valid entry, got %d", len(entries))
	}
}

// TestAtomicWALDetectsPartialHeader verifies partial header is detected.
func TestAtomicWALDetectsPartialHeader(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Write valid entry
	cfg := &WALConfig{Dir: dir, SyncMode: "immediate"}
	wal, _ := NewWAL(dir, cfg)
	wal.Append(OpCreateNode, WALNodeData{Node: &Node{ID: "n1", Labels: []string{"Test"}}})
	wal.Close()

	// Append magic + partial header (missing length)
	f, _ := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	header := make([]byte, 6) // magic(4) + version(1) + partial length(1)
	binary.LittleEndian.PutUint32(header[0:4], walMagic)
	header[4] = walFormatVersion
	header[5] = 0x10 // Partial length byte
	f.Write(header)
	f.Close()

	// Should recover first entry
	entries, err := ReadWALEntries(walPath)
	if err != nil {
		t.Fatalf("Should recover valid entries, got error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Expected 1 valid entry, got %d", len(entries))
	}
}

// TestAtomicWALDetectsPartialPayload verifies partial payload is detected.
func TestAtomicWALDetectsPartialPayload(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Write valid entry
	cfg := &WALConfig{Dir: dir, SyncMode: "immediate"}
	wal, _ := NewWAL(dir, cfg)
	wal.Append(OpCreateNode, WALNodeData{Node: &Node{ID: "n1", Labels: []string{"Test"}}})
	wal.Close()

	// Append valid header with truncated payload
	f, _ := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	header := make([]byte, 9)
	binary.LittleEndian.PutUint32(header[0:4], walMagic)
	header[4] = walFormatVersion
	binary.LittleEndian.PutUint32(header[5:9], 100) // Says 100 bytes
	f.Write(header)
	f.Write([]byte("short")) // But only write 5 bytes
	f.Close()

	// Should recover first entry
	entries, err := ReadWALEntries(walPath)
	if err != nil {
		t.Fatalf("Should recover valid entries, got error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Expected 1 valid entry, got %d", len(entries))
	}
}

// TestAtomicWALDetectsMissingCRC verifies missing CRC is detected.
func TestAtomicWALDetectsMissingCRC(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Write valid entry
	cfg := &WALConfig{Dir: dir, SyncMode: "immediate"}
	wal, _ := NewWAL(dir, cfg)
	wal.Append(OpCreateNode, WALNodeData{Node: &Node{ID: "n1", Labels: []string{"Test"}}})
	wal.Close()

	// Create a complete entry but without CRC
	entry := WALEntry{
		Sequence:  2,
		Timestamp: time.Now(),
		Operation: OpCreateNode,
		Data:      []byte(`{"node":{"id":"n2"}}`),
		Checksum:  crc32Checksum([]byte(`{"node":{"id":"n2"}}`)),
	}
	payload, _ := json.Marshal(&entry)

	f, _ := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	header := make([]byte, 9)
	binary.LittleEndian.PutUint32(header[0:4], walMagic)
	header[4] = walFormatVersion
	binary.LittleEndian.PutUint32(header[5:9], uint32(len(payload)))
	f.Write(header)
	f.Write(payload)
	// Don't write CRC - simulate crash
	f.Close()

	// Should recover first entry
	entries, err := ReadWALEntries(walPath)
	if err != nil {
		t.Fatalf("Should recover valid entries, got error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Expected 1 valid entry, got %d", len(entries))
	}
}

// =============================================================================
// CRC VERIFICATION TESTS
// =============================================================================

// TestAtomicWALDetectsCRCMismatch verifies CRC corruption is detected.
func TestAtomicWALDetectsCRCMismatch(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Manually create an entry with wrong CRC
	entry := WALEntry{
		Sequence:  1,
		Timestamp: time.Now(),
		Operation: OpCreateNode,
		Data:      []byte(`{"node":{"id":"n1"}}`),
		Checksum:  crc32Checksum([]byte(`{"node":{"id":"n1"}}`)),
	}
	payload, _ := json.Marshal(&entry)

	f, _ := os.Create(walPath)
	header := make([]byte, 9)
	binary.LittleEndian.PutUint32(header[0:4], walMagic)
	header[4] = walFormatVersion
	binary.LittleEndian.PutUint32(header[5:9], uint32(len(payload)))
	f.Write(header)
	f.Write(payload)
	// Write wrong CRC
	wrongCRC := make([]byte, 4)
	binary.LittleEndian.PutUint32(wrongCRC, 0xDEADBEEF)
	f.Write(wrongCRC)
	f.Close()

	// Should fail with checksum error
	_, err := ReadWALEntries(walPath)
	if err == nil {
		t.Fatal("Should fail on CRC mismatch")
	}
	if !containsAny(err.Error(), "CRC", "checksum") {
		t.Errorf("Error should mention CRC/checksum: %v", err)
	}
}

// =============================================================================
// BACKWARD COMPATIBILITY TESTS
// =============================================================================

// TestLegacyJSONFormatReadable verifies old JSON format can still be read.
func TestLegacyJSONFormatReadable(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Create legacy JSON format WAL (starts with '{')
	entries := []WALEntry{
		{
			Sequence:  1,
			Timestamp: time.Now(),
			Operation: OpCreateNode,
			Data:      []byte(`{"node":{"id":"n1","labels":["Test"]}}`),
			Checksum:  crc32Checksum([]byte(`{"node":{"id":"n1","labels":["Test"]}}`)),
		},
		{
			Sequence:  2,
			Timestamp: time.Now(),
			Operation: OpCreateNode,
			Data:      []byte(`{"node":{"id":"n2","labels":["Test"]}}`),
			Checksum:  crc32Checksum([]byte(`{"node":{"id":"n2","labels":["Test"]}}`)),
		},
	}

	f, _ := os.Create(walPath)
	encoder := json.NewEncoder(f)
	for _, entry := range entries {
		encoder.Encode(&entry)
	}
	f.Close()

	// Should read legacy format
	result, err := ReadWALEntries(walPath)
	if err != nil {
		t.Fatalf("Failed to read legacy format: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(result))
	}
}

// TestFormatAutoDetection verifies format is correctly auto-detected.
func TestFormatAutoDetection(t *testing.T) {
	t.Run("atomic format", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &WALConfig{Dir: dir, SyncMode: "immediate"}
		wal, _ := NewWAL(dir, cfg)
		wal.Append(OpCreateNode, WALNodeData{Node: &Node{ID: "n1", Labels: []string{"Test"}}})
		wal.Close()

		// Verify file starts with magic
		data, _ := os.ReadFile(filepath.Join(dir, "wal.log"))
		magic := binary.LittleEndian.Uint32(data[0:4])
		if magic != walMagic {
			t.Errorf("New files should use atomic format")
		}
	})

	t.Run("legacy format", func(t *testing.T) {
		dir := t.TempDir()
		walPath := filepath.Join(dir, "wal.log")

		// Create proper legacy JSON entry with correct checksum
		testData := []byte(`{"node":{"id":"n1"}}`)
		checksum := crc32Checksum(testData)

		entry := WALEntry{
			Sequence:  1,
			Timestamp: time.Now(),
			Operation: OpCreateNode,
			Data:      testData,
			Checksum:  checksum,
		}

		f, _ := os.Create(walPath)
		encoder := json.NewEncoder(f)
		encoder.Encode(&entry)
		f.Close()

		// Verify file starts with '{' (legacy format)
		data, _ := os.ReadFile(walPath)
		if data[0] != '{' {
			t.Error("Legacy files should start with '{'")
		}

		// Should still be readable
		entries, err := ReadWALEntries(walPath)
		if err != nil {
			t.Fatalf("Legacy format should be readable: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("Expected 1 entry, got %d", len(entries))
		}
	})
}

// =============================================================================
// INTEGRATION TESTS
// =============================================================================

// TestAtomicWALFullRecovery tests complete write-crash-recover cycle.
func TestAtomicWALFullRecovery(t *testing.T) {
	dir := t.TempDir()

	// Phase 1: Write data
	cfg := &WALConfig{Dir: dir, SyncMode: "immediate"}
	wal1, _ := NewWAL(dir, cfg)

	nodes := []*Node{
		{ID: "n1", Labels: []string{"Person"}, Properties: map[string]interface{}{"name": "Alice"}},
		{ID: "n2", Labels: []string{"Person"}, Properties: map[string]interface{}{"name": "Bob"}},
		{ID: "n3", Labels: []string{"Person"}, Properties: map[string]interface{}{"name": "Charlie"}},
	}

	for _, node := range nodes {
		wal1.Append(OpCreateNode, WALNodeData{Node: node})
	}
	wal1.Close()

	// Phase 2: Recover from WAL
	engine, result, err := RecoverFromWALWithResult(dir, "")
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}

	if result.Applied != 3 {
		t.Errorf("Expected 3 applied, got %d", result.Applied)
	}

	// Verify all nodes recovered
	for _, node := range nodes {
		recovered, err := engine.GetNode(node.ID)
		if err != nil {
			t.Errorf("Failed to get node %s: %v", node.ID, err)
			continue
		}
		if recovered == nil {
			t.Errorf("Node %s not found", node.ID)
		}
	}
}

// TestAtomicWALRecoveryWithPartialWrite tests recovery after simulated crash.
func TestAtomicWALRecoveryWithPartialWrite(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	// Write valid entries
	cfg := &WALConfig{Dir: dir, SyncMode: "immediate"}
	wal, _ := NewWAL(dir, cfg)
	wal.Append(OpCreateNode, WALNodeData{Node: &Node{ID: "n1", Labels: []string{"Test"}}})
	wal.Append(OpCreateNode, WALNodeData{Node: &Node{ID: "n2", Labels: []string{"Test"}}})
	wal.Close()

	// Simulate crash: append partial entry
	f, _ := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	header := make([]byte, 9)
	binary.LittleEndian.PutUint32(header[0:4], walMagic)
	header[4] = walFormatVersion
	binary.LittleEndian.PutUint32(header[5:9], 500) // Says 500 bytes coming
	f.Write(header)
	f.Write([]byte("incomplete...")) // But crash before completion
	f.Close()

	// Recovery should work, getting the 2 valid entries
	engine, result, err := RecoverFromWALWithResult(dir, "")
	if err != nil {
		t.Fatalf("Recovery should succeed: %v", err)
	}

	if result.Applied != 2 {
		t.Errorf("Expected 2 applied (ignoring partial), got %d", result.Applied)
	}

	// Verify data
	n1, _ := engine.GetNode("n1")
	n2, _ := engine.GetNode("n2")
	if n1 == nil || n2 == nil {
		t.Error("Valid nodes should be recovered")
	}
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if containsSubstr(s, sub) {
			return true
		}
	}
	return false
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
