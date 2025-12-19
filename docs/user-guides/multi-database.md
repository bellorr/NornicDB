# Multi-Database Support

NornicDB supports multiple isolated databases (multi-tenancy) within a single storage backend, similar to Neo4j 4.x.

## Overview

Multi-database support enables:
- **Complete data isolation** between databases
- **Multi-tenancy** - each database acts as a separate tenant
- **Neo4j 4.x compatibility** - works with existing Neo4j drivers and tools
- **Shared storage backend** - efficient resource usage

## Default Database

By default, NornicDB uses **`"nornic"`** as the default database name (Neo4j uses `"neo4j"`).

This is configurable:

**Config File:**
```yaml
database:
  default_database: "custom"
```

**Environment Variable:**
```bash
export NORNICDB_DEFAULT_DATABASE=custom
# Or Neo4j-compatible:
export NEO4J_dbms_default__database=custom
```

**Configuration Precedence:**
1. CLI arguments (highest priority)
2. Environment variables
3. Config file
4. Built-in defaults (`"nornic"`)

## Using Multiple Databases

### Creating Databases

```cypher
CREATE DATABASE tenant_a
CREATE DATABASE tenant_b
```

### Listing Databases

```cypher
SHOW DATABASES
```

### Dropping Databases

```cypher
DROP DATABASE tenant_a
```

### Switching Databases

**In Cypher Shell:**
```cypher
:USE tenant_a
```

**In Drivers:**
```python
# Python
driver = GraphDatabase.driver(
    "bolt://localhost:7687",
    database="tenant_a"
)

# JavaScript
const driver = neo4j.driver(
    "bolt://localhost:7687",
    neo4j.auth.basic("neo4j", "password"),
    { database: "tenant_a" }
)
```

**HTTP API:**
```
POST /db/tenant_a/tx/commit
```

## Data Isolation

Each database is completely isolated:

```cypher
// In tenant_a
CREATE (n:Person {name: "Alice"})

// In tenant_b
CREATE (n:Person {name: "Bob"})

// tenant_a only sees Alice
// tenant_b only sees Bob
```

## System Database

The `"system"` database stores metadata about all databases. It is:
- Automatically created
- Not accessible to users (internal use only)
- Cannot be dropped

## Automatic Migration

When you upgrade to NornicDB with multi-database support, **existing data is automatically migrated** to the default database namespace on first startup.

**What happens:**
- On first startup, NornicDB detects any data without namespace prefixes
- All unprefixed nodes and edges are automatically migrated to the default database (`"nornic"` by default)
- All indexes are automatically updated
- Migration status is saved - it only runs once
- Your existing data remains fully accessible through the default database

**No action required** - migration happens automatically and transparently.

**Example:**
```cypher
// Before upgrade: data stored as "node-123"
// After upgrade: automatically becomes "nornic:node-123"
// You access it the same way - no changes needed!
MATCH (n) RETURN n
```

## Backwards Compatibility

✅ **Fully backwards compatible:**
- Existing code without database parameter works with default database
- All existing data automatically migrated and accessible in default database
- No breaking changes to existing APIs
- No manual migration steps required

## Configuration Examples

### Default Configuration
```yaml
database:
  default_database: "nornic"  # Default
```

### Custom Default Database
```yaml
database:
  default_database: "main"
```

### Environment Variable Override
```bash
export NORNICDB_DEFAULT_DATABASE=production
./nornicdb serve
```

## Limitations (v1)

- ❌ Cross-database queries (not supported)
- ❌ Database aliases (future)
- ❌ Per-database resource limits (future)

## See Also

- [Configuration Guide](../operations/configuration.md) - Configuration options for multi-database

