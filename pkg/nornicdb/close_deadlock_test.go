package nornicdb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDBClose_DoesNotDeadlock(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmbeddingDimensions = 3
	db, err := Open("", cfg)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		_ = db.Close()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("db.Close() deadlocked or hung")
	}
}

