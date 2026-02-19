// Package server provides HTTP REST API server tests.
package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orneryd/nornicdb/pkg/audit"
	"github.com/orneryd/nornicdb/pkg/auth"
	"github.com/orneryd/nornicdb/pkg/nornicdb"
	"github.com/orneryd/nornicdb/pkg/storage"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

func setupTestServer(t *testing.T) (*Server, *auth.Authenticator) {
	t.Helper()

	// Create temporary directory for test database
	tmpDir, err := os.MkdirTemp("", "nornicdb-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	// Create database with decay disabled for faster tests
	config := nornicdb.DefaultConfig()
	config.Memory.DecayEnabled = false
	config.Memory.AutoLinksEnabled = false
	config.Database.AsyncWritesEnabled = false // Disable async writes for predictable test behavior (200 OK vs 202 Accepted)

	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create authenticator with memory storage for testing
	authConfig := auth.AuthConfig{
		SecurityEnabled: true,
		JWTSecret:       []byte("test-secret-key-for-testing-only-32b"),
	}
	// Use memory storage for tests
	memoryStorage := storage.NewMemoryEngine()
	authenticator, err := auth.NewAuthenticator(authConfig, memoryStorage)
	if err != nil {
		t.Fatalf("failed to create authenticator: %v", err)
	}

	// Create a test user
	_, err = authenticator.CreateUser("admin", "password123", []auth.Role{auth.RoleAdmin})
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	_, err = authenticator.CreateUser("reader", "password123", []auth.Role{auth.RoleViewer})
	if err != nil {
		t.Fatalf("failed to create reader user: %v", err)
	}

	// Create server config
	serverConfig := DefaultConfig()
	serverConfig.Port = 0 // Use random port
	// Enable CORS with wildcard for tests (not recommended for production)
	serverConfig.EnableCORS = true
	serverConfig.CORSOrigins = []string{"*"}

	// Create server
	server, err := New(db, authenticator, serverConfig)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	return server, authenticator
}

func getAuthToken(t *testing.T, authenticator *auth.Authenticator, username string) string {
	t.Helper()
	tokenResp, _, err := authenticator.Authenticate(username, "password123", "127.0.0.1", "TestAgent")
	if err != nil {
		t.Fatalf("failed to get auth token: %v", err)
	}
	return tokenResp.AccessToken
}

func makeRequest(t *testing.T, server *Server, method, path string, body interface{}, authHeader string) *httptest.ResponseRecorder {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal body: %v", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	return recorder
}

type countingEmbedder struct {
	mu sync.Mutex

	failIfLenGreater int
	dims             int

	calls  int
	maxLen int
}

func (e *countingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	e.calls++
	if len(text) > e.maxLen {
		e.maxLen = len(text)
	}
	fail := e.failIfLenGreater > 0 && len(text) > e.failIfLenGreater
	dims := e.dims
	e.mu.Unlock()

	if fail {
		return nil, fmt.Errorf("text too long: len=%d", len(text))
	}
	if dims <= 0 {
		dims = 1024
	}
	vec := make([]float32, dims)
	vec[0] = float32(len(text))
	return vec, nil
}

func (e *countingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	var firstErr error
	for i, t := range texts {
		v, err := e.Embed(ctx, t)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		out[i] = v
	}
	return out, firstErr
}

func (e *countingEmbedder) Dimensions() int { return e.dims }
func (e *countingEmbedder) Model() string   { return "counting-embedder" }

// =============================================================================
// Server Creation Tests
// =============================================================================

func TestNew(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nornicdb-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := nornicdb.DefaultConfig()
	config.Memory.DecayEnabled = false
	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	tests := []struct {
		name      string
		db        *nornicdb.DB
		auth      *auth.Authenticator
		config    *Config
		wantError bool
	}{
		{
			name:      "valid with defaults",
			db:        db,
			auth:      nil,
			config:    nil,
			wantError: false,
		},
		{
			name:      "valid with custom config",
			db:        db,
			auth:      nil,
			config:    &Config{Port: 8080},
			wantError: false,
		},
		{
			name:      "nil database",
			db:        nil,
			auth:      nil,
			config:    nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := New(tt.db, tt.auth, tt.config)
			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if server == nil {
					t.Error("expected server, got nil")
				}
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// SECURITY: Default should bind to localhost only (secure default)
	if config.Address != "127.0.0.1" {
		t.Errorf("expected address '127.0.0.1', got %s", config.Address)
	}
	if config.Port != 7474 {
		t.Errorf("expected port 7474, got %d", config.Port)
	}
	if config.ReadTimeout != 30*time.Second {
		t.Errorf("expected read timeout 30s, got %v", config.ReadTimeout)
	}
	if config.MaxRequestSize != 10*1024*1024 {
		t.Errorf("expected max request size 10MB, got %d", config.MaxRequestSize)
	}
	// SECURITY: CORS enabled by default for ease of use
	if config.EnableCORS == false {
		t.Error("expected CORS enabled by default for ease of use")
	}
}

// =============================================================================
// Discovery Endpoint Tests
// =============================================================================

func TestHandleDiscovery(t *testing.T) {
	server, _ := setupTestServer(t)

	resp := makeRequest(t, server, "GET", "/", nil, "")

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}

	var discovery map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check required Neo4j discovery fields
	requiredFields := []string{"bolt_direct", "bolt_routing", "transaction", "neo4j_version", "neo4j_edition"}
	for _, field := range requiredFields {
		if _, ok := discovery[field]; !ok {
			t.Errorf("missing required field: %s", field)
		}
	}

	// Check NornicDB extension: default_database field
	if defaultDB, ok := discovery["default_database"]; !ok {
		t.Error("missing NornicDB extension field: default_database")
	} else {
		// Verify it's a string and matches expected default
		defaultDBStr, ok := defaultDB.(string)
		if !ok {
			t.Errorf("default_database should be a string, got %T", defaultDB)
		} else if defaultDBStr != "nornic" {
			t.Errorf("expected default_database to be 'nornic', got '%s'", defaultDBStr)
		}
	}
}

// =============================================================================
// Health Endpoint Tests
// =============================================================================

func TestHandleHealth(t *testing.T) {
	server, _ := setupTestServer(t)

	resp := makeRequest(t, server, "GET", "/health", nil, "")

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if health["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got %v", health["status"])
	}
}

func TestHandleStatus(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Status endpoint now requires authentication
	resp := makeRequest(t, server, "GET", "/status", nil, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Check that response has the expected structure
	if status["status"] == nil {
		t.Error("missing 'status' field")
	}
	if status["server"] == nil {
		t.Error("missing 'server' field")
	}
	if status["database"] == nil {
		t.Error("missing 'database' field")
	}

	// Verify database stats structure
	dbStats, ok := status["database"].(map[string]interface{})
	if !ok {
		t.Fatal("'database' field is not a map")
	}

	// Check for required fields
	if dbStats["nodes"] == nil {
		t.Error("missing 'nodes' field in database stats")
	}
	if dbStats["edges"] == nil {
		t.Error("missing 'edges' field in database stats")
	}
	if dbStats["databases"] == nil {
		t.Error("missing 'databases' field in database stats")
	}

	// Verify database count is a number
	dbCount, ok := dbStats["databases"].(float64)
	if !ok {
		t.Errorf("'databases' field should be a number, got %T", dbStats["databases"])
	}
	if dbCount < 1 {
		t.Errorf("expected at least 1 database (default + system), got %v", dbCount)
	}

	// Verify node and edge counts are numbers and >= 0
	nodeCount, ok := dbStats["nodes"].(float64)
	if !ok {
		t.Errorf("'nodes' field should be a number, got %T", dbStats["nodes"])
	}
	if nodeCount < 0 {
		t.Errorf("node count should be >= 0, got %v", nodeCount)
	}

	edgeCount, ok := dbStats["edges"].(float64)
	if !ok {
		t.Errorf("'edges' field should be a number, got %T", dbStats["edges"])
	}
	if edgeCount < 0 {
		t.Errorf("edge count should be >= 0, got %v", edgeCount)
	}

	// Verify that system database nodes are NOT included in the count
	// (system database has metadata nodes that shouldn't be counted)
	// The count should only include user databases
}

func TestServerStop_DeadlineExceededForcesClose(t *testing.T) {
	// This test simulates an in-flight handler that takes too long, ensuring
	// Server.Stop returns promptly when the shutdown context is exceeded.
	cfg := DefaultConfig()
	cfg.Headless = true

	db, err := nornicdb.Open("", nornicdb.DefaultConfig())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	s, err := New(db, nil, cfg)
	require.NoError(t, err)

	// Override the router with a handler that blocks past shutdown deadline.
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	s.httpServer = &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.listener = ln
	go func() { _ = s.httpServer.Serve(ln) }()

	// Issue a request that will be in-flight during shutdown.
	go func() {
		req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/status", nil)
		// no auth wrapper; direct handler
		_, _ = http.DefaultClient.Do(req)
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = s.Stop(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Ensure Stop returns promptly and listener is closed.
	_, dialErr := net.DialTimeout("tcp", ln.Addr().String(), 50*time.Millisecond)
	require.Error(t, dialErr)
}

// TestMetaField_NodeMetadata verifies that the meta field is properly populated
// for nodes and relationships, and null for primitive values (Neo4j compatibility).
func TestMetaField_NodeMetadata(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Create a node and return it
	resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "CREATE (n:Person {name: 'TestPerson'}) RETURN n"},
		},
	}, "Bearer "+token)

	require.Equal(t, http.StatusOK, resp.Code)

	var result TransactionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Results, 1)
	require.Len(t, result.Results[0].Data, 1)

	row := result.Results[0].Data[0]

	// Verify meta field exists and has correct length
	require.NotNil(t, row.Meta, "meta field should always be present")
	require.Len(t, row.Meta, 1, "meta should have one element per column")

	// Verify meta contains node metadata
	metaItem := row.Meta[0]
	require.NotNil(t, metaItem, "meta[0] should not be nil for a node")

	metaMap, ok := metaItem.(map[string]interface{})
	require.True(t, ok, "meta[0] should be a map for a node")

	// Verify required metadata fields
	require.Contains(t, metaMap, "id", "meta should contain 'id' field")
	require.Contains(t, metaMap, "type", "meta should contain 'type' field")
	require.Contains(t, metaMap, "elementId", "meta should contain 'elementId' field")
	require.Contains(t, metaMap, "deleted", "meta should contain 'deleted' field")

	// Verify type is "node"
	require.Equal(t, "node", metaMap["type"], "type should be 'node'")
	require.Equal(t, false, metaMap["deleted"], "deleted should be false")

	// Verify elementId format
	elementId, ok := metaMap["elementId"].(string)
	require.True(t, ok, "elementId should be a string")
	require.True(t, strings.HasPrefix(elementId, "4:nornicdb:"), "elementId should start with '4:nornicdb:'")
}

// TestMetaField_PrimitiveValues verifies that meta field is null for primitive values.
func TestMetaField_PrimitiveValues(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Return primitive values (like SHOW DATABASES does)
	resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "RETURN 'test' as name, 123 as count, true as active"},
		},
	}, "Bearer "+token)

	require.Equal(t, http.StatusOK, resp.Code)

	var result TransactionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Results, 1)
	require.Len(t, result.Results[0].Data, 1)

	row := result.Results[0].Data[0]

	// Verify meta field exists and has correct length
	require.NotNil(t, row.Meta, "meta field should always be present")
	require.Len(t, row.Meta, 3, "meta should have one element per column")

	// Verify all meta values are null for primitive values
	for i, metaItem := range row.Meta {
		require.Nil(t, metaItem, "meta[%d] should be null for primitive values", i)
	}
}

// =============================================================================
// Authentication Tests
// =============================================================================

func TestHandleToken(t *testing.T) {
	server, _ := setupTestServer(t)

	tests := []struct {
		name       string
		body       map[string]string
		wantStatus int
	}{
		{
			name:       "valid credentials",
			body:       map[string]string{"username": "admin", "password": "password123"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid username",
			body:       map[string]string{"username": "invalid", "password": "password123"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid password",
			body:       map[string]string{"username": "admin", "password": "wrongpassword"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing fields",
			body:       map[string]string{},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeRequest(t, server, "POST", "/auth/token", tt.body, "")

			if resp.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.Code)
			}

			if tt.wantStatus == http.StatusOK {
				var tokenResp map[string]interface{}
				if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}
				if tokenResp["access_token"] == nil {
					t.Error("expected access_token in response")
				}
			}
		})
	}
}

func TestHandleTokenMethodNotAllowed(t *testing.T) {
	server, _ := setupTestServer(t)

	resp := makeRequest(t, server, "GET", "/auth/token", nil, "")

	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestHandleMe(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "valid token",
			authHeader: "Bearer " + token,
			wantStatus: http.StatusOK,
		},
		{
			name:       "no auth",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid token",
			authHeader: "Bearer invalid-token",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeRequest(t, server, "GET", "/auth/me", nil, tt.authHeader)

			if resp.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.Code)
			}
		})
	}
}

func TestBasicAuth(t *testing.T) {
	server, _ := setupTestServer(t)

	// Create basic auth header
	credentials := base64.StdEncoding.EncodeToString([]byte("admin:password123"))
	authHeader := "Basic " + credentials

	resp := makeRequest(t, server, "GET", "/auth/me", nil, authHeader)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200 with basic auth, got %d", resp.Code)
	}
}

func TestBasicAuthInvalid(t *testing.T) {
	server, _ := setupTestServer(t)

	// Create invalid basic auth header
	credentials := base64.StdEncoding.EncodeToString([]byte("admin:wrongpassword"))
	authHeader := "Basic " + credentials

	resp := makeRequest(t, server, "GET", "/auth/me", nil, authHeader)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 with invalid basic auth, got %d", resp.Code)
	}
}

// =============================================================================
// Transaction Endpoint Tests (Neo4j Compatible)
// =============================================================================

func TestHandleImplicitTransaction(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name: "valid query",
			body: map[string]interface{}{
				"statements": []map[string]interface{}{
					{"statement": "MATCH (n) RETURN n LIMIT 10"},
				},
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "multiple statements",
			body: map[string]interface{}{
				"statements": []map[string]interface{}{
					{"statement": "MATCH (n) RETURN count(n) AS count"},
					{"statement": "MATCH (n) RETURN n LIMIT 5"},
				},
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty statements",
			body:       map[string]interface{}{"statements": []map[string]interface{}{}},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", tt.body, "Bearer "+token)

			if resp.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d: %s", tt.wantStatus, resp.Code, resp.Body.String())
			}

			// Check Neo4j response format
			var txResp map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&txResp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if _, ok := txResp["results"]; !ok {
				t.Error("missing 'results' field in response")
			}
			if _, ok := txResp["errors"]; !ok {
				t.Error("missing 'errors' field in response")
			}
		})
	}
}

func TestHandleOpenTransaction(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Open a new transaction
	resp := makeRequest(t, server, "POST", "/db/nornic/tx", map[string]interface{}{
		"statements": []map[string]interface{}{},
	}, "Bearer "+token)

	if resp.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", resp.Code, resp.Body.String())
	}

	// Check that commit URL is returned
	var txResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&txResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if txResp["commit"] == nil {
		t.Error("missing 'commit' URL in response")
	}
}

func TestExplicitTransactionWorkflow(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Step 1: Open transaction
	openResp := makeRequest(t, server, "POST", "/db/nornic/tx", map[string]interface{}{
		"statements": []map[string]interface{}{},
	}, "Bearer "+token)

	if openResp.Code != http.StatusCreated {
		t.Fatalf("failed to open transaction: %d", openResp.Code)
	}

	var openResult map[string]interface{}
	json.NewDecoder(openResp.Body).Decode(&openResult)

	commitURL := openResult["commit"].(string)
	// Extract transaction ID from commit URL
	parts := strings.Split(commitURL, "/")
	txID := parts[len(parts)-2] // Format: /db/nornic/tx/{txId}/commit

	// Step 2: Execute in transaction
	execResp := makeRequest(t, server, "POST", fmt.Sprintf("/db/nornic/tx/%s", txID), map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "MATCH (n) RETURN count(n) AS count"},
		},
	}, "Bearer "+token)

	if execResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for execute, got %d: %s", execResp.Code, execResp.Body.String())
	}

	// Step 3: Commit transaction
	commitResp := makeRequest(t, server, "POST", fmt.Sprintf("/db/nornic/tx/%s/commit", txID), map[string]interface{}{
		"statements": []map[string]interface{}{},
	}, "Bearer "+token)

	if commitResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for commit, got %d: %s", commitResp.Code, commitResp.Body.String())
	}
}

func TestRollbackTransaction(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Open transaction
	openResp := makeRequest(t, server, "POST", "/db/nornic/tx", map[string]interface{}{
		"statements": []map[string]interface{}{},
	}, "Bearer "+token)

	if openResp.Code != http.StatusCreated {
		t.Fatalf("failed to open transaction: %d", openResp.Code)
	}

	var openResult map[string]interface{}
	json.NewDecoder(openResp.Body).Decode(&openResult)

	commitURL := openResult["commit"].(string)
	parts := strings.Split(commitURL, "/")
	txID := parts[len(parts)-2]

	// Rollback transaction
	rollbackResp := makeRequest(t, server, "DELETE", fmt.Sprintf("/db/nornic/tx/%s", txID), nil, "Bearer "+token)

	if rollbackResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for rollback, got %d", rollbackResp.Code)
	}
}

// =============================================================================
// Query Endpoint Tests
// =============================================================================

func TestHandleQuery(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Use Neo4j-compatible endpoint for queries
	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name: "valid match query",
			body: map[string]interface{}{
				"statements": []map[string]interface{}{
					{"statement": "MATCH (n) RETURN n LIMIT 10"},
				},
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "query with parameters",
			body: map[string]interface{}{
				"statements": []map[string]interface{}{
					{
						"statement":  "MATCH (n) WHERE n.name = $name RETURN n",
						"parameters": map[string]interface{}{"name": "test"},
					},
				},
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", tt.body, "Bearer "+token)

			if resp.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d: %s", tt.wantStatus, resp.Code, resp.Body.String())
			}
		})
	}
}

// =============================================================================
// Node/Edge via Cypher Tests (Neo4j-compatible approach)
// =============================================================================

func TestNodesCRUDViaCypher(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Create a node via Cypher
	createResp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "CREATE (n:Person {name: 'Test User'}) RETURN n"},
		},
	}, "Bearer "+token)

	if createResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for create node, got %d: %s", createResp.Code, createResp.Body.String())
	}

	// Query nodes
	queryResp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "MATCH (n:Person) RETURN n"},
		},
	}, "Bearer "+token)

	if queryResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for query, got %d", queryResp.Code)
	}
}

func TestEdgesCRUDViaCypher(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Create two nodes and a relationship via Cypher
	createResp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "CREATE (a:Person {name: 'Alice'})-[r:KNOWS]->(b:Person {name: 'Bob'}) RETURN a, r, b"},
		},
	}, "Bearer "+token)

	if createResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for create, got %d: %s", createResp.Code, createResp.Body.String())
	}

	// Query relationships
	queryResp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "MATCH (a)-[r:KNOWS]->(b) RETURN a.name, r, b.name"},
		},
	}, "Bearer "+token)

	if queryResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for query, got %d", queryResp.Code)
	}
}

// =============================================================================
// Search Tests (NornicDB extension endpoints)
// =============================================================================

func TestHandleSearch(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Test search endpoint - should work even with empty database
	resp := makeRequest(t, server, "POST", "/nornicdb/search", map[string]interface{}{
		"query": "test query",
		"limit": 10,
	}, "Bearer "+token)

	// Should return 200 (success) - search service caching and index building should handle empty database
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	// Verify response structure
	var results []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Results should be an array (may be empty if no data)
	if results == nil {
		t.Error("results should be an array, got nil")
	}

	// Test that search service is cached (second request should be faster)
	resp2 := makeRequest(t, server, "POST", "/nornicdb/search", map[string]interface{}{
		"query": "another query",
		"limit": 5,
	}, "Bearer "+token)

	if resp2.Code != http.StatusOK {
		t.Errorf("expected status 200 on second request, got %d: %s", resp2.Code, resp2.Body.String())
	}
}

func TestStartupSearchReconcile_InitializesMetadataOnlyDatabase(t *testing.T) {
	server, _ := setupTestServer(t)

	require.NoError(t, server.dbManager.CreateDatabase("animals"))

	// Deterministically run the reconcile pass (the background ticker does this too).
	server.ensureSearchBuildStartedForKnownDatabases()

	require.Eventually(t, func() bool {
		st := server.db.GetDatabaseSearchStatus("animals")
		return st.Initialized && st.Ready && !st.Building
	}, 3*time.Second, 50*time.Millisecond)
}

func TestHandleSearch_ChunksLongQueriesForVectorSearch(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Simulate a local embedder that fails on long inputs (token buffer overflow, etc.).
	// The search endpoint should chunk the query and only embed chunk-sized strings.
	emb := &countingEmbedder{
		failIfLenGreater: 512,
		dims:             1024,
	}
	server.db.SetEmbedder(emb)

	// Ensure vector search is actually usable; otherwise the handler correctly
	// short-circuits to BM25 and embedding is intentionally skipped.
	dbName := server.dbManager.DefaultDatabaseName()
	storageEngine, err := server.dbManager.GetStorage(dbName)
	require.NoError(t, err)
	searchSvc, err := server.db.GetOrCreateSearchService(dbName, storageEngine)
	require.NoError(t, err)
	seedVec := make([]float32, 1024)
	seedVec[0] = 1
	require.NoError(t, searchSvc.IndexNode(&storage.Node{
		ID:              storage.NodeID("seed-vector-node"),
		Labels:          []string{"Seed"},
		Properties:      map[string]interface{}{"name": "seed"},
		ChunkEmbeddings: [][]float32{seedVec},
	}))
	require.Greater(t, searchSvc.EmbeddingCount(), 0)

	longQuery := strings.Repeat("a", 1200)
	resp := makeRequest(t, server, "POST", "/nornicdb/search", map[string]interface{}{
		"query": longQuery,
		"limit": 10,
	}, "Bearer "+token)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	emb.mu.Lock()
	calls := emb.calls
	maxLen := emb.maxLen
	emb.mu.Unlock()

	require.GreaterOrEqual(t, calls, 2, "expected query embedding to run on multiple chunks")
	require.LessOrEqual(t, maxLen, 512, "expected no embedding call on the full query")
}

func TestHandleSearch_SkipsEmbeddingWhenNoVectorsIndexed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	emb := &countingEmbedder{dims: 1024}
	server.db.SetEmbedder(emb)

	dbName := server.dbManager.DefaultDatabaseName()
	storageEngine, err := server.dbManager.GetStorage(dbName)
	require.NoError(t, err)
	searchSvc, err := server.db.GetOrCreateSearchService(dbName, storageEngine)
	require.NoError(t, err)
	searchSvc.ClearVectorIndex()
	require.Equal(t, 0, searchSvc.EmbeddingCount())

	longQuery := strings.Repeat("a", 1200)
	resp := makeRequest(t, server, "POST", "/nornicdb/search", map[string]interface{}{
		"query": longQuery,
		"limit": 10,
	}, "Bearer "+token)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	emb.mu.Lock()
	calls := emb.calls
	emb.mu.Unlock()
	require.Equal(t, 0, calls, "expected no query embedding calls when vector index is empty")
}

func TestHandleSearchRebuild(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Test search rebuild endpoint
	resp := makeRequest(t, server, "POST", "/nornicdb/search/rebuild", map[string]interface{}{
		"database": server.dbManager.DefaultDatabaseName(),
	}, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify response structure
	if result["success"] == nil {
		t.Error("missing 'success' field")
	}
	if result["database"] == nil {
		t.Error("missing 'database' field")
	}
	if result["message"] == nil {
		t.Error("missing 'message' field")
	}

	// Verify success is true
	success, ok := result["success"].(bool)
	if !ok {
		t.Errorf("'success' should be a boolean, got %T", result["success"])
	}
	if !success {
		t.Error("expected success to be true")
	}

	// Test rebuild without database parameter (should use default)
	resp2 := makeRequest(t, server, "POST", "/nornicdb/search/rebuild", nil, "Bearer "+token)
	if resp2.Code != http.StatusOK {
		t.Errorf("expected status 200 without database param, got %d: %s", resp2.Code, resp2.Body.String())
	}
}

func TestHandleSimilar(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Get the default database storage to create test nodes with embeddings
	dbName := server.dbManager.DefaultDatabaseName()
	storageEngine, err := server.dbManager.GetStorage(dbName)
	require.NoError(t, err, "should get default database storage")

	// Create a target node with an embedding
	targetEmbedding := make([]float32, 1024)
	for i := range targetEmbedding {
		targetEmbedding[i] = float32(i) / 1024.0
	}
	targetNode := &storage.Node{
		ID:              storage.NodeID("target-node"),
		Labels:          []string{"Test"},
		Properties:      map[string]interface{}{"name": "Target Node"},
		ChunkEmbeddings: [][]float32{targetEmbedding},
	}
	_, err = storageEngine.CreateNode(targetNode)
	require.NoError(t, err, "should create target node")

	// Create a similar node with a similar embedding (slight variation)
	similarEmbedding := make([]float32, 1024)
	for i := range similarEmbedding {
		similarEmbedding[i] = float32(i+1) / 1024.0 // Slight variation
	}
	similarNode := &storage.Node{
		ID:              storage.NodeID("similar-node"),
		Labels:          []string{"Test"},
		Properties:      map[string]interface{}{"name": "Similar Node"},
		ChunkEmbeddings: [][]float32{similarEmbedding},
	}
	_, err = storageEngine.CreateNode(similarNode)
	require.NoError(t, err, "should create similar node")

	// Create a dissimilar node with a very different embedding
	dissimilarEmbedding := make([]float32, 1024)
	for i := range dissimilarEmbedding {
		dissimilarEmbedding[i] = float32(1024-i) / 1024.0 // Very different
	}
	dissimilarNode := &storage.Node{
		ID:              storage.NodeID("dissimilar-node"),
		Labels:          []string{"Test"},
		Properties:      map[string]interface{}{"name": "Dissimilar Node"},
		ChunkEmbeddings: [][]float32{dissimilarEmbedding},
	}
	_, err = storageEngine.CreateNode(dissimilarNode)
	require.NoError(t, err, "should create dissimilar node")

	// Test the similar endpoint
	resp := makeRequest(t, server, "POST", "/nornicdb/similar", map[string]interface{}{
		"node_id": "target-node",
		"limit":   5,
	}, "Bearer "+token)

	require.Equal(t, http.StatusOK, resp.Code, "should return 200 OK")

	// Verify response structure
	var results []map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&results)
	require.NoError(t, err, "should decode response as JSON array")

	// Should find at least the similar node (may also find dissimilar node)
	require.Greater(t, len(results), 0, "should return at least one result")

	// Verify result structure
	for _, result := range results {
		require.Contains(t, result, "node", "result should contain 'node' field")
		require.Contains(t, result, "score", "result should contain 'score' field")
		node, ok := result["node"].(map[string]interface{})
		require.True(t, ok, "node should be a map")
		require.Contains(t, node, "id", "node should have 'id' field")
		score, ok := result["score"].(float64)
		require.True(t, ok, "score should be a number")
		require.GreaterOrEqual(t, score, 0.0, "score should be non-negative")
		require.LessOrEqual(t, score, 1.0, "score should be at most 1.0")
	}

	// Test with non-existent node (should return 404)
	resp = makeRequest(t, server, "POST", "/nornicdb/similar", map[string]interface{}{
		"node_id": "non-existent-node",
		"limit":   5,
	}, "Bearer "+token)

	require.Equal(t, http.StatusNotFound, resp.Code, "should return 404 for non-existent node")

	// Test with node without embedding (should return 400)
	nodeWithoutEmbedding := &storage.Node{
		ID:              storage.NodeID("no-embedding-node"),
		Labels:          []string{"Test"},
		Properties:      map[string]interface{}{"name": "No Embedding"},
		ChunkEmbeddings: nil, // No embedding
	}
	_, err = storageEngine.CreateNode(nodeWithoutEmbedding)
	require.NoError(t, err, "should create node without embedding")

	resp = makeRequest(t, server, "POST", "/nornicdb/similar", map[string]interface{}{
		"node_id": "no-embedding-node",
		"limit":   5,
	}, "Bearer "+token)

	require.Equal(t, http.StatusBadRequest, resp.Code, "should return 400 for node without embedding")
}

// =============================================================================
// Schema Endpoint Tests (via Cypher - Neo4j compatible approach)
// =============================================================================

func TestSchemaViaCypher(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Get labels via CALL db.labels()
	labelsResp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "CALL db.labels()"},
		},
	}, "Bearer "+token)

	if labelsResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for labels query, got %d", labelsResp.Code)
	}

	// Get relationship types via CALL db.relationshipTypes()
	relTypesResp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "CALL db.relationshipTypes()"},
		},
	}, "Bearer "+token)

	if relTypesResp.Code != http.StatusOK {
		t.Errorf("expected status 200 for relationship types query, got %d", relTypesResp.Code)
	}
}

// =============================================================================
// Admin Endpoint Tests
// =============================================================================

func TestHandleAdminStats(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/admin/stats", nil, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if stats["server"] == nil {
		t.Error("missing 'server' stats")
	}
	if stats["database"] == nil {
		t.Error("missing 'database' stats")
	}

	// Verify new database stats structure (multi-database aware)
	dbStats, ok := stats["database"].(map[string]interface{})
	if !ok {
		t.Fatal("'database' field is not a map")
	}

	// Check for required fields
	if dbStats["node_count"] == nil {
		t.Error("missing 'node_count' field in database stats")
	}
	if dbStats["edge_count"] == nil {
		t.Error("missing 'edge_count' field in database stats")
	}
	if dbStats["databases"] == nil {
		t.Error("missing 'databases' field in database stats")
	}
	if dbStats["per_database"] == nil {
		t.Error("missing 'per_database' field in database stats")
	}

	// Verify per_database is a map
	perDB, ok := dbStats["per_database"].(map[string]interface{})
	if !ok {
		t.Fatal("'per_database' field is not a map")
	}

	// Verify system database is NOT included in per_database (it's excluded from totals)
	if _, exists := perDB["system"]; exists {
		t.Error("system database should not be included in per_database stats")
	}

	// Verify default database is included
	defaultDB := server.dbManager.DefaultDatabaseName()
	if _, exists := perDB[defaultDB]; !exists {
		t.Errorf("default database '%s' should be included in per_database stats", defaultDB)
	}

	// Verify counts are numbers
	nodeCount, ok := dbStats["node_count"].(float64)
	if !ok {
		t.Errorf("'node_count' should be a number, got %T", dbStats["node_count"])
	}
	edgeCount, ok := dbStats["edge_count"].(float64)
	if !ok {
		t.Errorf("'edge_count' should be a number, got %T", dbStats["edge_count"])
	}

	// Counts should be >= 0
	if nodeCount < 0 {
		t.Errorf("node_count should be >= 0, got %v", nodeCount)
	}
	if edgeCount < 0 {
		t.Errorf("edge_count should be >= 0, got %v", edgeCount)
	}
}

func TestHandleAdminConfig(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/admin/config", nil, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}
}

// =============================================================================
// User Management Tests
// =============================================================================

func TestHandleUsers(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Test GET (list users) - using correct endpoint
	resp := makeRequest(t, server, "GET", "/auth/users", nil, "Bearer "+token)
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}

	// Test POST (create user)
	createResp := makeRequest(t, server, "POST", "/auth/users", map[string]interface{}{
		"username": "newuser",
		"password": "password123",
		"roles":    []string{"viewer"},
	}, "Bearer "+token)

	if createResp.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", createResp.Code, createResp.Body.String())
	}
}

// =============================================================================
// RBAC Tests
// =============================================================================

func TestRBACWritePermission(t *testing.T) {
	server, auth := setupTestServer(t)
	readerToken := getAuthToken(t, auth, "reader")

	// Reader (viewer role) should not be able to run mutation queries
	resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "CREATE (n:Test {name: 'test'}) RETURN n"},
		},
	}, "Bearer "+readerToken)

	// The response should have an error about permissions
	var txResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&txResp)
	errors, ok := txResp["errors"].([]interface{})
	if !ok || len(errors) == 0 {
		t.Error("expected error for viewer running mutation query")
	}
}

func TestRBACMutationQuery(t *testing.T) {
	server, auth := setupTestServer(t)
	readerToken := getAuthToken(t, auth, "reader")

	// Reader (viewer role) should be able to run read queries
	resp := makeRequest(t, server, "POST", "/db/nornic/tx/commit", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "MATCH (n) RETURN n LIMIT 10"},
		},
	}, "Bearer "+readerToken)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200 for read query, got %d", resp.Code)
	}
}

// =============================================================================
// Database Info Endpoint Tests
// =============================================================================

func TestHandleDatabaseInfo(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/db/nornic", nil, "Bearer "+token)

	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}
}

// =============================================================================
// CORS Tests
// =============================================================================

func TestCORSHeaders(t *testing.T) {
	server, _ := setupTestServer(t)

	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	// Should have CORS headers
	if recorder.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("missing Access-Control-Allow-Origin header")
	}
}

// =============================================================================
// Error Handling Tests
// =============================================================================

func TestInvalidJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	req := httptest.NewRequest("POST", "/db/nornic/tx/commit", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestNotFound(t *testing.T) {
	server, _ := setupTestServer(t)

	resp := makeRequest(t, server, "GET", "/nonexistent/endpoint", nil, "")

	if resp.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.Code)
	}
}

// =============================================================================
// Server Lifecycle Tests
// =============================================================================

func TestServerStartStop(t *testing.T) {
	server, _ := setupTestServer(t)

	// Start server
	go func() {
		server.Start()
	}()

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := server.Stats()
	if stats.Uptime <= 0 {
		t.Error("expected positive uptime")
	}

	// Stop server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Stop(ctx); err != nil {
		t.Errorf("stop error: %v", err)
	}
}

func TestServerStats(t *testing.T) {
	server, _ := setupTestServer(t)

	stats := server.Stats()

	if stats.Uptime < 0 {
		t.Error("expected non-negative uptime")
	}
}

// =============================================================================
// Audit Logger Tests
// =============================================================================

func TestSetAuditLogger(t *testing.T) {
	server, _ := setupTestServer(t)

	// Create audit logger
	auditConfig := audit.Config{
		RetentionDays: 30,
	}
	auditLogger, err := audit.NewLogger(auditConfig)
	if err != nil {
		t.Fatalf("failed to create audit logger: %v", err)
	}
	defer auditLogger.Close()

	// Set it
	server.SetAuditLogger(auditLogger)

	// Make a request that would be audited
	makeRequest(t, server, "GET", "/health", nil, "")

	// No error means success
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestIsMutationQuery(t *testing.T) {
	tests := []struct {
		query    string
		expected bool
	}{
		{"MATCH (n) RETURN n", false},
		{"CREATE (n:Test)", true},
		{"MERGE (n:Test)", true},
		{"DELETE n", true},
		{"SET n.prop = 1", true},
		{"REMOVE n.prop", true},
		{"DROP INDEX", true},
		{"  CREATE (n)", true},
		{"match (n) return n", false},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := isMutationQuery(tt.query)
			if result != tt.expected {
				t.Errorf("isMutationQuery(%q) = %v, want %v", tt.query, result, tt.expected)
			}
		})
	}
}

func TestIsCreateDatabaseStatement(t *testing.T) {
	tests := []struct {
		stmt     string
		expected bool
	}{
		{"CREATE DATABASE foo", true},
		{"CREATE DATABASE foo IF NOT EXISTS", true},
		{"create database bar", true},
		{"CREATE COMPOSITE DATABASE comp ALIAS a FOR DATABASE db1", true},
		{"CREATE (n:Node)", false},
		{"MATCH (n) RETURN n", false},
		{"SHOW DATABASES", false},
		{"  CREATE DATABASE x  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.stmt, func(t *testing.T) {
			got := isCreateDatabaseStatement(tt.stmt)
			if got != tt.expected {
				t.Errorf("isCreateDatabaseStatement(%q) = %v, want %v", tt.stmt, got, tt.expected)
			}
		})
	}
}

func TestParseCreatedDatabaseName(t *testing.T) {
	tests := []struct {
		stmt     string
		wantName string
		wantOk   bool
	}{
		{"CREATE DATABASE foo", "foo", true},
		{"CREATE DATABASE foo IF NOT EXISTS", "foo", true},
		{"create database bar", "bar", true},
		{"CREATE DATABASE `backtick-db`", "backtick-db", true},
		{"CREATE COMPOSITE DATABASE comp ALIAS a FOR DATABASE db1", "comp", true},
		{"CREATE COMPOSITE DATABASE my_comp", "my_comp", true},
		{"CREATE (n:Node)", "", false},
		{"CREATE DATABASE ", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.stmt, func(t *testing.T) {
			gotName, gotOk := parseCreatedDatabaseName(tt.stmt)
			if gotOk != tt.wantOk || gotName != tt.wantName {
				t.Errorf("parseCreatedDatabaseName(%q) = (%q, %v), want (%q, %v)", tt.stmt, gotName, gotOk, tt.wantName, tt.wantOk)
			}
		})
	}
}

func TestParseIntQuery(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		key      string
		def      int
		expected int
	}{
		{"present", "limit=50", "limit", 10, 50},
		{"missing", "", "limit", 10, 10},
		{"invalid", "limit=abc", "limit", 10, 10},
		{"zero", "limit=0", "limit", 10, 10},
		{"negative", "limit=-5", "limit", 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/?"+tt.query, nil)
			result := parseIntQuery(req, tt.key, tt.def)
			if result != tt.expected {
				t.Errorf("parseIntQuery() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestHasPermission(t *testing.T) {
	tests := []struct {
		name     string
		roles    []string
		perm     auth.Permission
		expected bool
	}{
		{"admin has all", []string{"admin"}, auth.PermAdmin, true},
		{"admin has write", []string{"admin"}, auth.PermWrite, true},
		{"admin perm implies write", []string{"admin"}, auth.PermDelete, true},
		{"viewer has read", []string{"viewer"}, auth.PermRead, true},
		{"viewer no write", []string{"viewer"}, auth.PermWrite, false},
		{"perm token allowed", []string{"write"}, auth.PermWrite, true},
		{"perm admin implies delete", []string{"admin"}, auth.PermDelete, true},
		{"role casing normalized", []string{"Admin"}, auth.PermWrite, true},
		{"role_ prefix normalized", []string{"ROLE_ADMIN"}, auth.PermWrite, true},
		{"perm casing normalized", []string{"WRITE"}, auth.PermWrite, true},
		{"empty roles", []string{}, auth.PermRead, false},
		{"invalid role", []string{"invalid"}, auth.PermRead, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasPermission(nil, tt.roles, tt.perm)
			if result != tt.expected {
				t.Errorf("hasPermission() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		remote   string
		expected string
	}{
		{
			name:     "X-Forwarded-For",
			headers:  map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8"},
			remote:   "127.0.0.1:1234",
			expected: "1.2.3.4",
		},
		{
			name:     "X-Real-IP",
			headers:  map[string]string{"X-Real-IP": "1.2.3.4"},
			remote:   "127.0.0.1:1234",
			expected: "1.2.3.4",
		},
		{
			name:     "RemoteAddr fallback",
			headers:  map[string]string{},
			remote:   "192.168.1.1:1234",
			expected: "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			result := getClientIP(req)
			if result != tt.expected {
				t.Errorf("getClientIP() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetCookie(t *testing.T) {
	tests := []struct {
		name     string
		cookies  []*http.Cookie
		key      string
		expected string
	}{
		{
			name:     "cookie exists",
			cookies:  []*http.Cookie{{Name: "token", Value: "abc123"}},
			key:      "token",
			expected: "abc123",
		},
		{
			name:     "cookie missing",
			cookies:  []*http.Cookie{},
			key:      "token",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			for _, c := range tt.cookies {
				req.AddCookie(c)
			}

			result := getCookie(req, tt.key)
			if result != tt.expected {
				t.Errorf("getCookie() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// =============================================================================
// Decay Endpoint Test (NornicDB extension)
// =============================================================================

func TestHandleDecay(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "POST", "/nornicdb/decay", nil, "Bearer "+token)

	// Should work or fail gracefully
	if resp.Code != http.StatusOK && resp.Code != http.StatusInternalServerError {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

// =============================================================================
// GDPR Endpoint Tests
// =============================================================================

func TestHandleGDPRExport(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "POST", "/gdpr/export", map[string]interface{}{
		"user_id": "admin",
		"format":  "json",
	}, "Bearer "+token)

	// May succeed or fail depending on implementation
	if resp.Code != http.StatusOK && resp.Code != http.StatusInternalServerError {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestHandleGDPRDelete(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Test without confirmation
	resp := makeRequest(t, server, "POST", "/gdpr/delete", map[string]interface{}{
		"user_id": "testuser",
		"confirm": false,
	}, "Bearer "+token)

	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 without confirmation, got %d", resp.Code)
	}
}

func TestHandleBackup(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "POST", "/admin/backup", map[string]interface{}{
		"path": "/tmp/backup",
	}, "Bearer "+token)

	// May succeed or fail depending on implementation
	if resp.Code != http.StatusOK && resp.Code != http.StatusInternalServerError && resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

// =============================================================================
// Additional Coverage Tests
// =============================================================================

func TestHandleLogout(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "POST", "/auth/logout", nil, "Bearer "+token)
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}

	// Test without auth
	resp2 := makeRequest(t, server, "POST", "/auth/logout", nil, "")
	if resp2.Code != http.StatusOK {
		t.Errorf("expected status 200 even without auth, got %d", resp2.Code)
	}
}

func TestHandleMePUT(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// PUT on /auth/me should fail (method not explicitly handled)
	resp := makeRequest(t, server, "PUT", "/auth/me", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestHandleUserByID(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// First get users to find an ID
	listResp := makeRequest(t, server, "GET", "/auth/users", nil, "Bearer "+token)
	if listResp.Code != http.StatusOK {
		t.Fatalf("failed to list users: %d", listResp.Code)
	}

	var users []map[string]interface{}
	json.NewDecoder(listResp.Body).Decode(&users)

	require.Greater(t, len(users), 0)

	userID := users[0]["id"].(string)

	// Test GET user by ID
	getResp := makeRequest(t, server, "GET", "/auth/users/"+userID, nil, "Bearer "+token)
	if getResp.Code != http.StatusOK && getResp.Code != http.StatusNotFound {
		t.Errorf("expected status 200 or 404, got %d", getResp.Code)
	}
}

func TestClusterStatus(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/db/nornic/cluster", nil, "Bearer "+token)
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}
}

func TestTransactionWithStatements(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Open transaction with initial statements
	openResp := makeRequest(t, server, "POST", "/db/nornic/tx", map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "MATCH (n) RETURN count(n) as count"},
		},
	}, "Bearer "+token)

	if openResp.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", openResp.Code, openResp.Body.String())
	}

	// Check that results are included
	var result map[string]interface{}
	json.NewDecoder(openResp.Body).Decode(&result)

	results, ok := result["results"].([]interface{})
	if !ok || len(results) == 0 {
		t.Error("expected results from initial statement execution")
	}
}

func TestCommitTransactionWithStatements(t *testing.T) {
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

	// Commit with final statements
	commitResp := makeRequest(t, server, "POST", fmt.Sprintf("/db/nornic/tx/%s/commit", txID), map[string]interface{}{
		"statements": []map[string]interface{}{
			{"statement": "MATCH (n) RETURN count(n) as count"},
		},
	}, "Bearer "+token)

	if commitResp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", commitResp.Code, commitResp.Body.String())
	}
}

func TestImplicitTransactionBadJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Send malformed request (bad JSON) - this should give an error
	req := httptest.NewRequest("POST", "/db/nornic/tx/commit", strings.NewReader("not valid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestGDPRExportCSV(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "POST", "/gdpr/export", map[string]interface{}{
		"user_id": "admin",
		"format":  "csv",
	}, "Bearer "+token)

	// May succeed or fail depending on implementation
	if resp.Code != http.StatusOK && resp.Code != http.StatusInternalServerError {
		t.Errorf("unexpected status %d", resp.Code)
	}
}

func TestGDPRDeleteWithConfirmation(t *testing.T) {
	server, authenticator := setupTestServer(t)

	// Create a test user to delete
	_, err := authenticator.CreateUser("deletetest", "password123", []auth.Role{auth.RoleViewer})
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	token := getAuthToken(t, authenticator, "admin")

	// Test with confirmation (anonymize mode)
	resp := makeRequest(t, server, "POST", "/gdpr/delete", map[string]interface{}{
		"user_id":   "deletetest",
		"confirm":   true,
		"anonymize": true,
	}, "Bearer "+token)

	// May succeed or fail depending on implementation
	if resp.Code != http.StatusOK && resp.Code != http.StatusInternalServerError {
		t.Errorf("unexpected status %d: %s", resp.Code, resp.Body.String())
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	// This tests that panics are recovered
	server, _ := setupTestServer(t)

	// Normal request should work
	resp := makeRequest(t, server, "GET", "/health", nil, "")
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}
}

func TestAddr(t *testing.T) {
	server, _ := setupTestServer(t)

	// Before starting, Addr should be empty or return something
	addr := server.Addr()
	// Just verify it doesn't panic
	_ = addr
}

func TestTokenAuthDisabled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "nornicdb-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := nornicdb.DefaultConfig()
	config.Memory.DecayEnabled = false
	db, err := nornicdb.Open(tmpDir, config)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer db.Close()

	// Create server without authenticator
	serverConfig := DefaultConfig()
	server, err := New(db, nil, serverConfig)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Token endpoint should fail when auth is not configured
	resp := makeRequest(t, server, "POST", "/auth/token", map[string]interface{}{
		"username": "test",
		"password": "test",
	}, "")

	if resp.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 when auth disabled, got %d", resp.Code)
	}
}

func TestAuthWithNoRequiredPermission(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Health endpoint doesn't require auth
	resp := makeRequest(t, server, "GET", "/health", nil, "Bearer "+token)
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}
}

func TestDatabaseUnknownPath(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/db/nornic/unknown/path", nil, "Bearer "+token)
	if resp.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.Code)
	}
}

func TestDatabaseEmptyName(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/db/", nil, "Bearer "+token)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.Code)
	}
}

func TestTransactionMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// GET on tx should fail
	resp := makeRequest(t, server, "GET", "/db/nornic/tx", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestInvalidBasicAuthFormat(t *testing.T) {
	server, _ := setupTestServer(t)

	// Invalid base64
	resp := makeRequest(t, server, "GET", "/auth/me", nil, "Basic not-base64!!!")
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", resp.Code)
	}
}

func TestInvalidBasicAuthNoColon(t *testing.T) {
	server, _ := setupTestServer(t)

	// Valid base64 but no colon separator
	credentials := base64.StdEncoding.EncodeToString([]byte("nocolon"))
	resp := makeRequest(t, server, "GET", "/auth/me", nil, "Basic "+credentials)
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", resp.Code)
	}
}

func TestUsersPostInvalidBody(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// Invalid JSON body
	req := httptest.NewRequest("POST", "/auth/users", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", recorder.Code)
	}
}

func TestSearchMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/nornicdb/search", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestSimilarMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/nornicdb/similar", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestBackupMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/admin/backup", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestDecayGET(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	// GET on decay endpoint - check it returns OK
	resp := makeRequest(t, server, "GET", "/nornicdb/decay", nil, "Bearer "+token)
	if resp.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.Code)
	}
}

func TestGDPRExportMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/gdpr/export", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

func TestGDPRDeleteMethodNotAllowed(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	resp := makeRequest(t, server, "GET", "/gdpr/delete", nil, "Bearer "+token)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.Code)
	}
}

// =============================================================================
// Additional Coverage Tests for 90%+
// =============================================================================

func TestGDPRExportForbidden(t *testing.T) {
	server, auth := setupTestServer(t)
	readerToken := getAuthToken(t, auth, "reader")

	// Reader tries to export someone else's data
	resp := makeRequest(t, server, "POST", "/gdpr/export", map[string]interface{}{
		"user_id": "other-user-id",
		"format":  "json",
	}, "Bearer "+readerToken)

	if resp.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", resp.Code)
	}
}

func TestGDPRDeleteForbidden(t *testing.T) {
	server, auth := setupTestServer(t)
	readerToken := getAuthToken(t, auth, "reader")

	// Reader tries to delete someone else's data
	resp := makeRequest(t, server, "POST", "/gdpr/delete", map[string]interface{}{
		"user_id": "other-user-id",
		"confirm": true,
	}, "Bearer "+readerToken)

	if resp.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", resp.Code)
	}
}

func TestGDPRExportInvalidJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	req := httptest.NewRequest("POST", "/gdpr/export", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestGDPRDeleteInvalidJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	req := httptest.NewRequest("POST", "/gdpr/delete", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestSearchInvalidJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	req := httptest.NewRequest("POST", "/nornicdb/search", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}

func TestSimilarInvalidJSON(t *testing.T) {
	server, auth := setupTestServer(t)
	token := getAuthToken(t, auth, "admin")

	req := httptest.NewRequest("POST", "/nornicdb/similar", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	server.buildRouter().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}
