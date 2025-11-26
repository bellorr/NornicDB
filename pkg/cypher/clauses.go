// Cypher clause implementations for NornicDB.
// This file contains implementations for WITH, UNWIND, UNION, OPTIONAL MATCH,
// FOREACH, and LOAD CSV clauses.

package cypher

import (
	"context"
	"fmt"
	"strings"

	"github.com/orneryd/nornicdb/pkg/storage"
)

// ========================================
// WITH Clause
// ========================================

// executeWith handles WITH clause - intermediate result projection
func (e *StorageExecutor) executeWith(ctx context.Context, cypher string) (*ExecuteResult, error) {
	upper := strings.ToUpper(cypher)

	withIdx := strings.Index(upper, "WITH")
	if withIdx == -1 {
		return nil, fmt.Errorf("WITH clause not found")
	}

	remainderStart := withIdx + 4
	for remainderStart < len(cypher) && cypher[remainderStart] == ' ' {
		remainderStart++
	}

	nextClauses := []string{" MATCH ", " WHERE ", " RETURN ", " CREATE ", " MERGE ", " DELETE ", " SET ", " UNWIND ", " ORDER BY ", " SKIP ", " LIMIT "}
	nextClauseIdx := len(cypher)
	for _, clause := range nextClauses {
		idx := strings.Index(upper[remainderStart:], clause)
		if idx >= 0 && remainderStart+idx < nextClauseIdx {
			nextClauseIdx = remainderStart + idx
		}
	}

	withExpr := strings.TrimSpace(cypher[remainderStart:nextClauseIdx])
	boundVars := make(map[string]interface{})

	items := e.splitWithItems(withExpr)
	columns := make([]string, 0)
	values := make([]interface{}, 0)

	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		upperItem := strings.ToUpper(item)
		asIdx := strings.Index(upperItem, " AS ")
		var alias string
		var expr string
		if asIdx > 0 {
			expr = strings.TrimSpace(item[:asIdx])
			alias = strings.TrimSpace(item[asIdx+4:])
		} else {
			expr = item
			alias = item
		}

		val := e.evaluateExpressionWithContext(expr, make(map[string]*storage.Node), make(map[string]*storage.Edge))
		boundVars[alias] = val
		columns = append(columns, alias)
		values = append(values, val)
	}

	if nextClauseIdx < len(cypher) {
		remainder := strings.TrimSpace(cypher[nextClauseIdx:])
		return e.Execute(ctx, remainder, nil)
	}

	return &ExecuteResult{
		Columns: columns,
		Rows:    [][]interface{}{values},
	}, nil
}

// splitWithItems splits WITH expressions respecting nested brackets and quotes
func (e *StorageExecutor) splitWithItems(expr string) []string {
	var items []string
	var current strings.Builder
	depth := 0
	inQuote := false
	quoteChar := rune(0)

	for _, c := range expr {
		switch {
		case c == '\'' || c == '"':
			if !inQuote {
				inQuote = true
				quoteChar = c
			} else if c == quoteChar {
				inQuote = false
			}
			current.WriteRune(c)
		case c == '(' || c == '[' || c == '{':
			if !inQuote {
				depth++
			}
			current.WriteRune(c)
		case c == ')' || c == ']' || c == '}':
			if !inQuote {
				depth--
			}
			current.WriteRune(c)
		case c == ',' && depth == 0 && !inQuote:
			items = append(items, current.String())
			current.Reset()
		default:
			current.WriteRune(c)
		}
	}
	if current.Len() > 0 {
		items = append(items, current.String())
	}
	return items
}

// ========================================
// UNWIND Clause
// ========================================

// executeUnwind handles UNWIND clause - list expansion
func (e *StorageExecutor) executeUnwind(ctx context.Context, cypher string) (*ExecuteResult, error) {
	upper := strings.ToUpper(cypher)

	unwindIdx := strings.Index(upper, "UNWIND")
	if unwindIdx == -1 {
		return nil, fmt.Errorf("UNWIND clause not found")
	}

	asIdx := strings.Index(upper, " AS ")
	if asIdx == -1 {
		return nil, fmt.Errorf("UNWIND requires AS clause")
	}

	listExpr := strings.TrimSpace(cypher[unwindIdx+6 : asIdx])

	remainder := strings.TrimSpace(cypher[asIdx+4:])
	spaceIdx := strings.IndexAny(remainder, " \t\n")
	var variable string
	var restQuery string
	if spaceIdx > 0 {
		variable = remainder[:spaceIdx]
		restQuery = strings.TrimSpace(remainder[spaceIdx:])
	} else {
		variable = remainder
		restQuery = ""
	}

	list := e.evaluateExpressionWithContext(listExpr, make(map[string]*storage.Node), make(map[string]*storage.Edge))

	var items []interface{}
	switch v := list.(type) {
	case []interface{}:
		items = v
	case []string:
		items = make([]interface{}, len(v))
		for i, s := range v {
			items[i] = s
		}
	case []int64:
		items = make([]interface{}, len(v))
		for i, n := range v {
			items[i] = n
		}
	default:
		items = []interface{}{list}
	}

	if restQuery != "" && strings.HasPrefix(strings.ToUpper(restQuery), "RETURN") {
		result := &ExecuteResult{
			Columns: []string{variable},
			Rows:    make([][]interface{}, 0, len(items)),
		}
		for _, item := range items {
			result.Rows = append(result.Rows, []interface{}{item})
		}
		return result, nil
	}

	result := &ExecuteResult{
		Columns: []string{variable},
		Rows:    make([][]interface{}, 0, len(items)),
	}
	for _, item := range items {
		result.Rows = append(result.Rows, []interface{}{item})
	}
	return result, nil
}

// ========================================
// UNION Clause
// ========================================

// executeUnion handles UNION / UNION ALL
func (e *StorageExecutor) executeUnion(ctx context.Context, cypher string, unionAll bool) (*ExecuteResult, error) {
	upper := strings.ToUpper(cypher)

	var separator string
	if unionAll {
		separator = " UNION ALL "
	} else {
		separator = " UNION "
	}

	idx := strings.Index(upper, separator)
	if idx == -1 {
		return nil, fmt.Errorf("UNION clause not found")
	}

	query1 := strings.TrimSpace(cypher[:idx])
	query2 := strings.TrimSpace(cypher[idx+len(separator):])

	result1, err := e.Execute(ctx, query1, nil)
	if err != nil {
		return nil, fmt.Errorf("error in first UNION query: %w", err)
	}

	result2, err := e.Execute(ctx, query2, nil)
	if err != nil {
		return nil, fmt.Errorf("error in second UNION query: %w", err)
	}

	if len(result1.Columns) != len(result2.Columns) {
		return nil, fmt.Errorf("UNION queries must return the same number of columns")
	}

	combinedResult := &ExecuteResult{
		Columns: result1.Columns,
		Rows:    make([][]interface{}, 0, len(result1.Rows)+len(result2.Rows)),
	}

	combinedResult.Rows = append(combinedResult.Rows, result1.Rows...)

	if unionAll {
		combinedResult.Rows = append(combinedResult.Rows, result2.Rows...)
	} else {
		seen := make(map[string]bool)
		for _, row := range result1.Rows {
			key := fmt.Sprintf("%v", row)
			seen[key] = true
		}
		for _, row := range result2.Rows {
			key := fmt.Sprintf("%v", row)
			if !seen[key] {
				combinedResult.Rows = append(combinedResult.Rows, row)
				seen[key] = true
			}
		}
	}

	return combinedResult, nil
}

// ========================================
// OPTIONAL MATCH Clause
// ========================================

// executeOptionalMatch handles OPTIONAL MATCH - returns null for non-matches
func (e *StorageExecutor) executeOptionalMatch(ctx context.Context, cypher string) (*ExecuteResult, error) {
	upper := strings.ToUpper(cypher)
	optMatchIdx := strings.Index(upper, "OPTIONAL MATCH")
	if optMatchIdx == -1 {
		return nil, fmt.Errorf("OPTIONAL MATCH not found")
	}

	modifiedQuery := cypher[:optMatchIdx] + "MATCH" + cypher[optMatchIdx+14:]

	result, err := e.executeMatch(ctx, modifiedQuery)
	if err != nil || len(result.Rows) == 0 {
		nullRow := make([]interface{}, len(result.Columns))
		for i := range nullRow {
			nullRow[i] = nil
		}
		return &ExecuteResult{
			Columns: result.Columns,
			Rows:    [][]interface{}{nullRow},
		}, nil
	}

	return result, nil
}

// ========================================
// FOREACH Clause
// ========================================

// executeForeach handles FOREACH clause - iterate and perform updates
func (e *StorageExecutor) executeForeach(ctx context.Context, cypher string) (*ExecuteResult, error) {
	upper := strings.ToUpper(cypher)
	foreachIdx := strings.Index(upper, "FOREACH")
	if foreachIdx == -1 {
		return nil, fmt.Errorf("FOREACH clause not found")
	}

	parenStart := strings.Index(cypher[foreachIdx:], "(")
	if parenStart == -1 {
		return nil, fmt.Errorf("FOREACH requires parentheses")
	}
	parenStart += foreachIdx

	depth := 1
	parenEnd := parenStart + 1
	for parenEnd < len(cypher) && depth > 0 {
		if cypher[parenEnd] == '(' {
			depth++
		} else if cypher[parenEnd] == ')' {
			depth--
		}
		parenEnd++
	}

	inner := strings.TrimSpace(cypher[parenStart+1 : parenEnd-1])

	inIdx := strings.Index(strings.ToUpper(inner), " IN ")
	if inIdx == -1 {
		return nil, fmt.Errorf("FOREACH requires IN clause")
	}

	variable := strings.TrimSpace(inner[:inIdx])
	remainder := strings.TrimSpace(inner[inIdx+4:])

	pipeIdx := strings.Index(remainder, "|")
	if pipeIdx == -1 {
		return nil, fmt.Errorf("FOREACH requires | separator")
	}

	listExpr := strings.TrimSpace(remainder[:pipeIdx])
	updateClause := strings.TrimSpace(remainder[pipeIdx+1:])

	list := e.evaluateExpressionWithContext(listExpr, make(map[string]*storage.Node), make(map[string]*storage.Edge))

	var items []interface{}
	switch v := list.(type) {
	case []interface{}:
		items = v
	default:
		items = []interface{}{list}
	}

	result := &ExecuteResult{
		Columns: []string{},
		Rows:    [][]interface{}{},
		Stats:   &QueryStats{},
	}

	for _, item := range items {
		itemStr := e.valueToLiteral(item)
		substituted := strings.ReplaceAll(updateClause, variable, itemStr)

		updateResult, err := e.Execute(ctx, substituted, nil)
		if err == nil && updateResult.Stats != nil {
			result.Stats.NodesCreated += updateResult.Stats.NodesCreated
			result.Stats.PropertiesSet += updateResult.Stats.PropertiesSet
			result.Stats.RelationshipsCreated += updateResult.Stats.RelationshipsCreated
		}
	}

	return result, nil
}

// ========================================
// LOAD CSV Clause
// ========================================

// executeLoadCSV handles LOAD CSV clause
func (e *StorageExecutor) executeLoadCSV(ctx context.Context, cypher string) (*ExecuteResult, error) {
	return nil, fmt.Errorf("LOAD CSV is not supported in NornicDB embedded mode")
}
