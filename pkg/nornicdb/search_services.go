package nornicdb

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	featureflags "github.com/orneryd/nornicdb/pkg/config"
	"github.com/orneryd/nornicdb/pkg/gpu"
	"github.com/orneryd/nornicdb/pkg/search"
	"github.com/orneryd/nornicdb/pkg/storage"
)

type dbSearchService struct {
	dbName string
	engine storage.Engine
	svc    *search.Service

	buildOnce sync.Once
	buildErr  error

	clusterMu               sync.Mutex
	lastClusteredEmbedCount int
}

func splitQualifiedID(id string) (dbName string, local string, ok bool) {
	dbName, local, ok = strings.Cut(id, ":")
	if !ok || dbName == "" || local == "" {
		return "", "", false
	}
	return dbName, local, true
}

func (db *DB) defaultDatabaseName() string {
	if namespaced, ok := db.storage.(*storage.NamespacedEngine); ok {
		return namespaced.Namespace()
	}
	// DB storage must always be namespaced; anything else is a programmer error.
	panic("nornicdb: DB storage is not namespaced")
}

func (db *DB) getOrCreateSearchService(dbName string, storageEngine storage.Engine) (*search.Service, error) {
	if dbName == "" {
		dbName = db.defaultDatabaseName()
	}
	if dbName == "system" {
		return nil, fmt.Errorf("search service not available for system database")
	}

	dims := db.embeddingDims
	minSim := db.searchMinSimilarity

	var gpuMgr *gpu.Manager
	db.gpuManagerMu.RLock()
	if m, ok := db.gpuManager.(*gpu.Manager); ok {
		gpuMgr = m
	}
	db.gpuManagerMu.RUnlock()

	db.searchServicesMu.RLock()
	if entry, ok := db.searchServices[dbName]; ok {
		svc := entry.svc
		db.searchServicesMu.RUnlock()

		// If clustering is enabled globally, ensure cached services have clustering enabled too.
		// Services may be created before the feature flag is turned on (e.g., early HTTP calls),
		// in which case they need to be upgraded in place.
		if svc != nil && featureflags.IsGPUClusteringEnabled() && !svc.IsClusteringEnabled() {
			var mgr *gpu.Manager
			if gpuMgr != nil && gpuMgr.IsEnabled() {
				mgr = gpuMgr
			}
			svc.EnableClustering(mgr, 100)
		}
		return svc, nil
	}
	db.searchServicesMu.RUnlock()

	if storageEngine == nil {
		if db.baseStorage == nil {
			return nil, fmt.Errorf("search service unavailable: base storage is nil")
		}
		storageEngine = storage.NewNamespacedEngine(db.baseStorage, dbName)
	}

	if dims <= 0 {
		dims = 1024
	}
	svc := search.NewServiceWithDimensions(storageEngine, dims)
	svc.SetDefaultMinSimilarity(minSim)

	// Enable GPU brute-force search if a GPU manager is configured.
	if gpuMgr != nil {
		svc.SetGPUManager(gpuMgr)
	}

	// Enable per-database clustering if the feature flag is enabled.
	// Each Service maintains its own cluster index and must cluster independently.
	if featureflags.IsGPUClusteringEnabled() {
		var mgr *gpu.Manager
		if gpuMgr != nil && gpuMgr.IsEnabled() {
			mgr = gpuMgr
		}
		svc.EnableClustering(mgr, 100)
	}

	entry := &dbSearchService{
		dbName: dbName,
		engine: storageEngine,
		svc:    svc,
	}

	db.searchServicesMu.Lock()
	// Double-check in case someone else created it.
	if existing, ok := db.searchServices[dbName]; ok {
		db.searchServicesMu.Unlock()
		return existing.svc, nil
	}
	db.searchServices[dbName] = entry
	db.searchServicesMu.Unlock()

	return svc, nil
}

// GetOrCreateSearchService returns the per-database search service for dbName.
//
// storageEngine should be a *storage.NamespacedEngine for dbName (typically
// obtained via multidb.DatabaseManager). If nil, db.baseStorage is wrapped with
// a NamespacedEngine for dbName.
func (db *DB) GetOrCreateSearchService(dbName string, storageEngine storage.Engine) (*search.Service, error) {
	db.mu.RLock()
	closed := db.closed
	db.mu.RUnlock()
	if closed {
		return nil, ErrClosed
	}
	return db.getOrCreateSearchService(dbName, storageEngine)
}

// ResetSearchService drops the cached search service for a database.
// The next call to GetOrCreateSearchService will create a fresh, empty service.
func (db *DB) ResetSearchService(dbName string) {
	if dbName == "" {
		dbName = db.defaultDatabaseName()
	}
	db.searchServicesMu.Lock()
	delete(db.searchServices, dbName)
	db.searchServicesMu.Unlock()
}

func (db *DB) ensureSearchIndexesBuilt(ctx context.Context, dbName string) error {
	if dbName == "" {
		dbName = db.defaultDatabaseName()
	}

	db.searchServicesMu.RLock()
	entry, ok := db.searchServices[dbName]
	db.searchServicesMu.RUnlock()
	if !ok || entry == nil {
		return fmt.Errorf("search service not initialized for database %q", dbName)
	}

	entry.buildOnce.Do(func() {
		entry.buildErr = entry.svc.BuildIndexes(ctx)
	})
	return entry.buildErr
}

// EnsureSearchIndexesBuilt ensures the per-database search indexes are built exactly once.
// If the service doesn’t exist yet, it is created (using storageEngine if provided).
func (db *DB) EnsureSearchIndexesBuilt(ctx context.Context, dbName string, storageEngine storage.Engine) (*search.Service, error) {
	svc, err := db.getOrCreateSearchService(dbName, storageEngine)
	if err != nil {
		return nil, err
	}
	if err := db.ensureSearchIndexesBuilt(ctx, dbName); err != nil {
		return svc, err
	}
	return svc, nil
}

func (db *DB) indexNodeFromEvent(node *storage.Node) {
	if node == nil {
		return
	}

	dbName, local, ok := splitQualifiedID(string(node.ID))
	if !ok {
		// Unprefixed IDs are not supported. This indicates a bug in the storage event pipeline.
		log.Printf("⚠️ storage event had unprefixed node ID: %q", node.ID)
		return
	}
	// Qdrant gRPC points are stored under a reserved sub-namespace and are indexed
	// by the Qdrant vector index cache, not the hybrid search service.
	if strings.HasPrefix(local, "qdrant:") {
		return
	}

	svc, err := db.getOrCreateSearchService(dbName, nil)
	if err != nil || svc == nil {
		return
	}

	userNode := storage.CopyNode(node)
	userNode.ID = storage.NodeID(local)
	if err := svc.IndexNode(userNode); err != nil {
		log.Printf("⚠️ Failed to index node %s in db %s: %v", node.ID, dbName, err)
	}
}

func (db *DB) removeNodeFromEvent(nodeID storage.NodeID) {
	dbName, local, ok := splitQualifiedID(string(nodeID))
	if !ok {
		return
	}

	db.searchServicesMu.RLock()
	entry, ok := db.searchServices[dbName]
	db.searchServicesMu.RUnlock()
	if !ok || entry == nil {
		// Service not in cache yet; nothing to remove.
		return
	}

	if err := entry.svc.RemoveNode(storage.NodeID(local)); err != nil {
		log.Printf("⚠️ Failed to remove node %s from search indexes in db %s: %v", nodeID, dbName, err)
	}
}

func (db *DB) runClusteringOnceAllDatabases() {
	// Ensure the default database service exists so an immediate clustering run
	// produces deterministic behavior even before the first search request.
	if _, err := db.getOrCreateSearchService(db.defaultDatabaseName(), db.storage); err != nil {
		log.Printf("⚠️  K-means clustering: failed to initialize default search service: %v", err)
	}

	// If storage can enumerate namespaces, initialize per-database services so
	// clustering can run across all known databases (excluding system).
	if lister, ok := db.baseStorage.(storage.NamespaceLister); ok {
		for _, ns := range lister.ListNamespaces() {
			if ns == "" || ns == "system" {
				continue
			}
			if _, err := db.getOrCreateSearchService(ns, nil); err != nil {
				log.Printf("⚠️  K-means clustering: failed to initialize search service for db %s: %v", ns, err)
			}
		}
	}

	db.searchServicesMu.RLock()
	entries := make([]*dbSearchService, 0, len(db.searchServices))
	for _, entry := range db.searchServices {
		entries = append(entries, entry)
	}
	db.searchServicesMu.RUnlock()

	for _, entry := range entries {
		if entry == nil || entry.dbName == "system" {
			continue
		}
		if entry == nil || entry.svc == nil || !entry.svc.IsClusteringEnabled() {
			continue
		}

		// Serialize clustering per database to avoid duplicate work when multiple
		// triggers fire concurrently (startup hooks, manual triggers, timer ticks).
		entry.clusterMu.Lock()
		currentCount := entry.svc.EmbeddingCount()
		if currentCount == entry.lastClusteredEmbedCount && entry.lastClusteredEmbedCount > 0 {
			entry.clusterMu.Unlock()
			continue
		}

		if err := entry.svc.TriggerClustering(); err != nil {
			entry.clusterMu.Unlock()
			log.Printf("⚠️  K-means clustering skipped for db %s: %v", entry.dbName, err)
			continue
		}

		entry.lastClusteredEmbedCount = currentCount
		entry.clusterMu.Unlock()
		log.Printf("🔬 K-means clustering completed for db %s (%d embeddings)", entry.dbName, currentCount)
	}
}
