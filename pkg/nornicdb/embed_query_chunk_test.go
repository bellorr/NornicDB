package nornicdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type chunkingTestEmbedder struct {
	mu sync.Mutex

	dims int

	embedCalls     int
	embedBatchCall int
	maxTextLen     int
}

func (e *chunkingTestEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	e.embedCalls++
	if len(text) > e.maxTextLen {
		e.maxTextLen = len(text)
	}
	dims := e.dims
	e.mu.Unlock()

	// Simulate a tokenizer limit (e.g., llama.cpp fixed buffer).
	if len(text) > 512 {
		return nil, fmt.Errorf("simulated tokenizer overflow for len=%d", len(text))
	}
	if dims <= 0 {
		dims = 4
	}
	vec := make([]float32, dims)
	vec[0] = 1
	return vec, nil
}

func (e *chunkingTestEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.embedBatchCall++
	for _, t := range texts {
		if len(t) > e.maxTextLen {
			e.maxTextLen = len(t)
		}
	}
	dims := e.dims
	e.mu.Unlock()

	if dims <= 0 {
		dims = 4
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if len(t) > 512 {
			return nil, fmt.Errorf("chunk too long: len=%d", len(t))
		}
		vec := make([]float32, dims)
		vec[0] = 1
		out[i] = vec
	}
	return out, nil
}

func (e *chunkingTestEmbedder) Dimensions() int { return e.dims }
func (e *chunkingTestEmbedder) Model() string   { return "chunking-test-embedder" }

func TestDB_EmbedQuery_ShortQuery_UsesEmbed(t *testing.T) {
	emb := &chunkingTestEmbedder{dims: 4}
	db := &DB{embedQueue: &EmbedQueue{embedder: emb}}

	vec, err := db.EmbedQuery(context.Background(), "hello world")
	require.NoError(t, err)
	require.Len(t, vec, 4)

	emb.mu.Lock()
	embedCalls := emb.embedCalls
	batchCalls := emb.embedBatchCall
	emb.mu.Unlock()

	require.Equal(t, 1, embedCalls)
	require.Equal(t, 0, batchCalls)
}

func TestDB_EmbedQuery_LongQuery_UsesEmbedBatchOnChunks(t *testing.T) {
	emb := &chunkingTestEmbedder{dims: 4}
	db := &DB{embedQueue: &EmbedQueue{embedder: emb}}

	longQuery := strings.Repeat("a", 1200)
	vec, err := db.EmbedQuery(context.Background(), longQuery)
	require.NoError(t, err)
	require.Len(t, vec, 4)

	emb.mu.Lock()
	embedCalls := emb.embedCalls
	batchCalls := emb.embedBatchCall
	maxLen := emb.maxTextLen
	emb.mu.Unlock()

	require.Equal(t, 0, embedCalls, "expected long query path to avoid single-text embedding")
	require.Equal(t, 1, batchCalls, "expected long query path to batch-embed chunks once")
	require.LessOrEqual(t, maxLen, 512, "expected all embedded chunks to be <= 512 characters")
}

func TestDB_EmbedQueryForDB_NoResolver_ReturnsSameAsEmbedQuery(t *testing.T) {
	emb := &chunkingTestEmbedder{dims: 8}
	db := &DB{embedQueue: &EmbedQueue{embedder: emb}}

	vec, err := db.EmbedQueryForDB(context.Background(), "mydb", "hello")
	require.NoError(t, err)
	require.Len(t, vec, 8)
}

func TestDB_EmbedQueryForDB_ResolverMatchesDims_Success(t *testing.T) {
	emb := &chunkingTestEmbedder{dims: 8}
	db := &DB{
		embedQueue: &EmbedQueue{embedder: emb},
		dbConfigResolver: func(dbName string) (embeddingDims int, searchMinSimilarity float64) {
			return 8, 0.5
		},
	}

	vec, err := db.EmbedQueryForDB(context.Background(), "mydb", "hello")
	require.NoError(t, err)
	require.Len(t, vec, 8)
}

func TestDB_EmbedQueryForDB_ResolverMismatchDims_ReturnsErrQueryEmbeddingDimensionMismatch(t *testing.T) {
	emb := &chunkingTestEmbedder{dims: 8}
	db := &DB{
		embedQueue: &EmbedQueue{embedder: emb},
		dbConfigResolver: func(dbName string) (embeddingDims int, searchMinSimilarity float64) {
			return 768, 0.5 // index is 768-d, query will be 8-d from global embedder
		},
	}

	vec, err := db.EmbedQueryForDB(context.Background(), "mydb", "hello")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrQueryEmbeddingDimensionMismatch))
	require.Nil(t, vec)
	require.Contains(t, err.Error(), "index dims 768")
	require.Contains(t, err.Error(), "query dims 8")
}
