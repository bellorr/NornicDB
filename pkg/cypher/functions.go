// Cypher function implementations for NornicDB.
//
// This file holds the public expression-evaluation entrypoints. The heavy
// implementation is split across `functions_eval_part*.go`.
package cypher

import (
	"strings"

	"github.com/orneryd/nornicdb/pkg/storage"
)

// PluginFunctionLookup is a callback to look up functions from loaded plugins.
// Set by pkg/nornicdb during database initialization.
// Returns the function handler and true if found, nil and false otherwise.
var PluginFunctionLookup func(name string) (handler interface{}, found bool)

func isFunctionCall(expr, funcName string) bool {
	return isFunctionCallWS(expr, funcName)
}

// evaluateExpression evaluates an expression for a single node context.
func (e *StorageExecutor) evaluateExpression(expr string, varName string, node *storage.Node) interface{} {
	return e.evaluateExpressionWithContext(expr, map[string]*storage.Node{varName: node}, nil)
}

// evaluateExpressionWithPathContext evaluates an expression with full path context.
func (e *StorageExecutor) evaluateExpressionWithPathContext(expr string, pathCtx PathContext) interface{} {
	return e.evaluateExpressionWithContextFull(expr, pathCtx.nodes, pathCtx.rels, pathCtx.paths, pathCtx.allPathEdges, pathCtx.allPathNodes, pathCtx.pathLength)
}

func (e *StorageExecutor) evaluateExpressionWithContext(expr string, nodes map[string]*storage.Node, rels map[string]*storage.Edge) interface{} {
	return e.evaluateExpressionWithContextFull(expr, nodes, rels, nil, nil, nil, 0)
}

func (e *StorageExecutor) evaluateExpressionWithContextFull(expr string, nodes map[string]*storage.Node, rels map[string]*storage.Edge, paths map[string]*PathResult, allPathEdges []*storage.Edge, allPathNodes []*storage.Node, pathLength int) interface{} {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	return e.evaluateExpressionWithContextFullFunctions(expr, nodes, rels, paths, allPathEdges, allPathNodes, pathLength)
}
