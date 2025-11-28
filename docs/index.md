# NornicDB Documentation

Welcome to **NornicDB** - A production-ready graph database with GPU acceleration, Neo4j compatibility, and advanced indexing.

## 🚀 Quick Links

- **[Getting Started](guides/getting-started.md)** - Set up NornicDB in 5 minutes
- **[Cursor Chat Mode Guide](guides/cursor-chatmode.md)** - 🆕 Use with AI agents
- **[MCP Tools Quick Reference](MCP_TOOLS_QUICKREF.md)** - Tool cheat sheet
- **[Architecture Overview](guides/architecture.md)** - Understand the system design
- **[API Reference](api-reference.md)** - Auto-generated from GoDoc comments
- **[Configuration Guide](guides/configuration.md)** - Production setup

## 📚 Documentation Sections

### For Users

- **[Getting Started](guides/getting-started.md)** - Installation and first steps
- **[Vector Search Guide](guides/vector-search.md)** - Semantic search with embeddings
- **[Cypher Query Guide](guides/cypher-queries.md)** - Neo4j query language
- **[Memory Decay System](guides/memory-decay.md)** - Cognitive science-based memory
- **[GPU Acceleration](guides/gpu-acceleration.md)** - Boost performance with GPUs
- **[Neo4j Compatibility](guides/neo4j-compatibility.md)** - Use existing Neo4j tools

### For Developers

- **[Architecture](guides/architecture.md)** - System design and components
- **[API Reference](api-reference.md)** - Complete API documentation
- **[Configuration](guides/configuration.md)** - Advanced setup options
- **[Performance Tuning](guides/performance-tuning.md)** - Optimization strategies
- **[Compliance & Security](guides/compliance.md)** - GDPR, HIPAA, SOC2

### For AI/Agent Integration

- **[Cursor Chat Mode Guide](guides/cursor-chatmode.md)** - 🆕 Use NornicDB with Cursor IDE
- **[MCP Tools Quick Reference](MCP_TOOLS_QUICKREF.md)** - 8-tool cheat sheet
- **[Agent Preamble (mimir-v2)](../docs/agents/claudette-mimir-v2.md)** - Memory-augmented AI agent

### For DevOps

- **[Deployment Guide](guides/deployment.md)** - Docker, Kubernetes, cloud
- **[Monitoring & Observability](guides/monitoring.md)** - Health checks, metrics
- **[Backup & Recovery](guides/backup-recovery.md)** - Data protection
- **[Troubleshooting](guides/troubleshooting.md)** - Common issues and solutions

## 🎯 Key Features

### 🧠 Graph-Powered Memory
- Semantic relationships between data
- Multi-hop graph traversal
- Automatic relationship inference
- Memory decay simulation

### 🚀 GPU Acceleration
- 10-100x speedup for vector search
- Multi-backend support (CUDA, OpenCL, Metal, Vulkan)
- Automatic CPU fallback
- Memory-optimized embeddings

### 🔍 Advanced Search
- Vector similarity search with cosine similarity
- Full-text search with BM25 scoring
- Hybrid search combining both methods
- HNSW indexing for O(log N) performance

### 🔗 Neo4j Compatible
- Bolt protocol support
- Cypher query language
- Standard Neo4j drivers work out-of-the-box
- Easy migration from Neo4j

### 🔐 Enterprise-Ready
- GDPR, HIPAA, SOC2 compliance
- Field-level encryption
- RBAC and audit logging
- ACID transactions

## 📊 Documentation Statistics

- **21 packages** fully documented
- **13,400+ lines** of GoDoc comments
- **350+ functions** with examples
- **40+ ELI12 explanations** for complex concepts
- **4.1:1 documentation-to-code ratio**

## 🤝 Contributing

Found an issue or want to improve documentation? Check out our [Contributing Guide](CONTRIBUTING.md).

## 📄 License

NornicDB is MIT licensed. See [LICENSE](../LICENSE) for details.

---

**Last Updated:** November 28, 2025  
**Version:** 0.1.3  
**Docker:** `timothyswt/nornicdb-arm64-metal:0.1.3`  
**Status:** Production Ready ✅
