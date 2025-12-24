package fn

import (
	"time"

	"github.com/orneryd/nornicdb/pkg/storage"
)

// Context provides runtime access for function evaluation without importing the parent
// `cypher` package (avoids import cycles).
//
// Eval must evaluate a Cypher expression in the caller's scope (row bindings).
// Functions can use Eval for lazy argument evaluation (e.g. coalesce).
type Context struct {
	Nodes map[string]*storage.Node
	Rels  map[string]*storage.Edge

	Eval func(expr string) (interface{}, error)
	Now  func() time.Time

	// IsInternalProperty returns true for properties that must not be exposed
	// in Cypher function outputs (e.g., embedding vectors).
	IsInternalProperty func(key string) bool
}
