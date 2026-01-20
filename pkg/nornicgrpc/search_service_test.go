package nornicgrpc

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	gen "github.com/orneryd/nornicdb/pkg/nornicgrpc/gen"
	"github.com/orneryd/nornicdb/pkg/search"
	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

type stubSearcher struct {
	lastQuery     string
	lastEmbedding []float32
	lastOpts      *search.SearchOptions

	resp *search.SearchResponse
	err  error
}

func (s *stubSearcher) Search(ctx context.Context, query string, embedding []float32, opts *search.SearchOptions) (*search.SearchResponse, error) {
	s.lastQuery = query
	s.lastEmbedding = embedding
	s.lastOpts = opts
	return s.resp, s.err
}

func TestService_SearchText_EmbedsAndConvertsResults(t *testing.T) {
	st := &stubSearcher{
		resp: &search.SearchResponse{
			SearchMethod:      "rrf_hybrid",
			FallbackTriggered: false,
			Message:           "",
			Results: []search.SearchResult{
				{
					NodeID:     storage.NodeID("nornic:node1"),
					Labels:     []string{"Doc"},
					Properties: map[string]any{"title": "hello"},
					Score:      0.9,
					RRFScore:   0.8,
					VectorRank: 1,
					BM25Rank:   2,
				},
			},
		},
	}

	svc, err := NewService(
		Config{DefaultDatabase: "nornic", MaxLimit: 100},
		func(ctx context.Context, query string) ([]float32, error) {
			require.Equal(t, "database performance", query)
			return []float32{0.1, 0.2}, nil
		},
		st,
	)
	require.NoError(t, err)

	resp, err := svc.SearchText(context.Background(), &gen.SearchTextRequest{
		Query:  "database performance",
		Limit:  10,
		Labels: []string{"Doc"},
	})
	require.NoError(t, err)
	require.Equal(t, "rrf_hybrid", resp.SearchMethod)
	require.Len(t, resp.Hits, 1)
	require.Equal(t, "nornic:node1", resp.Hits[0].NodeId)
	require.Equal(t, []string{"Doc"}, resp.Hits[0].Labels)
	require.NotNil(t, resp.Hits[0].Properties)
	require.Equal(t, float32(0.9), resp.Hits[0].Score)
	require.Equal(t, float32(0.8), resp.Hits[0].RrfScore)
	require.Equal(t, int32(1), resp.Hits[0].VectorRank)
	require.Equal(t, int32(2), resp.Hits[0].Bm25Rank)

	require.Equal(t, "database performance", st.lastQuery)
	require.Equal(t, []float32{0.1, 0.2}, st.lastEmbedding)
	require.NotNil(t, st.lastOpts)
	require.Equal(t, 10, st.lastOpts.Limit)
	require.Equal(t, []string{"Doc"}, st.lastOpts.Types)
}

type recordingSearcher struct {
	mu      sync.Mutex
	queries []string
}

func (s *recordingSearcher) Search(ctx context.Context, query string, embedding []float32, opts *search.SearchOptions) (*search.SearchResponse, error) {
	s.mu.Lock()
	s.queries = append(s.queries, query)
	s.mu.Unlock()

	// Return deterministic results keyed off which marker appears in this query chunk.
	// Markers are placed far apart so chunking should isolate them.
	var results []search.SearchResult
	switch {
	case strings.Contains(query, "ONE"):
		results = []search.SearchResult{
			{NodeID: storage.NodeID("n2"), Labels: []string{"Doc"}, Properties: map[string]any{"title": "B"}, Score: 0.03, RRFScore: 0.03, VectorRank: 1, BM25Rank: 1},
			{NodeID: storage.NodeID("n1"), Labels: []string{"Doc"}, Properties: map[string]any{"title": "A"}, Score: 0.02, RRFScore: 0.02, VectorRank: 2, BM25Rank: 2},
		}
	case strings.Contains(query, "TWO"):
		results = []search.SearchResult{
			{NodeID: storage.NodeID("n2"), Labels: []string{"Doc"}, Properties: map[string]any{"title": "B"}, Score: 0.03, RRFScore: 0.03, VectorRank: 1, BM25Rank: 1},
			{NodeID: storage.NodeID("n3"), Labels: []string{"Doc"}, Properties: map[string]any{"title": "C"}, Score: 0.02, RRFScore: 0.02, VectorRank: 2, BM25Rank: 2},
		}
	case strings.Contains(query, "THREE"):
		results = []search.SearchResult{
			{NodeID: storage.NodeID("n1"), Labels: []string{"Doc"}, Properties: map[string]any{"title": "A"}, Score: 0.03, RRFScore: 0.03, VectorRank: 1, BM25Rank: 1},
		}
	default:
		results = nil
	}

	return &search.SearchResponse{
		SearchMethod:      "rrf_hybrid",
		FallbackTriggered: false,
		Results:           results,
	}, nil
}

func TestService_SearchText_ChunksLongQueryAndFusesAcrossChunks(t *testing.T) {
	st := &recordingSearcher{}

	var (
		mu       sync.Mutex
		embedMax int
		embeds   int
	)

	svc, err := NewService(
		Config{DefaultDatabase: "nornic", MaxLimit: 100},
		func(ctx context.Context, query string) ([]float32, error) {
			mu.Lock()
			embeds++
			if len(query) > embedMax {
				embedMax = len(query)
			}
			mu.Unlock()

			if len(query) > 512 {
				return nil, fmt.Errorf("simulated tokenizer overflow for len=%d", len(query))
			}
			return []float32{0.1, 0.2}, nil
		},
		st,
	)
	require.NoError(t, err)

	// Construct a long query with markers spaced so chunking isolates them.
	longQuery := strings.Repeat("a", 100) + "ONE" +
		strings.Repeat("a", 500) + "TWO" +
		strings.Repeat("a", 500) + "THREE" +
		strings.Repeat("a", 200)

	resp, err := svc.SearchText(context.Background(), &gen.SearchTextRequest{
		Query: longQuery,
		Limit: 3,
	})
	require.NoError(t, err)
	require.Equal(t, "chunked_rrf_hybrid", resp.SearchMethod)
	require.NotEmpty(t, resp.Hits)
	require.Equal(t, "n2", resp.Hits[0].NodeId, "expected fusion to rank node present in multiple chunks highest")

	mu.Lock()
	gotEmbeds := embeds
	gotEmbedMax := embedMax
	mu.Unlock()
	require.GreaterOrEqual(t, gotEmbeds, 2, "expected multiple chunk embeddings")
	require.LessOrEqual(t, gotEmbedMax, 512, "expected no embedding call on the full long query")

	st.mu.Lock()
	qs := append([]string(nil), st.queries...)
	st.mu.Unlock()
	require.GreaterOrEqual(t, len(qs), 2, "expected multiple per-chunk searches")
	maxQ := 0
	for _, q := range qs {
		if len(q) > maxQ {
			maxQ = len(q)
		}
	}
	require.LessOrEqual(t, maxQ, 512, "expected no search call on the full long query")
}
