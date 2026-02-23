---
name: hybrid-search-3phase
overview: Deliver a 3-phase roadmap to improve hybrid retrieval quality, ranking stability, and ANN scale economics in NornicDB with measurable ops/sec, latency, recall, and memory gains, without feature-flag gating for universally positive changes.
todos:
  - id: phase1-hybrid-order
    content: Refactor hybrid execution to filter->BM25->vector and add candidate-domain plumbing through search + vector pipeline.
    status: pending
  - id: phase1-adaptive-switch
    content: Implement adaptive exact-vs-ANN strategy selection based on candidate set size, dims, and live latency counters.
    status: pending
  - id: phase1-metrics-bench
    content: Expand telemetry and benchmark suites to report recall@k, p50/p95, ops/sec, allocations, and memory.
    status: pending
  - id: phase2-global-bm25
    content: Implement global BM25 stats snapshots with atomic query-time snapshot binding and persistence metadata.
    status: pending
  - id: phase2-rank-stability
    content: Add deterministic rank-stability regression tests across reload/compaction/rebuild paths.
    status: pending
  - id: phase3-compressed-ann
    content: Introduce compressed ANN profile(s) integrated with IVF-HNSW routing and explicit profile config semantics.
    status: pending
  - id: phase3-public-bench
    content: Publish reproducible large-scale performance/memory/recall benchmark reports for all ANN profiles.
    status: pending
---

# Hybrid Retrieval 3-Phase Execution Plan

## Goal and Guardrails

- Implement all three phases in production paths (no feature-flag gating for changes that are strictly quality/perf improvements).
- Keep Neo4j-compatible semantics where applicable (especially BM25 and type/ranking behavior).
- Require per-phase benchmark and regression evidence before merge.

## Current Baseline (What we are changing)

- Current RRF hybrid flow runs vector search first, BM25 second, then type filtering and RRF fusion in [pkg/search/search.go](/Users/c815719/src/NornicDB/pkg/search/search.go).
- Vector strategy already has static N-based switching and candidate generators in [pkg/search/vector_pipeline.go](/Users/c815719/src/NornicDB/pkg/search/vector_pipeline.go) and [pkg/search/search.go](/Users/c815719/src/NornicDB/pkg/search/search.go).
- BM25 v2 is local-index scoped with mutable IDF recomputation in [pkg/search/fulltext_index_v2.go](/Users/c815719/src/NornicDB/pkg/search/fulltext_index_v2.go).
- HNSW quality profiles exist today (`fast|balanced|accurate`) in [pkg/search/hnsw_config.go](/Users/c815719/src/NornicDB/pkg/search/hnsw_config.go).

## Phase 1: Fastest ROI (Execution Order, Adaptive Switch, Telemetry)

### 1) Hybrid planner ordering: filter -> keyword -> vector

- Refactor `rrfHybridSearch` in [pkg/search/search.go](/Users/c815719/src/NornicDB/pkg/search/search.go) to execute:
- type/metadata filter preselection (cheap filter set)
- BM25 candidate generation on filtered domain
- vector search restricted/reranked against candidate pool (or cluster subset)
- final RRF fusion
- Add a candidate-domain abstraction (e.g., `AllowedIDs


Great read. Here’s a distilled extraction of the article and a direct NornicDB application map.

Source: [How ByteDance Solved Billion-Scale Vector Search Problem with Apache Doris 4.0](https://www.velodb.io/blog/bytedance-solved-billion-scale-vector-search-problem-with-apache-doris-4-0)

## Key points extracted + NornicDB application

- **Problem 1: Pure vector search misses exact-intent constraints**
  - **Article point:** Semantic similarity alone confuses exact values (city, numbers, legal identifiers).
  - **Apply to NornicDB:** Make hybrid retrieval the default for enterprise queries:
    - structured filters first (`WHERE` exact predicates)
    - lexical/BM25 next (must-match terms)
    - vector similarity last
  - **Concrete NornicDB change:** Planner rule that reorders candidate reduction in this sequence before vector scoring.

- **Problem 2: Ranking instability from segment-local stats**
  - **Article point:** BM25 computed per segment causes ranking drift after merges/maintenance.
  - **Apply to NornicDB:** Compute and cache **global corpus stats** (DF/N) per index/database namespace, not local shard/segment stats.
  - **Concrete change:** Add a stats layer for full-text indexes with refresh policy + versioning so same query yields stable rankings over time.

- **Problem 3: HNSW memory cost explodes at billion scale**
  - **Article point:** Graph ANN index can be memory-heavy; IVFPQ drastically cuts footprint with acceptable recall tradeoff.
  - **Apply to NornicDB:** Add optional compressed ANN backend (IVF/PQ-style) for very large datasets.
  - **Concrete change:** Introduce pluggable vector index profiles:
    - `hnsw_high_recall`
    - `ivf_pq_balanced`
    - `disk_optimized`
    and expose selection in index config.

- **Technique: Progressive filtering**
  - **Article point:** Cheapest ops first shrink candidate set before expensive vector compute.
  - **Apply to NornicDB:** Push down filters and keyword constraints aggressively in Cypher execution.
  - **Concrete change:** Add optimizer pass for filter pushdown and candidate-size-aware execution path.

- **Technique: Global BM25 scoring**
  - **Article point:** Stable relevance from global term stats.
  - **Apply to NornicDB:** Add `bm25_mode = global|local` with global as default for production.
  - **Concrete change:** Build background-maintained global DF tables and include stats snapshot ID in query profile for explainability.

- **Technique: Compression tradeoff (accuracy vs memory)**
  - **Article point:** Slight recall drop can be worth 20x memory win.
  - **Apply to NornicDB:** Make recall/latency/memory tradeoffs explicit in config and docs.
  - **Concrete change:** Benchmark harness that reports `recall@k`, `p95 latency`, memory footprint per index type.

- **Optimization: Brute force beats index on small candidate sets**
  - **Article point:** After heavy filtering, sequential exact distance is faster than index traversal.
  - **Apply to NornicDB:** Add adaptive switch:
    - if candidate count < threshold, run exact vector scan
    - else ANN search
  - **Concrete change:** dynamic threshold tuning by dimension, hardware profile, and observed p95 latency.

- **Operational lesson: Trust/stability matters as much as raw speed**
  - **Article point:** Ranking churn kills user trust.
  - **Apply to NornicDB:** Add rank-stability regression tests and “query replay consistency” CI checks.
  - **Concrete change:** test suite asserting rank variance bounds across compaction/merge cycles.

## Recommended NornicDB rollout (practical order)

- **Phase 1 (fastest ROI):**
  - Hybrid planner ordering (filter → keyword → vector)
  - Candidate-size adaptive exact/ANN switch
  - Bench + telemetry (`recall@k`, p50/p95, memory)

- **Phase 2 (quality/trust):**
  - Global BM25 stats and stable scoring snapshots
  - Rank-stability regression tests

- **Phase 3 (scale economics):**
  - Compressed ANN mode (IVF/PQ-like) with explicit profile configs
  - Large-scale memory/recall benchmark publication

If you want, I can turn this into a concrete NornicDB engineering RFC with package-level changes (`pkg/search`, `pkg/cypher` planner, config knobs, test matrix, and benchmark acceptance criteria).