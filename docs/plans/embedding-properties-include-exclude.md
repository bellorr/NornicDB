# Plan: Configurable property include/exclude for embeddings

**Status:** Draft  
**Goal:** Let users limit which node properties are used when building text for embedding (e.g. embed only `content`, or embed everything except `internal_id`), so they can avoid re-embedding stored embeddings and control what gets sent to the embedder.

---

## Current behavior

- **Location:** `pkg/nornicdb/embed_queue.go` → `buildEmbeddingText(properties, labels)`.
- **Logic:**
  - Labels are always included first (`labels: A, B`).
  - All properties are included except a **fixed** internal list: `embedding`, `has_embedding`, `embedding_skipped`, `embedding_model`, `embedding_dimensions`, `embedded_at`, `createdAt`, `updatedAt`, `id`.
- **Result:** “Embed the entire node except metadata.” No user-configurable include/exclude.

---

## Proposed behavior

- **Include list (optional):** If set, **only** these property keys (and optionally labels) are used when building embedding text. Enables “embed only `content`” or “embed only `title` and `description`”.
- **Exclude list (optional):** If set, these keys are **excluded** in addition to the built-in metadata list. Enables “embed everything except `internal_id`, `raw_html`”.
- **Defaults:** Empty include and empty exclude → keep current behavior (all properties except built-in skip list).

---

## Config shape

### 1. Embed worker config (in-memory)

Add to `EmbedWorkerConfig` in `pkg/nornicdb/embed_queue.go`:

```go
// PropertiesInclude: if non-empty, ONLY these property keys are used when building
// embedding text (plus labels if IncludeLabels is true). Enables "embed only content".
// Empty = use all properties (subject to PropertiesExclude and built-in skips).
PropertiesInclude []string

// PropertiesExclude: these property keys are never used when building embedding text.
// Applied in addition to the built-in metadata skip list. Empty = no extra exclusions.
PropertiesExclude []string

// IncludeLabels: if true (default), node labels are prepended to the embedding text.
// Set false to embed only the configured properties (e.g. when using a single field).
IncludeLabels bool
```

### 2. Resolution order

When building text for a node:

1. **Built-in skip list** always applies (embedding metadata, `createdAt`, `updatedAt`, `id`, etc.).
2. If **PropertiesInclude** is non-empty:
   - Consider only keys in this list (and still apply built-in skips).
   - If **PropertiesExclude** is also set, exclude those keys from the include set (so we never embed an excluded key even if mistakenly listed in include).
3. If **PropertiesInclude** is empty:
   - Consider all property keys except built-in skips and **PropertiesExclude**.

So:

- **Include only:** `PropertiesInclude = ["content"]` → only `content` (and labels if `IncludeLabels`) used.
- **Exclude only:** `PropertiesExclude = ["internal_id", "raw_html"]` → all properties except built-in + these.
- **Both:** Include defines the candidate set; Exclude is then removed from that set (and built-in skips always apply).

### 3. Labels

- **IncludeLabels** (default `true`): current behavior — always prepend `labels: A, B`.
- **IncludeLabels = false**: only property-derived text is used (for “single field only” use cases). If both include and labels are minimal, `buildEmbeddingText` still returns a non-empty string (e.g. fallback `"node"` when nothing is left).

---

## Where config is set (Option C: defaults → file → env)

Same precedence as all other NornicDB configuration:

1. **Built-in defaults** (in code) — lowest priority  
2. **Config file** (YAML) — overrides defaults  
3. **Environment variables** — highest priority (before CLI)

Implementation follows existing patterns: `LoadDefaults()` sets initial values; `LoadFromFile()` merges YAML into config; `ApplyEnvVars()` (called by main after `LoadFromFile`) applies env overrides.

### 1. Built-in defaults

**File:** `pkg/config/config.go`  
**Struct:** Add to `EmbeddingWorkerConfig`: `PropertiesInclude []string`, `PropertiesExclude []string`, `IncludeLabels bool`.  
**Function:** In `LoadDefaults()` (around line 1541), set on `config.EmbeddingWorker`:

- `PropertiesInclude = nil` (empty slice or nil → “use all properties”)
- `PropertiesExclude = nil`
- `IncludeLabels = true`

### 2. Config file (YAML)

**File:** `pkg/config/config.go`  
**Struct:** `YAMLConfig.EmbeddingWorker` (tag `yaml:"embedding_worker"`), same block that already has `scan_interval`, `batch_delay`, `chunk_size`, etc.

Add to the YAML struct:

- `PropertiesInclude []string` with `yaml:"properties_include"`
- `PropertiesExclude []string` with `yaml:"properties_exclude"`
- `IncludeLabels *bool` with `yaml:"include_labels"` (pointer so “unset” can keep default)

**YAML keys** (under existing `embedding_worker` section):

- **properties_include** – list of strings. Example: `["content"]` or `["content", "title", "description"]`. Omit or empty = use all (subject to exclude).
- **properties_exclude** – list of strings. Example: `["internal_id", "raw_html"]`. Omit or empty = no extra exclusions.
- **include_labels** – bool. Omit = true. Set to `false` to omit labels from embedding text.

**Merge in LoadFromFile():** In the same block where other `EmbeddingWorker` fields are applied (around 2447–2465), add:

- If `yamlCfg.EmbeddingWorker.PropertiesInclude != nil` → `config.EmbeddingWorker.PropertiesInclude = yamlCfg.EmbeddingWorker.PropertiesInclude`
- If `yamlCfg.EmbeddingWorker.PropertiesExclude != nil` → `config.EmbeddingWorker.PropertiesExclude = yamlCfg.EmbeddingWorker.PropertiesExclude`
- If `yamlCfg.EmbeddingWorker.IncludeLabels != nil` → `config.EmbeddingWorker.IncludeLabels = *yamlCfg.EmbeddingWorker.IncludeLabels`

### 3. Environment variables

**File:** `pkg/config/config.go`  
**Function:** `applyEnvVars(config)` (and the embedding-worker block that uses `getEnvInt`, `getEnvDuration`, etc.)

Add after the existing `NORNICDB_EMBED_*` handling:

- **NORNICDB_EMBEDDING_PROPERTIES_INCLUDE** – comma-separated list. Example: `content` or `content,title,description`. Empty/unset = do not override (keep file/default). Parsing: `strings.Split(value, ",")`, trim spaces, drop empty strings. Case-sensitive.
- **NORNICDB_EMBEDDING_PROPERTIES_EXCLUDE** – comma-separated list. Example: `internal_id,raw_html`. Same parsing.
- **NORNICDB_EMBEDDING_INCLUDE_LABELS** – boolean. Use existing `getEnvBool("NORNICDB_EMBEDDING_INCLUDE_LABELS", config.EmbeddingWorker.IncludeLabels)` so “unset” preserves current value (from file or default).

Only override from env when the env var is set (for list vars: when non-empty after trim; for bool, getEnvBool with current config as default already does that).

### 4. Wiring into the embed worker

NornicDB uses two config layers: `pkg/config.Config` (file + env) and `pkg/nornicdb.Config` (passed into the DB). Main maps the former into the latter; the DB then builds `EmbedWorkerConfig` from `nornicdb.Config`.

**File:** `pkg/nornicdb/db.go`  
- **nornicdb.Config** (the struct passed to the DB, around line 299): add `EmbeddingPropertiesInclude []string`, `EmbeddingPropertiesExclude []string`, `EmbeddingIncludeLabels bool` (with `yaml:"..."` tags if desired). DefaultConfig() (around 407) should set them to nil, nil, true.
- **Building `db.embedWorkerConfig`** (around 1125): add to the struct literal:
  - `PropertiesInclude: config.EmbeddingPropertiesInclude`
  - `PropertiesExclude: config.EmbeddingPropertiesExclude`
  - `IncludeLabels: config.EmbeddingIncludeLabels`

**File:** `cmd/nornicdb/main.go`  
Where `dbConfig` is populated from `cfg` (around 444–483), add:
- `dbConfig.EmbeddingPropertiesInclude = cfg.EmbeddingWorker.PropertiesInclude`
- `dbConfig.EmbeddingPropertiesExclude = cfg.EmbeddingWorker.PropertiesExclude`
- `dbConfig.EmbeddingIncludeLabels = cfg.EmbeddingWorker.IncludeLabels`

**File:** `pkg/nornicdb/embed_queue.go`  
`EmbedWorkerConfig` must define `PropertiesInclude []string`, `PropertiesExclude []string`, `IncludeLabels bool` (see “Config shape” above). `DefaultEmbedWorkerConfig()` should set them to nil, nil, true. The worker calls `buildEmbeddingText(properties, labels, opts)` with opts built from `ew.config`.

---

## Code changes (high level)

1. **EmbedWorkerConfig**  
   Add `PropertiesInclude []string`, `PropertiesExclude []string`, `IncludeLabels bool` (default true). Set in `DefaultEmbedWorkerConfig()` to nil/nil/true.

2. **buildEmbeddingText**  
   - Change signature to accept filter options, e.g.  
     `buildEmbeddingText(properties, labels, opts *EmbedTextOptions)`  
     with `Include []string`, `Exclude []string`, `IncludeLabels bool`.
   - Apply built-in skips first.
   - If `opts.Include` non-empty, restrict to those keys (minus built-in and opts.Exclude).
   - Else, use all keys minus built-in and opts.Exclude.
   - If `opts.IncludeLabels`, prepend labels as today.
   - Keep existing value-to-string conversion and “at least return something” fallback.

3. **Embed worker**  
   When calling `buildEmbeddingText`, pass `ew.config.PropertiesInclude`, `ew.config.PropertiesExclude`, `ew.config.IncludeLabels` (or an `EmbedTextOptions` built from config).

4. **Config wiring**  
   See “Where config is set (Option C)” above. In short: add fields to `pkg/config.EmbeddingWorkerConfig` and `YAMLConfig.EmbeddingWorker`; set defaults in `LoadDefaults()`; merge YAML in `LoadFromFile()`; override from env in `applyEnvVars()`; pass `cfg.EmbeddingWorker` into the embed worker in `db.go`.

5. **Tests**  
   - Unit tests for `buildEmbeddingText` with include-only, exclude-only, both, and include-labels on/off.
   - Integration test: node with `content`, `title`, `internal_id`; with include `["content"]` only `content` (and labels) appear in embedder input; with exclude `["internal_id"]` both `content` and `title` appear.

---

## Edge cases

- **Include list with key that doesn’t exist on node:** Key is skipped (no error). Result is whatever other keys + labels contribute.
- **Empty result:** If after filters no labels and no properties remain, keep current fallback (e.g. `"node"`) so embedder always gets non-empty text.
- **Stored embeddings:** This plan does not change when we clear or keep existing `ChunkEmbeddings`/embedding metadata; that stays “invalidate on property change” as today. The plan only controls **which properties** are used to build the text for (re-)embedding.

---

## Summary

| Config             | Default | Effect |
|--------------------|---------|--------|
| PropertiesInclude  | empty   | Use all properties (minus built-in skips and Exclude). |
| PropertiesExclude  | empty   | Additional keys to never embed. |
| IncludeLabels      | true    | Prepend `labels: ...` to embedding text. |

- **Include** = “only these keys (plus optional labels)”.
- **Exclude** = “never these keys (on top of built-in skips)”.
- **Precedence (Option C):** defaults (code) → YAML `embedding_worker.*` → env `NORNICDB_EMBEDDING_*`.
- **Env vars:** `NORNICDB_EMBEDDING_PROPERTIES_INCLUDE`, `NORNICDB_EMBEDDING_PROPERTIES_EXCLUDE`, `NORNICDB_EMBEDDING_INCLUDE_LABELS`.
- **YAML (under `embedding_worker`):** `properties_include`, `properties_exclude`, `include_labels`.

This gives users a way to limit embedding to a specific field (e.g. `content`) or to exclude noisy/internal fields without changing the rest of the embedding pipeline.
