# Performance

**Benchmarks, optimization guides, and performance tuning.**

## 📚 Documentation

- **[Benchmarks vs Neo4j](benchmarks-vs-neo4j.md)** - Performance comparison
- **[Test Results](test-results.md)** - Test suite results
- **[Optimization Guide](optimization-guide.md)** - Performance tuning
- **[GPU Acceleration](gpu-acceleration.md)** - GPU performance
- **[Query Optimization](query-optimization.md)** - Query tuning

## ⚡ Performance Highlights

### Vector Search
- **10-100x faster** with GPU acceleration
- **O(log N)** HNSW index lookups
- **Sub-millisecond** queries on 1M vectors

### Query Execution
- **Parallel execution** for independent operations
- **Query caching** with LRU eviction
- **Index-backed** property lookups

### Storage
- **Badger LSM** for write-heavy workloads
- **Batch writes** for bulk imports
- **Compression** for reduced disk usage

## 📊 Benchmarks

See **[Benchmarks vs Neo4j](benchmarks-vs-neo4j.md)** for detailed comparisons.

---

**Optimize your database** → **[Optimization Guide](optimization-guide.md)**
