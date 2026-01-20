package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReceiptHash_Deterministic(t *testing.T) {
	ts := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)

	r1, err := NewReceipt("tx-123", 10, 15, "nornic", ts)
	require.NoError(t, err)

	r2, err := NewReceipt("tx-123", 10, 15, "nornic", ts)
	require.NoError(t, err)

	assert.Equal(t, r1.Hash, r2.Hash)
}

func TestReceiptHash_ChangesOnFields(t *testing.T) {
	ts := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)

	base, err := NewReceipt("tx-123", 10, 15, "nornic", ts)
	require.NoError(t, err)

	changedSeq, err := NewReceipt("tx-123", 10, 16, "nornic", ts)
	require.NoError(t, err)
	assert.NotEqual(t, base.Hash, changedSeq.Hash)

	changedTx, err := NewReceipt("tx-124", 10, 15, "nornic", ts)
	require.NoError(t, err)
	assert.NotEqual(t, base.Hash, changedTx.Hash)
}
