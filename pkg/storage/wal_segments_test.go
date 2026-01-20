package storage

import (
	"fmt"
	"testing"

	"github.com/orneryd/nornicdb/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestWALEntriesFromDirWithSegments(t *testing.T) {
	cleanup := config.WithWALEnabled()
	defer cleanup()

	dir := t.TempDir()
	cfg := &WALConfig{
		Dir:        dir,
		SyncMode:   "none",
		MaxEntries: 2,
		MaxFileSize: 0,
	}

	wal, err := NewWAL(dir, cfg)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		node := &Node{ID: NodeID(fmt.Sprintf("test:%d", i))}
		_, err := wal.AppendWithDatabaseReturningSeq(OpCreateNode, WALNodeData{
			Node: node,
			TxID: "tx-seg",
		}, "test")
		require.NoError(t, err)
	}

	require.NoError(t, wal.Close())

	entries, err := ReadWALEntriesFromDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 5)

	for i := 1; i < len(entries); i++ {
		if entries[i].Sequence <= entries[i-1].Sequence {
			t.Fatalf("expected ascending sequences, got %d then %d", entries[i-1].Sequence, entries[i].Sequence)
		}
	}

	matches, err := FindWALEntriesByTxID(dir, "tx-seg", 0)
	require.NoError(t, err)
	require.Len(t, matches, 5)
}
