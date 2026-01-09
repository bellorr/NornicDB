# Embedding Search (Index)

This document is a short entry point to NornicDB’s embedding/vector search documentation.

Start here:

- `docs/architecture/embedding-search.md` (overview + key concepts)
- `docs/architecture/embedding-search-architecture.md` (data model + execution paths)
- `docs/architecture/embedding-search-flow-diagrams.md` (Mermaid diagrams)
- `docs/architecture/embedding-search-examples.md` (end-to-end examples)

User-facing guides:

- `docs/features/vector-embeddings.md` (embedding generation)
- `docs/user-guides/vector-search.md` (hybrid search / RRF usage)
- `docs/user-guides/qdrant-grpc.md` (Qdrant compatibility layer)

Implementation references:

- `pkg/search/search.go` (unified search service + vector pipeline selection)
- `pkg/qdrantgrpc/points_service.go` (Qdrant Points API mapping)
