# Latest (Untagged) Changelog

This document summarizes the changes that are **not yet released as a version tag**, but are being shipped in newly-built images under the `latest` tag.

---

## Highlights

### Added

- **IVFPQ Compressed ANN**: End-to-end compressed ANN pipeline with build, query, persistence, and quality coverage (`pkg/search/ivfpq_*`). This enables significantly higher vector scale with lower memory footprint while preserving practical recall through rerank.
- **IVFPQ Candidate Generation and Routing Enhancements**: Hybrid cluster routing and compressed candidate generation improvements reduce bad-cluster routing and improve search quality consistency under mixed lexical/semantic queries.
- **Async vs Explicit Transaction Clobber Regression Suite**: Added deterministic tests for write-behind queue vs explicit transaction ordering (`pkg/storage/async_engine_clobber_test.go`) to prevent stale async writes from overwriting newer committed data.
- **OAuth Cookie HTTPS Detection Test Coverage**: Added proxy-aware cookie security tests (`pkg/server/server_auth_cookie_test.go`) to ensure secure cookie behavior behind TLS-terminating load balancers.
- **WAL Segment Path Traversal Regression Test**: Added coverage for manifest path sanitization (`pkg/storage/wal_segments_test.go`) to prevent unsafe file path usage during WAL segment replay.
- **SwiftPM for macOS Menu Bar App**: Added `Package.swift` and `Package.resolved` for deterministic dependency management and cleaner macOS build tooling.

### Changed

- **Go Runtime Upgrade to 1.26.0**:
  - Improves CGO-heavy paths (local GGUF embedding/inference) via reduced CGO overhead.
  - Brings Green Tea GC by default, improving allocation-heavy workloads.
  - Adds runtime hardening (heap base randomization) relevant to mixed Go/C deployments.
- **llama.cpp Upgrade to b8157 Across Build Targets**:
  - Updated Docker/build pins and scripts for CPU/CUDA/Metal/Vulkan flows.
  - Aligned local build scripts and release image tags to a single modern baseline.
- **Local LLM / BGE-M3 Defaults and Buffers**:
  - Context/token handling now aligns with model capabilities (e.g. larger default chunk handling for BGE-M3-class models).
  - Internal buffer and tokenization behavior hardened with regression tests.
- **macOS Config and UX Improvements**:
  - Replaced fragile manual YAML string/regex editing with Yams-based read/write.
  - Added UI control for embedding chunk size and provider-aware effective limits (Apple ML vs local GGUF).
- **Search Memory Layout Optimization**:
  - Flattened IVFPQ per-list code layout for more cache-friendly scoring.
  - Reintroduced fixed-size pooled scratch buffers (64 KB) in vector file scoring path for stable throughput without locality-regression heuristics.
- **Cypher and Transaction Semantics**:
  - Continued parser/runtime compatibility hardening for type constraints and permutation-heavy query patterns.
  - Unified explicit transaction lifecycle semantics across HTTP/Bolt paths for consistency.
- **Performance Baseline Documentation Refresh (Go 1.26)**:
  - Updated single-concurrency HTTP write benchmark baseline in `docs/performance/single-request-benchmark.md`.
  - Updated `MaxConcurrentStreams` comparison baseline in `docs/performance/maxconcurrentstreams-comparison.md` with newly re-run 100/250 concurrency results.

### Fixed

- **AsyncEngine stale-write clobber window**: Prevented queued async updates from overwriting newer explicit transaction commits via rebase/retry conflict handling.
- **Auth cookies behind reverse proxies**: `Secure` attribute now correctly set for HTTPS sessions terminated at proxies (`X-Forwarded-Proto=https`), not only direct TLS.
- **WAL manifest path traversal risk**: Sanitized WAL segment paths before file open to block `../` and nested path escapes.
- **WAL auto-compaction recovery race**: Serialized auto-snapshot/truncate against in-flight mutating operations to prevent rare missing-node recovery outcomes under compaction.
- **macOS config corruption under repeated settings edits**: Eliminated corruption-prone regex write path by switching to structured YAML parsing/writing.

### Performance

- **Compressed ANN scalability**: IVFPQ compressed search path now supports materially larger embedding corpora with reduced RAM pressure.
- **IVFPQ scoring throughput**: Contiguous code storage + tuned candidate scoring improves cache locality and reduces per-query overhead in compressed paths.
- **CGO-heavy embedding/generation latency**: Go 1.26 runtime and modern llama.cpp backend changes improve baseline efficiency for local model inference stacks.
- **HTTP write benchmark baselines refreshed**: Latest single-thread and high-concurrency figures were re-measured and published to keep performance guidance aligned with current runtime and build stack.

### Security

- **Cookie transport security**: Browser auth cookies now reliably use `Secure` in proxied HTTPS deployments, reducing accidental insecure-cookie exposure.
- **Filesystem safety in WAL replay**: Segment-path sanitization prevents manifest-sourced path traversal to arbitrary files.
- **Runtime hardening via Go 1.26**: Heap base randomization raises exploit difficulty for memory-corruption scenarios involving CGO boundaries.

### Stability / Ops

- **Go 1.26 operational tooling improvements**: New goroutine leak profiling support can aid diagnosis of background loop leaks in long-running server processes.
- **llama.cpp API drift guardrails**: Updated integration assumptions for modern llama.cpp APIs and removed brittle logging call usage to avoid signature drift breakage.
- **Recovery consistency**: Snapshot/compaction sequencing now better preserves WAL and engine state coherence under concurrent mutation load.

### What This Means For Users

- **Faster and more predictable local AI features**: Local embeddings/reranking and GGUF-backed workflows run with better baseline performance and fewer runtime edge-case failures.
- **Bigger datasets on the same hardware**: Compressed ANN improvements make it practical to index and query larger embedding corpora without proportional memory growth.
- **Safer production defaults**: Stronger cookie security and WAL path validation reduce common deployment and hardening risks.
- **Less config fragility on macOS**: Settings changes are now reliably persisted without YAML corruption.
- **Better correctness under concurrency**: Explicit transactions and async writes now coexist more safely in high-write systems.
- **More accurate performance guidance**: Published benchmark docs now reflect current Go 1.26-era behavior for both low-concurrency and high-concurrency HTTP write paths.

### Technical Details

- **Delta since `v1.0.12-preview`**: 19 commits, 130 files changed, +10,305 / -4,063 lines.
- **Primary focus areas**: compressed ANN (IVFPQ), local LLM runtime upgrades (Go + llama.cpp), storage/WAL correctness, auth hardening, and macOS configuration reliability.
