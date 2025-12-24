// Package server provides HTTP REST API server tests.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orneryd/nornicdb/pkg/nornicdb"
	"github.com/stretchr/testify/assert"
)

func TestBackupInvalidJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	req := httptest.NewRequest("POST", "/admin/backup", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestHandleUserByIDGet(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Get admin user by username (not ID in this case)
	resp := makeRequest(t, server, "GET", "/auth/users/admin", nil, "Bearer "+token)
	// May be 200 or 404 depending on if GetUser finds by username
	if resp.Code != http.StatusOK && resp.Code != http.StatusNotFound {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestHandleUserByIDPut(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Create a user first
	_ = makeRequest(t, server, "POST", "/auth/users", map[string]interface{}{
		"username": "updatetestuser",
		"password": "password123",
		"roles":    []string{"viewer"},
	}, "Bearer "+token)

	// Update the user
	resp := makeRequest(t, server, "PUT", "/auth/users/updatetestuser", map[string]interface{}{
		"roles": []string{"editor"},
	}, "Bearer "+token)

	if resp.Code != http.StatusOK && resp.Code != http.StatusBadRequest {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestHandleUserByIDPutDisable(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Create a user first
	createResp := makeRequest(t, server, "POST", "/auth/users", map[string]interface{}{
		"username": "disabletestuser",
		"password": "password123",
		"roles":    []string{"viewer"},
	}, "Bearer "+token)

	assert.Equal(t, http.StatusCreated, createResp.Code)

	// Disable the user
	disabled := true
	resp := makeRequest(t, server, "PUT", "/auth/users/disabletestuser", map[string]interface{}{
		"disabled": &disabled,
	}, "Bearer "+token)

	if resp.Code != http.StatusOK && resp.Code != http.StatusBadRequest {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestHandleUserByIDPutEnable(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Create and disable a user first
	createResp := makeRequest(t, server, "POST", "/auth/users", map[string]interface{}{
		"username": "enabletestuser",
		"password": "password123",
		"roles":    []string{"viewer"},
	}, "Bearer "+token)

	assert.Equal(t, http.StatusCreated, createResp.Code)
	// Enable the user
	disabled := false
	resp := makeRequest(t, server, "PUT", "/auth/users/enabletestuser", map[string]interface{}{
		"disabled": &disabled,
	}, "Bearer "+token)

	if resp.Code != http.StatusOK && resp.Code != http.StatusBadRequest {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestHandleUserByIDPutInvalidJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	req := httptest.NewRequest("PUT", "/auth/users/testuser", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestHandleUserByIDDelete(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Create a user first
	createResp := makeRequest(t, server, "POST", "/auth/users", map[string]interface{}{
		"username": "deletetestuser",
		"password": "password123",
		"roles":    []string{"viewer"},
	}, "Bearer "+token)

	assert.Equal(t, http.StatusCreated, createResp.Code)

	// Delete the user
	resp := makeRequest(t, server, "DELETE", "/auth/users/deletetestuser", nil, "Bearer "+token)

	if resp.Code != http.StatusOK && resp.Code != http.StatusNotFound {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestHandleUserByIDEmptyUsername(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/auth/users/", nil, "Bearer "+token)
	// This should route to /auth/users (list) not the by-ID handler
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200 for /auth/users/, got %d", resp.Code)
	}
}

func TestHandleUsersMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "PUT", "/auth/users", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for PUT on /auth/users, got %d", resp.Code)
	}
}

func TestHandleUserByIDMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "POST", "/auth/users/admin", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed && resp.Code != http.StatusBadRequest {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestImplicitTransactionWithError(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Send a query with syntax error
	resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "INVALID CYPHER SYNTAX HERE"},
		},
	}, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200 (with errors in response), got %d", resp.Code)
	}

	// Check that response contains errors
	var txResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&txResp)
	errors, ok := txResp["errors"].([]interface{})
	if !ok || len(errors) == 0 {
		t.Error("expected errors in response for invalid query")
	}
}

func TestImplicitTransactionMultipleStatementsWithError(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// First statement is valid, second is invalid
	resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "MATCH (n) RETURN count(n)"},
			{"statement": "INVALID SYNTAX"},
		},
	}, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}

	var txResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&txResp)
	errors, ok := txResp["errors"].([]interface{})
	if !ok || len(errors) == 0 {
		t.Error("expected errors in response")
	}
}

func TestOpenTransactionWithError(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Open transaction with invalid statement
	resp := makeRequest(t, server, "POST", "/db/nornic/tx", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "INVALID CYPHER"},
		},
	}, "Bearer "+token)

	if resp.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.Code)
	}

	var txResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&txResp)
	errors, ok := txResp["errors"].([]interface{})
	if !ok || len(errors) == 0 {
		t.Error("expected errors in response")
	}
}

func TestCommitTransactionWithError(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Open transaction
	openResp := makeRequest(t, server, "POST", "/db/nornic/tx", map[string]interface{}{
		"statements": []map[string]interface{}{},
	}, "Bearer "+token)

	var openResult map[string]interface{}
	json.NewDecoder(openResp.Body).Decode(&openResult)

	commitURL := openResult["commit"].(string)
	parts := strings.Split(commitURL, "/")
	txID := parts[len(parts)-2]

	// Commit with invalid statement
	commitResp := makeRequest(t, server, "POST", fmt.Sprintf("/db/nornic/tx/%s/commit", txID), map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "INVALID SYNTAX"},
		},
	}, "Bearer "+token)

	if commitResp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", commitResp.Code)
	}

	var txResp map[string]interface{}
	json.NewDecoder(commitResp.Body).Decode(&txResp)
	errors, ok := txResp["errors"].([]interface{})
	if !ok || len(errors) == 0 {
		t.Error("expected errors in response")
	}
}

func TestTransactionMethodNotAllowedCommit(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/db/nornic/tx/commit", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestTransactionMethodNotAllowedTxID(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/db/nornic/tx/123456", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestTransactionMethodNotAllowedCommitID(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/db/nornic/tx/123456/commit", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestTokenGrantTypeUnsupported(t *testing.T) {
	server, _ := setupTestServer(t)

	resp := makeRequest(t, server, "POST", "/auth/token", map[string]interface{}{
		"username":   "admin",
		"password":   "password123",
		"grant_type": "unsupported_type",
	}, "")

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for unsupported grant_type, got %d", resp.Code)
	}
}

func TestCreateUserError(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Try to create user with existing username
	resp := makeRequest(t, server, "POST", "/auth/users", map[string]interface{}{
		"username": "admin", // Already exists
		"password": "password123",
		"roles":    []string{"viewer"},
	}, "Bearer "+token)

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for duplicate username, got %d", resp.Code)
	}
}

func TestUpdateUserRolesError(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Try to update non-existent user
	resp := makeRequest(t, server, "PUT", "/auth/users/nonexistentuser", map[string]interface{}{
		"roles": []string{"admin"},
	}, "Bearer "+token)

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for non-existent user, got %d", resp.Code)
	}
}

func TestAuthWithNilClaims(t *testing.T) {
	server, _ := setupTestServer(t)

	// Request without any auth should fail on protected endpoint
	resp := makeRequest(t, server, "GET", "/admin/stats", nil, "")

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 without auth, got %d", resp.Code)
	}
}

func TestCORSWithSpecificOrigin(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	// Should have CORS headers
	if recorder.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("missing Access-Control-Allow-Origin header")
	}
}

func TestMetricsAfterRequests(t *testing.T) {
	server, _ := setupTestServer(t)

	// Make a request
	makeRequest(t, server, "GET", "/health", nil, "")

	stats := server.Stats()
	if stats.RequestCount < 1 {
		t.Errorf("expected request count >= 1, got %d", stats.RequestCount)
	}
}

func TestServerStopWithoutStart(t *testing.T) {
	server, _ := setupTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Stop without starting should not error
	err := server.Stop(ctx)
	if err != nil {
		t.Errorf("stop without start should not error: %v", err)
	}
}

func TestServerStopTwice(t *testing.T) {
	server, _ := setupTestServer(t)

	go server.Start()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First stop
	err := server.Stop(ctx)
	if err != nil {
		t.Errorf("first stop error: %v", err)
	}

	// Second stop should be idempotent
	err = server.Stop(ctx)
	if err != nil {
		t.Errorf("second stop should be idempotent: %v", err)
	}
}

// =============================================================================
// CORS Security Tests
// =============================================================================

func TestCORSWildcardDoesNotSendCredentials(t *testing.T) {
	// SECURITY TEST: When CORS origin is wildcard (*), we must NOT send
	// Access-Control-Allow-Credentials header to prevent CSRF attacks.
	tmpDir, err := os.MkdirTemp("", "nornicdb-cors-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := nornicdb.DefaultConfig()
	config.DecayEnabled = false
	config.AsyncWritesEnabled = false

	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create server with wildcard CORS
	serverConfig := DefaultConfig()
	serverConfig.EnableCORS = true
	serverConfig.CORSOrigins = []string{"*"}

	server, err := New(db, nil, serverConfig)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://evil.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	// Should have wildcard origin
	if origin := recorder.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected wildcard origin, got %s", origin)
	}

	// CRITICAL: Should NOT have credentials header with wildcard
	if creds := recorder.Header().Get("Access-Control-Allow-Credentials"); creds != "" {
		t.Errorf("SECURITY VULNERABILITY: credentials header should NOT be sent with wildcard origin, got %s", creds)
	}
}

func TestCORSSpecificOriginAllowsCredentials(t *testing.T) {
	// When CORS has specific origins (not wildcard), credentials are safe to allow
	tmpDir, err := os.MkdirTemp("", "nornicdb-cors-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := nornicdb.DefaultConfig()
	config.DecayEnabled = false
	config.AsyncWritesEnabled = false

	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create server with specific CORS origins
	serverConfig := DefaultConfig()
	serverConfig.EnableCORS = true
	serverConfig.CORSOrigins = []string{"http://trusted.com", "http://localhost:3000"}

	server, err := New(db, nil, serverConfig)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://trusted.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	// Should echo back the specific origin
	if origin := recorder.Header().Get("Access-Control-Allow-Origin"); origin != "http://trusted.com" {
		t.Errorf("expected trusted.com origin, got %s", origin)
	}

	// Should allow credentials for specific origins
	if creds := recorder.Header().Get("Access-Control-Allow-Credentials"); creds != "true" {
		t.Errorf("expected credentials=true for specific origin, got %s", creds)
	}
}

func TestCORSDisallowedOriginNoHeaders(t *testing.T) {
	// When origin is not in allowed list, no CORS headers should be sent
	tmpDir, err := os.MkdirTemp("", "nornicdb-cors-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := nornicdb.DefaultConfig()
	config.DecayEnabled = false
	config.AsyncWritesEnabled = false

	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create server with specific CORS origins (not including evil.com)
	serverConfig := DefaultConfig()
	serverConfig.EnableCORS = true
	serverConfig.CORSOrigins = []string{"http://trusted.com"}

	server, err := New(db, nil, serverConfig)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://evil.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	// Should NOT have origin header for disallowed origins
	if origin := recorder.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Errorf("expected no origin header for disallowed origin, got %s", origin)
	}
}

// =============================================================================
// Rate Limiter Tests
// =============================================================================

func TestIPRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewIPRateLimiter(10, 100, 5) // 10/min, 100/hour, burst 5
	defer rl.Stop()

	// Should allow requests within limit
	for i := 0; i < 10; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Errorf("request %d should be allowed within limit", i+1)
		}
	}
}

func TestIPRateLimiter_BlocksExcessRequests(t *testing.T) {
	rl := NewIPRateLimiter(5, 100, 2) // 5/min, 100/hour, burst 2
	defer rl.Stop()

	// Use up the limit
	for i := 0; i < 5; i++ {
		rl.Allow("192.168.1.1")
	}

	// Next request should be blocked
	if rl.Allow("192.168.1.1") {
		t.Error("request exceeding limit should be blocked")
	}
}

func TestIPRateLimiter_DifferentIPsAreSeparate(t *testing.T) {
	rl := NewIPRateLimiter(3, 100, 1) // 3/min
	defer rl.Stop()

	// Use up limit for IP1
	for i := 0; i < 3; i++ {
		rl.Allow("192.168.1.1")
	}

	// IP2 should still be allowed
	if !rl.Allow("192.168.1.2") {
		t.Error("different IP should have separate limit")
	}

	// IP1 should be blocked
	if rl.Allow("192.168.1.1") {
		t.Error("IP1 should be rate limited")
	}
}

func TestRateLimitMiddleware_Returns429WhenLimited(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nornicdb-ratelimit-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := nornicdb.DefaultConfig()
	config.DecayEnabled = false
	config.AsyncWritesEnabled = false

	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create server with rate limiting enabled
	serverConfig := DefaultConfig()
	serverConfig.RateLimitEnabled = true
	serverConfig.RateLimitPerMinute = 2
	serverConfig.RateLimitPerHour = 100
	serverConfig.RateLimitBurst = 1

	server, err := New(db, nil, serverConfig)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer server.rateLimiter.Stop()

	router := server.buildRouter()

	// First two requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if recorder.Code == http.StatusTooManyRequests {
			t.Errorf("request %d should not be rate limited", i+1)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", recorder.Code)
	}

	// Check Retry-After header
	if retry := recorder.Header().Get("Retry-After"); retry == "" {
		t.Error("expected Retry-After header on rate limited response")
	}
}

func TestRateLimitMiddleware_SkipsHealthEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nornicdb-ratelimit-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := nornicdb.DefaultConfig()
	config.DecayEnabled = false
	config.AsyncWritesEnabled = false

	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create server with very strict rate limiting
	serverConfig := DefaultConfig()
	serverConfig.RateLimitEnabled = true
	serverConfig.RateLimitPerMinute = 1
	serverConfig.RateLimitPerHour = 1
	serverConfig.RateLimitBurst = 1

	server, err := New(db, nil, serverConfig)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer server.rateLimiter.Stop()

	router := server.buildRouter()

	// Exhaust rate limit on regular endpoint
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	// Health endpoint should STILL work (not rate limited)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)

		if recorder.Code == http.StatusTooManyRequests {
			t.Error("health endpoint should not be rate limited")
		}
	}
}

// =============================================================================
// Secure Default Configuration Tests
// =============================================================================

func TestDefaultConfig_SecureDefaults(t *testing.T) {
	config := DefaultConfig()

	// SECURITY: Default should bind to localhost only
	if config.Address != "127.0.0.1" {
		t.Errorf("expected default address 127.0.0.1, got %s", config.Address)
	}

	// SECURITY: Default CORS origins should be asterisk (explicit configuration required)
	if len(config.CORSOrigins) != 1 || config.CORSOrigins[0] != "*" {
		t.Errorf("expected default CORS origins to be [\"*\"], got %v", config.CORSOrigins)
	}

	// SECURITY: CORS should be enabled by default - must be explicitly diabled if not desired
	if config.EnableCORS == false {
		t.Error("expected EnableCORS=true by default")
	}
}

// =============================================================================
// Protected Endpoint Tests
// =============================================================================

func TestStatusEndpointRequiresAuth(t *testing.T) {
	server, _ := setupTestServer(t)

	// Request without auth should fail
	resp := makeRequest(t, server, "GET", "/status", nil, "")

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for /status without auth, got %d", resp.Code)
	}
}

func TestMetricsEndpointRequiresAuth(t *testing.T) {
	server, _ := setupTestServer(t)

	// Request without auth should fail
	resp := makeRequest(t, server, "GET", "/metrics", nil, "")

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for /metrics without auth, got %d", resp.Code)
	}
}

func TestHealthEndpointMinimalInfo(t *testing.T) {
	server, _ := setupTestServer(t)

	resp := makeRequest(t, server, "GET", "/health", nil, "")

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200 for /health, got %d", resp.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse health response: %v", err)
	}

	// Should only have minimal info
	if _, hasEmbeddings := result["embeddings"]; hasEmbeddings {
		t.Error("health endpoint should not expose embedding details")
	}

	// Should have status
	if status, ok := result["status"].(string); !ok || status != "healthy" {
		t.Errorf("expected status=healthy, got %v", result["status"])
	}
}

func TestStatusEndpointWithAuth(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Request with auth should succeed
	resp := makeRequest(t, server, "GET", "/status", nil, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200 for /status with auth, got %d", resp.Code)
	}
}

func TestMetricsEndpointWithAuth(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Request with auth should succeed
	resp := makeRequest(t, server, "GET", "/metrics", nil, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200 for /metrics with auth, got %d", resp.Code)
	}
}

// TestStripCypherComments tests the stripCypherComments function.
func TestStripCypherComments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no comments",
			input:    "MATCH (n) RETURN n",
			expected: "MATCH (n) RETURN n",
		},
		{
			name:     "single-line comment at end",
			input:    "MATCH (n) RETURN n // comment",
			expected: "MATCH (n) RETURN n ",
		},
		{
			name:     "single-line comment on own line",
			input:    "MATCH (n)\n// comment\nRETURN n",
			expected: "MATCH (n)\n\nRETURN n",
		},
		{
			name:     "multi-line comment inline",
			input:    "MATCH (n) /* comment */ RETURN n",
			expected: "MATCH (n)  RETURN n",
		},
		{
			name:     "multi-line comment spanning lines",
			input:    "MATCH (n)\n/* comment\n   more comment */\nRETURN n",
			expected: "MATCH (n)\n\nRETURN n",
		},
		{
			name:     "multiple single-line comments",
			input:    "MATCH (n) // first\nWHERE n.age > 25 // second\nRETURN n // third",
			expected: "MATCH (n) \nWHERE n.age > 25 \nRETURN n ",
		},
		{
			name:     "comment only line",
			input:    "// comment only\nMATCH (n) RETURN n",
			expected: "\nMATCH (n) RETURN n",
		},
		{
			name:     "mixed comments",
			input:    "MATCH (n) /* multi */ // single\nRETURN n",
			expected: "MATCH (n)  \nRETURN n",
		},
		{
			name:     "empty query",
			input:    "",
			expected: "",
		},
		{
			name:     "only comments",
			input:    "// comment\n/* another */",
			expected: "\n",
		},
		{
			name:     "comment with :USE command",
			input:    ":USE test_db\n// comment\nMATCH (n) RETURN n",
			expected: ":USE test_db\n\nMATCH (n) RETURN n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripCypherComments(tt.input)
			if result != tt.expected {
				t.Errorf("stripCypherComments(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
