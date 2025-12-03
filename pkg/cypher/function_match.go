// Whitespace-tolerant function matching helpers for Cypher parsing.
// These helpers allow optional whitespace between function names and opening parentheses,
// making the parser compatible with formatted Cypher queries like "COUNT (n)" or "count  (n)".

package cypher

import (
	"regexp"
	"strings"
	"sync"
)

// funcMatchCache stores compiled regex patterns for function matching.
// Using sync.Map for thread-safe lazy initialization.
var funcMatchCache sync.Map

// getFuncMatcher returns a compiled regex for matching a function name with optional whitespace.
// Pattern: ^funcname\s*\( (case-insensitive)
// Cached for performance - each function name is compiled only once.
func getFuncMatcher(funcName string) *regexp.Regexp {
	if cached, ok := funcMatchCache.Load(funcName); ok {
		return cached.(*regexp.Regexp)
	}
	// Pattern: start of string, function name, optional whitespace, open paren
	pattern := `(?i)^` + regexp.QuoteMeta(funcName) + `\s*\(`
	re := regexp.MustCompile(pattern)
	funcMatchCache.Store(funcName, re)
	return re
}

// matchFuncStart checks if expr starts with funcName followed by optional whitespace and '('.
// Case-insensitive. Returns true if matched.
//
// Examples:
//   - matchFuncStart("count(n)", "count")     → true
//   - matchFuncStart("COUNT (n)", "count")    → true
//   - matchFuncStart("count  (n)", "count")   → true
//   - matchFuncStart("count\n(n)", "count")   → true
//   - matchFuncStart("countx(n)", "count")    → false (different function)
//   - matchFuncStart("xcount(n)", "count")    → false (prefix doesn't match)
func matchFuncStart(expr, funcName string) bool {
	return getFuncMatcher(funcName).MatchString(expr)
}

// matchFuncStartAndSuffix checks if expr starts with funcName( and ends with ).
// Allows optional whitespace between function name and opening paren.
// This is the whitespace-tolerant replacement for:
//
//	strings.HasPrefix(lowerExpr, "funcname(") && strings.HasSuffix(expr, ")")
//
// Examples:
//   - matchFuncStartAndSuffix("count(n)", "count")    → true
//   - matchFuncStartAndSuffix("COUNT (n)", "count")   → true
//   - matchFuncStartAndSuffix("count(n) + 1", "count") → false (doesn't end with ))
func matchFuncStartAndSuffix(expr, funcName string) bool {
	return matchFuncStart(expr, funcName) && strings.HasSuffix(expr, ")")
}

// extractFuncArgs extracts the arguments string from a function call expression.
// Assumes the expression is already validated as a function call.
// Returns empty string if not a valid function call format.
//
// Examples:
//   - extractFuncArgs("count(n)", "count")           → "n"
//   - extractFuncArgs("COUNT (n, m)", "count")       → "n, m"
//   - extractFuncArgs("substring('hello', 0)", "substring") → "'hello', 0"
func extractFuncArgs(expr, funcName string) string {
	if !matchFuncStart(expr, funcName) {
		return ""
	}
	// Find the opening paren position
	idx := strings.Index(strings.ToLower(expr), "(")
	if idx == -1 || !strings.HasSuffix(expr, ")") {
		return ""
	}
	// Return content between ( and )
	return strings.TrimSpace(expr[idx+1 : len(expr)-1])
}

// extractFuncArgsLen returns the arguments and the position of the opening paren.
// Useful when you need to know where the function name ends.
//
// Returns: (argsString, openParenIndex)
// If not matched, returns ("", -1)
func extractFuncArgsLen(expr, funcName string) (string, int) {
	if !matchFuncStart(expr, funcName) {
		return "", -1
	}
	idx := strings.Index(strings.ToLower(expr), "(")
	if idx == -1 || !strings.HasSuffix(expr, ")") {
		return "", -1
	}
	return strings.TrimSpace(expr[idx+1 : len(expr)-1]), idx
}

// isFunctionCallWS is a whitespace-tolerant version of isFunctionCall.
// Checks if an expression is a standalone function call with balanced parentheses,
// allowing optional whitespace between function name and opening paren.
//
// Examples:
//   - isFunctionCallWS("count(n)", "count")           → true
//   - isFunctionCallWS("COUNT (n)", "count")          → true
//   - isFunctionCallWS("count (n) + 1", "count")      → false (not standalone)
//   - isFunctionCallWS("toLower(count (n))", "count") → false (nested)
func isFunctionCallWS(expr, funcName string) bool {
	if !matchFuncStart(expr, funcName) {
		return false
	}

	// Find the matching closing parenthesis for the opening one
	depth := 0
	inQuote := false
	quoteChar := rune(0)

	for i, ch := range expr {
		switch {
		case (ch == '\'' || ch == '"') && !inQuote:
			inQuote = true
			quoteChar = ch
		case ch == quoteChar && inQuote:
			inQuote = false
			quoteChar = 0
		case ch == '(' && !inQuote:
			depth++
		case ch == ')' && !inQuote:
			depth--
			if depth == 0 {
				// Found the matching closing parenthesis
				// Check if this is the end of the expression
				return i == len(expr)-1
			}
		}
	}
	return false
}
