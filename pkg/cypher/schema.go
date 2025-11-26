// Schema command parsing and execution for Cypher.
//
// This file implements Neo4j schema management commands:
//   - CREATE CONSTRAINT
//   - CREATE INDEX
//   - CREATE FULLTEXT INDEX
//   - CREATE VECTOR INDEX
package cypher

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// executeSchemaCommand handles CREATE CONSTRAINT and CREATE INDEX commands.
func (e *StorageExecutor) executeSchemaCommand(ctx context.Context, cypher string) (*ExecuteResult, error) {
	upper := strings.ToUpper(cypher)

	// Order matters: check more specific patterns first
	if strings.Contains(upper, "CREATE CONSTRAINT") {
		return e.executeCreateConstraint(ctx, cypher)
	} else if strings.Contains(upper, "CREATE FULLTEXT INDEX") {
		return e.executeCreateFulltextIndex(ctx, cypher)
	} else if strings.Contains(upper, "CREATE VECTOR INDEX") {
		return e.executeCreateVectorIndex(ctx, cypher)
	} else if strings.Contains(upper, "CREATE INDEX") {
		return e.executeCreateIndex(ctx, cypher)
	}

	return nil, fmt.Errorf("unknown schema command: %s", cypher)
}

// executeCreateConstraint handles CREATE CONSTRAINT commands.
//
// Supported syntax (Neo4j 5.x):
//   CREATE CONSTRAINT constraint_name IF NOT EXISTS FOR (n:Label) REQUIRE n.property IS UNIQUE
// Supported syntax (Neo4j 4.x):
//   CREATE CONSTRAINT IF NOT EXISTS ON (n:Label) ASSERT n.property IS UNIQUE
func (e *StorageExecutor) executeCreateConstraint(ctx context.Context, cypher string) (*ExecuteResult, error) {
	// Pattern 1 (Neo4j 5.x): CREATE CONSTRAINT name IF NOT EXISTS FOR (n:Label) REQUIRE n.property IS UNIQUE
	re := regexp.MustCompile(`(?i)CREATE\s+CONSTRAINT\s+(\w+)(?:\s+IF\s+NOT\s+EXISTS)?\s+FOR\s+\((\w+):(\w+)\)\s+REQUIRE\s+(\w+)\.(\w+)\s+IS\s+UNIQUE`)
	matches := re.FindStringSubmatch(cypher)

	if matches != nil {
		constraintName := matches[1]
		label := matches[3]
		property := matches[5]

		// Add constraint to schema
		if err := e.storage.GetSchema().AddUniqueConstraint(constraintName, label, property); err != nil {
			return nil, err
		}
		return &ExecuteResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
	}

	// Pattern 2 (Neo4j 5.x without name): CREATE CONSTRAINT IF NOT EXISTS FOR (n:Label) REQUIRE n.property IS UNIQUE
	re2 := regexp.MustCompile(`(?i)CREATE\s+CONSTRAINT(?:\s+IF\s+NOT\s+EXISTS)?\s+FOR\s+\((\w+):(\w+)\)\s+REQUIRE\s+(\w+)\.(\w+)\s+IS\s+UNIQUE`)
	matches2 := re2.FindStringSubmatch(cypher)

	if matches2 != nil {
		label := matches2[2]
		property := matches2[4]
		constraintName := fmt.Sprintf("constraint_%s_%s", strings.ToLower(label), strings.ToLower(property))

		if err := e.storage.GetSchema().AddUniqueConstraint(constraintName, label, property); err != nil {
			return nil, err
		}
		return &ExecuteResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
	}

	// Pattern 3 (Neo4j 4.x): CREATE CONSTRAINT IF NOT EXISTS ON (n:Label) ASSERT n.property IS UNIQUE
	re3 := regexp.MustCompile(`(?i)CREATE\s+CONSTRAINT(?:\s+IF\s+NOT\s+EXISTS)?\s+ON\s+\((\w+):(\w+)\)\s+ASSERT\s+(\w+)\.(\w+)\s+IS\s+UNIQUE`)
	matches3 := re3.FindStringSubmatch(cypher)

	if matches3 != nil {
		label := matches3[2]
		property := matches3[4]
		constraintName := fmt.Sprintf("constraint_%s_%s", strings.ToLower(label), strings.ToLower(property))

		if err := e.storage.GetSchema().AddUniqueConstraint(constraintName, label, property); err != nil {
			return nil, err
		}
		return &ExecuteResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
	}

	return nil, fmt.Errorf("invalid CREATE CONSTRAINT syntax")
}

// executeCreateIndex handles CREATE INDEX commands.
//
// Supported syntax:
//   CREATE INDEX index_name IF NOT EXISTS FOR (n:Label) ON (n.property)
func (e *StorageExecutor) executeCreateIndex(ctx context.Context, cypher string) (*ExecuteResult, error) {
	// Pattern: CREATE INDEX name IF NOT EXISTS FOR (n:Label) ON (n.property)
	re := regexp.MustCompile(`(?i)CREATE\s+INDEX\s+(\w+)(?:\s+IF\s+NOT\s+EXISTS)?\s+FOR\s+\((\w+):(\w+)\)\s+ON\s+\((\w+)\.(\w+)\)`)
	matches := re.FindStringSubmatch(cypher)

	if matches == nil {
		// Try without index name
		re2 := regexp.MustCompile(`(?i)CREATE\s+INDEX(?:\s+IF\s+NOT\s+EXISTS)?\s+FOR\s+\((\w+):(\w+)\)\s+ON\s+\((\w+)\.(\w+)\)`)
		matches2 := re2.FindStringSubmatch(cypher)

		if matches2 == nil {
			return nil, fmt.Errorf("invalid CREATE INDEX syntax")
		}

		label := matches2[2]
		property := matches2[4]
		indexName := fmt.Sprintf("index_%s_%s", strings.ToLower(label), strings.ToLower(property))

		// Add index
		if err := e.storage.GetSchema().AddPropertyIndex(indexName, label, []string{property}); err != nil {
			return nil, err
		}

		return &ExecuteResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
	}

	indexName := matches[1]
	label := matches[3]
	property := matches[5]

	// Add index to schema
	if err := e.storage.GetSchema().AddPropertyIndex(indexName, label, []string{property}); err != nil {
		return nil, err
	}

	return &ExecuteResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
}

// executeCreateFulltextIndex handles CREATE FULLTEXT INDEX commands.
//
// Supported syntax:
//   CREATE FULLTEXT INDEX index_name IF NOT EXISTS
//   FOR (n:Label) ON EACH [n.prop1, n.prop2]
func (e *StorageExecutor) executeCreateFulltextIndex(ctx context.Context, cypher string) (*ExecuteResult, error) {
	// Pattern: CREATE FULLTEXT INDEX name IF NOT EXISTS FOR (n:Label) ON EACH [n.prop1, n.prop2]
	re := regexp.MustCompile(`(?i)CREATE\s+FULLTEXT\s+INDEX\s+(\w+)(?:\s+IF\s+NOT\s+EXISTS)?\s+FOR\s+\((\w+):(\w+)\)\s+ON\s+EACH\s+\[([^\]]+)\]`)
	matches := re.FindStringSubmatch(cypher)

	if matches == nil {
		return nil, fmt.Errorf("invalid CREATE FULLTEXT INDEX syntax: %s", cypher)
	}

	indexName := matches[1]
	label := matches[3]
	propertiesStr := matches[4]

	// Parse properties: "n.prop1, n.prop2" -> ["prop1", "prop2"]
	properties := []string{}
	for _, prop := range strings.Split(propertiesStr, ",") {
		prop = strings.TrimSpace(prop)
		// Extract property name from "n.property"
		if parts := strings.Split(prop, "."); len(parts) == 2 {
			properties = append(properties, parts[1])
		}
	}

	if len(properties) == 0 {
		return nil, fmt.Errorf("no properties found in fulltext index definition")
	}

	// Add fulltext index
	schema := e.storage.GetSchema()
	if schema == nil {
		return nil, fmt.Errorf("schema manager not available")
	}
	
	if err := schema.AddFulltextIndex(indexName, []string{label}, properties); err != nil {
		return nil, fmt.Errorf("failed to add fulltext index: %w", err)
	}

	return &ExecuteResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
}

// executeCreateVectorIndex handles CREATE VECTOR INDEX commands.
//
// Supported syntax:
//   CREATE VECTOR INDEX index_name IF NOT EXISTS
//   FOR (n:Label) ON (n.property)
//   OPTIONS {indexConfig: {`vector.dimensions`: 1024, `vector.similarity_function`: 'cosine'}}
func (e *StorageExecutor) executeCreateVectorIndex(ctx context.Context, cypher string) (*ExecuteResult, error) {
	// Pattern: CREATE VECTOR INDEX name IF NOT EXISTS FOR (n:Label) ON (n.property)
	re := regexp.MustCompile(`(?i)CREATE\s+VECTOR\s+INDEX\s+(\w+)(?:\s+IF\s+NOT\s+EXISTS)?\s+FOR\s+\((\w+):(\w+)\)\s+ON\s+\((\w+)\.(\w+)\)`)
	matches := re.FindStringSubmatch(cypher)

	if matches == nil {
		return nil, fmt.Errorf("invalid CREATE VECTOR INDEX syntax")
	}

	indexName := matches[1]
	label := matches[3]
	property := matches[5]

	// Parse OPTIONS if present
	dimensions := 1024 // Default
	similarityFunc := "cosine" // Default

	if strings.Contains(cypher, "OPTIONS") {
		// Extract dimensions
		dimRe := regexp.MustCompile(`vector\.dimensions[:\s]+(\d+)`)
		if dimMatches := dimRe.FindStringSubmatch(cypher); dimMatches != nil {
			if dim, err := strconv.Atoi(dimMatches[1]); err == nil {
				dimensions = dim
			}
		}

		// Extract similarity function
		simRe := regexp.MustCompile(`vector\.similarity_function[:\s]+['"]?(\w+)['"]?`)
		if simMatches := simRe.FindStringSubmatch(cypher); simMatches != nil {
			similarityFunc = simMatches[1]
		}
	}

	// Add vector index
	if err := e.storage.GetSchema().AddVectorIndex(indexName, label, property, dimensions, similarityFunc); err != nil {
		return nil, err
	}

	return &ExecuteResult{Columns: []string{}, Rows: [][]interface{}{}}, nil
}
