# Changelog

All notable changes to NornicDB will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.4] - 2025-12-01

### Added
- Comprehensive documentation reorganization with 12 logical categories
- Complete user guides for Cypher queries, vector search, and transactions
- Getting started guides with Docker deployment
- API reference documentation for all 52 Cypher functions
- Feature guides for GPU acceleration, memory decay, and link prediction
- Architecture documentation for system design and plugin system
- Performance benchmarks and optimization guides
- Advanced topics: clustering, embeddings, custom functions
- Compliance guides for GDPR, HIPAA, and SOC2
- AI agent integration guides for Cursor and MCP tools
- Neo4j migration guide with 96% feature parity
- Operations guides for deployment, monitoring, and scaling
- Development guides for contributors

### Changed
- Documentation structure reorganized from flat hierarchy to logical categories
- File naming standardized to kebab-case
- All cross-references updated to new locations
- README files created for all directories

### Documentation
- 350+ functions documented with examples
- 13,400+ lines of GoDoc comments
- 40+ ELI12 explanations for complex concepts
- 4.1:1 documentation-to-code ratio

## [0.1.3] - 2025-11-25

### Added
- Complete Cypher function documentation (52 functions)
- Pool package documentation with memory management examples
- Cache package documentation with LRU and TTL examples
- Real-world examples for all public functions

### Improved
- Code documentation coverage to 100% for public APIs
- ELI12 explanations for complex algorithms
- Performance characteristics documented

## [0.1.2] - 2025-11-20

### Added
- GPU acceleration for vector search (Metal, CUDA, OpenCL)
- Automatic embedding generation with Ollama integration
- Memory decay system for time-based importance
- Link prediction with ML-based relationship inference
- Cross-encoder reranking for improved search accuracy

### Performance
- 10-100x speedup for vector operations with GPU
- Sub-millisecond queries on 1M vectors with HNSW index
- Query caching with LRU eviction

## [0.1.1] - 2025-11-15

### Added
- Hybrid search with Reciprocal Rank Fusion (RRF)
- Full-text search with BM25 scoring
- HNSW vector index for O(log N) performance
- Eval harness for search quality validation

### Fixed
- Memory leaks in query execution
- Race conditions in concurrent transactions
- Index corruption on crash recovery

## [0.1.0] - 2025-11-01

### Added
- Initial release of NornicDB
- Neo4j Bolt protocol compatibility
- Cypher query language support (96% Neo4j parity)
- ACID transactions
- Property graph model
- Badger storage engine
- In-memory engine for testing
- JWT authentication with RBAC
- Field-level encryption (AES-256-GCM)
- Audit logging for compliance
- Docker images for ARM64 and x86_64

### Features
- Vector similarity search with cosine similarity
- Automatic relationship inference
- GDPR, HIPAA, SOC2 compliance features
- REST HTTP API
- Prometheus metrics

## [Unreleased]

### Planned
- Horizontal scaling with read replicas
- Distributed transactions
- Graph algorithms (PageRank, community detection)
- Time-travel queries
- Multi-tenancy support
- GraphQL API
- WebSocket support for real-time updates

---

## Version History

- **0.1.4** (2025-12-01) - Documentation reorganization
- **0.1.3** (2025-11-25) - Complete API documentation
- **0.1.2** (2025-11-20) - GPU acceleration and ML features
- **0.1.1** (2025-11-15) - Hybrid search and indexing
- **0.1.0** (2025-11-01) - Initial release

## Links

- [GitHub Repository](https://github.com/orneryd/nornicdb)
- [Documentation](https://github.com/orneryd/nornicdb/tree/main/docs)
- [Docker Hub](https://hub.docker.com/r/timothyswt/nornicdb-arm64-metal)
- [Issue Tracker](https://github.com/orneryd/nornicdb/issues)
