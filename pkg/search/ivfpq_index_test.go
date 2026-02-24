package search

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIVFPQIndex_SearchApprox(t *testing.T) {
	dir := t.TempDir()
	vfs, err := NewVectorFileStore(fmt.Sprintf("%s/vectors", dir), 8)
	require.NoError(t, err)
	defer vfs.Close()

	for i := 0; i < 800; i++ {
		vec := []float32{1, 0, 0, 0, 0, 0, 0, 0}
		if i%2 == 1 {
			vec = []float32{0, 1, 0, 0, 0, 0, 0, 0}
		}
		require.NoError(t, vfs.Add(fmt.Sprintf("doc-%d", i), vec))
	}

	profile := IVFPQProfile{
		Dimensions:          8,
		IVFLists:            16,
		PQSegments:          4,
		PQBits:              4,
		NProbe:              4,
		RerankTopK:          50,
		TrainingSampleMax:   700,
		KMeansMaxIterations: 6,
	}
	idx, _, err := BuildIVFPQFromVectorStore(context.Background(), vfs, profile, nil)
	require.NoError(t, err)

	out, err := idx.SearchApprox(context.Background(), []float32{1, 0, 0, 0, 0, 0, 0, 0}, 10, -1, 4)
	require.NoError(t, err)
	require.NotEmpty(t, out)
	require.GreaterOrEqual(t, out[0].Score, out[len(out)-1].Score)
}

func TestIVFPQCandidateLimit_TightWindow(t *testing.T) {
	// Uses tighter compressed rerank window than generic candidate defaults.
	require.Equal(t, 96, ivfpqCandidateLimit(10, 16, 200))
	require.Equal(t, 24, ivfpqCandidateLimit(10, 4, 200))
	// Respects rerank cap when explicitly set lower.
	require.Equal(t, 64, ivfpqCandidateLimit(10, 16, 64))
	// Still keeps a minimum floor for tiny k/nprobe.
	require.Equal(t, 16, ivfpqCandidateLimit(1, 1, 0))
}
