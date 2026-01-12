package qdrantgrpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/orneryd/nornicdb/pkg/multidb"
	qpb "github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orneryd/nornicdb/pkg/storage"
)

func setupSnapshotsTest(t *testing.T) (*SnapshotsService, *PointsService, CollectionStore, string, func()) {
	base := storage.NewMemoryEngine()
	dbm, err := multidb.NewDatabaseManager(base, nil)
	require.NoError(t, err)
	vecIndex := newVectorIndexCache()
	collections, err := NewDatabaseCollectionStore(dbm, vecIndex)
	require.NoError(t, err)

	snapshotDir := t.TempDir()

	config := &Config{
		ListenAddr:           ":6334",
		AllowVectorMutations: true,
		MaxVectorDim:         4096,
		MaxBatchPoints:       1000,
		MaxTopK:              1000,
		SnapshotDir:          snapshotDir,
	}

	snapshotsService := NewSnapshotsService(config, collections, base, snapshotDir)
	pointsService := NewPointsService(config, collections, nil, vecIndex)

	// Create test collection
	ctx := context.Background()
	err = collections.Create(ctx, "test_collection", 4, qpb.Distance_Cosine)
	require.NoError(t, err)

	// Add some test points
	_, err = pointsService.Upsert(ctx, &qpb.UpsertPoints{
		CollectionName: "test_collection",
		Points: []*qpb.PointStruct{
			{
				Id: &qpb.PointId{PointIdOptions: &qpb.PointId_Uuid{Uuid: "point1"}},
				Vectors: &qpb.Vectors{
					VectorsOptions: &qpb.Vectors_Vector{
						Vector: &qpb.Vector{Vector: &qpb.Vector_Dense{Dense: &qpb.DenseVector{Data: []float32{1, 0, 0, 0}}}},
					},
				},
				Payload: map[string]*qpb.Value{"name": {Kind: &qpb.Value_StringValue{StringValue: "first"}}},
			},
			{
				Id: &qpb.PointId{PointIdOptions: &qpb.PointId_Uuid{Uuid: "point2"}},
				Vectors: &qpb.Vectors{
					VectorsOptions: &qpb.Vectors_Vector{
						Vector: &qpb.Vector{Vector: &qpb.Vector_Dense{Dense: &qpb.DenseVector{Data: []float32{0, 1, 0, 0}}}},
					},
				},
				Payload: map[string]*qpb.Value{"name": {Kind: &qpb.Value_StringValue{StringValue: "second"}}},
			},
		},
	})
	require.NoError(t, err)

	cleanup := func() {
		base.Close()
	}

	return snapshotsService, pointsService, collections, snapshotDir, cleanup
}

func TestSnapshotsService_Create(t *testing.T) {
	service, _, _, snapshotDir, cleanup := setupSnapshotsTest(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("create snapshot successfully", func(t *testing.T) {
		resp, err := service.Create(ctx, &qpb.CreateSnapshotRequest{
			CollectionName: "test_collection",
		})
		require.NoError(t, err)
		require.NotNil(t, resp.SnapshotDescription)
		assert.NotEmpty(t, resp.SnapshotDescription.Name)
		assert.NotNil(t, resp.SnapshotDescription.CreationTime)
		assert.True(t, resp.SnapshotDescription.Size > 0)

		// Verify file exists
		snapshotPath := filepath.Join(snapshotDir, "collections", "test_collection", resp.SnapshotDescription.Name)
		_, err = os.Stat(snapshotPath)
		assert.NoError(t, err)
	})

	t.Run("error on empty collection name", func(t *testing.T) {
		_, err := service.Create(ctx, &qpb.CreateSnapshotRequest{})
		assert.Error(t, err)
	})

	t.Run("error on non-existent collection", func(t *testing.T) {
		_, err := service.Create(ctx, &qpb.CreateSnapshotRequest{
			CollectionName: "non_existent",
		})
		assert.Error(t, err)
	})
}

func TestSnapshotsService_List(t *testing.T) {
	service, _, _, _, cleanup := setupSnapshotsTest(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("list empty snapshots", func(t *testing.T) {
		resp, err := service.List(ctx, &qpb.ListSnapshotsRequest{
			CollectionName: "test_collection",
		})
		require.NoError(t, err)
		assert.Empty(t, resp.SnapshotDescriptions)
	})

	t.Run("list multiple snapshots", func(t *testing.T) {
		// Create a few snapshots
		_, err := service.Create(ctx, &qpb.CreateSnapshotRequest{CollectionName: "test_collection"})
		require.NoError(t, err)
		_, err = service.Create(ctx, &qpb.CreateSnapshotRequest{CollectionName: "test_collection"})
		require.NoError(t, err)

		resp, err := service.List(ctx, &qpb.ListSnapshotsRequest{
			CollectionName: "test_collection",
		})
		require.NoError(t, err)
		assert.Len(t, resp.SnapshotDescriptions, 2)
	})

	t.Run("error on empty collection name", func(t *testing.T) {
		_, err := service.List(ctx, &qpb.ListSnapshotsRequest{})
		assert.Error(t, err)
	})

	t.Run("error on non-existent collection", func(t *testing.T) {
		_, err := service.List(ctx, &qpb.ListSnapshotsRequest{
			CollectionName: "non_existent",
		})
		assert.Error(t, err)
	})
}

func TestSnapshotsService_Delete(t *testing.T) {
	service, _, _, _, cleanup := setupSnapshotsTest(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("delete snapshot successfully", func(t *testing.T) {
		// Create a snapshot first
		createResp, err := service.Create(ctx, &qpb.CreateSnapshotRequest{
			CollectionName: "test_collection",
		})
		require.NoError(t, err)
		require.NotNil(t, createResp.SnapshotDescription)

		// Delete it
		resp, err := service.Delete(ctx, &qpb.DeleteSnapshotRequest{
			CollectionName: "test_collection",
			SnapshotName:   createResp.SnapshotDescription.Name,
		})
		require.NoError(t, err)
		assert.True(t, resp.Time >= 0)

		// Verify it's gone
		listResp, err := service.List(ctx, &qpb.ListSnapshotsRequest{
			CollectionName: "test_collection",
		})
		require.NoError(t, err)
		assert.Empty(t, listResp.SnapshotDescriptions)
	})

	t.Run("error on empty collection name", func(t *testing.T) {
		_, err := service.Delete(ctx, &qpb.DeleteSnapshotRequest{
			SnapshotName: "some-snapshot",
		})
		assert.Error(t, err)
	})

	t.Run("error on empty snapshot name", func(t *testing.T) {
		_, err := service.Delete(ctx, &qpb.DeleteSnapshotRequest{
			CollectionName: "test_collection",
		})
		assert.Error(t, err)
	})

	t.Run("error on non-existent snapshot", func(t *testing.T) {
		_, err := service.Delete(ctx, &qpb.DeleteSnapshotRequest{
			CollectionName: "test_collection",
			SnapshotName:   "non-existent.snapshot",
		})
		assert.Error(t, err)
	})
}

func TestSnapshotsService_CreateFull(t *testing.T) {
	service, _, _, snapshotDir, cleanup := setupSnapshotsTest(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("create full snapshot successfully", func(t *testing.T) {
		resp, err := service.CreateFull(ctx, &qpb.CreateFullSnapshotRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp.SnapshotDescription)
		assert.NotEmpty(t, resp.SnapshotDescription.Name)
		assert.NotNil(t, resp.SnapshotDescription.CreationTime)
		assert.True(t, resp.SnapshotDescription.Size > 0)

		// Verify file exists
		snapshotPath := filepath.Join(snapshotDir, "full", resp.SnapshotDescription.Name)
		_, err = os.Stat(snapshotPath)
		assert.NoError(t, err)
	})
}

func TestSnapshotsService_ListFull(t *testing.T) {
	service, _, _, _, cleanup := setupSnapshotsTest(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("list empty full snapshots", func(t *testing.T) {
		resp, err := service.ListFull(ctx, &qpb.ListFullSnapshotsRequest{})
		require.NoError(t, err)
		assert.Empty(t, resp.SnapshotDescriptions)
	})

	t.Run("list multiple full snapshots", func(t *testing.T) {
		// Create a few full snapshots
		_, err := service.CreateFull(ctx, &qpb.CreateFullSnapshotRequest{})
		require.NoError(t, err)
		_, err = service.CreateFull(ctx, &qpb.CreateFullSnapshotRequest{})
		require.NoError(t, err)

		resp, err := service.ListFull(ctx, &qpb.ListFullSnapshotsRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.SnapshotDescriptions, 2)
	})
}

func TestSnapshotsService_DeleteFull(t *testing.T) {
	service, _, _, _, cleanup := setupSnapshotsTest(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("delete full snapshot successfully", func(t *testing.T) {
		// Create a full snapshot first
		createResp, err := service.CreateFull(ctx, &qpb.CreateFullSnapshotRequest{})
		require.NoError(t, err)
		require.NotNil(t, createResp.SnapshotDescription)

		// Delete it
		resp, err := service.DeleteFull(ctx, &qpb.DeleteFullSnapshotRequest{
			SnapshotName: createResp.SnapshotDescription.Name,
		})
		require.NoError(t, err)
		assert.True(t, resp.Time >= 0)

		// Verify it's gone
		listResp, err := service.ListFull(ctx, &qpb.ListFullSnapshotsRequest{})
		require.NoError(t, err)
		assert.Empty(t, listResp.SnapshotDescriptions)
	})

	t.Run("error on empty snapshot name", func(t *testing.T) {
		_, err := service.DeleteFull(ctx, &qpb.DeleteFullSnapshotRequest{})
		assert.Error(t, err)
	})

	t.Run("error on non-existent snapshot", func(t *testing.T) {
		_, err := service.DeleteFull(ctx, &qpb.DeleteFullSnapshotRequest{
			SnapshotName: "non-existent.snapshot",
		})
		assert.Error(t, err)
	})
}
