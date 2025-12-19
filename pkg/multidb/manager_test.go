package multidb

import (
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseManager_DefaultConfig(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Default database should be "nornic"
	assert.Equal(t, "nornic", manager.DefaultDatabaseName())

	// System database should exist
	assert.True(t, manager.Exists("system"))

	// Default database should exist
	assert.True(t, manager.Exists("nornic"))

	// Default database should be marked as default
	info, err := manager.GetDatabase("nornic")
	require.NoError(t, err)
	assert.True(t, info.IsDefault)
	assert.Equal(t, "standard", info.Type)
	assert.Equal(t, "online", info.Status)
}

func TestDatabaseManager_CustomConfig(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	config := &Config{
		DefaultDatabase:  "customdb",
		SystemDatabase:   "system",
		MaxDatabases:     10,
		AllowDropDefault: false,
	}

	manager, err := NewDatabaseManager(inner, config)
	require.NoError(t, err)
	defer manager.Close()

	assert.Equal(t, "customdb", manager.DefaultDatabaseName())
	assert.True(t, manager.Exists("customdb"))
	assert.True(t, manager.Exists("system"))
}

func TestDatabaseManager_CreateDatabase(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Create a new database
	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)

	// Verify it exists
	assert.True(t, manager.Exists("tenant_a"))

	// Get database info
	info, err := manager.GetDatabase("tenant_a")
	require.NoError(t, err)
	assert.Equal(t, "tenant_a", info.Name)
	assert.Equal(t, "standard", info.Type)
	assert.Equal(t, "online", info.Status)
	assert.False(t, info.IsDefault)

	// Create another database
	err = manager.CreateDatabase("tenant_b")
	require.NoError(t, err)
	assert.True(t, manager.Exists("tenant_b"))
}

func TestDatabaseManager_CreateDatabase_Duplicate(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)

	// Try to create duplicate
	err = manager.CreateDatabase("tenant_a")
	assert.Error(t, err)
	assert.Equal(t, ErrDatabaseExists, err)
}

func TestDatabaseManager_CreateDatabase_InvalidName(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Empty name should fail
	err = manager.CreateDatabase("")
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidDatabaseName, err)
}

func TestDatabaseManager_CreateDatabase_MaxLimit(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	config := &Config{
		DefaultDatabase: "nornic",
		SystemDatabase:  "system",
		MaxDatabases:    3, // system + nornic + 1 more
	}

	manager, err := NewDatabaseManager(inner, config)
	require.NoError(t, err)
	defer manager.Close()

	// Create one more (should succeed)
	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)

	// Try to create another (should fail)
	err = manager.CreateDatabase("tenant_b")
	assert.Error(t, err)
	assert.Equal(t, ErrMaxDatabasesReached, err)
}

func TestDatabaseManager_DropDatabase(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Create database
	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)

	// Add some data to the database
	store, err := manager.GetStorage("tenant_a")
	require.NoError(t, err)

	node := &storage.Node{
		ID:     storage.NodeID("test-node"),
		Labels: []string{"Test"},
		Properties: map[string]any{
			"name": "test",
		},
	}
	err = store.CreateNode(node)
	require.NoError(t, err)

	// Drop database
	err = manager.DropDatabase("tenant_a")
	require.NoError(t, err)

	// Verify it's gone
	assert.False(t, manager.Exists("tenant_a"))

	// Verify data is deleted (try to get storage - should fail)
	_, err = manager.GetStorage("tenant_a")
	assert.Error(t, err)
	assert.Equal(t, ErrDatabaseNotFound, err)
}

func TestDatabaseManager_DropDatabase_NotFound(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.DropDatabase("nonexistent")
	assert.Error(t, err)
	assert.Equal(t, ErrDatabaseNotFound, err)
}

func TestDatabaseManager_DropDatabase_System(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Cannot drop system database
	err = manager.DropDatabase("system")
	assert.Error(t, err)
	assert.Equal(t, ErrCannotDropSystemDB, err)
}

func TestDatabaseManager_DropDatabase_Default(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Cannot drop default database (by default)
	err = manager.DropDatabase("nornic")
	assert.Error(t, err)
	assert.Equal(t, ErrCannotDropDefaultDB, err)
}

func TestDatabaseManager_DropDatabase_Default_Allowed(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	config := &Config{
		DefaultDatabase:  "nornic",
		SystemDatabase:   "system",
		AllowDropDefault: true,
	}

	manager, err := NewDatabaseManager(inner, config)
	require.NoError(t, err)
	defer manager.Close()

	// Should be able to drop default if allowed
	err = manager.DropDatabase("nornic")
	require.NoError(t, err)
	assert.False(t, manager.Exists("nornic"))
}

func TestDatabaseManager_GetStorage(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Get storage for default database
	store, err := manager.GetStorage("nornic")
	require.NoError(t, err)
	assert.NotNil(t, store)

	// Create a node
	node := &storage.Node{
		ID:     storage.NodeID("test-node"),
		Labels: []string{"Test"},
		Properties: map[string]any{
			"name": "test",
		},
	}
	err = store.CreateNode(node)
	require.NoError(t, err)

	// Get the same storage again (should be cached)
	store2, err := manager.GetStorage("nornic")
	require.NoError(t, err)
	assert.Equal(t, store, store2) // Should be same instance (cached)
}

func TestDatabaseManager_GetStorage_NotFound(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	_, err = manager.GetStorage("nonexistent")
	assert.Error(t, err)
	assert.Equal(t, ErrDatabaseNotFound, err)
}

func TestDatabaseManager_GetStorage_Isolation(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Create two databases
	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)
	err = manager.CreateDatabase("tenant_b")
	require.NoError(t, err)

	// Get storage for each
	storeA, err := manager.GetStorage("tenant_a")
	require.NoError(t, err)
	storeB, err := manager.GetStorage("tenant_b")
	require.NoError(t, err)

	// Create node in tenant_a
	nodeA := &storage.Node{
		ID:     storage.NodeID("node-a"),
		Labels: []string{"Test"},
		Properties: map[string]any{
			"tenant": "a",
		},
	}
	err = storeA.CreateNode(nodeA)
	require.NoError(t, err)

	// Create node in tenant_b
	nodeB := &storage.Node{
		ID:     storage.NodeID("node-b"),
		Labels: []string{"Test"},
		Properties: map[string]any{
			"tenant": "b",
		},
	}
	err = storeB.CreateNode(nodeB)
	require.NoError(t, err)

	// Verify isolation: tenant_a should only see its node
	nodesA, err := storeA.AllNodes()
	require.NoError(t, err)
	assert.Len(t, nodesA, 1)
	assert.Equal(t, "node-a", string(nodesA[0].ID))

	// Verify isolation: tenant_b should only see its node
	nodesB, err := storeB.AllNodes()
	require.NoError(t, err)
	assert.Len(t, nodesB, 1)
	assert.Equal(t, "node-b", string(nodesB[0].ID))
}

func TestDatabaseManager_ListDatabases(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Create some databases
	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)
	err = manager.CreateDatabase("tenant_b")
	require.NoError(t, err)

	// List all databases
	databases := manager.ListDatabases()

	// Should have system, nornic, tenant_a, tenant_b
	assert.GreaterOrEqual(t, len(databases), 4)

	names := make(map[string]bool)
	for _, db := range databases {
		names[db.Name] = true
	}

	assert.True(t, names["system"])
	assert.True(t, names["nornic"])
	assert.True(t, names["tenant_a"])
	assert.True(t, names["tenant_b"])
}

func TestDatabaseManager_SetDatabaseStatus(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)

	// Set to offline
	err = manager.SetDatabaseStatus("tenant_a", "offline")
	require.NoError(t, err)

	info, err := manager.GetDatabase("tenant_a")
	require.NoError(t, err)
	assert.Equal(t, "offline", info.Status)

	// Try to get storage for offline database
	_, err = manager.GetStorage("tenant_a")
	assert.Error(t, err)
	assert.Equal(t, ErrDatabaseOffline, err)

	// Set back to online
	err = manager.SetDatabaseStatus("tenant_a", "online")
	require.NoError(t, err)

	// Should be able to get storage now
	_, err = manager.GetStorage("tenant_a")
	require.NoError(t, err)
}

func TestDatabaseManager_SetDatabaseStatus_Invalid(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	err = manager.CreateDatabase("tenant_a")
	require.NoError(t, err)

	// Invalid status
	err = manager.SetDatabaseStatus("tenant_a", "invalid")
	assert.Error(t, err)
}

func TestDatabaseManager_GetDefaultStorage(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	storage, err := manager.GetDefaultStorage()
	require.NoError(t, err)
	assert.NotNil(t, storage)

	// Should be the same as getting "nornic"
	storage2, err := manager.GetStorage("nornic")
	require.NoError(t, err)
	assert.Equal(t, storage, storage2)
}

func TestDatabaseManager_MetadataPersistence(t *testing.T) {
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	// Create first manager
	manager1, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)

	err = manager1.CreateDatabase("tenant_a")
	require.NoError(t, err)
	err = manager1.CreateDatabase("tenant_b")
	require.NoError(t, err)

	// Don't close the manager - just create a new one with same storage
	// (In real usage, the storage would persist to disk, but for memory engine
	// we just reuse the same instance)
	manager2, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager2.Close()

	// Should still see the databases (metadata persisted in storage)
	assert.True(t, manager2.Exists("tenant_a"))
	assert.True(t, manager2.Exists("tenant_b"))
	assert.True(t, manager2.Exists("nornic"))
	assert.True(t, manager2.Exists("system"))
}

func TestDatabaseManager_BackwardsCompatibility(t *testing.T) {
	// Test that existing code without multi-db still works
	inner := storage.NewMemoryEngine()
	defer inner.Close()

	// Create manager with default config (simulates old behavior)
	manager, err := NewDatabaseManager(inner, nil)
	require.NoError(t, err)
	defer manager.Close()

	// Default database should work
	store, err := manager.GetDefaultStorage()
	require.NoError(t, err)

	// Can create nodes in default database (backwards compatible)
	node := &storage.Node{
		ID:     storage.NodeID("legacy-node"),
		Labels: []string{"Legacy"},
		Properties: map[string]any{
			"name": "legacy",
		},
	}
	err = store.CreateNode(node)
	require.NoError(t, err)

	// Can query nodes
	nodes, err := store.AllNodes()
	require.NoError(t, err)
	assert.Len(t, nodes, 1)
}
