package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAsyncEngine_RebaseRetry_PreservesExplicitTxUpdate reproduces a stale async update
// clobbering a newer explicit tx update on the same node.
//
// Sequence:
// 1) async UpdateNode queues stale snapshot (name + age)
// 2) explicit transaction commits age change to 31 before async flush
// 3) async flush must rebase stale async update (name change) onto latest committed node
func TestAsyncEngine_RebaseRetry_PreservesExplicitTxUpdate(t *testing.T) {
	base := NewMemoryEngine()
	t.Cleanup(func() { _ = base.Close() })

	ae := NewAsyncEngine(base, &AsyncEngineConfig{
		FlushInterval: time.Hour, // manual flush only
	})
	t.Cleanup(func() { _ = ae.Close() })

	id := NodeID(prefixTestID("clobber-node"))
	initial := &Node{
		ID:     id,
		Labels: []string{"Person"},
		Properties: map[string]any{
			"name": "Alice",
			"age":  int64(30),
		},
	}
	_, err := base.CreateNode(CopyNode(initial))
	require.NoError(t, err)

	// Queue async stale update (captured before explicit tx commit).
	asyncUpdate := CopyNode(initial)
	asyncUpdate.Properties["name"] = "Alice Async"
	require.NoError(t, ae.UpdateNode(asyncUpdate))

	// Explicit tx commits newer age value while async update is still queued.
	tx, err := base.BeginTransaction()
	require.NoError(t, err)
	txNode, err := tx.GetNode(id)
	require.NoError(t, err)
	txNode.Properties["age"] = int64(31)
	require.NoError(t, tx.UpdateNode(txNode))
	require.NoError(t, tx.Commit())

	// Now flush queued async write.
	require.NoError(t, ae.Flush())

	finalNode, err := base.GetNode(id)
	require.NoError(t, err)

	// Expected after rebase:
	// - async intent ("name" changed) is preserved
	// - explicit tx update ("age" changed later) is preserved
	require.Equal(t, "Alice Async", finalNode.Properties["name"])
	require.Equal(t, int64(31), finalNode.Properties["age"])
}

// TestAsyncEngine_RebaseRetry_AppliesAsyncTouchedKeysOnly ensures rebasing only
// overwrites keys touched by the async intent and preserves unrelated tx updates.
func TestAsyncEngine_RebaseRetry_AppliesAsyncTouchedKeysOnly(t *testing.T) {
	base := NewMemoryEngine()
	t.Cleanup(func() { _ = base.Close() })

	ae := NewAsyncEngine(base, &AsyncEngineConfig{
		FlushInterval: time.Hour,
	})
	t.Cleanup(func() { _ = ae.Close() })

	id := NodeID(prefixTestID("clobber-node-keys"))
	initial := &Node{
		ID:     id,
		Labels: []string{"Person"},
		Properties: map[string]any{
			"name":   "Alice",
			"age":    int64(30),
			"status": "active",
		},
	}
	_, err := base.CreateNode(CopyNode(initial))
	require.NoError(t, err)

	// Async intent touches "name" and removes "status".
	asyncUpdate := CopyNode(initial)
	asyncUpdate.Properties["name"] = "Alice Async"
	delete(asyncUpdate.Properties, "status")
	require.NoError(t, ae.UpdateNode(asyncUpdate))

	// Explicit tx updates unrelated key while async update is queued.
	tx, err := base.BeginTransaction()
	require.NoError(t, err)
	txNode, err := tx.GetNode(id)
	require.NoError(t, err)
	txNode.Properties["age"] = int64(31)
	require.NoError(t, tx.UpdateNode(txNode))
	require.NoError(t, tx.Commit())

	require.NoError(t, ae.Flush())

	finalNode, err := base.GetNode(id)
	require.NoError(t, err)
	require.Equal(t, "Alice Async", finalNode.Properties["name"])
	require.Equal(t, int64(31), finalNode.Properties["age"])
	_, hasStatus := finalNode.Properties["status"]
	require.False(t, hasStatus, "async removal of status should be preserved after rebase")
}
