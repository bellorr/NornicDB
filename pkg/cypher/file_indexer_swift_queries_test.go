package cypher

import (
	"context"
	"strings"
	"testing"

	"github.com/orneryd/nornicdb/pkg/config"
	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

// TestSwiftFileIndexerQueriesParseWithExplain validates Cypher statements used by
// the macOS Swift File Indexer flows by parsing/planning them with EXPLAIN. This
// avoids mutating storage while still exercising parser compatibility.
func TestSwiftFileIndexerQueriesParseWithExplain(t *testing.T) {
	t.Parallel()

	store := storage.NewMemoryEngine()
	exec := NewStorageExecutor(store)
	ctx := context.Background()

	props := map[string]any{
		"path":          "/tmp/ws/main.go",
		"name":          "main.go",
		"extension":     "go",
		"language":      "go",
		"size_bytes":    42,
		"content":       "package main",
		"type":          "text",
		"folder_root":   "/tmp/ws",
		"folder_name":   "ws",
		"folder_tags":   []string{"File", "ws", "backend"},
		"file_tags":     []string{"important"},
		"tags":          []string{"File", "ws", "backend", "important"},
		"last_modified": "2026-01-01T00:00:00Z",
	}

	testCases := []struct {
		name   string
		query  string
		params map[string]any
	}{
		{
			name: "check file exists query",
			query: `
MATCH (f:File {path: $path})
RETURN f.last_modified as last_modified, f.indexed_date as indexed_date, coalesce(f.file_tags, []) as file_tags
`,
			params: map[string]any{"path": "/tmp/ws/main.go"},
		},
		{
			name: "create file node query",
			query: `
CREATE (f:File:Node {
    path: $path,
    id: $node_id
})
SET f += $props
SET f.indexed_date = datetime()
RETURN f.id as id
`,
			params: map[string]any{
				"path":    "/tmp/ws/main.go",
				"node_id": "file-123",
				"props":   props,
			},
		},
		{
			name: "update file node query",
			query: `
MATCH (f:File {path: $path})
SET f:Node
SET f += $props
SET f.indexed_date = datetime()
RETURN f.id as id
`,
			params: map[string]any{
				"path":  "/tmp/ws/main.go",
				"props": props,
			},
		},
		{
			name: "update folder tags cascade query",
			query: `
MATCH (f:File {folder_root: $folder_root})
WITH f, coalesce(f.file_tags, []) AS file_tags
SET f.folder_tags = $folder_tags
SET f.tags = reduce(acc = [], t IN ($folder_tags + file_tags) |
    CASE WHEN t IN acc THEN acc ELSE acc + t END
)
RETURN count(f) as updated
`,
			params: map[string]any{
				"folder_root": "/tmp/ws",
				"folder_tags": []string{"File", "ws", "backend"},
			},
		},
		{
			name: "load indexed files query",
			query: `
MATCH (f:File {folder_root: $folder_root})
RETURN f.path as path,
       f.name as name,
       f.folder_root as folder_root,
       coalesce(f.folder_tags, $default_folder_tags) as folder_tags,
       coalesce(f.file_tags, []) as file_tags,
       coalesce(f.tags, []) as tags
ORDER BY f.path
`,
			params: map[string]any{
				"folder_root":         "/tmp/ws",
				"default_folder_tags": []string{"File", "ws"},
			},
		},
		{
			name: "add tag to file query",
			query: `
MATCH (f:File {path: $path, folder_root: $folder_root})
WITH f, coalesce(f.file_tags, []) AS file_tags
SET f.file_tags = reduce(acc = [], t IN (file_tags + [$new_tag]) |
    CASE WHEN t IN acc THEN acc ELSE acc + t END
)
SET f.folder_tags = $folder_tags
SET f.tags = reduce(acc = [], t IN ($folder_tags + f.file_tags) |
    CASE WHEN t IN acc THEN acc ELSE acc + t END
)
RETURN f.path as path
`,
			params: map[string]any{
				"path":        "/tmp/ws/main.go",
				"folder_root": "/tmp/ws",
				"new_tag":     "hotfix",
				"folder_tags": []string{"File", "ws", "backend"},
			},
		},
		{
			name: "remove tag from file query",
			query: `
MATCH (f:File {path: $path, folder_root: $folder_root})
WITH f, [t IN coalesce(f.file_tags, []) WHERE t <> $tag] AS file_tags
SET f.file_tags = file_tags
SET f.folder_tags = $folder_tags
SET f.tags = reduce(acc = [], t IN ($folder_tags + file_tags) |
    CASE WHEN t IN acc THEN acc ELSE acc + t END
)
RETURN f.path as path
`,
			params: map[string]any{
				"path":        "/tmp/ws/main.go",
				"folder_root": "/tmp/ws",
				"tag":         "hotfix",
				"folder_tags": []string{"File", "ws", "backend"},
			},
		},
		{
			name: "count folder files and chunks query",
			query: `
MATCH (f:File {folder_root: $folder_root})
OPTIONAL MATCH (f)-[:HAS_CHUNK]->(c)
RETURN count(DISTINCT f) as fileCount, count(DISTINCT c) as chunkCount
`,
			params: map[string]any{
				"folder_root": "/tmp/ws",
			},
		},
		{
			name: "delete folder files and chunks query",
			query: `
MATCH (f:File {folder_root: $folder_root})
OPTIONAL MATCH (f)-[:HAS_CHUNK]->(c)
WITH f, collect(c) as chunks
DETACH DELETE f
FOREACH (chunk IN chunks | DETACH DELETE chunk)
`,
			params: map[string]any{
				"folder_root": "/tmp/ws",
			},
		},
	}

	run := func(t *testing.T) {
		for _, tc := range testCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				explainQuery := "EXPLAIN " + strings.TrimSpace(tc.query)
				_, err := exec.Execute(ctx, explainQuery, tc.params)
				require.NoError(t, err)
			})
		}
	}

	t.Run("nornic_parser", func(t *testing.T) {
		cleanup := config.WithNornicParser()
		defer cleanup()
		run(t)
	})

	t.Run("antlr_parser", func(t *testing.T) {
		cleanup := config.WithANTLRParser()
		defer cleanup()
		run(t)
	})
}
