// Package multidb provides multi-database support for NornicDB.
//
// This package implements Neo4j 4.x-style multi-database support, allowing
// multiple logical databases (tenants) to share a single physical storage backend
// while maintaining complete data isolation.
package multidb

import (
	"fmt"
	"sync"
	"time"

	"github.com/orneryd/nornicdb/pkg/storage"
)

// DatabaseManager manages multiple logical databases within a single storage engine.
//
// It provides:
//   - Database creation and deletion
//   - Database metadata tracking
//   - Namespaced storage engine views
//   - Neo4j 4.x multi-database compatibility
//
// Thread-safe: all operations are protected by mutex.
//
// Example:
//
//	// Create manager with shared storage
//	inner := storage.NewBadgerEngine("./data")
//	manager := multidb.NewDatabaseManager(inner, nil)
//
//	// Create databases
//	manager.CreateDatabase("tenant_a")
//	manager.CreateDatabase("tenant_b")
//
//	// Get namespaced storage for a tenant
//	tenantStorage, _ := manager.GetStorage("tenant_a")
//
//	// Use storage (isolated to tenant_a)
//	tenantStorage.CreateNode(&storage.Node{ID: "123"})
type DatabaseManager struct {
	mu sync.RWMutex

	// Shared underlying storage
	inner storage.Engine

	// Database metadata (persisted in "system" namespace)
	databases map[string]*DatabaseInfo

	// Configuration
	config *Config

	// Cached namespaced engines (avoid recreating)
	engines map[string]*storage.NamespacedEngine
}

// DatabaseInfo holds metadata about a database.
type DatabaseInfo struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
	Status    string    `json:"status"` // "online", "offline"
	Type      string    `json:"type"`   // "standard", "system"
	IsDefault bool      `json:"is_default"`
	NodeCount int64     `json:"node_count,omitempty"` // Cached, may be stale
	UpdatedAt time.Time `json:"updated_at"`
}

// Config holds DatabaseManager configuration.
type Config struct {
	// DefaultDatabase is the database used when none is specified (default: "nornic")
	// This matches Neo4j's behavior where "neo4j" is the default, but NornicDB uses "nornic"
	DefaultDatabase string

	// SystemDatabase stores metadata (default: "system")
	SystemDatabase string

	// MaxDatabases limits total databases (0 = unlimited)
	MaxDatabases int

	// AllowDropDefault allows dropping the default database
	AllowDropDefault bool
}

// DefaultConfig returns default configuration.
// The default database name is "nornic" (NornicDB's equivalent of Neo4j's "neo4j").
func DefaultConfig() *Config {
	return &Config{
		DefaultDatabase:  "nornic",
		SystemDatabase:   "system",
		MaxDatabases:     0, // Unlimited
		AllowDropDefault: false,
	}
}

// NewDatabaseManager creates a new database manager.
//
// Parameters:
//   - inner: The underlying storage engine (shared by all databases)
//   - config: Configuration (nil for defaults)
//
// On creation, initializes:
//   - System database (for metadata)
//   - Default database ("nornic" by default, configurable)
func NewDatabaseManager(inner storage.Engine, config *Config) (*DatabaseManager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	m := &DatabaseManager{
		inner:     inner,
		databases: make(map[string]*DatabaseInfo),
		config:    config,
		engines:   make(map[string]*storage.NamespacedEngine),
	}

	// Load existing databases from system namespace
	if err := m.loadMetadata(); err != nil {
		return nil, fmt.Errorf("failed to load database metadata: %w", err)
	}

	// Ensure system and default databases exist
	if err := m.ensureSystemDatabases(); err != nil {
		return nil, err
	}

	// Migrate existing unprefixed data to default database namespace
	// This handles backwards compatibility for databases created before multi-db support
	if err := m.migrateLegacyData(); err != nil {
		return nil, fmt.Errorf("failed to migrate legacy data: %w", err)
	}

	return m, nil
}

// ensureSystemDatabases creates system and default databases if they don't exist.
func (m *DatabaseManager) ensureSystemDatabases() error {
	// System database
	if _, exists := m.databases[m.config.SystemDatabase]; !exists {
		m.databases[m.config.SystemDatabase] = &DatabaseInfo{
			Name:      m.config.SystemDatabase,
			CreatedAt: time.Now(),
			Status:    "online",
			Type:      "system",
			IsDefault: false,
			UpdatedAt: time.Now(),
		}
	}

	// Default database
	if _, exists := m.databases[m.config.DefaultDatabase]; !exists {
		m.databases[m.config.DefaultDatabase] = &DatabaseInfo{
			Name:      m.config.DefaultDatabase,
			CreatedAt: time.Now(),
			Status:    "online",
			Type:      "standard",
			IsDefault: true,
			UpdatedAt: time.Now(),
		}
	}

	return m.persistMetadata()
}

// CreateDatabase creates a new database.
//
// Parameters:
//   - name: Database name (must be unique, lowercase recommended)
//
// Returns ErrDatabaseExists if database already exists.
// Returns ErrMaxDatabasesReached if limit exceeded.
func (m *DatabaseManager) CreateDatabase(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate name
	if name == "" {
		return ErrInvalidDatabaseName
	}

	// Check if exists
	if _, exists := m.databases[name]; exists {
		return ErrDatabaseExists
	}

	// Check limit
	if m.config.MaxDatabases > 0 && len(m.databases) >= m.config.MaxDatabases {
		return ErrMaxDatabasesReached
	}

	// Create metadata
	m.databases[name] = &DatabaseInfo{
		Name:      name,
		CreatedAt: time.Now(),
		Status:    "online",
		Type:      "standard",
		IsDefault: false,
		UpdatedAt: time.Now(),
	}

	return m.persistMetadata()
}

// DropDatabase removes a database and all its data.
//
// Parameters:
//   - name: Database name to drop
//
// Returns ErrDatabaseNotFound if database doesn't exist.
// Returns ErrCannotDropSystemDB for system/default databases.
func (m *DatabaseManager) DropDatabase(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if exists
	info, exists := m.databases[name]
	if !exists {
		return ErrDatabaseNotFound
	}

	// Prevent dropping system database
	if info.Type == "system" {
		return ErrCannotDropSystemDB
	}

	// Prevent dropping default (unless allowed)
	if info.IsDefault && !m.config.AllowDropDefault {
		return ErrCannotDropDefaultDB
	}

	// Delete all data with this namespace prefix
	prefix := name + ":"
	nodesDeleted, edgesDeleted, err := m.inner.DeleteByPrefix(prefix)
	if err != nil {
		return fmt.Errorf("failed to delete database data: %w", err)
	}

	// Update metadata with deletion info (for logging/debugging)
	_ = nodesDeleted
	_ = edgesDeleted

	// Remove from metadata
	delete(m.databases, name)
	delete(m.engines, name) // Clear cached engine

	return m.persistMetadata()
}

// GetStorage returns a namespaced storage engine for the specified database.
//
// The returned engine is scoped to the database - all operations only
// affect data within that namespace.
func (m *DatabaseManager) GetStorage(name string) (storage.Engine, error) {
	m.mu.RLock()

	// Check cache first
	if engine, exists := m.engines[name]; exists {
		m.mu.RUnlock()
		return engine, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if engine, exists := m.engines[name]; exists {
		return engine, nil
	}

	// Validate database exists
	info, exists := m.databases[name]
	if !exists {
		return nil, ErrDatabaseNotFound
	}

	if info.Status != "online" {
		return nil, ErrDatabaseOffline
	}

	// Create namespaced engine
	engine := storage.NewNamespacedEngine(m.inner, name)
	m.engines[name] = engine

	return engine, nil
}

// GetDefaultStorage returns storage for the default database.
func (m *DatabaseManager) GetDefaultStorage() (storage.Engine, error) {
	return m.GetStorage(m.config.DefaultDatabase)
}

// ListDatabases returns all database info.
func (m *DatabaseManager) ListDatabases() []*DatabaseInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*DatabaseInfo, 0, len(m.databases))
	for _, info := range m.databases {
		// Return a copy
		infoCopy := *info
		result = append(result, &infoCopy)
	}
	return result
}

// GetDatabase returns info for a specific database.
func (m *DatabaseManager) GetDatabase(name string) (*DatabaseInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, exists := m.databases[name]
	if !exists {
		return nil, ErrDatabaseNotFound
	}

	infoCopy := *info
	return &infoCopy, nil
}

// Exists checks if a database exists.
func (m *DatabaseManager) Exists(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.databases[name] != nil
}

// DefaultDatabaseName returns the default database name.
func (m *DatabaseManager) DefaultDatabaseName() string {
	return m.config.DefaultDatabase
}

// SetDatabaseStatus sets a database online/offline.
func (m *DatabaseManager) SetDatabaseStatus(name, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, exists := m.databases[name]
	if !exists {
		return ErrDatabaseNotFound
	}

	if status != "online" && status != "offline" {
		return fmt.Errorf("invalid status: %s (must be 'online' or 'offline')", status)
	}

	info.Status = status
	info.UpdatedAt = time.Now()

	// Clear cached engine if going offline
	if status == "offline" {
		delete(m.engines, name)
	}

	return m.persistMetadata()
}

// Close releases resources.
func (m *DatabaseManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear all cached engines
	m.engines = make(map[string]*storage.NamespacedEngine)

	// Close the underlying storage
	return m.inner.Close()
}

