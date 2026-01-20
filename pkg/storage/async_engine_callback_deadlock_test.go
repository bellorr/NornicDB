package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAsyncEngine_DeleteNode_CallbackCanReenterStorage(t *testing.T) {
	underlying := NewMemoryEngine()
	ae := NewAsyncEngine(underlying, &AsyncEngineConfig{
		FlushInterval:    time.Hour,
		MaxNodeCacheSize: 0,
		MaxEdgeCacheSize: 0,
	})
	t.Cleanup(func() { _ = ae.Close() })

	cbCalled := make(chan struct{}, 1)
	ae.OnNodeDeleted(func(id NodeID) {
		_, _ = ae.GetNode(id)
		select {
		case cbCalled <- struct{}{}:
		default:
		}
	})

	id, err := ae.CreateNode(&Node{ID: NodeID(prefixTestID("n1")), Labels: []string{"L"}})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- ae.DeleteNode(id) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("DeleteNode deadlocked")
	}

	select {
	case <-cbCalled:
	default:
		t.Fatal("expected OnNodeDeleted callback to be invoked")
	}
}

func TestAsyncEngine_BulkDeleteNodes_CallbackCanReenterStorage(t *testing.T) {
	underlying := NewMemoryEngine()
	ae := NewAsyncEngine(underlying, &AsyncEngineConfig{
		FlushInterval:    time.Hour,
		MaxNodeCacheSize: 0,
		MaxEdgeCacheSize: 0,
	})
	t.Cleanup(func() { _ = ae.Close() })

	cbCalled := make(chan struct{}, 1)
	ae.OnNodeDeleted(func(id NodeID) {
		_, _ = ae.GetNode(id)
		select {
		case cbCalled <- struct{}{}:
		default:
		}
	})

	id, err := ae.CreateNode(&Node{ID: NodeID(prefixTestID("n1")), Labels: []string{"L"}})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- ae.BulkDeleteNodes([]NodeID{id}) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("BulkDeleteNodes deadlocked")
	}

	select {
	case <-cbCalled:
	default:
		t.Fatal("expected OnNodeDeleted callback to be invoked")
	}
}
