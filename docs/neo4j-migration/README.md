# Neo4j Migration

**Migrate from Neo4j to NornicDB with 96% feature parity.**

## 📚 Documentation

- **[Feature Parity](feature-parity.md)** - 96% Neo4j compatibility
- **[Cypher Compatibility](cypher-compatibility.md)** - Cypher language support
- **[Migration Guide](migration-guide.md)** - Step-by-step migration
- **[Driver Compatibility](driver-compatibility.md)** - Client driver support

## 🎯 Why Migrate?

### NornicDB Advantages
- **GPU Acceleration** - 10-100x faster vector search
- **Automatic Embeddings** - Built-in semantic search
- **Memory Decay** - Time-based importance
- **Link Prediction** - ML-based relationship inference
- **Lower Cost** - No licensing fees

### Neo4j Compatibility
- **96% Feature Parity** - Most features work identically
- **Bolt Protocol** - Use existing drivers
- **Cypher Language** - Same query language
- **JSON Export/Import** - Easy data migration

## 🚀 Quick Migration

### 1. Export from Neo4j

```bash
# Export Neo4j data
neo4j-admin dump --database=neo4j --to=neo4j-dump.dump
```

### 2. Import to NornicDB

```bash
# Import to NornicDB
nornicdb import --from=neo4j-dump.dump
```

### 3. Update Connection Strings

```python
# Before (Neo4j)
driver = GraphDatabase.driver("bolt://neo4j-server:7687")

# After (NornicDB)
driver = GraphDatabase.driver("bolt://nornicdb-server:7687")
```

[Complete migration guide →](migration-guide.md)

## 📊 Feature Comparison

| Feature | Neo4j | NornicDB |
|---------|-------|----------|
| Cypher Queries | ✅ | ✅ 96% |
| Bolt Protocol | ✅ | ✅ |
| ACID Transactions | ✅ | ✅ |
| Indexes | ✅ | ✅ |
| Vector Search | ❌ | ✅ |
| GPU Acceleration | ❌ | ✅ |
| Auto Embeddings | ❌ | ✅ |
| Memory Decay | ❌ | ✅ |

[Complete comparison →](feature-parity.md)

## 📖 Learn More

- **[Feature Parity](feature-parity.md)** - Detailed comparison
- **[Migration Guide](migration-guide.md)** - Step-by-step process
- **[Cypher Compatibility](cypher-compatibility.md)** - Language support

---

**Start migrating** → **[Migration Guide](migration-guide.md)**
