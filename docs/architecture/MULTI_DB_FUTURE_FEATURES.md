# Multi-Database Future Features Plan

**Status:** 📋 Planning  
**Last Updated:** 2024-12-04

This document outlines the implementation plan for advanced multi-database features. **Note:** Database Aliases and Per-Database Resource Limits are now **✅ IMPLEMENTED** - see [Multi-Database User Guide](../user-guides/multi-database.md) for usage.

---

## Table of Contents

1. [Cross-Database Queries](#cross-database-queries)
2. [Database Aliases](#database-aliases) - ✅ **IMPLEMENTED**
3. [Per-Database Resource Limits](#per-database-resource-limits) - ✅ **IMPLEMENTED**

---

## Cross-Database Queries

### Overview

Enable Cypher queries that can access and join data from multiple databases within a single query. This allows users to query across tenant boundaries for analytics, reporting, or administrative purposes.

### Use Cases

- **Analytics**: Aggregate data across all tenants
- **Administration**: Find patterns across databases
- **Reporting**: Generate cross-tenant reports
- **Data Migration**: Copy data between databases

### Design Approach

#### Option 1: Explicit Database References (Recommended)

Allow explicit database references in Cypher queries:

```cypher
// Query across multiple databases
MATCH (n:Person) FROM DATABASE tenant_a
MATCH (m:Person) FROM DATABASE tenant_b
WHERE n.name = m.name
RETURN n, m

// Join across databases
MATCH (a:Customer) FROM DATABASE tenant_a
MATCH (b:Order) FROM DATABASE tenant_b
WHERE a.id = b.customer_id
RETURN a, b
```

**Advantages:**
- Explicit and clear
- Easy to understand
- Good security model (explicit access)
- Neo4j-compatible syntax

**Disadvantages:**
- More verbose syntax
- Requires query parser changes

#### Option 2: Database Variables

Use variables to reference databases:

```cypher
USE DATABASE tenant_a AS db1, tenant_b AS db2
MATCH (n:Person) IN db1
MATCH (m:Person) IN db2
WHERE n.name = m.name
RETURN n, m
```

**Advantages:**
- Cleaner syntax for multiple databases
- Reusable database references

**Disadvantages:**
- More complex parser
- Less intuitive

### Implementation Strategy

#### Phase 1: Query Parser Extensions

**File:** `pkg/cypher/parser.go`

1. **Extend AST** to support database references:
   ```go
   type DatabaseRef struct {
       Name      string
       Alias     string  // Optional alias
   }
   
   type MatchClause struct {
       Database *DatabaseRef  // Optional database reference
       Pattern  Pattern
       Where    *WhereClause
   }
   ```

2. **Parse database references**:
   - `FROM DATABASE name` clause
   - `IN DATABASE name` clause
   - Support in MATCH, CREATE, MERGE, DELETE, SET

#### Phase 2: Multi-Database Executor

**File:** `pkg/cypher/multi_db_executor.go` (new)

1. **Cross-Database Query Planner**:
   ```go
   type CrossDatabaseExecutor struct {
       dbManager *multidb.DatabaseManager
       executors map[string]*StorageExecutor  // Per-database executors
   }
   
   func (e *CrossDatabaseExecutor) ExecuteCrossDB(
       ctx context.Context,
       query *Query,
   ) (*ExecuteResult, error) {
       // 1. Identify all databases referenced in query
       databases := e.extractDatabaseRefs(query)
       
       // 2. Execute sub-queries per database
       results := make(map[string]*ExecuteResult)
       for dbName, subQuery := range e.splitByDatabase(query) {
           executor := e.getExecutor(dbName)
           results[dbName], _ = executor.Execute(ctx, subQuery, params)
       }
       
       // 3. Merge results (join, union, etc.)
       return e.mergeResults(results, query)
   }
   ```

2. **Result Merging**:
   - **UNION**: Combine results from multiple databases
   - **JOIN**: Match nodes/edges across databases by properties
   - **AGGREGATION**: Aggregate across databases (COUNT, SUM, etc.)

#### Phase 3: Security & Access Control

1. **Database Access Permissions**:
   ```go
   type DatabasePermission struct {
       Database string
       User     string
       Access   []string  // ["read", "write", "cross_db"]
   }
   ```

2. **Query Validation**:
   - Check user has access to all referenced databases
   - Enforce read-only for cross-database queries (optional)
   - Log cross-database queries for audit

#### Phase 4: Performance Optimization

1. **Parallel Execution**:
   - Execute queries against multiple databases in parallel
   - Use goroutines for concurrent database access
   - Merge results efficiently

2. **Caching**:
   - Cache cross-database query results
   - Invalidate on database updates

3. **Query Optimization**:
   - Push filters down to individual databases
   - Minimize data transfer between databases
   - Use indexes from each database

### Example Implementation

```go
// pkg/cypher/multi_db_executor.go

type CrossDatabaseExecutor struct {
    dbManager *multidb.DatabaseManager
    executors sync.Map  // map[string]*StorageExecutor
}

func (e *CrossDatabaseExecutor) ExecuteCrossDB(
    ctx context.Context,
    query string,
    params map[string]interface{},
) (*ExecuteResult, error) {
    // Parse query to extract database references
    ast, err := e.parseQuery(query)
    if err != nil {
        return nil, err
    }
    
    // Get all referenced databases
    databases := e.extractDatabases(ast)
    
    // Validate access
    if err := e.validateAccess(ctx, databases); err != nil {
        return nil, err
    }
    
    // Execute in parallel
    var wg sync.WaitGroup
    results := make(map[string]*ExecuteResult)
    errors := make(map[string]error)
    
    for _, dbName := range databases {
        wg.Add(1)
        go func(db string) {
            defer wg.Done()
            executor := e.getExecutor(db)
            subQuery := e.extractSubQuery(ast, db)
            result, err := executor.Execute(ctx, subQuery, params)
            if err != nil {
                errors[db] = err
            } else {
                results[db] = result
            }
        }(dbName)
    }
    
    wg.Wait()
    
    // Check for errors
    if len(errors) > 0 {
        return nil, fmt.Errorf("cross-database query failed: %v", errors)
    }
    
    // Merge results
    return e.mergeResults(results, ast)
}
```

### Testing Strategy

1. **Unit Tests**:
   - Query parsing with database references
   - Result merging (union, join, aggregation)
   - Access control validation

2. **Integration Tests**:
   - Cross-database MATCH queries
   - Cross-database CREATE (if allowed)
   - Performance with multiple databases

3. **E2E Tests**:
   - Real-world cross-database analytics queries
   - Large-scale data across databases

### Security Considerations

- **Access Control**: Users must have explicit permission for cross-database queries
- **Audit Logging**: Log all cross-database queries
- **Read-Only Mode**: Option to restrict cross-database queries to read-only
- **Rate Limiting**: Prevent abuse of cross-database queries

### Performance Considerations

- **Parallel Execution**: Critical for performance
- **Result Size Limits**: Prevent memory exhaustion
- **Query Timeout**: Set reasonable timeouts for cross-database queries
- **Index Usage**: Leverage indexes from each database

### Estimated Effort

- **Parser Extensions**: 2-3 days
- **Multi-Database Executor**: 5-7 days
- **Security & Access Control**: 2-3 days
- **Testing**: 3-4 days
- **Total**: ~12-17 days

---

## Database Aliases

**Status:** ✅ **IMPLEMENTED** (v1.2)  
**See:** [Multi-Database User Guide - Database Aliases](../user-guides/multi-database.md#database-aliases)

### Overview

Allow creating alternate names (aliases) for databases, enabling easier database management and migration scenarios.

**✅ This feature is now fully implemented and available for use.**

### Use Cases

- **Database Renaming**: Create alias while migrating to new name
- **Environment Mapping**: `prod` → `production_v2`
- **Version Management**: `current` → `v1.2.3`
- **Simplified Access**: `main` → `tenant_primary_2024`

### Design Approach

#### Alias Storage

Store aliases in the system database metadata:

```go
type DatabaseInfo struct {
    Name        string
    Aliases     []string  // NEW: List of aliases
    // ... existing fields
}
```

#### Alias Resolution

Resolve aliases to actual database names at query time:

```go
func (m *DatabaseManager) ResolveDatabase(nameOrAlias string) (string, error) {
    // Check if it's an actual database name
    if m.Exists(nameOrAlias) {
        return nameOrAlias, nil
    }
    
    // Check if it's an alias
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    for dbName, info := range m.databases {
        for _, alias := range info.Aliases {
            if alias == nameOrAlias {
                return dbName, nil
            }
        }
    }
    
    return "", ErrDatabaseNotFound
}
```

### Implementation Strategy

#### Phase 1: Alias Storage

**File:** `pkg/multidb/manager.go`

1. **Add Aliases to DatabaseInfo**:
   ```go
   type DatabaseInfo struct {
       Name        string
       Aliases     []string  // NEW
       // ... existing fields
   }
   ```

2. **Update Metadata Persistence**:
   - Include aliases in JSON serialization
   - Load aliases on startup

#### Phase 2: Alias Management Commands

**File:** `pkg/cypher/executor.go`

1. **CREATE ALIAS**:
   ```cypher
   CREATE ALIAS main FOR DATABASE tenant_primary_2024
   ```

2. **DROP ALIAS**:
   ```cypher
   DROP ALIAS main
   ```

3. **SHOW ALIASES**:
   ```cypher
   SHOW ALIASES
   SHOW ALIASES FOR DATABASE tenant_a
   ```

#### Phase 3: Alias Resolution

**Files:** `pkg/multidb/manager.go`, `pkg/server/server.go`, `pkg/bolt/server.go`

1. **Update GetStorage()**:
   ```go
   func (m *DatabaseManager) GetStorage(nameOrAlias string) (storage.Engine, error) {
       // Resolve alias first
       actualName, err := m.ResolveDatabase(nameOrAlias)
       if err != nil {
           return nil, err
       }
       
       // Use existing logic with actual name
       return m.getStorageInternal(actualName)
   }
   ```

2. **Update All Routing**:
   - HTTP API: `/db/{database}/tx/commit` - resolve alias
   - Bolt Protocol: HELLO message `db` parameter - resolve alias
   - Cypher: `:USE database` - resolve alias

#### Phase 4: Validation & Constraints

1. **Alias Validation**:
   - Alias must be unique across all databases
   - Alias cannot conflict with existing database names
   - Alias cannot be reserved names (`system`, `nornic`)

2. **Circular Reference Prevention**:
   - Alias cannot point to another alias (only direct database names)
   - Validate on alias creation

### Example Implementation

```go
// pkg/multidb/manager.go

// CreateAlias creates an alias for a database.
func (m *DatabaseManager) CreateAlias(alias, databaseName string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    // Validate database exists
    info, exists := m.databases[databaseName]
    if !exists {
        return ErrDatabaseNotFound
    }
    
    // Validate alias doesn't exist
    if m.Exists(alias) {
        return fmt.Errorf("alias '%s' conflicts with existing database", alias)
    }
    
    // Check if alias is already used
    for _, dbInfo := range m.databases {
        for _, existingAlias := range dbInfo.Aliases {
            if existingAlias == alias {
                return fmt.Errorf("alias '%s' already exists", alias)
            }
        }
    }
    
    // Validate alias name
    if err := m.validateAliasName(alias); err != nil {
        return err
    }
    
    // Add alias
    info.Aliases = append(info.Aliases, alias)
    info.UpdatedAt = time.Now()
    
    return m.persistMetadata()
}

// ResolveDatabase resolves an alias or database name to the actual database name.
func (m *DatabaseManager) ResolveDatabase(nameOrAlias string) (string, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    // Check if it's an actual database name
    if _, exists := m.databases[nameOrAlias]; exists {
        return nameOrAlias, nil
    }
    
    // Check if it's an alias
    for dbName, info := range m.databases {
        for _, alias := range info.Aliases {
            if alias == nameOrAlias {
                return dbName, nil
            }
        }
    }
    
    return "", ErrDatabaseNotFound
}
```

### Testing Strategy

1. **Unit Tests**:
   - Alias creation and deletion
   - Alias resolution
   - Alias validation (conflicts, reserved names)
   - Circular reference prevention

2. **Integration Tests**:
   - Using aliases in Cypher queries
   - Using aliases in Bolt protocol
   - Using aliases in HTTP API
   - Alias persistence across restarts

### Security Considerations

- **Access Control**: Aliases inherit permissions from the target database
- **Audit Logging**: Log alias creation/deletion
- **Reserved Names**: Prevent aliasing system databases

### Estimated Effort

- **Alias Storage**: 1 day
- **Alias Management Commands**: 2 days
- **Alias Resolution**: 2 days
- **Testing**: 2 days
- **Total**: ~7 days

---

## Per-Database Resource Limits

**Status:** ✅ **IMPLEMENTED** (v1.2)  
**See:** [Multi-Database User Guide - Resource Limits](../user-guides/multi-database.md#per-database-resource-limits)

### Overview

Enable administrators to set resource limits per database, preventing any single database from consuming excessive resources.

**✅ This feature is now fully implemented and available for use.**

### Use Cases

- **Fair Resource Allocation**: Ensure no tenant monopolizes resources
- **Cost Control**: Limit storage per tenant
- **Performance Protection**: Prevent slow queries from affecting other databases
- **Compliance**: Enforce data retention limits

### Resource Types to Limit

1. **Storage Limits**:
   - Max nodes per database
   - Max edges per database
   - Max storage size (bytes)

2. **Query Limits**:
   - Max query execution time
   - Max results returned
   - Max concurrent queries

3. **Connection Limits**:
   - Max concurrent connections
   - Max connections per user

4. **Rate Limits**:
   - Max queries per second
   - Max writes per second

### Design Approach

#### Limit Configuration

```go
type DatabaseLimits struct {
    // Storage limits
    MaxNodes      int64
    MaxEdges      int64
    MaxStorageBytes int64
    
    // Query limits
    MaxQueryTime     time.Duration
    MaxResults        int64
    MaxConcurrentQueries int
    
    // Connection limits
    MaxConnections   int
    
    // Rate limits
    MaxQueriesPerSecond int
    MaxWritesPerSecond  int
}

type DatabaseInfo struct {
    Name        string
    Limits      *DatabaseLimits  // NEW: Resource limits
    // ... existing fields
}
```

#### Limit Enforcement Points

1. **Storage Limits**: Enforce in `CreateNode()` / `CreateEdge()`
2. **Query Limits**: Enforce in query executor
3. **Connection Limits**: Enforce in Bolt/HTTP server
4. **Rate Limits**: Enforce in request handlers

### Implementation Strategy

#### Phase 1: Limit Configuration

**File:** `pkg/multidb/limits.go` (new)

1. **Define Limit Types**:
   ```go
   type DatabaseLimits struct {
       Storage    StorageLimits
       Query      QueryLimits
       Connection ConnectionLimits
       Rate       RateLimits
   }
   
   type StorageLimits struct {
       MaxNodes      int64
       MaxEdges      int64
       MaxBytes      int64
   }
   ```

2. **Default Limits**:
   ```go
   func DefaultLimits() *DatabaseLimits {
       return &DatabaseLimits{
           Storage: StorageLimits{
               MaxNodes: 0,  // 0 = unlimited
               MaxEdges: 0,
               MaxBytes: 0,
           },
           Query: QueryLimits{
               MaxQueryTime: 30 * time.Second,
               MaxResults: 10000,
               MaxConcurrentQueries: 10,
           },
           // ...
       }
   }
   ```

#### Phase 2: Limit Storage

**File:** `pkg/multidb/manager.go`

1. **Add Limits to DatabaseInfo**:
   ```go
   type DatabaseInfo struct {
       Name        string
       Limits      *DatabaseLimits
       // ... existing fields
   }
   ```

2. **Limit Management Commands**:
   ```cypher
   ALTER DATABASE tenant_a SET LIMIT max_nodes = 1000000
   ALTER DATABASE tenant_a SET LIMIT max_query_time = '60s'
   SHOW LIMITS FOR DATABASE tenant_a
   ```

#### Phase 3: Limit Enforcement

**Files:** `pkg/storage/namespaced.go`, `pkg/cypher/executor.go`, `pkg/server/server.go`

1. **Storage Enforcement**:
   ```go
   // In NamespacedEngine.CreateNode()
   func (n *NamespacedEngine) CreateNode(node *Node) error {
       // Check limits
       if err := n.checkStorageLimits(); err != nil {
           return err
       }
       
       return n.inner.CreateNode(node)
   }
   
   func (n *NamespacedEngine) checkStorageLimits() error {
       limits := n.getLimits()
       if limits == nil {
           return nil  // No limits
       }
       
       nodeCount, _ := n.NodeCount()
       if limits.MaxNodes > 0 && nodeCount >= limits.MaxNodes {
           return ErrStorageLimitExceeded
       }
       
       return nil
   }
   ```

2. **Query Enforcement**:
   ```go
   // In StorageExecutor.Execute()
   func (e *StorageExecutor) Execute(ctx context.Context, query string, params map[string]interface{}) (*ExecuteResult, error) {
       // Check query limits
       if err := e.checkQueryLimits(ctx); err != nil {
           return nil, err
       }
       
       // Execute with timeout
       ctx, cancel := context.WithTimeout(ctx, e.getMaxQueryTime())
       defer cancel()
       
       // ... execute query
   }
   ```

3. **Connection Enforcement**:
   ```go
   // In Bolt Server
   func (s *Server) handleHello(ctx context.Context, conn *Connection, msg map[string]interface{}) error {
       dbName := s.getDatabaseName(msg)
       
       // Check connection limit
       if err := s.checkConnectionLimit(dbName); err != nil {
           return err
       }
       
       // ... continue
   }
   ```

#### Phase 4: Limit Monitoring

1. **Usage Tracking**:
   ```go
   type DatabaseUsage struct {
       NodeCount      int64
       EdgeCount      int64
       StorageBytes   int64
       ActiveQueries  int
       ActiveConnections int
       QueriesPerSecond float64
   }
   
   func (m *DatabaseManager) GetUsage(dbName string) (*DatabaseUsage, error) {
       // Query current usage
   }
   ```

2. **Monitoring Endpoints**:
   ```
   GET /db/{database}/limits
   GET /db/{database}/usage
   ```

### Example Implementation

```go
// pkg/multidb/limits.go

type DatabaseLimits struct {
    Storage    StorageLimits
    Query      QueryLimits
    Connection ConnectionLimits
    Rate       RateLimits
}

type StorageLimits struct {
    MaxNodes int64
    MaxEdges int64
    MaxBytes int64
}

type QueryLimits struct {
    MaxQueryTime        time.Duration
    MaxResults          int64
    MaxConcurrentQueries int
}

// EnforceStorageLimits checks if storage operations are within limits.
func (m *DatabaseManager) EnforceStorageLimits(
    dbName string,
    operation string,  // "create_node", "create_edge"
) error {
    info, err := m.GetDatabase(dbName)
    if err != nil {
        return err
    }
    
    if info.Limits == nil {
        return nil  // No limits
    }
    
    // Get current usage
    usage, err := m.GetUsage(dbName)
    if err != nil {
        return err
    }
    
    // Check limits
    limits := info.Limits.Storage
    if limits.MaxNodes > 0 && operation == "create_node" {
        if usage.NodeCount >= limits.MaxNodes {
            return ErrStorageLimitExceeded
        }
    }
    
    if limits.MaxEdges > 0 && operation == "create_edge" {
        if usage.EdgeCount >= limits.MaxEdges {
            return ErrStorageLimitExceeded
        }
    }
    
    return nil
}
```

### Testing Strategy

1. **Unit Tests**:
   - Limit configuration and validation
   - Limit enforcement (storage, query, connection)
   - Usage tracking

2. **Integration Tests**:
   - Creating nodes/edges when at limit
   - Query timeout enforcement
   - Connection limit enforcement
   - Rate limiting

3. **E2E Tests**:
   - Real-world limit scenarios
   - Performance under limits

### Security Considerations

- **Admin Only**: Only administrators can set limits
- **Audit Logging**: Log all limit violations
- **Graceful Degradation**: Return clear error messages when limits exceeded

### Performance Considerations

- **Efficient Checking**: Cache usage statistics
- **Minimal Overhead**: Limit checks should be fast
- **Batch Operations**: Check limits once per batch

### Estimated Effort

- **Limit Configuration**: 2 days
- **Storage Enforcement**: 3 days
- **Query Enforcement**: 2 days
- **Connection Enforcement**: 2 days
- **Monitoring & Usage Tracking**: 3 days
- **Testing**: 3 days
- **Total**: ~15 days

---

## Implementation Priority

### Completed ✅

1. **Database Aliases** - ✅ **IMPLEMENTED** (v1.2)
   - Fully functional with CREATE/DROP/SHOW ALIAS commands
   - Alias resolution integrated into all routing points
   - Persisted in database metadata

2. **Per-Database Resource Limits** - ✅ **IMPLEMENTED** (v1.2)
   - Limit configuration and storage implemented
   - Limits persisted in database metadata
   - Note: Limit enforcement is implemented but may require additional work for full production use

### Remaining Work

3. **Cross-Database Queries** (12-17 days) - Complex, lower priority
   - Most complex feature
   - Security implications
   - Performance challenges
   - Lower immediate value

### Dependencies

- **Cross-Database Queries**: Can use aliases in queries (aliases are now available)

---

## Configuration Examples

### Database Aliases

```cypher
-- Create alias
CREATE ALIAS main FOR DATABASE tenant_primary_2024
CREATE ALIAS prod FOR DATABASE production_v2

-- Use alias
:USE main
MATCH (n) RETURN n

-- Drop alias
DROP ALIAS main

-- Show aliases
SHOW ALIASES
SHOW ALIASES FOR DATABASE tenant_primary_2024
```

### Resource Limits

```cypher
-- Set storage limits
ALTER DATABASE tenant_a SET LIMIT max_nodes = 1000000
ALTER DATABASE tenant_a SET LIMIT max_edges = 5000000
ALTER DATABASE tenant_a SET LIMIT max_storage_bytes = 10737418240  -- 10GB

-- Set query limits
ALTER DATABASE tenant_a SET LIMIT max_query_time = '60s'
ALTER DATABASE tenant_a SET LIMIT max_results = 10000
ALTER DATABASE tenant_a SET LIMIT max_concurrent_queries = 10

-- Set connection limits
ALTER DATABASE tenant_a SET LIMIT max_connections = 50

-- Set rate limits
ALTER DATABASE tenant_a SET LIMIT max_queries_per_second = 100
ALTER DATABASE tenant_a SET LIMIT max_writes_per_second = 50

-- Show limits
SHOW LIMITS FOR DATABASE tenant_a

-- Show usage
SHOW USAGE FOR DATABASE tenant_a
```

### Cross-Database Queries

```cypher
-- Query across databases
MATCH (n:Person) FROM DATABASE tenant_a
MATCH (m:Person) FROM DATABASE tenant_b
WHERE n.email = m.email
RETURN n, m

-- Aggregate across databases
MATCH (n:Order) FROM DATABASE tenant_a
MATCH (m:Order) FROM DATABASE tenant_b
RETURN count(n) + count(m) as total_orders

-- Join across databases
MATCH (c:Customer) FROM DATABASE tenant_a
MATCH (o:Order) FROM DATABASE tenant_b
WHERE c.id = o.customer_id
RETURN c, o
```

---

## See Also

- [Multi-Database User Guide](../user-guides/multi-database.md) - Current multi-database features
- [Multi-Database Implementation Spec](MULTI_DB_IMPLEMENTATION_SPEC.md) - Current implementation details

