package search

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIVFPQPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	base := fmt.Sprintf("%s/hnsw", dir)
	vfs, err := NewVectorFileStore(fmt.Sprintf("%s/vectors", dir), 8)
	require.NoError(t, err)
	defer vfs.Close()

	for i := 0; i < 900; i++ {
		vec := []float32{1, 0, 0, 0, 0, 0, 0, 0}
		if i%2 == 0 {
			vec = []float32{0, 1, 0, 0, 0, 0, 0, 0}
		}
		require.NoError(t, vfs.Add(fmt.Sprintf("doc-%d", i), vec))
	}
	idx, _, err := BuildIVFPQFromVectorStore(context.Background(), vfs, IVFPQProfile{
		Dimensions:          8,
		IVFLists:            20,
		PQSegments:          4,
		PQBits:              4,
		NProbe:              5,
		RerankTopK:          50,
		TrainingSampleMax:   800,
		KMeansMaxIterations: 6,
	}, nil)
	require.NoError(t, err)

	require.NoError(t, SaveIVFPQBundle(base, idx))
	loaded, err := LoadIVFPQBundle(base)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, idx.Count(), loaded.Count())
	require.True(t, loaded.compatibleProfile(idx.profile))
}
