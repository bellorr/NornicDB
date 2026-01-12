package server

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/orneryd/nornicdb/pkg/auth"
)

type basicAuthCache struct {
	mu         sync.Mutex
	entries    map[[32]byte]basicAuthCacheEntry
	maxEntries int
	ttl        time.Duration
}

type basicAuthCacheEntry struct {
	claims    *auth.JWTClaims
	expiresAt time.Time
}

func newBasicAuthCache(maxEntries int, ttl time.Duration) *basicAuthCache {
	if maxEntries <= 0 || ttl <= 0 {
		return nil
	}
	return &basicAuthCache{
		entries:    make(map[[32]byte]basicAuthCacheEntry, maxEntries),
		maxEntries: maxEntries,
		ttl:        ttl,
	}
}

func basicAuthCacheKey(authHeader string) [32]byte {
	return sha256.Sum256([]byte(authHeader))
}

func (c *basicAuthCache) get(key [32]byte) (*auth.JWTClaims, bool) {
	if c == nil {
		return nil, false
	}
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if now.After(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return cloneJWTClaims(entry.claims), true
}

func (c *basicAuthCache) set(key [32]byte, claims *auth.JWTClaims) {
	if c == nil || claims == nil {
		return
	}

	now := time.Now()
	expiresAt := now.Add(c.ttl)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Opportunistic cleanup of expired entries.
	for k, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, k)
		}
	}

	// Bound the map size with simple eviction.
	for len(c.entries) >= c.maxEntries {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}

	c.entries[key] = basicAuthCacheEntry{
		claims:    cloneJWTClaims(claims),
		expiresAt: expiresAt,
	}
}

func cloneJWTClaims(in *auth.JWTClaims) *auth.JWTClaims {
	if in == nil {
		return nil
	}
	out := *in
	if in.Roles != nil {
		out.Roles = append([]string(nil), in.Roles...)
	}
	return &out
}

