# Plan: BM25 V2 Construction Speed Optimization

**Status:** Draft  
**Owner:** Search/Indexing  
**Scope:** Speed up BM25 V2 index construction (`BuildIndexes` / `IndexBatch`) for large datasets without reducing search correctness.

---

## 1) Problem statement

BM25 V2 query latency is strong, but **construction throughput is too slow** on large datasets. The current build path spends too much time in:

- per-term lexicon insertions (`insertLexiconTermLocked`)
- repeated full-vocabulary IDF recomputation after each batch
- high allocation pressure during token/term-frequency creation
- unnecessary build-time cache invalidation work

The result is long startup indexing windows and delayed readiness for dependent subsystems (embed queue / clustering).

---

## 2) Goals and non-goals

### Goals

- Reduce BM25 V2 construction wall-clock time by at least **3x** on `data/test/search/translations`.
- Reduce allocations/op in V2 build path by at least **40%**.
- Keep result quality equivalent to current V2 behavior.
- Keep rollback simple (single env switch back to V1 or previous V2 behavior).

### Non-goals

- Changing ranking semantics (BM25 formula, prefix weighting, RRF behavior).
- Reworking vector/HNSW build path in this plan.
- Immediate full SPIMI rewrite in phase 1 (that is phase 3).

---

## 3) Baseline and measurement protocol

Before each phase, capture baseline:

1. **Build time**  
   - end-to-end startup build logs for target DB (`translations`)
2. **CPU profile**  
   - focused profile during BM25 V2 indexing window
3. **Allocation profile**
   - heap/alloc profile during `IndexBatch` heavy section
4. **Correctness checks**
   - existing V2 tests and overlap tests vs legacy/V1 where applicable

Recommended benchmark additions (if missing):

- `BenchmarkBM25V2_IndexBatch_SyntheticLarge`
- `BenchmarkBM25V2_IndexBatch_DiskFixture`
- `BenchmarkBM25V2_RecomputeIDF`
- `BenchmarkBM25V2_LexiconBuild`

Success gate per phase: measurable speedup with no correctness regression.

---

## 4) Phased implementation plan

## Phase 0: Instrumentation and guardrails (low risk, immediate)

### Changes

- Add build-stage timing in V2:
  - tokenization
  - term frequency map creation
  - postings merge
  - lexicon maintenance
  - IDF update
- Add counters:
  - docs indexed
  - unique terms seen
  - postings appended
  - total allocations (benchmark-driven)

### Files

- `pkg/search/fulltext_index_v2.go`
- `pkg/search/fulltext_index_v2_benchmark_test.go`

### Exit criteria

- We can attribute build time to concrete sub-stages.

---

## Phase 1: High-impact, low-risk data-path fixes

### 1. Deferred lexicon construction (replace per-term insertion)

Current: each unseen term calls `insertLexiconTermLocked` (binary search + slice copy).  
Plan: during build, only update `termIndex`; rebuild `lexicon` once at finalize:

- gather term keys from `termIndex`
- `sort.Strings` once
- assign to `f.lexicon`

Expected impact: **high** on large vocab.

### 2. Deferred global IDF recomputation

Current: recompute all term IDFs after each batch mutation.  
Plan:

- update only doc freq during ingestion
- do one global IDF pass at finalize (or checkpoint every N docs if needed)
- maintain exact same final IDF values as now

Expected impact: **very high** as vocab grows.

### 3. Allocation reduction via pooling

Current: per-doc `map[string]int` and slices are repeatedly allocated.  
Plan:

- add pools for term-frequency maps and temporary token buffers
- clear and reuse objects in build path

Expected impact: **high** allocation reduction, moderate-high CPU benefit.

### 4. Disable query-plan cache churn during build

Current: `markDirtyLocked()` rebuilds cache frequently while indexing.  
Plan:

- introduce build mode flag (or guarded dirty path)
- skip query cache maintenance until build finalize

Expected impact: **medium-high** (GC/allocation pressure reduction).

### Files

- `pkg/search/fulltext_index_v2.go`
- `pkg/search/search.go` (build-mode start/end hooks if needed)
- tests in `pkg/search/fulltext_index_v2_test.go`

### Exit criteria

- At least **2x** V2 build speedup from baseline on target fixture.
- No ranking diffs beyond expected tie-order noise.

---

## Phase 2: Concurrency and lock-duration reduction

### 1. Two-stage indexing pipeline

Stage A (parallel, no index lock):
- tokenize
- term frequency computation
- normalized doc payload creation

Stage B (short lock):
- doc id mapping updates
- postings append
- doc length/count totals update

This preserves correctness while reducing lock hold time and enabling multicore usage.

### 2. Batch merge optimizations

- append postings in larger contiguous chunks
- pre-size postings when feasible using per-batch term histograms

Expected impact: **medium-high**, especially on multi-core systems.

### Files

- `pkg/search/fulltext_index_v2.go`

### Exit criteria

- Additional **1.3x-1.8x** speedup over phase 1.
- No race conditions (`go test ./pkg/search -race` for touched tests).

---

## Phase 3: Structural upgrades (higher effort)

### 1. SPIMI/segmented builder + merge

- Build temporary in-memory term segments
- Optionally spill segments to disk for memory control
- Final merge into compact postings format

Expected impact: **very high** at very large corpus sizes with predictable scaling.

### 2. Offline artifact build + atomic swap

- Build BM25 V2 in background/offline path
- atomically swap `bm25.v2` only when complete
- startup can stay responsive and avoid long blocking build

Expected impact: major operational improvement even when total CPU work is similar.

---

## 5) Correctness and quality safeguards

- Keep BM25 formula and tokenization semantics unchanged.
- Preserve deterministic document mapping behavior where currently required.
- Maintain/expand tests:
  - save/load round-trip
  - V1->V2 migration tests
  - top-k overlap tests on disk fixture
  - phrase search behavior parity

Add regression tests for:

- lexicon rebuild correctness after deletes/reinserts
- IDF values before/after deferred recompute finalization
- build-mode to ready-mode transition invariants

---

## 6) Runtime controls and feature flags

Add explicit controls for safe rollout:

- `NORNICDB_BM25_V2_BUILD_MODE=optimized|legacy`
- `NORNICDB_BM25_V2_BUILD_PARALLELISM=<n>`
- `NORNICDB_BM25_V2_IDF_RECOMPUTE_INTERVAL_DOCS=<n>` (0 means final-only)
- `NORNICDB_BM25_V2_LEXICON_REBUILD_STRATEGY=finalize|incremental`

Default rollout:

1. ship with `legacy` default
2. enable `optimized` in controlled environments
3. promote to default after benchmark and production confidence

---

## 7) Rollout and rollback plan

### Rollout

1. Merge phase 0 + phase 1 behind flags.
2. Benchmark on `translations` and synthetic stress datasets.
3. Enable optimized mode in staging.
4. Enable for one production-like DB.
5. Expand to all DBs.

### Rollback

- Flip `NORNICDB_BM25_V2_BUILD_MODE=legacy`
- or switch BM25 engine to V1 (`NORNICDB_SEARCH_BM25_ENGINE=v2`)
- no data migration rollback required.

---

## 8) Expected impact summary

Short term (phase 1): **2x-4x** build speedup likely on large corpora.  
Mid term (phase 2): additional **30-80%** speedup depending on CPU cores and token distribution.  
Long term (phase 3): significant scaling gains and far better startup operability.

---

## 9) Work breakdown (concrete tasks)

1. Add phase timing + counters in V2 builder.
2. Implement build finalize hook:
   - one-shot lexicon rebuild
   - one-shot global IDF recompute
3. Add pooling for per-doc temporary structures.
4. Add build-mode guard to skip query cache churn.
5. Add/extend benchmarks and correctness tests.
6. Add runtime flags and docs for toggles.
7. Run benchmark report with before/after table.
8. Decide whether phase 2 starts immediately based on measured gains.

---

## 10) Acceptance criteria

- Construction speed improved by at least **3x** on target dataset.
- No functional regressions in search test suite.
- No increase in false rebuilds due to build-settings mismatch.
- Clear operational runbook for enabling/disabling optimized V2 build mode.

