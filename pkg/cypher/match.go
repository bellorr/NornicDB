// MATCH clause implementation for NornicDB.
// This file contains MATCH execution, aggregation, ordering, and filtering.

package cypher

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/orneryd/nornicdb/pkg/storage"
)
func (e *StorageExecutor) executeMatch(ctx context.Context, cypher string) (*ExecuteResult, error) {
	result := &ExecuteResult{
		Columns: []string{},
		Rows:    [][]interface{}{},
		Stats:   &QueryStats{},
	}

	upper := strings.ToUpper(cypher)

	// Extract return variables - use word boundary detection to avoid matching substrings like "RemoveReturn"
	returnIdx := findKeywordIndex(cypher, "RETURN")
	if returnIdx == -1 {
		// No RETURN clause - just match and return count
		result.Columns = []string{"matched"}
		result.Rows = [][]interface{}{{true}}
		return result, nil
	}

	// Parse RETURN part (everything after RETURN, before ORDER BY/SKIP/LIMIT)
	returnPart := cypher[returnIdx+6:]

	// Find end of RETURN clause
	returnEndIdx := len(returnPart)
	for _, keyword := range []string{" ORDER BY ", " SKIP ", " LIMIT "} {
		idx := strings.Index(strings.ToUpper(returnPart), keyword)
		if idx >= 0 && idx < returnEndIdx {
			returnEndIdx = idx
		}
	}
	returnClause := strings.TrimSpace(returnPart[:returnEndIdx])

	// Check for DISTINCT
	distinct := false
	if strings.HasPrefix(strings.ToUpper(returnClause), "DISTINCT ") {
		distinct = true
		returnClause = strings.TrimSpace(returnClause[9:])
	}

	// Parse RETURN items
	returnItems := e.parseReturnItems(returnClause)
	result.Columns = make([]string, len(returnItems))
	for i, item := range returnItems {
		if item.alias != "" {
			result.Columns[i] = item.alias
		} else {
			result.Columns[i] = item.expr
		}
	}

	// Check if this is an aggregation query
	hasAggregation := false
	for _, item := range returnItems {
		upperExpr := strings.ToUpper(item.expr)
		if strings.HasPrefix(upperExpr, "COUNT(") ||
			strings.HasPrefix(upperExpr, "SUM(") ||
			strings.HasPrefix(upperExpr, "AVG(") ||
			strings.HasPrefix(upperExpr, "MIN(") ||
			strings.HasPrefix(upperExpr, "MAX(") ||
			strings.HasPrefix(upperExpr, "COLLECT(") {
			hasAggregation = true
			break
		}
	}

	// Extract pattern between MATCH and WHERE/RETURN
	matchPart := cypher[5:] // Skip "MATCH"
	whereIdx := findKeywordIndex(cypher, "WHERE")
	if whereIdx > 0 {
		matchPart = cypher[5:whereIdx]
	} else if returnIdx > 0 {
		matchPart = cypher[5:returnIdx]
	}
	matchPart = strings.TrimSpace(matchPart)

	// Check for relationship pattern: (a)-[r:TYPE]->(b) or (a)<-[r]-(b)
	if strings.Contains(matchPart, "-[") || strings.Contains(matchPart, "]-") {
		// Extract WHERE clause if present
		var whereClause string
		if whereIdx > 0 {
			whereClause = strings.TrimSpace(cypher[whereIdx+5 : returnIdx])
		}
		return e.executeMatchWithRelationships(matchPart, whereClause, returnItems)
	}

	// Parse node pattern
	nodePattern := e.parseNodePattern(matchPart)

	// Get matching nodes
	var nodes []*storage.Node
	var err error

	if len(nodePattern.labels) > 0 {
		nodes, err = e.storage.GetNodesByLabel(nodePattern.labels[0])
	} else {
		nodes, err = e.storage.AllNodes()
	}
	if err != nil {
		return nil, fmt.Errorf("storage error: %w", err)
	}

	// Apply WHERE filter if present
	if whereIdx > 0 {
		// Find end of WHERE clause (before RETURN)
		wherePart := cypher[whereIdx+5 : returnIdx]
		nodes = e.filterNodes(nodes, nodePattern.variable, strings.TrimSpace(wherePart))
	}

	// Handle aggregation queries
	if hasAggregation {
		return e.executeAggregation(nodes, nodePattern.variable, returnItems, result)
	}

	// Parse ORDER BY
	orderByIdx := strings.Index(upper, "ORDER BY")
	if orderByIdx > 0 {
		orderPart := upper[orderByIdx+8:]
		// Find end
		endIdx := len(orderPart)
		for _, kw := range []string{" SKIP ", " LIMIT "} {
			if idx := strings.Index(orderPart, kw); idx >= 0 && idx < endIdx {
				endIdx = idx
			}
		}
		orderExpr := strings.TrimSpace(cypher[orderByIdx+8 : orderByIdx+8+endIdx])
		nodes = e.orderNodes(nodes, nodePattern.variable, orderExpr)
	}

	// Parse SKIP
	skipIdx := strings.Index(upper, "SKIP")
	skip := 0
	if skipIdx > 0 {
		skipPart := strings.TrimSpace(cypher[skipIdx+4:])
		skipPart = strings.Split(skipPart, " ")[0]
		if s, err := strconv.Atoi(skipPart); err == nil {
			skip = s
		}
	}

	// Parse LIMIT
	limitIdx := strings.Index(upper, "LIMIT")
	limit := -1
	if limitIdx > 0 {
		limitPart := strings.TrimSpace(cypher[limitIdx+5:])
		limitPart = strings.Split(limitPart, " ")[0]
		if l, err := strconv.Atoi(limitPart); err == nil {
			limit = l
		}
	}

	// Build result rows with SKIP and LIMIT
	seen := make(map[string]bool) // For DISTINCT
	rowCount := 0
	for i, node := range nodes {
		// Apply SKIP
		if i < skip {
			continue
		}

		// Apply LIMIT
		if limit >= 0 && rowCount >= limit {
			break
		}

		row := make([]interface{}, len(returnItems))
		for j, item := range returnItems {
			row[j] = e.resolveReturnItem(item, nodePattern.variable, node)
		}

		// Handle DISTINCT
		if distinct {
			key := fmt.Sprintf("%v", row)
			if seen[key] {
				continue
			}
			seen[key] = true
		}

		result.Rows = append(result.Rows, row)
		rowCount++
	}

	return result, nil
}

// executeAggregation handles aggregate functions (COUNT, SUM, AVG, etc.)
func (e *StorageExecutor) executeAggregation(nodes []*storage.Node, variable string, items []returnItem, result *ExecuteResult) (*ExecuteResult, error) {
	row := make([]interface{}, len(items))

	// Case-insensitive regex patterns for aggregation functions
	countPropRe := regexp.MustCompile(`(?i)COUNT\((\w+)\.(\w+)\)`)
	sumRe := regexp.MustCompile(`(?i)SUM\((\w+)\.(\w+)\)`)
	avgRe := regexp.MustCompile(`(?i)AVG\((\w+)\.(\w+)\)`)
	minRe := regexp.MustCompile(`(?i)MIN\((\w+)\.(\w+)\)`)
	maxRe := regexp.MustCompile(`(?i)MAX\((\w+)\.(\w+)\)`)
	collectRe := regexp.MustCompile(`(?i)COLLECT\((\w+)(?:\.(\w+))?\)`)

	for i, item := range items {
		upperExpr := strings.ToUpper(item.expr)

		switch {
		case strings.HasPrefix(upperExpr, "COUNT("):
			// COUNT(*) or COUNT(n)
			if strings.Contains(upperExpr, "*") || strings.Contains(upperExpr, "("+strings.ToUpper(variable)+")") {
				row[i] = int64(len(nodes))
			} else {
				// COUNT(n.property) - count non-null values
				propMatch := countPropRe.FindStringSubmatch(item.expr)
				if len(propMatch) == 3 {
					count := int64(0)
					for _, node := range nodes {
						if _, exists := node.Properties[propMatch[2]]; exists {
							count++
						}
					}
					row[i] = count
				} else {
					row[i] = int64(len(nodes))
				}
			}

		case strings.HasPrefix(upperExpr, "SUM("):
			propMatch := sumRe.FindStringSubmatch(item.expr)
			if len(propMatch) == 3 {
				sum := float64(0)
				for _, node := range nodes {
					if val, exists := node.Properties[propMatch[2]]; exists {
						if num, ok := toFloat64(val); ok {
							sum += num
						}
					}
				}
				row[i] = sum
			} else {
				row[i] = float64(0)
			}

		case strings.HasPrefix(upperExpr, "AVG("):
			propMatch := avgRe.FindStringSubmatch(item.expr)
			if len(propMatch) == 3 {
				sum := float64(0)
				count := 0
				for _, node := range nodes {
					if val, exists := node.Properties[propMatch[2]]; exists {
						if num, ok := toFloat64(val); ok {
							sum += num
							count++
						}
					}
				}
				if count > 0 {
					row[i] = sum / float64(count)
				} else {
					row[i] = nil
				}
			} else {
				row[i] = nil
			}

		case strings.HasPrefix(upperExpr, "MIN("):
			propMatch := minRe.FindStringSubmatch(item.expr)
			if len(propMatch) == 3 {
				var min *float64
				for _, node := range nodes {
					if val, exists := node.Properties[propMatch[2]]; exists {
						if num, ok := toFloat64(val); ok {
							if min == nil || num < *min {
								minVal := num
								min = &minVal
							}
						}
					}
				}
				if min != nil {
					row[i] = *min
				} else {
					row[i] = nil
				}
			} else {
				row[i] = nil
			}

		case strings.HasPrefix(upperExpr, "MAX("):
			propMatch := maxRe.FindStringSubmatch(item.expr)
			if len(propMatch) == 3 {
				var max *float64
				for _, node := range nodes {
					if val, exists := node.Properties[propMatch[2]]; exists {
						if num, ok := toFloat64(val); ok {
							if max == nil || num > *max {
								maxVal := num
								max = &maxVal
							}
						}
					}
				}
				if max != nil {
					row[i] = *max
				} else {
					row[i] = nil
				}
			} else {
				row[i] = nil
			}

		case strings.HasPrefix(upperExpr, "COLLECT("):
			propMatch := collectRe.FindStringSubmatch(item.expr)
			collected := make([]interface{}, 0)
			if len(propMatch) >= 2 {
				for _, node := range nodes {
					if len(propMatch) == 3 && propMatch[2] != "" {
						// COLLECT(n.property)
						if val, exists := node.Properties[propMatch[2]]; exists {
							collected = append(collected, val)
						}
					} else {
						// COLLECT(n)
						collected = append(collected, map[string]interface{}{
							"id":         string(node.ID),
							"labels":     node.Labels,
							"properties": node.Properties,
						})
					}
				}
			}
			row[i] = collected

		default:
			// Non-aggregate in aggregation query - return first value
			if len(nodes) > 0 {
				row[i] = e.resolveReturnItem(item, variable, nodes[0])
			} else {
				row[i] = nil
			}
		}
	}

	result.Rows = [][]interface{}{row}
	return result, nil
}

// orderNodes sorts nodes by the given expression
func (e *StorageExecutor) orderNodes(nodes []*storage.Node, variable, orderExpr string) []*storage.Node {
	// Parse: n.property [ASC|DESC]
	desc := strings.HasSuffix(strings.ToUpper(orderExpr), " DESC")
	orderExpr = strings.TrimSuffix(strings.TrimSuffix(orderExpr, " DESC"), " ASC")
	orderExpr = strings.TrimSpace(orderExpr)

	// Extract property name
	var propName string
	if strings.HasPrefix(orderExpr, variable+".") {
		propName = orderExpr[len(variable)+1:]
	} else {
		propName = orderExpr
	}

	// Sort using a simple bubble sort (could use sort.Slice for efficiency)
	sorted := make([]*storage.Node, len(nodes))
	copy(sorted, nodes)

	for i := 0; i < len(sorted)-1; i++ {
		for j := 0; j < len(sorted)-i-1; j++ {
			val1, _ := sorted[j].Properties[propName]
			val2, _ := sorted[j+1].Properties[propName]

			shouldSwap := false
			num1, ok1 := toFloat64(val1)
			num2, ok2 := toFloat64(val2)

			if ok1 && ok2 {
				if desc {
					shouldSwap = num1 < num2
				} else {
					shouldSwap = num1 > num2
				}
			} else {
				str1 := fmt.Sprintf("%v", val1)
				str2 := fmt.Sprintf("%v", val2)
				if desc {
					shouldSwap = str1 < str2
				} else {
					shouldSwap = str1 > str2
				}
			}

			if shouldSwap {
				sorted[j], sorted[j+1] = sorted[j+1], sorted[j]
			}
		}
	}

	return sorted
}

// executeCreate handles CREATE queries.
