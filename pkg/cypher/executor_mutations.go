package cypher

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/orneryd/nornicdb/pkg/storage"
)

// ========================================
// WITH Clause
// ========================================

// executeMatch handles MATCH queries.
func (e *StorageExecutor) parseMergePattern(pattern string) (string, []string, map[string]interface{}, error) {
	pattern = strings.TrimSpace(pattern)
	if !strings.HasPrefix(pattern, "(") || !strings.HasSuffix(pattern, ")") {
		return "", nil, nil, fmt.Errorf("invalid pattern: %s", pattern)
	}
	pattern = pattern[1 : len(pattern)-1]

	// Extract variable name and labels
	varName := ""
	labels := []string{}
	props := make(map[string]interface{})

	// Find properties block
	propsStart := strings.Index(pattern, "{")
	labelPart := pattern
	if propsStart > 0 {
		labelPart = pattern[:propsStart]
		propsEnd := strings.LastIndex(pattern, "}")
		if propsEnd > propsStart {
			propsStr := pattern[propsStart+1 : propsEnd]
			props = e.parseProperties(propsStr)
		}
	}

	// Parse variable and labels
	parts := strings.Split(labelPart, ":")
	if len(parts) > 0 {
		varName = strings.TrimSpace(parts[0])
	}
	for i := 1; i < len(parts); i++ {
		label := strings.TrimSpace(parts[i])
		if label != "" {
			labels = append(labels, label)
		}
	}

	return varName, labels, props, nil
}

// nodeToMap converts a storage.Node to a map for result output.
// Filters out internal properties like embeddings which are huge.
// Properties are included at the top level for Neo4j compatibility.
// Embeddings are replaced with a summary showing status and dimensions.

// executeDelete handles DELETE queries.
func (e *StorageExecutor) executeDelete(ctx context.Context, cypher string) (*ExecuteResult, error) {
	// Substitute parameters AFTER routing to avoid keyword detection issues
	if params := getParamsFromContext(ctx); params != nil {
		cypher = e.substituteParams(cypher, params)
	}

	result := &ExecuteResult{
		Columns: []string{},
		Rows:    [][]interface{}{},
		Stats:   &QueryStats{},
	}

	// Parse: MATCH (n) WHERE ... DELETE n or DETACH DELETE n
	upper := strings.ToUpper(cypher)
	detach := strings.Contains(upper, "DETACH")

	// Get MATCH part - use word boundary detection
	matchIdx := findKeywordIndex(cypher, "MATCH")

	// Find the delete clause - could be "DELETE" or "DETACH DELETE"
	// IMPORTANT: Search for "DETACH DELETE" first (longer string) to avoid matching just "DETACH"
	var deleteIdx int
	if detach {
		// Try "DETACH DELETE" first (longer, more specific)
		deleteIdx = findKeywordIndex(cypher, "DETACH DELETE")
		if deleteIdx == -1 {
			// Fallback to just "DETACH" if "DETACH DELETE" not found
			deleteIdx = findKeywordIndex(cypher, "DETACH")
		}
	} else {
		deleteIdx = findKeywordIndex(cypher, "DELETE")
	}

	if matchIdx == -1 || deleteIdx == -1 {
		return nil, fmt.Errorf("DELETE requires a MATCH clause first (e.g., MATCH (n) DELETE n)")
	}

	// Parse the delete target variable(s) - e.g., "DELETE n" or "DETACH DELETE n"
	// Preserve original case of variable names
	deleteClause := strings.TrimSpace(cypher[deleteIdx:])
	upperDeleteClause := strings.ToUpper(deleteClause)

	// Handle DETACH DELETE - must check for "DETACH DELETE " first (longer string)
	if detach {
		if strings.HasPrefix(upperDeleteClause, "DETACH DELETE ") {
			// Found "DETACH DELETE " - remove it to get variable name
			deleteClause = deleteClause[14:] // len("DETACH DELETE ")
		} else if strings.HasPrefix(upperDeleteClause, "DETACH ") {
			// Found just "DETACH " - this shouldn't happen if we found "DETACH DELETE" above
			// but handle it for safety
			deleteClause = deleteClause[7:] // len("DETACH ")
		}
	}

	// After handling DETACH, check for remaining "DELETE " prefix
	upperDeleteClause = strings.ToUpper(deleteClause)
	if strings.HasPrefix(upperDeleteClause, "DELETE ") {
		deleteClause = deleteClause[7:] // len("DELETE ")
	}

	// Strip RETURN clause from deleteVars if present
	returnInDelete := findKeywordIndex(deleteClause, "RETURN")
	if returnInDelete > 0 {
		deleteClause = strings.TrimSpace(deleteClause[:returnInDelete])
	}
	deleteVars := strings.TrimSpace(deleteClause)

	if deleteVars == "" {
		return nil, fmt.Errorf("DELETE clause must specify variable(s) to delete (e.g., DELETE n)")
	}

	// Execute the match first - return the specific variables being deleted
	// Can't use RETURN * because it returns literal "*" instead of expanding
	// For DETACH DELETE, we need to ensure deleteIdx points to the start of "DETACH DELETE"
	// not just "DETACH", so the match query doesn't include "DETACH"
	if detach && deleteIdx > 0 {
		// Double-check: if we found "DETACH DELETE", make sure deleteIdx points to it
		// If the substring starting at deleteIdx is "DETACH DELETE", we're good
		// If it's just "DETACH", we need to adjust
		checkSubstring := strings.ToUpper(strings.TrimSpace(cypher[deleteIdx:]))
		if strings.HasPrefix(checkSubstring, "DETACH ") && !strings.HasPrefix(checkSubstring, "DETACH DELETE ") {
			// We found "DETACH" but not "DETACH DELETE" - this is an error
			return nil, fmt.Errorf("DETACH DELETE requires both DETACH and DELETE keywords together")
		}
	}

	matchQuery := cypher[matchIdx:deleteIdx] + " RETURN " + deleteVars
	matchResult, err := e.executeMatch(ctx, matchQuery)
	if err != nil {
		return nil, err
	}

	// Delete matched nodes and/or relationships
	for _, row := range matchResult.Rows {
		for _, val := range row {
			// Try to extract node ID or edge ID
			var nodeID string
			var edgeID string

			switch v := val.(type) {
			case map[string]interface{}:
				// Check if it's a relationship or node by looking for internal ID keys
				// Relationships have _edgeId, nodes have _nodeId
				if id, ok := v["_edgeId"].(string); ok {
					edgeID = id
				} else if id, ok := v["_nodeId"].(string); ok {
					nodeID = id
				}
			case *storage.Node:
				// Direct node pointer
				nodeID = string(v.ID)
			case *storage.Edge:
				// Direct edge pointer
				edgeID = string(v.ID)
			case string:
				// Just an ID string - could be node or edge
				nodeID = v
			}

			// Handle relationship deletion
			if edgeID != "" {
				if err := e.storage.DeleteEdge(storage.EdgeID(edgeID)); err == nil {
					result.Stats.RelationshipsDeleted++
				}
				continue
			}

			// Handle node deletion
			if nodeID == "" {
				continue
			}

			if detach {
				// Count edges that will be deleted with the node (for stats)
				// DeleteNode() automatically deletes connected edges and updates counts internally
				// We just need to count them for the result stats
				outgoingEdges, _ := e.storage.GetOutgoingEdges(storage.NodeID(nodeID))
				incomingEdges, _ := e.storage.GetIncomingEdges(storage.NodeID(nodeID))
				edgesCount := len(outgoingEdges) + len(incomingEdges)

				// DeleteNode() handles edge deletion internally and updates internal counts
				if err := e.storage.DeleteNode(storage.NodeID(nodeID)); err == nil {
					result.Stats.NodesDeleted++
					result.Stats.RelationshipsDeleted += edgesCount
				}
			} else {
				// Non-detach delete - just delete the node (will fail if edges exist)
				if err := e.storage.DeleteNode(storage.NodeID(nodeID)); err == nil {
					result.Stats.NodesDeleted++
				}
			}
		}
	}

	// Handle RETURN clause (e.g., RETURN count(*) as deleted)
	returnIdx := findKeywordIndex(cypher, "RETURN")
	if returnIdx > 0 {
		returnPart := strings.TrimSpace(cypher[returnIdx+6:])
		returnItems := e.parseReturnItems(returnPart)
		result.Columns = make([]string, len(returnItems))
		row := make([]interface{}, len(returnItems))

		for i, item := range returnItems {
			if item.alias != "" {
				result.Columns[i] = item.alias
			} else {
				result.Columns[i] = item.expr
			}

			// Handle count(*) - return number of deleted items
			upperExpr := strings.ToUpper(item.expr)
			if strings.HasPrefix(upperExpr, "COUNT(") {
				row[i] = int64(result.Stats.NodesDeleted + result.Stats.RelationshipsDeleted)
			}
		}
		result.Rows = [][]interface{}{row}
	}

	return result, nil
}

// executeSet handles MATCH ... SET queries.
func (e *StorageExecutor) executeSet(ctx context.Context, cypher string) (*ExecuteResult, error) {
	// Substitute parameters AFTER routing to avoid keyword detection issues
	if params := getParamsFromContext(ctx); params != nil {
		cypher = e.substituteParams(cypher, params)
	}

	result := &ExecuteResult{
		Columns: []string{},
		Rows:    [][]interface{}{},
		Stats:   &QueryStats{},
	}

	// Normalize whitespace for index finding (newlines/tabs become spaces)
	normalized := strings.ReplaceAll(strings.ReplaceAll(cypher, "\n", " "), "\t", " ")

	// Use word boundary detection to avoid matching substrings
	matchIdx := findKeywordIndex(normalized, "MATCH")
	setIdx := findKeywordIndex(normalized, "SET")
	returnIdx := findKeywordIndex(normalized, "RETURN")

	if matchIdx == -1 || setIdx == -1 {
		return nil, fmt.Errorf("SET requires a MATCH clause first (e.g., MATCH (n) SET n.property = value)")
	}

	// Execute the match first (use normalized query for slicing)
	// Extract variable names from the MATCH pattern to handle RETURN * correctly
	matchPattern := strings.TrimSpace(normalized[matchIdx+5 : setIdx]) // Skip "MATCH"
	varNames := e.extractVariableNamesFromPattern(matchPattern)

	var matchQuery string
	if len(varNames) > 0 {
		// Use explicit variable names instead of RETURN * for better handling
		matchQuery = normalized[matchIdx:setIdx] + " RETURN " + strings.Join(varNames, ", ")
	} else {
		matchQuery = normalized[matchIdx:setIdx] + " RETURN *"
	}
	matchResult, err := e.executeMatch(ctx, matchQuery)
	if err != nil {
		return nil, err
	}

	// Parse SET clause: SET n.property = value or SET n += $properties
	// "SET " is 4 characters, so setIdx + 4 skips past "SET "
	var setPart string
	if returnIdx > 0 {
		setPart = strings.TrimSpace(normalized[setIdx+4 : returnIdx])
	} else {
		setPart = strings.TrimSpace(normalized[setIdx+4:])
	}

	// Check for property merge operator: n += $properties
	if strings.Contains(setPart, "+=") {
		return e.executeSetMerge(ctx, matchResult, setPart, result, cypher, returnIdx)
	}

	// Split SET clause into individual assignments, respecting brackets
	// e.g., "n.embedding = [0.1, 0.2], n.dim = 4" -> ["n.embedding = [0.1, 0.2]", "n.dim = 4"]
	assignments := e.splitSetAssignmentsRespectingBrackets(setPart)

	if len(assignments) == 0 || (len(assignments) == 1 && strings.TrimSpace(assignments[0]) == "") {
		return nil, fmt.Errorf("SET clause requires at least one assignment")
	}

	var variable string
	validAssignments := 0
	for _, assignment := range assignments {
		assignment = strings.TrimSpace(assignment)
		if assignment == "" {
			continue
		}

		// Check for label assignment: n:Label (no = sign, has : for label)
		eqIdx := strings.Index(assignment, "=")
		if eqIdx == -1 {
			// Could be a label assignment like "n:Label"
			colonIdx := strings.Index(assignment, ":")
			if colonIdx > 0 {
				// This is a label assignment
				labelVar := strings.TrimSpace(assignment[:colonIdx])
				labelName := strings.TrimSpace(assignment[colonIdx+1:])
				if labelVar != "" && labelName != "" {
					validAssignments++
					variable = labelVar
					// Add label to matched nodes
					for _, row := range matchResult.Rows {
						for _, val := range row {
							node, ok := val.(*storage.Node)
							if !ok || node == nil {
								continue
							}
							// Add label if not already present
							hasLabel := false
							for _, l := range node.Labels {
								if l == labelName {
									hasLabel = true
									break
								}
							}
							if !hasLabel {
								node.Labels = append(node.Labels, labelName)
								if err := e.storage.UpdateNode(node); err == nil {
									result.Stats.LabelsAdded++
								}
							}
						}
					}
					continue
				}
			}
			return nil, fmt.Errorf("invalid SET assignment: %q (expected n.property = value or n:Label)", assignment)
		}

		left := strings.TrimSpace(assignment[:eqIdx])
		right := strings.TrimSpace(assignment[eqIdx+1:])

		// Extract variable and property
		parts := strings.SplitN(left, ".", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid SET assignment: %q (expected variable.property)", left)
		}
		validAssignments++
		variable = parts[0]
		propName := parts[1]
		propValue := e.parseValue(right)

		// Update matched nodes
		for _, row := range matchResult.Rows {
			for _, val := range row {
				node, ok := val.(*storage.Node)
				if !ok || node == nil {
					continue
				}
				// Use setNodeProperty to properly route "embedding" to node.ChunkEmbeddings (always stored as array of arrays)
				setNodeProperty(node, propName, propValue)
				if err := e.storage.UpdateNode(node); err == nil {
					result.Stats.PropertiesSet++
				}
			}
		}
	}
	_ = variable // silence unused warning

	// Handle RETURN
	if returnIdx > 0 {
		returnPart := strings.TrimSpace(cypher[returnIdx+6:])
		returnItems := e.parseReturnItems(returnPart)
		result.Columns = make([]string, len(returnItems))
		for i, item := range returnItems {
			if item.alias != "" {
				result.Columns[i] = item.alias
			} else {
				result.Columns[i] = item.expr
			}
		}

		// Return updated nodes
		// Build a map of variable names to nodes from match result columns
		// This handles multiple variables (e.g., n, m) correctly
		for _, row := range matchResult.Rows {
			// Map column names to values in this row
			varMap := make(map[string]*storage.Node)
			for i, colName := range matchResult.Columns {
				if i < len(row) {
					if node, ok := row[i].(*storage.Node); ok && node != nil {
						varMap[colName] = node
					}
				}
			}

			// Build a single row with all return items
			newRow := make([]interface{}, len(returnItems))
			for j, item := range returnItems {
				// Extract variable name from return item expression
				// Handle cases like: "n", "n.name", "id(n)", etc.
				varName := extractVariableNameFromReturnItem(item.expr)
				if varName != "" {
					if node, ok := varMap[varName]; ok {
						newRow[j] = e.resolveReturnItem(item, varName, node)
						continue
					}
				}
				// Fallback: try to resolve with the first variable (for backward compatibility)
				if variable != "" {
					if node, ok := varMap[variable]; ok {
						newRow[j] = e.resolveReturnItem(item, variable, node)
						continue
					}
				}
				// If no variable matches, try to evaluate expression with all variables
				newRow[j] = e.evaluateExpressionWithContext(item.expr, varMap, make(map[string]*storage.Edge))
			}
			result.Rows = append(result.Rows, newRow)
		}
	}

	return result, nil
}

// executeSetMerge handles SET n += $properties for property merging.
// This implements the Cypher property merge operator which merges properties from a map
// or parameter into existing node properties.
//
// Example:
//
//	MATCH (n:Person) SET n += {age: 30, city: 'NYC'}  // Inline map
//	MATCH (n:Person) SET n += $props                  // Parameter map
//
// Parameters are retrieved from context (stored during query execution).
func (e *StorageExecutor) executeSetMerge(ctx context.Context, matchResult *ExecuteResult, setPart string, result *ExecuteResult, cypher string, returnIdx int) (*ExecuteResult, error) {
	// Parse: n += $properties or n += {key: value}
	plusEqIdx := strings.Index(setPart, "+=")
	if plusEqIdx == -1 {
		return nil, fmt.Errorf("expected += operator")
	}

	variable := strings.TrimSpace(setPart[:plusEqIdx])
	right := strings.TrimSpace(setPart[plusEqIdx+2:])

	// Parse the properties to merge
	var propsToMerge map[string]interface{}

	if strings.HasPrefix(right, "{") {
		// Inline properties: {key: value, ...}
		propsToMerge = e.parseProperties(right)
	} else if strings.HasPrefix(right, "$") {
		// Parameter reference: $properties
		// Extract parameter name (remove $ prefix)
		paramName := strings.TrimSpace(right[1:])
		if paramName == "" {
			return nil, fmt.Errorf("SET += requires a valid parameter name after $")
		}

		// Retrieve parameters from context
		params := getParamsFromContext(ctx)
		if params == nil {
			return nil, fmt.Errorf("SET += parameter $%s requires parameters to be provided", paramName)
		}

		// Look up the parameter value
		paramValue, exists := params[paramName]
		if !exists {
			return nil, fmt.Errorf("SET += parameter $%s not found in provided parameters", paramName)
		}

		// Validate that the parameter value is a map
		propsMap, ok := paramValue.(map[string]interface{})
		if !ok {
			// Try to convert from map[interface{}]interface{} (common when parsing JSON)
			if genericMap, ok := paramValue.(map[interface{}]interface{}); ok {
				propsMap = make(map[string]interface{})
				for k, v := range genericMap {
					keyStr, ok := k.(string)
					if !ok {
						return nil, fmt.Errorf("SET += parameter $%s must be a map with string keys, got key type %T", paramName, k)
					}
					propsMap[keyStr] = v
				}
			} else {
				return nil, fmt.Errorf("SET += parameter $%s must be a map, got type %T", paramName, paramValue)
			}
		}

		propsToMerge = propsMap
	} else {
		return nil, fmt.Errorf("SET += requires a map or parameter (got: %q)", right)
	}

	// Collect updated nodes for RETURN
	var updatedNodes []*storage.Node

	// Update matched nodes
	for _, row := range matchResult.Rows {
		for _, val := range row {
			node, ok := val.(*storage.Node)
			if !ok || node == nil {
				continue
			}

			// Merge properties (new values override existing)
			for k, v := range propsToMerge {
				setNodeProperty(node, k, v)
				result.Stats.PropertiesSet++
			}
			_ = e.storage.UpdateNode(node)
			updatedNodes = append(updatedNodes, node)
		}
	}

	// Handle RETURN clause
	if returnIdx > 0 {
		returnPart := strings.TrimSpace(cypher[returnIdx+6:])
		returnItems := e.parseReturnItems(returnPart)
		result.Columns = make([]string, len(returnItems))
		for i, item := range returnItems {
			if item.alias != "" {
				result.Columns[i] = item.alias
			} else {
				result.Columns[i] = item.expr
			}
		}

		// Return updated nodes (Neo4j compatible: return *storage.Node)
		for _, storageNode := range updatedNodes {
			newRow := make([]interface{}, len(returnItems))
			for j, item := range returnItems {
				newRow[j] = e.resolveReturnItem(item, variable, storageNode)
			}
			result.Rows = append(result.Rows, newRow)
		}
	} else {
		// No RETURN clause - return matched count
		result.Columns = []string{"matched"}
		result.Rows = [][]interface{}{{len(matchResult.Rows)}}
	}

	return result, nil
}

// executeRemove handles MATCH ... REMOVE queries for property removal.
// Syntax: MATCH (n:Label) REMOVE n.property [, n.property2] [RETURN ...]
func (e *StorageExecutor) executeRemove(ctx context.Context, cypher string) (*ExecuteResult, error) {
	// Substitute parameters AFTER routing to avoid keyword detection issues
	if params := getParamsFromContext(ctx); params != nil {
		cypher = e.substituteParams(cypher, params)
	}

	result := &ExecuteResult{
		Columns: []string{},
		Rows:    [][]interface{}{},
		Stats:   &QueryStats{},
	}

	// Normalize whitespace
	normalized := strings.ReplaceAll(strings.ReplaceAll(cypher, "\n", " "), "\t", " ")

	// Use word boundary detection to avoid matching substrings
	matchIdx := findKeywordIndex(normalized, "MATCH")
	removeIdx := findKeywordIndex(normalized, "REMOVE")
	returnIdx := findKeywordIndex(normalized, "RETURN")

	if matchIdx == -1 || removeIdx == -1 {
		return nil, fmt.Errorf("REMOVE requires a MATCH clause first (e.g., MATCH (n) REMOVE n.property)")
	}

	// Execute the match first
	matchQuery := normalized[matchIdx:removeIdx] + " RETURN *"
	matchResult, err := e.executeMatch(ctx, matchQuery)
	if err != nil {
		return nil, err
	}

	// Parse REMOVE clause: REMOVE n.prop1, n.prop2
	var removePart string
	removeLen := len("REMOVE")
	if returnIdx > 0 && returnIdx > removeIdx {
		removePart = strings.TrimSpace(normalized[removeIdx+removeLen : returnIdx])
	} else {
		removePart = strings.TrimSpace(normalized[removeIdx+removeLen:])
	}

	// Split by comma and parse each property to remove
	propsToRemove := e.parseRemoveProperties(removePart)

	// Update matched nodes
	for _, row := range matchResult.Rows {
		for _, val := range row {
			node, ok := val.(*storage.Node)
			if !ok || node == nil {
				continue
			}
			// Remove specified properties
			for _, prop := range propsToRemove {
				if _, exists := node.Properties[prop]; exists {
					delete(node.Properties, prop)
					result.Stats.PropertiesSet++ // Neo4j counts removals as properties set
				}
			}
			_ = e.storage.UpdateNode(node)
		}
	}

	// Handle RETURN
	if returnIdx > 0 && returnIdx > removeIdx {
		returnPart := strings.TrimSpace(normalized[returnIdx+6:])
		returnItems := e.parseReturnItems(returnPart)
		result.Columns = make([]string, len(returnItems))
		for i, item := range returnItems {
			if item.alias != "" {
				result.Columns[i] = item.alias
			} else {
				result.Columns[i] = item.expr
			}
		}
		// Return updated nodes
		for _, row := range matchResult.Rows {
			for _, val := range row {
				node, ok := val.(*storage.Node)
				if !ok || node == nil {
					continue
				}
				resultRow := make([]interface{}, len(returnItems))
				for i, item := range returnItems {
					resultRow[i] = e.resolveReturnItem(item, "n", node)
				}
				result.Rows = append(result.Rows, resultRow)
			}
		}
	}

	return result, nil
}

// parseRemoveProperties parses "n.prop1, n.prop2, m.prop3" into property names
func (e *StorageExecutor) parseRemoveProperties(removePart string) []string {
	var props []string
	parts := strings.Split(removePart, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if dotIdx := strings.Index(part, "."); dotIdx >= 0 {
			propName := strings.TrimSpace(part[dotIdx+1:])
			if propName != "" {
				props = append(props, propName)
			}
		}
	}
	return props
}

// executeCall handles CALL procedure queries.

// smartSplitReturnItems splits a RETURN clause by commas, but respects:
// - CASE/END boundaries
// - Parentheses (function calls)
// - Curly braces (map projections like n { .*, key: value })
// - Square brackets (list literals)
// - String literals
// smartSplitReturnItems splits RETURN items by comma, respecting strings, parentheses, and CASE/END.
// Properly handles UTF-8 encoded strings with multi-byte characters.
func (e *StorageExecutor) smartSplitReturnItems(returnPart string) []string {
	var result []string
	var current strings.Builder
	var inString bool
	var stringChar rune
	var parenDepth int
	var braceDepth int
	var bracketDepth int
	var caseDepth int

	runes := []rune(returnPart)
	runeLen := len(runes)

	// Build rune-to-byte index mapping for keyword checking
	runeToByteIndex := make([]int, runeLen+1)
	byteIdx := 0
	for ri, r := range runes {
		runeToByteIndex[ri] = byteIdx
		byteIdx += len(string(r))
	}
	runeToByteIndex[runeLen] = byteIdx

	upper := strings.ToUpper(returnPart)

	for ri := 0; ri < runeLen; ri++ {
		ch := runes[ri]
		bytePos := runeToByteIndex[ri]

		// Track string literals
		if ch == '\'' || ch == '"' {
			if !inString {
				inString = true
				stringChar = ch
			} else if ch == stringChar {
				inString = false
			}
			current.WriteRune(ch)
			continue
		}

		if inString {
			current.WriteRune(ch)
			continue
		}

		// Track parentheses
		if ch == '(' {
			parenDepth++
			current.WriteRune(ch)
			continue
		}
		if ch == ')' {
			parenDepth--
			current.WriteRune(ch)
			continue
		}

		// Track curly braces (map projections)
		if ch == '{' {
			braceDepth++
			current.WriteRune(ch)
			continue
		}
		if ch == '}' {
			braceDepth--
			current.WriteRune(ch)
			continue
		}

		// Track square brackets (list literals)
		if ch == '[' {
			bracketDepth++
			current.WriteRune(ch)
			continue
		}
		if ch == ']' {
			bracketDepth--
			current.WriteRune(ch)
			continue
		}

		// Track CASE/END keywords (using byte positions for substring comparison)
		if bytePos+4 <= len(returnPart) && upper[bytePos:bytePos+4] == "CASE" {
			// Check if CASE is a word boundary
			prevOk := ri == 0 || !isAlphaNum(runes[ri-1])
			nextRuneIdx := ri + 4 // Skip 4 runes for "CASE"
			// Need to find which rune corresponds to bytePos+4
			for nextRuneIdx < runeLen && runeToByteIndex[nextRuneIdx] < bytePos+4 {
				nextRuneIdx++
			}
			nextOk := nextRuneIdx >= runeLen || !isAlphaNum(runes[nextRuneIdx])
			if prevOk && nextOk {
				caseDepth++
			}
		}
		if bytePos+3 <= len(returnPart) && upper[bytePos:bytePos+3] == "END" {
			// Check if END is a word boundary
			prevOk := ri == 0 || !isAlphaNum(runes[ri-1])
			nextRuneIdx := ri + 3 // Skip 3 runes for "END"
			for nextRuneIdx < runeLen && runeToByteIndex[nextRuneIdx] < bytePos+3 {
				nextRuneIdx++
			}
			nextOk := nextRuneIdx >= runeLen || !isAlphaNum(runes[nextRuneIdx])
			if prevOk && nextOk && caseDepth > 0 {
				caseDepth--
			}
		}

		// Split on comma only if we're not inside parens, braces, brackets, CASE, or strings
		if ch == ',' && parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 && caseDepth == 0 {
			result = append(result, current.String())
			current.Reset()
			continue
		}

		current.WriteRune(ch)
	}

	// Add remaining content
	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// isAlphaNum checks if a character is alphanumeric or underscore
func isAlphaNum(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

func (e *StorageExecutor) parseReturnItems(returnPart string) []returnItem {
	items := []returnItem{}

	// Handle LIMIT clause
	upper := strings.ToUpper(returnPart)
	limitIdx := strings.Index(upper, "LIMIT")
	if limitIdx > 0 {
		returnPart = returnPart[:limitIdx]
	}

	// Handle ORDER BY clause - use whitespace-tolerant helper to avoid matching "order_id"
	// This is a safety check; most callers should already strip ORDER BY before calling parseReturnItems
	orderIdx := findKeywordIndex(returnPart, "ORDER")
	if orderIdx >= 0 {
		// Found "ORDER" - check if it's followed by "BY" (not part of "order_id")
		afterOrder := strings.TrimSpace(returnPart[orderIdx+5:]) // Skip "ORDER"
		if strings.HasPrefix(strings.ToUpper(afterOrder), "BY") {
			// This is "ORDER BY" - strip it
			returnPart = strings.TrimSpace(returnPart[:orderIdx])
		}
		// If it's just "ORDER" without "BY", leave it (might be a variable name)
	}

	// Split by comma, but respect CASE/END boundaries and parentheses
	parts := e.smartSplitReturnItems(returnPart)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "*" {
			continue
		}

		item := returnItem{expr: part}

		// Check for AS alias
		upperPart := strings.ToUpper(part)
		asIdx := strings.Index(upperPart, " AS ")
		if asIdx > 0 {
			item.expr = strings.TrimSpace(part[:asIdx])
			item.alias = strings.TrimSpace(part[asIdx+4:])
		} else {
			// Handle map projection without AS alias: n { .*, key: value } -> column name is "n"
			// Neo4j infers the column name from the variable before the map projection
			if braceIdx := strings.Index(part, " {"); braceIdx > 0 {
				varName := strings.TrimSpace(part[:braceIdx])
				if varName != "" && !strings.Contains(varName, "(") {
					item.alias = varName
				}
			}
		}

		items = append(items, item)
	}

	// If empty or *, return all
	if len(items) == 0 {
		items = append(items, returnItem{expr: "*"})
	}

	return items
}

func (e *StorageExecutor) filterNodes(nodes []*storage.Node, variable, whereClause string) []*storage.Node {
	// Create filter function for parallel execution
	filterFn := func(node *storage.Node) bool {
		return e.evaluateWhere(node, variable, whereClause)
	}

	// Use parallel filtering for large datasets
	return parallelFilterNodes(nodes, filterFn)
}

func (e *StorageExecutor) evaluateWhere(node *storage.Node, variable, whereClause string) bool {
	whereClause = strings.TrimSpace(whereClause)
	upperClause := strings.ToUpper(whereClause)

	// Handle parenthesized expressions - strip outer parens and recurse
	if strings.HasPrefix(whereClause, "(") && strings.HasSuffix(whereClause, ")") {
		// Verify these are matching outer parens, not separate groups
		depth := 0
		isOuterParen := true
		for i, ch := range whereClause {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
			}
			// If depth goes to 0 before the last char, these aren't outer parens
			if depth == 0 && i < len(whereClause)-1 {
				isOuterParen = false
				break
			}
		}
		if isOuterParen {
			return e.evaluateWhere(node, variable, whereClause[1:len(whereClause)-1])
		}
	}

	// CRITICAL: Handle AND/OR at top level FIRST before subqueries
	// This ensures "EXISTS {} AND COUNT {} >= 2" is properly split
	if andIdx := findTopLevelKeyword(whereClause, " AND "); andIdx > 0 {
		left := strings.TrimSpace(whereClause[:andIdx])
		right := strings.TrimSpace(whereClause[andIdx+5:])
		return e.evaluateWhere(node, variable, left) && e.evaluateWhere(node, variable, right)
	}

	// Handle OR at top level only
	if orIdx := findTopLevelKeyword(whereClause, " OR "); orIdx > 0 {
		left := strings.TrimSpace(whereClause[:orIdx])
		right := strings.TrimSpace(whereClause[orIdx+4:])
		return e.evaluateWhere(node, variable, left) || e.evaluateWhere(node, variable, right)
	}

	// Handle NOT EXISTS { } subquery FIRST (before other NOT handling)
	// Uses regex for whitespace-flexible matching
	if hasSubqueryPattern(whereClause, notExistsSubqueryRe) {
		return e.evaluateNotExistsSubquery(node, variable, whereClause)
	}

	// Handle EXISTS { } subquery (whitespace-flexible)
	if hasSubqueryPattern(whereClause, existsSubqueryRe) {
		return e.evaluateExistsSubquery(node, variable, whereClause)
	}

	// Handle COUNT { } subquery with comparison (whitespace-flexible)
	if hasSubqueryPattern(whereClause, countSubqueryRe) {
		return e.evaluateCountSubqueryComparison(node, variable, whereClause)
	}

	// Handle NOT prefix
	if strings.HasPrefix(upperClause, "NOT ") {
		inner := strings.TrimSpace(whereClause[4:])
		return !e.evaluateWhere(node, variable, inner)
	}

	// Handle label check: n:Label or variable:Label
	if colonIdx := strings.Index(whereClause, ":"); colonIdx > 0 {
		labelVar := strings.TrimSpace(whereClause[:colonIdx])
		labelName := strings.TrimSpace(whereClause[colonIdx+1:])
		// Check if this looks like a simple variable:Label pattern
		if len(labelVar) > 0 && len(labelName) > 0 &&
			!strings.ContainsAny(labelVar, " .(") &&
			!strings.ContainsAny(labelName, " .(=<>") {
			// If the variable matches our node variable, check the label
			if labelVar == variable {
				for _, l := range node.Labels {
					if l == labelName {
						return true
					}
				}
				return false
			}
		}
	}

	// Handle string operators (case-insensitive check)
	if strings.Contains(upperClause, " CONTAINS ") {
		return e.evaluateStringOp(node, variable, whereClause, "CONTAINS")
	}
	if strings.Contains(upperClause, " STARTS WITH ") {
		return e.evaluateStringOp(node, variable, whereClause, "STARTS WITH")
	}
	if strings.Contains(upperClause, " ENDS WITH ") {
		return e.evaluateStringOp(node, variable, whereClause, "ENDS WITH")
	}
	if strings.Contains(upperClause, " IN ") {
		return e.evaluateInOp(node, variable, whereClause)
	}
	if strings.Contains(upperClause, " IS NULL") {
		return e.evaluateIsNull(node, variable, whereClause, false)
	}
	if strings.Contains(upperClause, " IS NOT NULL") {
		return e.evaluateIsNull(node, variable, whereClause, true)
	}

	// Determine operator and split accordingly
	var op string
	var opIdx int

	// Check operators in order of length (longest first to avoid partial matches)
	operators := []string{"<>", "!=", ">=", "<=", "=~", ">", "<", "="}
	for _, testOp := range operators {
		idx := strings.Index(whereClause, testOp)
		if idx >= 0 {
			op = testOp
			opIdx = idx
			break
		}
	}

	if op == "" {
		return true // No valid operator found, include all
	}

	left := strings.TrimSpace(whereClause[:opIdx])
	right := strings.TrimSpace(whereClause[opIdx+len(op):])

	// Handle id(variable) = value comparisons
	lowerLeft := strings.ToLower(left)
	if strings.HasPrefix(lowerLeft, "id(") && strings.HasSuffix(left, ")") {
		// Extract variable name from id(varName)
		idVar := strings.TrimSpace(left[3 : len(left)-1])
		if idVar == variable {
			// Compare node ID with expected value
			expectedVal := e.parseValue(right)
			actualId := string(node.ID)
			switch op {
			case "=":
				return e.compareEqual(actualId, expectedVal)
			case "<>", "!=":
				return !e.compareEqual(actualId, expectedVal)
			default:
				return true
			}
		}
		return true // Different variable, not our concern
	}

	// Handle elementId(variable) = value comparisons
	if strings.HasPrefix(lowerLeft, "elementid(") && strings.HasSuffix(left, ")") {
		// Extract variable name from elementId(varName)
		idVar := strings.TrimSpace(left[10 : len(left)-1])
		if idVar == variable {
			// Compare node ID with expected value
			expectedVal := e.parseValue(right)
			actualId := string(node.ID)
			switch op {
			case "=":
				return e.compareEqual(actualId, expectedVal)
			case "<>", "!=":
				return !e.compareEqual(actualId, expectedVal)
			default:
				return true
			}
		}
		return true // Different variable, not our concern
	}

	// Extract property from left side (e.g., "n.name")
	if !strings.HasPrefix(left, variable+".") {
		return true // Not a property comparison we can handle
	}

	propName := left[len(variable)+1:]

	// Get actual value
	actualVal, exists := node.Properties[propName]
	if !exists {
		return false
	}

	// Parse the expected value from right side
	expectedVal := e.parseValue(right)

	// Perform comparison based on operator
	switch op {
	case "=":
		return e.compareEqual(actualVal, expectedVal)
	case "<>", "!=":
		return !e.compareEqual(actualVal, expectedVal)
	case ">":
		return e.compareGreater(actualVal, expectedVal)
	case ">=":
		return e.compareGreater(actualVal, expectedVal) || e.compareEqual(actualVal, expectedVal)
	case "<":
		return e.compareLess(actualVal, expectedVal)
	case "<=":
		return e.compareLess(actualVal, expectedVal) || e.compareEqual(actualVal, expectedVal)
	case "=~":
		return e.compareRegex(actualVal, expectedVal)
	default:
		return true
	}
}

// parseValue extracts the actual value from a Cypher literal
func (e *StorageExecutor) parseValue(s string) interface{} {
	s = strings.TrimSpace(s)

	// Handle arrays: [0.1, 0.2, 0.3]
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return e.parseArrayValue(s)
	}

	// Handle quoted strings with escape sequence support
	if (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) ||
		(strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"")) {
		inner := s[1 : len(s)-1]
		// Unescape: \' -> ', \" -> ", \\ -> \
		inner = strings.ReplaceAll(inner, "\\'", "'")
		inner = strings.ReplaceAll(inner, "\\\"", "\"")
		inner = strings.ReplaceAll(inner, "\\\\", "\\")
		return inner
	}

	// Handle booleans
	upper := strings.ToUpper(s)
	if upper == "TRUE" {
		return true
	}
	if upper == "FALSE" {
		return false
	}
	if upper == "NULL" {
		return nil
	}

	// Handle numbers - preserve int64 for integers, use float64 only for decimals
	// The comparison functions use toFloat64() which handles both types
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i // Keep as int64 for Neo4j compatibility
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	return s
}

func (e *StorageExecutor) resolveReturnItem(item returnItem, variable string, node *storage.Node) interface{} {
	expr := item.expr

	// Handle wildcard - return the whole node (Neo4j compatible: return *storage.Node)
	if expr == "*" || expr == variable {
		return node
	}

	// Check for COLLECT { } subquery FIRST (before other function checks)
	// This is a Neo4j 5.0+ feature that executes a subquery and collects results
	if hasSubqueryPattern(expr, collectSubqueryRe) {
		// We need context to execute the subquery, but resolveReturnItem doesn't have it
		// Return a placeholder that will be handled by the caller
		// This is a limitation - we'll need to handle collect { } at a higher level
		// For now, return nil and handle it in the calling code
		return nil // Will be handled by evaluateCollectSubquery in calling code
	}

	// Check for CASE expression FIRST (before property access check)
	// CASE expressions contain dots (like p.age) but should not be treated as property access
	if isCaseExpression(expr) {
		return e.evaluateExpression(expr, variable, node)
	}

	// Check for function calls - these should be evaluated, not treated as property access
	// e.g., coalesce(p.nickname, p.name), toString(p.age), etc.
	if strings.Contains(expr, "(") {
		return e.evaluateExpression(expr, variable, node)
	}

	// Check for IS NULL / IS NOT NULL - these need full evaluation
	upperExpr := strings.ToUpper(expr)
	if strings.Contains(upperExpr, " IS NULL") || strings.Contains(upperExpr, " IS NOT NULL") {
		return e.evaluateExpression(expr, variable, node)
	}

	// Check for arithmetic operators - need full evaluation
	if strings.ContainsAny(expr, "+-*/%") {
		return e.evaluateExpression(expr, variable, node)
	}

	// Handle property access: variable.property
	if strings.Contains(expr, ".") {
		parts := strings.SplitN(expr, ".", 2)
		varName := strings.TrimSpace(parts[0])
		propName := strings.TrimSpace(parts[1])

		// Check if variable matches
		if varName != variable {
			// Different variable - return nil (variable not in scope)
			return nil
		}

		// Handle special "id" property - return node's internal ID
		if propName == "id" {
			// Check if there's an "id" property first
			if val, ok := node.Properties["id"]; ok {
				return val
			}
			// Fall back to internal node ID
			return string(node.ID)
		}

		// Handle special "embedding" property - return summary, never the raw array
		if propName == "embedding" {
			return e.buildEmbeddingSummary(node)
		}

		// Handle has_embedding specially - check both property and native embedding field
		// This supports Mimir's query: WHERE f.has_embedding = true
		if propName == "has_embedding" {
			// Check property first
			if val, ok := node.Properties["has_embedding"]; ok {
				return val
			}
			// Fall back to checking native embedding field (always stored in ChunkEmbeddings)
			return len(node.ChunkEmbeddings) > 0 && len(node.ChunkEmbeddings[0]) > 0
		}

		// Filter out internal embedding-related properties (except has_embedding handled above)
		if e.isInternalProperty(propName) {
			return nil
		}

		// Regular property access
		if val, ok := node.Properties[propName]; ok {
			return val
		}
		return nil
	}

	// Use the comprehensive expression evaluator for all expressions
	// This supports: id(n), labels(n), keys(n), properties(n), literals, etc.
	result := e.evaluateExpression(expr, variable, node)

	// If the result is just the expression string unchanged, return nil
	// (expression wasn't recognized/evaluated)
	if str, ok := result.(string); ok && str == expr && !strings.HasPrefix(expr, "'") && !strings.HasPrefix(expr, "\"") {
		return nil
	}

	return result
}

func (e *StorageExecutor) generateID() string {
	// Use UUID for globally unique IDs
	// This prevents ID collisions across server restarts which caused
	// the race condition where CREATE would cancel pending DELETEs
	return uuid.New().String()
}

// Deprecated: Sequential counter replaced with UUID generation
var idCounter int64

func (e *StorageExecutor) idCounter() int64 {
	// Keep for backward compatibility but not used in generateID anymore
	atomic.AddInt64(&idCounter, 1)
	return atomic.LoadInt64(&idCounter)
}

// evaluateExistsSubquery checks if an EXISTS { } subquery returns any matches
// Syntax: EXISTS { MATCH (node)<-[:TYPE]-(other) }
func (e *StorageExecutor) evaluateExistsSubquery(node *storage.Node, variable, whereClause string) bool {
	// Extract the subquery from EXISTS { ... }
	subquery := e.extractSubquery(whereClause, "EXISTS")
	if subquery == "" {
		return true // No valid subquery, pass through
	}

	// Execute the subquery with the current node as context
	return e.checkSubqueryMatch(node, variable, subquery)
}

// evaluateNotExistsSubquery checks if a NOT EXISTS { } subquery returns no matches
func (e *StorageExecutor) evaluateNotExistsSubquery(node *storage.Node, variable, whereClause string) bool {
	// Extract the subquery from NOT EXISTS { ... }
	subquery := e.extractSubquery(whereClause, "NOT EXISTS")
	if subquery == "" {
		return true // No valid subquery, pass through
	}

	// Return true if no matches found
	return !e.checkSubqueryMatch(node, variable, subquery)
}

// extractSubquery extracts the MATCH pattern from EXISTS { MATCH ... } or NOT EXISTS { MATCH ... }
func (e *StorageExecutor) extractSubquery(whereClause, prefix string) string {
	upperClause := strings.ToUpper(whereClause)
	prefixUpper := strings.ToUpper(prefix)

	// Find the prefix position
	prefixIdx := strings.Index(upperClause, prefixUpper)
	if prefixIdx < 0 {
		return ""
	}

	// Find the opening brace
	rest := whereClause[prefixIdx+len(prefix):]
	braceStart := strings.Index(rest, "{")
	if braceStart < 0 {
		return ""
	}

	// Find matching closing brace
	depth := 0
	for i := braceStart; i < len(rest); i++ {
		if rest[i] == '{' {
			depth++
		} else if rest[i] == '}' {
			depth--
			if depth == 0 {
				return strings.TrimSpace(rest[braceStart+1 : i])
			}
		}
	}

	return ""
}

// extractCollectSubquery extracts the subquery body from COLLECT { ... }
func (e *StorageExecutor) extractCollectSubquery(expr string) string {
	// Find "collect" (case-insensitive)
	upperExpr := strings.ToUpper(expr)
	collectIdx := strings.Index(upperExpr, "COLLECT")
	if collectIdx < 0 {
		return ""
	}

	// Find the opening brace after COLLECT
	rest := expr[collectIdx+7:] // Skip "COLLECT"
	braceStart := strings.Index(rest, "{")
	if braceStart < 0 {
		return ""
	}

	// Find matching closing brace
	depth := 0
	for i := braceStart; i < len(rest); i++ {
		if rest[i] == '{' {
			depth++
		} else if rest[i] == '}' {
			depth--
			if depth == 0 {
				return strings.TrimSpace(rest[braceStart+1 : i])
			}
		}
	}

	return ""
}

// evaluateCollectSubquery executes a COLLECT { } subquery for a given node and returns collected values
func (e *StorageExecutor) evaluateCollectSubquery(ctx context.Context, node *storage.Node, variable, subquery string) ([]interface{}, error) {
	// Extract the subquery body from COLLECT { ... }
	subqueryBody := e.extractCollectSubquery(subquery)
	if subqueryBody == "" {
		return nil, fmt.Errorf("invalid COLLECT subquery syntax")
	}

	// The subquery body should be a complete query like:
	// MATCH (p)-[:KNOWS]->(friend) RETURN friend.name
	// We need to execute it with the node bound to the variable.
	// We'll add a WHERE clause to bind the variable to the node ID.
	// Format: MATCH (p)-[:KNOWS]->(friend) WHERE id(p) = nodeID RETURN friend.name

	// Find WHERE clause position (if any)
	whereIdx := findKeywordIndex(subqueryBody, "WHERE")
	returnIdx := findKeywordIndex(subqueryBody, "RETURN")

	var substitutedQuery string
	if whereIdx > 0 && whereIdx < returnIdx {
		// WHERE clause exists - add id() check to it
		whereClause := strings.TrimSpace(subqueryBody[whereIdx+5 : returnIdx])
		beforeWhere := subqueryBody[:whereIdx+5]
		afterReturn := subqueryBody[returnIdx:]
		// Add id() check: WHERE id(variable) = nodeID AND existing_where_clause
		newWhere := fmt.Sprintf("WHERE id(%s) = '%s' AND %s", variable, string(node.ID), whereClause)
		substitutedQuery = beforeWhere + newWhere + afterReturn
	} else if returnIdx > 0 {
		// No WHERE clause - add one before RETURN
		beforeReturn := subqueryBody[:returnIdx]
		afterReturn := subqueryBody[returnIdx:]
		// Add WHERE clause: WHERE id(variable) = nodeID
		newWhere := fmt.Sprintf(" WHERE id(%s) = '%s'", variable, string(node.ID))
		substitutedQuery = beforeReturn + newWhere + afterReturn
	} else {
		// No RETURN clause - this shouldn't happen, but handle it
		return nil, fmt.Errorf("COLLECT subquery must have a RETURN clause")
	}

	// Execute the subquery
	subqueryResult, err := e.Execute(ctx, substitutedQuery, nil)
	if err != nil {
		return nil, fmt.Errorf("COLLECT subquery execution failed: %w", err)
	}

	// Collect all values from the first column of the subquery result
	collected := make([]interface{}, 0, len(subqueryResult.Rows))
	for _, row := range subqueryResult.Rows {
		if len(row) > 0 {
			collected = append(collected, row[0])
		}
	}

	return collected, nil
}

// substituteNodeInSubquery substitutes a node variable in a subquery with its actual ID
// Example: MATCH (p)-[:KNOWS]->(friend) RETURN friend.name
//
//	where p is bound to a node -> MATCH (nodeID)-[:KNOWS]->(friend) RETURN friend.name
func (e *StorageExecutor) substituteNodeInSubquery(subquery, variable string, node *storage.Node) string {
	// Replace (variable) or (variable:Label) patterns with the actual node ID
	// We need to be careful to only replace node patterns, not property accesses
	result := subquery

	// Pattern 1: (variable) -> (nodeID)
	// Use word boundaries to avoid matching variable names that are substrings
	pattern1 := regexp.MustCompile(`\(` + regexp.QuoteMeta(variable) + `\)`)
	replacement1 := "(" + string(node.ID) + ")"
	result = pattern1.ReplaceAllString(result, replacement1)

	// Pattern 2: (variable:Label) -> (nodeID:Label)
	// This preserves the label
	labelPattern := regexp.MustCompile(`\(` + regexp.QuoteMeta(variable) + `:([^)]+)\)`)
	result = labelPattern.ReplaceAllStringFunc(result, func(match string) string {
		// Extract the label part
		labelMatch := regexp.MustCompile(`:` + `([^)]+)`).FindStringSubmatch(match)
		if len(labelMatch) > 1 {
			return "(" + string(node.ID) + ":" + labelMatch[1] + ")"
		}
		return "(" + string(node.ID) + ")"
	})

	return result
}

// checkSubqueryMatch checks if the subquery matches for a given node
func (e *StorageExecutor) checkSubqueryMatch(node *storage.Node, variable, subquery string) bool {
	// Parse the MATCH pattern from the subquery
	// Format: MATCH (var)<-[:TYPE]-(other) WHERE ...
	upperSub := strings.ToUpper(subquery)

	if !strings.HasPrefix(upperSub, "MATCH ") {
		return false
	}

	// Split out any WHERE clause from the pattern
	pattern := strings.TrimSpace(subquery[6:])
	innerWhere := ""

	// Use regex to find WHERE with any whitespace before it (including newlines)
	whereRe := regexp.MustCompile(`(?i)\s+WHERE\s+`)
	if loc := whereRe.FindStringIndex(pattern); loc != nil {
		innerWhere = strings.TrimSpace(pattern[loc[1]:])
		pattern = strings.TrimSpace(pattern[:loc[0]])
	}

	// Check if pattern references our variable
	if !strings.Contains(pattern, "("+variable+")") && !strings.Contains(pattern, "("+variable+":") {
		return false
	}

	// Check for chained relationship pattern (e.g., (p)-[:KNOWS]->()-[:KNOWS]->())
	// Count the number of relationship hops by counting relationship brackets [-
	// Each hop has one -[...]-
	relationshipCount := strings.Count(pattern, "-[")
	if relationshipCount > 1 {
		return e.checkChainedPattern(node, variable, pattern, innerWhere)
	}

	// Extract the target variable name from pattern (e.g., "report" from "(m)-[:MANAGES]->(report)")
	targetVar := e.extractTargetVariable(pattern, variable)

	// Parse relationship pattern
	// Simplified: check for incoming or outgoing relationships
	var checkIncoming, checkOutgoing bool
	var relTypes []string

	if strings.Contains(pattern, "<-[") {
		checkIncoming = true
		// Extract relationship type if specified
		relTypes = e.extractRelTypesFromPattern(pattern, "<-[")
	}
	if strings.Contains(pattern, "]->(") || strings.Contains(pattern, "]->") {
		checkOutgoing = true
		relTypes = e.extractRelTypesFromPattern(pattern, "-[")
	}

	// Check for matching edges
	if checkIncoming {
		edges, _ := e.storage.GetIncomingEdges(node.ID)
		for _, edge := range edges {
			if len(relTypes) == 0 || e.edgeTypeMatches(edge.Type, relTypes) {
				// If there's an inner WHERE, check it against the connected node
				// Only evaluate WHERE if we have a target variable (otherwise we can't match properties)
				if innerWhere != "" && targetVar != "" {
					sourceNode, err := e.storage.GetNode(edge.StartNode)
					if err != nil || !e.evaluateInnerWhere(sourceNode, targetVar, innerWhere) {
						continue
					}
				} else if innerWhere != "" && targetVar == "" {
					// If we have a WHERE clause but no target variable, we can't evaluate it
					// This means the pattern doesn't have a named target, so skip this edge
					continue
				}
				return true
			}
		}
	}

	if checkOutgoing {
		edges, _ := e.storage.GetOutgoingEdges(node.ID)
		for _, edge := range edges {
			if len(relTypes) == 0 || e.edgeTypeMatches(edge.Type, relTypes) {
				// If there's an inner WHERE, check it against the connected node
				// Only evaluate WHERE if we have a target variable (otherwise we can't match properties)
				if innerWhere != "" && targetVar != "" {
					targetNode, err := e.storage.GetNode(edge.EndNode)
					if err != nil || !e.evaluateInnerWhere(targetNode, targetVar, innerWhere) {
						continue
					}
				} else if innerWhere != "" && targetVar == "" {
					// If we have a WHERE clause but no target variable, we can't evaluate it
					// This means the pattern doesn't have a named target, so skip this edge
					continue
				}
				return true
			}
		}
	}

	// If no direction specified, check both
	if !checkIncoming && !checkOutgoing {
		incoming, _ := e.storage.GetIncomingEdges(node.ID)
		outgoing, _ := e.storage.GetOutgoingEdges(node.ID)
		return len(incoming) > 0 || len(outgoing) > 0
	}

	return false
}

// checkChainedPattern handles chained relationship patterns like (p)-[:KNOWS]->()-[:KNOWS]->()
func (e *StorageExecutor) checkChainedPattern(node *storage.Node, variable, pattern, innerWhere string) bool {
	// Parse the pattern to extract relationship hops
	// E.g., (p)-[:KNOWS]->()-[:KNOWS]->() has two hops

	// Find the first relationship part
	// Pattern looks like: (variable)-[rel1]->(intermediate)-[rel2]->...

	// Find the start of the first relationship (after the variable node)
	varPattern := "(" + variable + ")"
	if !strings.Contains(pattern, varPattern) {
		// Try with label: (variable:Label)
		idx := strings.Index(pattern, "("+variable+":")
		if idx < 0 {
			return false
		}
	}

	// Extract relationship hops
	hops := e.parseRelationshipHops(pattern, variable)
	if len(hops) == 0 {
		return false
	}

	// Traverse the chain starting from the given node
	return e.traverseChain(node, hops, 0)
}

// relationshipHop represents one step in a chained relationship pattern
type relationshipHop struct {
	relTypes []string
	outgoing bool
}

// parseRelationshipHops extracts relationship hops from a pattern
func (e *StorageExecutor) parseRelationshipHops(pattern, variable string) []relationshipHop {
	var hops []relationshipHop

	// Find all relationship patterns: -[...]->  or  <-[...]-
	remaining := pattern

	for len(remaining) > 0 {
		// Look for outgoing: -[...]->(
		outIdx := strings.Index(remaining, "-[")
		inIdx := strings.Index(remaining, "<-[")

		if outIdx >= 0 && (inIdx < 0 || outIdx < inIdx) {
			// Found outgoing pattern
			relStart := outIdx + 2
			relEnd := strings.Index(remaining[relStart:], "]")
			if relEnd < 0 {
				break
			}
			relEnd += relStart

			relPart := remaining[relStart:relEnd]
			// Extract relationship types
			var relTypes []string
			if strings.HasPrefix(relPart, ":") {
				typePart := relPart[1:]
				// Handle multiple types separated by |
				for _, t := range strings.Split(typePart, "|") {
					if t = strings.TrimSpace(t); t != "" {
						relTypes = append(relTypes, t)
					}
				}
			}

			hops = append(hops, relationshipHop{
				relTypes: relTypes,
				outgoing: true,
			})

			remaining = remaining[relEnd+1:]
		} else if inIdx >= 0 {
			// Found incoming pattern
			relStart := inIdx + 3
			relEnd := strings.Index(remaining[relStart:], "]")
			if relEnd < 0 {
				break
			}
			relEnd += relStart

			relPart := remaining[relStart:relEnd]
			// Extract relationship types
			var relTypes []string
			if strings.HasPrefix(relPart, ":") {
				typePart := relPart[1:]
				for _, t := range strings.Split(typePart, "|") {
					if t = strings.TrimSpace(t); t != "" {
						relTypes = append(relTypes, t)
					}
				}
			}

			hops = append(hops, relationshipHop{
				relTypes: relTypes,
				outgoing: false,
			})

			remaining = remaining[relEnd+1:]
		} else {
			break
		}
	}

	return hops
}

// traverseChain recursively checks if a chain of relationships exists
func (e *StorageExecutor) traverseChain(node *storage.Node, hops []relationshipHop, hopIndex int) bool {
	if hopIndex >= len(hops) {
		return true // All hops matched
	}

	hop := hops[hopIndex]

	if hop.outgoing {
		edges, _ := e.storage.GetOutgoingEdges(node.ID)
		for _, edge := range edges {
			if len(hop.relTypes) == 0 || e.edgeTypeMatches(edge.Type, hop.relTypes) {
				// Get the target node and recurse
				nextNode, err := e.storage.GetNode(edge.EndNode)
				if err != nil {
					continue
				}
				if e.traverseChain(nextNode, hops, hopIndex+1) {
					return true
				}
			}
		}
	} else {
		edges, _ := e.storage.GetIncomingEdges(node.ID)
		for _, edge := range edges {
			if len(hop.relTypes) == 0 || e.edgeTypeMatches(edge.Type, hop.relTypes) {
				// Get the source node and recurse
				nextNode, err := e.storage.GetNode(edge.StartNode)
				if err != nil {
					continue
				}
				if e.traverseChain(nextNode, hops, hopIndex+1) {
					return true
				}
			}
		}
	}

	return false
}

// extractVariableNameFromReturnItem extracts the variable name from a return item expression.
// Examples:
//   - "n" -> "n"
//   - "n.name" -> "n"
//   - "id(n)" -> "n"
//   - "n.age + 1" -> "n"
func extractVariableNameFromReturnItem(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}

	// Handle function calls like id(n), elementId(n), etc.
	if strings.Contains(expr, "(") {
		// Extract variable from function call: id(n) -> n
		openParen := strings.Index(expr, "(")
		closeParen := strings.LastIndex(expr, ")")
		if openParen > 0 && closeParen > openParen {
			inner := strings.TrimSpace(expr[openParen+1 : closeParen])
			// If inner contains a dot, it's property access: id(n.name) -> n
			if dotIdx := strings.Index(inner, "."); dotIdx > 0 {
				return strings.TrimSpace(inner[:dotIdx])
			}
			return inner
		}
	}

	// Handle property access: n.name -> n
	if strings.Contains(expr, ".") {
		parts := strings.SplitN(expr, ".", 2)
		return strings.TrimSpace(parts[0])
	}

	// Simple variable name
	return expr
}

// extractTargetVariable extracts the target variable name from a relationship pattern
// e.g., from "(m)-[:MANAGES]->(report)" it extracts "report"
func (e *StorageExecutor) extractTargetVariable(pattern, sourceVar string) string {
	// Look for outgoing pattern: (source)-[...]->(target)
	if arrowIdx := strings.Index(pattern, "]->"); arrowIdx >= 0 {
		rest := pattern[arrowIdx+3:]
		if parenIdx := strings.Index(rest, "("); parenIdx >= 0 {
			rest = rest[parenIdx+1:]
			// Extract variable name (before : or ))
			endIdx := strings.IndexAny(rest, ":)")
			if endIdx > 0 {
				return strings.TrimSpace(rest[:endIdx])
			}
		}
	}

	// Look for incoming pattern: (target)<-[...]-(source)
	if arrowIdx := strings.Index(pattern, "<-["); arrowIdx >= 0 {
		// Target is before the arrow
		before := pattern[:arrowIdx]
		if parenIdx := strings.LastIndex(before, "("); parenIdx >= 0 {
			inner := before[parenIdx+1:]
			endIdx := strings.IndexAny(inner, ":)")
			if endIdx > 0 {
				return strings.TrimSpace(inner[:endIdx])
			}
		}
	}

	return ""
}

// evaluateInnerWhere evaluates an inner WHERE clause against a node
// Handles nested EXISTS subqueries and property comparisons
func (e *StorageExecutor) evaluateInnerWhere(node *storage.Node, variable, whereClause string) bool {
	whereClause = strings.TrimSpace(whereClause)
	upperWhere := strings.ToUpper(whereClause)

	// Handle parenthesized expressions - strip outer parens and recurse
	if strings.HasPrefix(whereClause, "(") && strings.HasSuffix(whereClause, ")") {
		// Verify these are matching outer parens, not separate groups
		depth := 0
		isOuterParen := true
		for i, ch := range whereClause {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
			}
			// If depth goes to 0 before the last char, these aren't outer parens
			if depth == 0 && i < len(whereClause)-1 {
				isOuterParen = false
				break
			}
		}
		if isOuterParen {
			return e.evaluateInnerWhere(node, variable, whereClause[1:len(whereClause)-1])
		}
	}

	// Handle AND/OR at top level
	if andIdx := findTopLevelKeyword(whereClause, " AND "); andIdx > 0 {
		left := strings.TrimSpace(whereClause[:andIdx])
		right := strings.TrimSpace(whereClause[andIdx+5:])
		return e.evaluateInnerWhere(node, variable, left) && e.evaluateInnerWhere(node, variable, right)
	}

	if orIdx := findTopLevelKeyword(whereClause, " OR "); orIdx > 0 {
		left := strings.TrimSpace(whereClause[:orIdx])
		right := strings.TrimSpace(whereClause[orIdx+4:])
		return e.evaluateInnerWhere(node, variable, left) || e.evaluateInnerWhere(node, variable, right)
	}

	// Check for nested EXISTS subquery
	if hasSubqueryPattern(whereClause, existsSubqueryRe) {
		// Check for NOT EXISTS first
		if hasSubqueryPattern(whereClause, notExistsSubqueryRe) {
			return e.evaluateNotExistsSubquery(node, variable, whereClause)
		}
		return e.evaluateExistsSubquery(node, variable, whereClause)
	}

	// Check for nested COUNT subquery
	if hasSubqueryPattern(whereClause, countSubqueryRe) {
		return e.evaluateCountSubqueryComparison(node, variable, whereClause)
	}

	// Handle NOT prefix
	if strings.HasPrefix(upperWhere, "NOT ") {
		inner := strings.TrimSpace(whereClause[4:])
		return !e.evaluateInnerWhere(node, variable, inner)
	}

	// Handle string operators
	if strings.Contains(upperWhere, " CONTAINS ") {
		return e.evaluateStringOp(node, variable, whereClause, "CONTAINS")
	}
	if strings.Contains(upperWhere, " STARTS WITH ") {
		return e.evaluateStringOp(node, variable, whereClause, "STARTS WITH")
	}
	if strings.Contains(upperWhere, " ENDS WITH ") {
		return e.evaluateStringOp(node, variable, whereClause, "ENDS WITH")
	}
	if strings.Contains(upperWhere, " IN ") {
		return e.evaluateInOp(node, variable, whereClause)
	}

	// Handle IS NULL / IS NOT NULL
	if strings.Contains(upperWhere, " IS NULL") {
		return e.evaluateIsNull(node, variable, whereClause, false)
	}
	if strings.Contains(upperWhere, " IS NOT NULL") {
		return e.evaluateIsNull(node, variable, whereClause, true)
	}

	// Determine operator and split accordingly
	var op string
	var opIdx int

	// Check operators in order of length (longest first to avoid partial matches)
	operators := []string{"<>", "!=", ">=", "<=", "=~", ">", "<", "="}
	for _, testOp := range operators {
		idx := strings.Index(whereClause, testOp)
		if idx >= 0 {
			op = testOp
			opIdx = idx
			break
		}
	}

	if op == "" {
		// No valid operator found - check if clause is empty/whitespace
		trimmed := strings.TrimSpace(whereClause)
		if trimmed == "" {
			// Empty WHERE clause means no filter - include all
			return true
		}
		// Non-empty clause with no recognized operator - cannot evaluate properly
		// Return false (exclude) rather than true (include all) for safety
		// This prevents incorrect results from malformed or unsupported WHERE clauses
		return false
	}

	left := strings.TrimSpace(whereClause[:opIdx])
	right := strings.TrimSpace(whereClause[opIdx+len(op):])

	// Handle id(variable) = value comparisons
	lowerLeft := strings.ToLower(left)
	if strings.HasPrefix(lowerLeft, "id(") && strings.HasSuffix(left, ")") {
		// Extract variable name from id(varName)
		idVar := strings.TrimSpace(left[3 : len(left)-1])
		if idVar == variable {
			// Compare node ID with expected value
			expectedVal := e.parseValue(right)
			actualId := string(node.ID)
			switch op {
			case "=":
				return e.compareEqual(actualId, expectedVal)
			case "<>", "!=":
				return !e.compareEqual(actualId, expectedVal)
			default:
				return true
			}
		}
		return true // Different variable, not our concern
	}

	// Handle elementId(variable) = value comparisons
	if strings.HasPrefix(lowerLeft, "elementid(") && strings.HasSuffix(left, ")") {
		// Extract variable name from elementId(varName)
		idVar := strings.TrimSpace(left[10 : len(left)-1])
		if idVar == variable {
			// Compare node ID with expected value
			expectedVal := e.parseValue(right)
			actualId := string(node.ID)
			switch op {
			case "=":
				return e.compareEqual(actualId, expectedVal)
			case "<>", "!=":
				return !e.compareEqual(actualId, expectedVal)
			default:
				return true
			}
		}
		return true // Different variable, not our concern
	}

	// Extract property from left side (e.g., "n.name")
	// Use TrimSpace to handle whitespace around the dot
	varName := strings.TrimSpace(left)
	if !strings.HasPrefix(varName, variable+".") {
		// In EXISTS subqueries, if we can't evaluate the condition (variable doesn't match),
		// we should return false (exclude) rather than true (include all)
		// This ensures WHERE clauses in subqueries actually filter correctly
		return false // Not a property comparison we can handle for this variable
	}

	propName := strings.TrimSpace(varName[len(variable)+1:])

	// Get actual value
	actualVal, exists := node.Properties[propName]
	if !exists {
		return false
	}

	// Parse the expected value from right side
	expectedVal := e.parseValue(right)

	// Perform comparison based on operator
	switch op {
	case "=":
		return e.compareEqual(actualVal, expectedVal)
	case "<>", "!=":
		return !e.compareEqual(actualVal, expectedVal)
	case ">":
		return e.compareGreater(actualVal, expectedVal)
	case ">=":
		return e.compareGreater(actualVal, expectedVal) || e.compareEqual(actualVal, expectedVal)
	case "<":
		return e.compareLess(actualVal, expectedVal)
	case "<=":
		return e.compareLess(actualVal, expectedVal) || e.compareEqual(actualVal, expectedVal)
	case "=~":
		return e.compareRegex(actualVal, expectedVal)
	default:
		return true
	}
}

// extractRelTypesFromPattern extracts relationship types from a pattern
func (e *StorageExecutor) extractRelTypesFromPattern(pattern, prefix string) []string {
	var types []string

	idx := strings.Index(pattern, prefix)
	if idx < 0 {
		return types
	}

	rest := pattern[idx+len(prefix):]
	endIdx := strings.Index(rest, "]")
	if endIdx < 0 {
		return types
	}

	relPart := rest[:endIdx]

	// Extract type after colon
	if colonIdx := strings.Index(relPart, ":"); colonIdx >= 0 {
		typePart := relPart[colonIdx+1:]
		// Handle multiple types (TYPE1|TYPE2)
		for _, t := range strings.Split(typePart, "|") {
			t = strings.TrimSpace(t)
			if t != "" {
				types = append(types, t)
			}
		}
	}

	return types
}

// edgeTypeMatches checks if an edge type matches any of the allowed types
func (e *StorageExecutor) edgeTypeMatches(edgeType string, allowedTypes []string) bool {
	for _, t := range allowedTypes {
		if edgeType == t {
			return true
		}
	}
	return false
}

// evaluateCountSubqueryComparison evaluates COUNT { } subquery with comparison
// Syntax: COUNT { MATCH (node)-[:TYPE]->(other) } > 5
// Returns true if the comparison holds
func (e *StorageExecutor) evaluateCountSubqueryComparison(node *storage.Node, variable, whereClause string) bool {
	// Extract the subquery from COUNT { ... }
	subquery := e.extractSubquery(whereClause, "COUNT")
	if subquery == "" {
		return true // No valid subquery, pass through
	}

	// Count matching relationships
	count := e.countSubqueryMatches(node, variable, subquery)

	// Extract and evaluate the comparison operator
	// Find the closing brace to get what comes after
	upperClause := strings.ToUpper(whereClause)
	countIdx := strings.Index(upperClause, "COUNT")
	if countIdx < 0 {
		return false
	}

	remaining := whereClause[countIdx:]
	braceDepth := 0
	closeIdx := -1
	for i := 0; i < len(remaining); i++ {
		if remaining[i] == '{' {
			braceDepth++
		} else if remaining[i] == '}' {
			braceDepth--
			if braceDepth == 0 {
				closeIdx = i
				break
			}
		}
	}

	if closeIdx == -1 {
		// No closing brace, invalid
		return false
	}

	// Get comparison part after COUNT { }
	comparison := strings.TrimSpace(remaining[closeIdx+1:])
	if comparison == "" {
		// No comparison, return true if count > 0
		return count > 0
	}

	// Parse comparison operator and value
	var op string
	var valueStr string

	if strings.HasPrefix(comparison, ">=") {
		op = ">="
		valueStr = strings.TrimSpace(comparison[2:])
	} else if strings.HasPrefix(comparison, "<=") {
		op = "<="
		valueStr = strings.TrimSpace(comparison[2:])
	} else if strings.HasPrefix(comparison, ">") {
		op = ">"
		valueStr = strings.TrimSpace(comparison[1:])
	} else if strings.HasPrefix(comparison, "<") {
		op = "<"
		valueStr = strings.TrimSpace(comparison[1:])
	} else if strings.HasPrefix(comparison, "=") {
		op = "="
		valueStr = strings.TrimSpace(comparison[1:])
	} else if strings.HasPrefix(comparison, "!=") || strings.HasPrefix(comparison, "<>") {
		op = "!="
		if strings.HasPrefix(comparison, "!=") {
			valueStr = strings.TrimSpace(comparison[2:])
		} else {
			valueStr = strings.TrimSpace(comparison[2:])
		}
	} else {
		// No valid operator, treat as > 0
		return count > 0
	}

	// Parse the comparison value
	var compareValue int64
	_, err := fmt.Sscanf(valueStr, "%d", &compareValue)
	if err != nil {
		// Invalid number, treat as false
		return false
	}

	// Perform comparison
	switch op {
	case ">":
		return count > compareValue
	case ">=":
		return count >= compareValue
	case "<":
		return count < compareValue
	case "<=":
		return count <= compareValue
	case "=":
		return count == compareValue
	case "!=":
		return count != compareValue
	default:
		return false
	}
}

// countSubqueryMatches counts how many matches a subquery produces
func (e *StorageExecutor) countSubqueryMatches(node *storage.Node, variable, subquery string) int64 {
	// Parse the MATCH pattern from the subquery
	upperSub := strings.ToUpper(subquery)

	if !strings.HasPrefix(upperSub, "MATCH ") {
		return 0
	}

	pattern := strings.TrimSpace(subquery[6:])

	// Check if pattern references our variable
	if !strings.Contains(pattern, "("+variable+")") && !strings.Contains(pattern, "("+variable+":") {
		return 0
	}

	// Parse relationship pattern
	var checkIncoming, checkOutgoing bool
	var relTypes []string

	if strings.Contains(pattern, "<-[") {
		checkIncoming = true
		relTypes = e.extractRelTypesFromPattern(pattern, "<-[")
	}
	if strings.Contains(pattern, "]->(") || strings.Contains(pattern, "]->") {
		checkOutgoing = true
		relTypes = e.extractRelTypesFromPattern(pattern, "-[")
	}

	// Count matching edges
	var count int64

	if checkIncoming {
		edges, _ := e.storage.GetIncomingEdges(node.ID)
		for _, edge := range edges {
			if len(relTypes) == 0 || e.edgeTypeMatches(edge.Type, relTypes) {
				count++
			}
		}
	}

	if checkOutgoing {
		edges, _ := e.storage.GetOutgoingEdges(node.ID)
		for _, edge := range edges {
			if len(relTypes) == 0 || e.edgeTypeMatches(edge.Type, relTypes) {
				count++
			}
		}
	}

	// If no direction specified, count both
	if !checkIncoming && !checkOutgoing {
		incoming, _ := e.storage.GetIncomingEdges(node.ID)
		outgoing, _ := e.storage.GetOutgoingEdges(node.ID)
		count = int64(len(incoming) + len(outgoing))
	}

	return count
}
