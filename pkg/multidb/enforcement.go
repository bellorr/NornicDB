// Package multidb provides limit enforcement for multi-database support.
package multidb

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LimitChecker provides an interface for checking resource limits.
// This allows storage engines to check limits without depending on DatabaseManager.
// It implements both storage.LimitChecker and storage.QueryLimitChecker.
type LimitChecker interface {
	// CheckStorageLimits checks if storage operations are within limits.
	// Returns error if limit would be exceeded.
	CheckStorageLimits(operation string) error // "create_node", "create_edge"

	// CheckQueryLimits checks if query execution is allowed.
	// Returns error if limit would be exceeded.
	CheckQueryLimits(ctx context.Context) (context.Context, context.CancelFunc, error)

	// GetQueryLimits returns the query limits for this database.
	GetQueryLimits() *QueryLimits

	// GetRateLimits returns the rate limits for this database.
	GetRateLimits() *RateLimits

	// CheckQueryRate checks if query rate limit is allowed.
	CheckQueryRate() error

	// CheckWriteRate checks if write rate limit is allowed.
	CheckWriteRate() error
}

// databaseLimitChecker implements LimitChecker for a specific database.
type databaseLimitChecker struct {
	manager      *DatabaseManager
	databaseName string
	limits       *Limits

	// Query tracking
	activeQueries   int
	activeQueriesMu sync.Mutex

	// Rate limiting
	queryRateLimiter *rateLimiter
	writeRateLimiter *rateLimiter
}

// newDatabaseLimitChecker creates a limit checker for a specific database.
func newDatabaseLimitChecker(manager *DatabaseManager, databaseName string) (*databaseLimitChecker, error) {
	manager.mu.RLock()
	info, exists := manager.databases[databaseName]
	manager.mu.RUnlock()

	if !exists {
		return nil, ErrDatabaseNotFound
	}

	checker := &databaseLimitChecker{
		manager:      manager,
		databaseName: databaseName,
		limits:       info.Limits,
	}

	// Initialize rate limiters if limits are set
	if info.Limits != nil {
		if info.Limits.Rate.MaxQueriesPerSecond > 0 {
			checker.queryRateLimiter = newRateLimiter(info.Limits.Rate.MaxQueriesPerSecond)
		}
		if info.Limits.Rate.MaxWritesPerSecond > 0 {
			checker.writeRateLimiter = newRateLimiter(info.Limits.Rate.MaxWritesPerSecond)
		}
	}

	return checker, nil
}

// CheckStorageLimits checks if storage operations are within limits.
func (c *databaseLimitChecker) CheckStorageLimits(operation string) error {
	if c.limits == nil || c.limits.IsUnlimited() {
		return nil // No limits
	}

	// Get current usage
	storage, err := c.manager.GetStorage(c.databaseName)
	if err != nil {
		return err
	}

	nodeCount, err := storage.NodeCount()
	if err != nil {
		return fmt.Errorf("failed to get node count: %w", err)
	}

	edgeCount, err := storage.EdgeCount()
	if err != nil {
		return fmt.Errorf("failed to get edge count: %w", err)
	}

	// Check limits
	storageLimits := c.limits.Storage

	if operation == "create_node" {
		if storageLimits.MaxNodes > 0 && nodeCount >= storageLimits.MaxNodes {
			return fmt.Errorf("%w: database '%s' has reached max_nodes limit (%d/%d)",
				ErrStorageLimitExceeded, c.databaseName, nodeCount, storageLimits.MaxNodes)
		}
	}

	if operation == "create_edge" {
		if storageLimits.MaxEdges > 0 && edgeCount >= storageLimits.MaxEdges {
			return fmt.Errorf("%w: database '%s' has reached max_edges limit (%d/%d)",
				ErrStorageLimitExceeded, c.databaseName, edgeCount, storageLimits.MaxEdges)
		}
	}

	// TODO: MaxBytes check would require estimating node/edge size
	// This is complex and may not be worth the overhead
	// For now, we skip MaxBytes enforcement

	return nil
}

// CheckQueryLimits checks if query execution is allowed and returns a context with timeout.
func (c *databaseLimitChecker) CheckQueryLimits(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if c.limits == nil || c.limits.IsUnlimited() {
		return ctx, func() {}, nil // No limits
	}

	queryLimits := c.limits.Query

	// Check concurrent query limit
	if queryLimits.MaxConcurrentQueries > 0 {
		c.activeQueriesMu.Lock()
		if c.activeQueries >= queryLimits.MaxConcurrentQueries {
			c.activeQueriesMu.Unlock()
			return nil, nil, fmt.Errorf("%w: database '%s' has reached max_concurrent_queries limit (%d/%d)",
				ErrQueryLimitExceeded, c.databaseName, c.activeQueries, queryLimits.MaxConcurrentQueries)
		}
		c.activeQueries++
		c.activeQueriesMu.Unlock()
	}

	// Create context with timeout if limit is set
	var cancel context.CancelFunc
	if queryLimits.MaxQueryTime > 0 {
		ctx, cancel = context.WithTimeout(ctx, queryLimits.MaxQueryTime)
	} else {
		cancel = func() {}
	}

	return ctx, func() {
		cancel()
		// Decrement active queries when done
		if queryLimits.MaxConcurrentQueries > 0 {
			c.activeQueriesMu.Lock()
			c.activeQueries--
			c.activeQueriesMu.Unlock()
		}
	}, nil
}

// GetQueryLimits returns the query limits for this database.
// Implements storage.QueryLimitChecker interface.
func (c *databaseLimitChecker) GetQueryLimits() interface{} {
	if c.limits == nil {
		return nil
	}
	return &c.limits.Query
}

// GetRateLimits returns the rate limits for this database.
func (c *databaseLimitChecker) GetRateLimits() *RateLimits {
	if c.limits == nil {
		return nil
	}
	return &c.limits.Rate
}

// CheckQueryRate checks if query rate limit is allowed.
func (c *databaseLimitChecker) CheckQueryRate() error {
	if c.queryRateLimiter == nil {
		return nil // No limit
	}
	if !c.queryRateLimiter.Allow() {
		return fmt.Errorf("%w: database '%s' exceeded max_queries_per_second (%d)",
			ErrRateLimitExceeded, c.databaseName, c.limits.Rate.MaxQueriesPerSecond)
	}
	return nil
}

// CheckWriteRate checks if write rate limit is allowed.
func (c *databaseLimitChecker) CheckWriteRate() error {
	if c.writeRateLimiter == nil {
		return nil // No limit
	}
	if !c.writeRateLimiter.Allow() {
		return fmt.Errorf("%w: database '%s' exceeded max_writes_per_second (%d)",
			ErrRateLimitExceeded, c.databaseName, c.limits.Rate.MaxWritesPerSecond)
	}
	return nil
}

// rateLimiter implements a simple token bucket rate limiter.
type rateLimiter struct {
	rate      int // Requests per second
	lastCheck time.Time
	tokens    int
	mu        sync.Mutex
}

// newRateLimiter creates a new rate limiter.
func newRateLimiter(rate int) *rateLimiter {
	return &rateLimiter{
		rate:      rate,
		lastCheck: time.Now(),
		tokens:    rate, // Start with full bucket
	}
}

// Allow checks if a request is allowed under the rate limit.
func (r *rateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(r.lastCheck)

	// Add tokens based on elapsed time (1 token per second)
	if elapsed > 0 {
		tokensToAdd := int(elapsed.Seconds() * float64(r.rate))
		if tokensToAdd > 0 {
			r.tokens = min(r.tokens+tokensToAdd, r.rate) // Cap at rate
			r.lastCheck = now
		}
	}

	// Check if we have tokens
	if r.tokens > 0 {
		r.tokens--
		return true
	}

	return false
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ConnectionTracker tracks active connections per database.
type ConnectionTracker struct {
	connections map[string]int // database -> connection count
	mu          sync.RWMutex
}

// NewConnectionTracker creates a new connection tracker.
func NewConnectionTracker() *ConnectionTracker {
	return &ConnectionTracker{
		connections: make(map[string]int),
	}
}

// CheckConnectionLimit checks if a new connection is allowed.
func (t *ConnectionTracker) CheckConnectionLimit(manager *DatabaseManager, databaseName string) error {
	manager.mu.RLock()
	info, exists := manager.databases[databaseName]
	manager.mu.RUnlock()

	if !exists {
		return ErrDatabaseNotFound
	}

	if info.Limits == nil || info.Limits.Connection.MaxConnections == 0 {
		return nil // No limit
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	current := t.connections[databaseName]
	if current >= info.Limits.Connection.MaxConnections {
		return fmt.Errorf("%w: database '%s' has reached max_connections limit (%d/%d)",
			ErrConnectionLimitExceeded, databaseName, current, info.Limits.Connection.MaxConnections)
	}

	return nil
}

// IncrementConnection increments the connection count for a database.
func (t *ConnectionTracker) IncrementConnection(databaseName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connections[databaseName]++
}

// DecrementConnection decrements the connection count for a database.
func (t *ConnectionTracker) DecrementConnection(databaseName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.connections[databaseName] > 0 {
		t.connections[databaseName]--
	}
}

// GetConnectionCount returns the current connection count for a database.
func (t *ConnectionTracker) GetConnectionCount(databaseName string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connections[databaseName]
}
