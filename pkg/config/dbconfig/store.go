// Package dbconfig provides per-database configuration override storage in the system database.
//
// Overrides are stored as _DbConfig nodes (one per database). Global config remains the default;
// per-DB overrides are merged at resolution time. See the per-database configuration overrides plan.
package dbconfig

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/orneryd/nornicdb/pkg/storage"
)

const (
	dbConfigLabel   = "_DbConfig"
	dbConfigPrefix  = "db_config:"
	dbConfigSystems = "_System"
)

// Store loads and saves per-database config overrides in the system database.
// Same pattern as auth.AllowlistStore and auth.PrivilegesStore.
type Store struct {
	storage storage.Engine
	mu      sync.RWMutex
	// dbName -> overrides (key = env-style name, value = string)
	overrides map[string]map[string]string
}

// NewStore creates a store that reads/writes per-DB overrides to the given system storage.
func NewStore(systemStorage storage.Engine) *Store {
	return &Store{storage: systemStorage, overrides: make(map[string]map[string]string)}
}

// Load reads all _DbConfig nodes from storage into memory. Call at startup and after PUT.
func (s *Store) Load(ctx context.Context) error {
	m := make(map[string]map[string]string)
	err := storage.StreamNodesWithFallback(ctx, s.storage, 1000, func(n *storage.Node) error {
		hasLabel := false
		for _, l := range n.Labels {
			if l == dbConfigLabel {
				hasLabel = true
				break
			}
		}
		if !hasLabel {
			return nil
		}
		dbName := dbNameFromNodeID(string(n.ID))
		if dbName == "" {
			return nil
		}
		overrides := overridesFromProperties(n.Properties)
		m[dbName] = overrides
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.overrides = m
	s.mu.Unlock()
	return nil
}

// GetOverrides returns a copy of the overrides for the given database. Nil or empty = no overrides.
func (s *Store) GetOverrides(dbName string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o := s.overrides[dbName]
	if o == nil {
		return nil
	}
	out := make(map[string]string, len(o))
	for k, v := range o {
		out[k] = v
	}
	return out
}

// SetOverrides persists overrides for the given database and refreshes in-memory cache.
// Pass nil or empty map to clear all overrides for that database.
func (s *Store) SetOverrides(ctx context.Context, dbName string, overrides map[string]string) error {
	dbName = strings.TrimSpace(dbName)
	if dbName == "" {
		return nil
	}
	nodeID := storage.NodeID(dbConfigPrefix + dbName)
	if len(overrides) == 0 {
		if err := s.storage.DeleteNode(nodeID); err != nil && err != storage.ErrNotFound {
			return err
		}
		s.mu.Lock()
		delete(s.overrides, dbName)
		s.mu.Unlock()
		return nil
	}
	overridesJSON, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	node := &storage.Node{
		ID:     nodeID,
		Labels: []string{dbConfigLabel, dbConfigSystems},
		Properties: map[string]any{
			"database":  dbName,
			"overrides": string(overridesJSON),
		},
	}
	existing, err := s.storage.GetNode(nodeID)
	if err == storage.ErrNotFound {
		_, err = s.storage.CreateNode(node)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		node.CreatedAt = existing.CreatedAt
		err = s.storage.UpdateNode(node)
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	if s.overrides == nil {
		s.overrides = make(map[string]map[string]string)
	}
	s.overrides[dbName] = overrides
	s.mu.Unlock()
	return nil
}

func dbNameFromNodeID(id string) string {
	if strings.HasPrefix(id, dbConfigPrefix) {
		return id[len(dbConfigPrefix):]
	}
	return ""
}

func overridesFromProperties(p map[string]any) map[string]string {
	if p == nil {
		return nil
	}
	s, ok := p["overrides"].(string)
	if !ok {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
