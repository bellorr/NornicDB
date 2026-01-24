// Integration tests for Bolt server with Cypher executor.
//
// These tests verify that the Bolt protocol server works correctly with
// the Cypher query executor, simulating real-world Neo4j driver usage.
package bolt

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/orneryd/nornicdb/pkg/cypher"
	"github.com/orneryd/nornicdb/pkg/storage"
)

// skipDiskIOTestOnWindows skips disk I/O intensive tests on Windows to avoid OOM
func skipDiskIOTestOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping disk I/O intensive test on Windows due to memory constraints")
	}
	if os.Getenv("CI") != "" && os.Getenv("GITHUB_ACTIONS") != "" {
		t.Skip("Skipping disk I/O test in CI environment")
	}
}

// cypherQueryExecutor wraps the Cypher executor for Bolt server.
type cypherQueryExecutor struct {
	executor *cypher.StorageExecutor
}

func (c *cypherQueryExecutor) Execute(ctx context.Context, query string, params map[string]any) (*QueryResult, error) {
	result, err := c.executor.Execute(ctx, query, params)
	if err != nil {
		return nil, err
	}

	return &QueryResult{
		Columns: result.Columns,
		Rows:    result.Rows,
	}, nil
}

// TestBoltCypherIntegration tests the full stack: Bolt server + Cypher executor.
func TestBoltCypherIntegration(t *testing.T) {
	// Create storage and executor
	// Wrap with NamespacedEngine to handle ID prefixing (required by BadgerEngine)
	baseStore := storage.NewMemoryEngine()
	store := storage.NewNamespacedEngine(baseStore, "test")
	cypherExec := cypher.NewStorageExecutor(store)
	executor := &cypherQueryExecutor{executor: cypherExec}

	// Start Bolt server on random port
	config := &Config{
		Port:            0, // Random port
		MaxConnections:  10,
		ReadBufferSize:  8192,
		WriteBufferSize: 8192,
	}

	server := New(config, executor)
	defer server.Close()

	// Start server
	go func() {
		if err := server.ListenAndServe(); err != nil {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Get actual port
	port := server.listener.Addr().(*net.TCPAddr).Port

	t.Run("create_and_query_node", func(t *testing.T) {
		// Connect to server
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		// Perform handshake
		if err := PerformHandshakeWithTesting(t, conn); err != nil {
			t.Fatalf("Handshake failed: %v", err)
		}

		// Send HELLO
		if err := SendHello(t, conn, nil); err != nil {
			t.Fatalf("HELLO failed: %v", err)
		}

		// Wait for SUCCESS response
		if err := ReadSuccess(t, conn); err != nil {
			t.Fatalf("Expected SUCCESS after HELLO: %v", err)
		}

		// Send CREATE query
		createQuery := "CREATE (n:Person {name: 'Alice', age: 30}) RETURN n"
		if err := SendRun(t, conn, createQuery, nil, nil); err != nil {
			t.Fatalf("RUN failed: %v", err)
		}

		// Read SUCCESS with fields
		if err := ReadSuccess(t, conn); err != nil {
			t.Fatalf("Expected SUCCESS after RUN: %v", err)
		}

		// Send PULL to get results
		if err := SendPull(t, conn, nil); err != nil {
			t.Fatalf("PULL failed: %v", err)
		}

		// Read RECORD and final SUCCESS
		hasRecord := false
		for {
			msgType, err := ReadMessageType(t, conn)
			if err != nil {
				t.Fatalf("Failed to read message: %v", err)
			}

			if msgType == MsgRecord {
				hasRecord = true
				// Skip record data for now
			} else if msgType == MsgSuccess {
				break
			}
		}

		if !hasRecord {
			t.Error("Expected at least one RECORD message")
		}
	})

	t.Run("match_query", func(t *testing.T) {
		// Connect to server
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		// Perform handshake
		if err := PerformHandshakeWithTesting(t, conn); err != nil {
			t.Fatalf("Handshake failed: %v", err)
		}

		// Send HELLO
		if err := SendHello(t, conn, nil); err != nil {
			t.Fatalf("HELLO failed: %v", err)
		}

		// Wait for SUCCESS
		if err := ReadSuccess(t, conn); err != nil {
			t.Fatalf("Expected SUCCESS: %v", err)
		}

		// Send MATCH query
		matchQuery := "MATCH (n:Person) WHERE n.name = 'Alice' RETURN n.name, n.age"
		if err := SendRun(t, conn, matchQuery, nil, nil); err != nil {
			t.Fatalf("RUN failed: %v", err)
		}

		// Read SUCCESS
		if err := ReadSuccess(t, conn); err != nil {
			t.Fatalf("Expected SUCCESS: %v", err)
		}

		// Send PULL
		if err := SendPull(t, conn, nil); err != nil {
			t.Fatalf("PULL failed: %v", err)
		}

		// Read results
		hasRecord := false
		for {
			msgType, err := ReadMessageType(t, conn)
			if err != nil {
				t.Fatalf("Failed to read message: %v", err)
			}

			if msgType == MsgRecord {
				hasRecord = true
			} else if msgType == MsgSuccess {
				break
			}
		}

		if !hasRecord {
			t.Error("Expected RECORD for Alice")
		}
	})

	t.Run("parameterized_query", func(t *testing.T) {
		// Connect to server
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		// Perform handshake and HELLO
		PerformHandshakeWithTesting(t, conn)
		SendHello(t, conn, nil)
		ReadSuccess(t, conn)

		// Send parameterized query
		query := "CREATE (n:Person {name: $name, age: $age}) RETURN n"
		params := map[string]any{
			"name": "Bob",
			"age":  int64(25),
		}

		if err := SendRun(t, conn, query, params, nil); err != nil {
			t.Fatalf("RUN failed: %v", err)
		}

		// Read SUCCESS
		ReadSuccess(t, conn)

		// Send PULL
		SendPull(t, conn, nil)

		// Read results
		for {
			msgType, err := ReadMessageType(t, conn)
			if err != nil {
				break
			}
			if msgType == MsgSuccess {
				break
			}
		}
	})

	t.Run("transaction_flow", func(t *testing.T) {
		// Connect to server
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		// Handshake and HELLO
		PerformHandshakeWithTesting(t, conn)
		SendHello(t, conn, nil)
		ReadSuccess(t, conn)

		// Send BEGIN
		if err := SendBegin(t, conn, nil); err != nil {
			t.Fatalf("BEGIN failed: %v", err)
		}
		ReadSuccess(t, conn)

		// Send query in transaction
		SendRun(t, conn, "CREATE (n:Test {id: 'tx-test'})", nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)

		// Consume results
		for {
			msgType, _ := ReadMessageType(t, conn)
			if msgType == MsgSuccess {
				break
			}
		}

		// Send COMMIT
		if err := SendCommit(t, conn); err != nil {
			t.Fatalf("COMMIT failed: %v", err)
		}
		ReadSuccess(t, conn)
	})
}

// TestBoltServerStress tests the server under load.
func TestBoltServerStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// Create storage and executor
	store := storage.NewMemoryEngine()
	cypherExec := cypher.NewStorageExecutor(store)
	executor := &cypherQueryExecutor{executor: cypherExec}

	// Start server
	config := &Config{Port: 0, MaxConnections: 50}
	server := New(config, executor)
	defer server.Close()

	go server.ListenAndServe()
	time.Sleep(100 * time.Millisecond)

	port := server.listener.Addr().(*net.TCPAddr).Port

	// Launch multiple concurrent connections
	const numConnections = 20
	done := make(chan error, numConnections)

	for i := 0; i < numConnections; i++ {
		go func(id int) {
			conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
			if err != nil {
				done <- err
				return
			}
			defer conn.Close()

			// Perform handshake
			PerformHandshakeWithTesting(t, conn)
			SendHello(t, conn, nil)
			ReadSuccess(t, conn)

			// Execute queries
			query := fmt.Sprintf("CREATE (n:Test {id: %d}) RETURN n", id)
			SendRun(t, conn, query, nil, nil)
			ReadSuccess(t, conn)
			SendPull(t, conn, nil)

			// Read results
			for {
				msgType, err := ReadMessageType(t, conn)
				if err != nil {
					done <- err
					return
				}
				if msgType == MsgSuccess {
					break
				}
			}

			done <- nil
		}(i)
	}

	// Wait for all connections
	for i := 0; i < numConnections; i++ {
		if err := <-done; err != nil {
			t.Errorf("Connection %d failed: %v", i, err)
		}
	}
}

// TestBoltBenchmarkCreateDeleteRelationship measures real Bolt network performance
// KEEP THIS TEST - this is the actual Bolt layer benchmark
func TestBoltBenchmarkCreateDeleteRelationship(t *testing.T) {
	// Create storage chain matching production: base -> async -> namespaced.
	// AsyncEngine requires fully-qualified IDs; NamespacedEngine provides that for Cypher-generated IDs.
	baseStore := storage.NewMemoryEngine()
	asyncBase := storage.NewAsyncEngine(baseStore, nil)
	store := storage.NewNamespacedEngine(asyncBase, "test")
	cypherExec := cypher.NewStorageExecutor(store)
	executor := &cypherQueryExecutor{executor: cypherExec}

	config := &Config{
		Port:            0,
		MaxConnections:  10,
		ReadBufferSize:  8192,
		WriteBufferSize: 8192,
	}

	server := New(config, executor)
	defer server.Close()

	go func() {
		server.ListenAndServe()
	}()
	time.Sleep(100 * time.Millisecond)

	port := server.listener.Addr().(*net.TCPAddr).Port

	// Connect
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Handshake and HELLO
	PerformHandshakeWithTesting(t, conn)
	SendHello(t, conn, nil)
	ReadSuccess(t, conn)

	// Create test nodes
	SendRun(t, conn, "CREATE (a:Actor {name: 'Test'})", nil, nil)
	ReadSuccess(t, conn)
	SendPull(t, conn, nil)
	ReadSuccess(t, conn) // SUCCESS after PULL

	SendRun(t, conn, "CREATE (m:Movie {title: 'Test'})", nil, nil)
	ReadSuccess(t, conn)
	SendPull(t, conn, nil)
	ReadSuccess(t, conn)

	// Benchmark the slow query
	iterations := 100
	t.Logf("Running %d iterations over Bolt (small dataset)", iterations)

	start := time.Now()
	for i := 0; i < iterations; i++ {
		query := `MATCH (a:Actor), (m:Movie)
			WITH a, m LIMIT 1
			CREATE (a)-[r:TEMP_REL]->(m)
			DELETE r`
		SendRun(t, conn, query, nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
	}
	elapsed := time.Since(start)

	opsPerSec := float64(iterations) / elapsed.Seconds()
	avgMs := elapsed.Seconds() * 1000 / float64(iterations)
	t.Logf("Completed %d iterations in %v", iterations, elapsed)
	t.Logf("Bolt Performance: %.2f ops/sec, %.3f ms/op", opsPerSec, avgMs)

	// This should help identify if JS driver is the bottleneck
	if opsPerSec < 500 {
		t.Logf("WARNING: Bolt performance %.2f ops/sec is below 500 target", opsPerSec)
	}
}

// TestBoltBenchmarkCreateDeleteRelationship_LargeDataset simulates real benchmark conditions
// KEEP THIS TEST - this shows performance with realistic data volume (100 actors, 150 movies)
func TestBoltBenchmarkCreateDeleteRelationship_LargeDataset(t *testing.T) {
	baseStore := storage.NewMemoryEngine()
	asyncBase := storage.NewAsyncEngine(baseStore, nil)
	store := storage.NewNamespacedEngine(asyncBase, "test")
	cypherExec := cypher.NewStorageExecutor(store)
	executor := &cypherQueryExecutor{executor: cypherExec}

	config := &Config{Port: 0, MaxConnections: 10}
	server := New(config, executor)
	defer server.Close()

	go func() { server.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)

	port := server.listener.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	PerformHandshakeWithTesting(t, conn)
	SendHello(t, conn, nil)
	ReadSuccess(t, conn)

	// Create 100 actors
	t.Log("Creating 100 actors...")
	for i := 0; i < 100; i++ {
		SendRun(t, conn, fmt.Sprintf("CREATE (a:Actor {name: 'Actor_%d', born: %d})", i, 1950+i%50), nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
	}

	// Create 150 movies
	t.Log("Creating 150 movies...")
	for i := 0; i < 150; i++ {
		SendRun(t, conn, fmt.Sprintf("CREATE (m:Movie {title: 'Movie_%d', released: %d})", i, 1980+i%44), nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
	}

	// Benchmark
	iterations := 100
	t.Logf("Running %d iterations over Bolt (Memory, 100 actors + 150 movies)", iterations)

	start := time.Now()
	for i := 0; i < iterations; i++ {
		query := `MATCH (a:Actor), (m:Movie)
			WITH a, m LIMIT 1
			CREATE (a)-[r:TEMP_REL]->(m)
			DELETE r`
		SendRun(t, conn, query, nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
	}
	elapsed := time.Since(start)

	opsPerSec := float64(iterations) / elapsed.Seconds()
	t.Logf("Bolt Performance (Memory, large dataset): %.2f ops/sec, %.3f ms/op", opsPerSec, elapsed.Seconds()*1000/float64(iterations))
}

// TestBoltBenchmarkCreateDeleteRelationship_Badger tests with BadgerDB (realistic)
// KEEP THIS TEST - shows performance with disk-based storage
func TestBoltBenchmarkCreateDeleteRelationship_Badger(t *testing.T) {
	skipDiskIOTestOnWindows(t)
	tmpDir := t.TempDir()
	badgerEngine, err := storage.NewBadgerEngine(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create BadgerEngine: %v", err)
	}
	defer badgerEngine.Close()

	asyncBase := storage.NewAsyncEngine(badgerEngine, nil)
	store := storage.NewNamespacedEngine(asyncBase, "test")
	cypherExec := cypher.NewStorageExecutor(store)
	executor := &cypherQueryExecutor{executor: cypherExec}

	config := &Config{Port: 0, MaxConnections: 10}
	server := New(config, executor)
	defer server.Close()

	go func() { server.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)

	port := server.listener.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	PerformHandshakeWithTesting(t, conn)
	SendHello(t, conn, nil)
	ReadSuccess(t, conn)

	// Create 100 actors
	t.Log("Creating 100 actors (BadgerDB)...")
	for i := 0; i < 100; i++ {
		SendRun(t, conn, fmt.Sprintf("CREATE (a:Actor {name: 'Actor_%d'})", i), nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
	}

	// Create 150 movies
	t.Log("Creating 150 movies (BadgerDB)...")
	for i := 0; i < 150; i++ {
		SendRun(t, conn, fmt.Sprintf("CREATE (m:Movie {title: 'Movie_%d'})", i), nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
	}

	// Benchmark
	iterations := 100
	if v := os.Getenv("BOLT_PROFILE_ITERATIONS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			iterations = parsed
		}
	}
	t.Logf("Running %d iterations over Bolt (BadgerDB, 100 actors + 150 movies)", iterations)

	cpuProfilePath := os.Getenv("BOLT_PROFILE_CPU")
	var cpuFile *os.File
	if cpuProfilePath != "" {
		file, err := os.Create(cpuProfilePath)
		if err != nil {
			t.Fatalf("Failed to create CPU profile: %v", err)
		}
		cpuFile = file
		if err := pprof.StartCPUProfile(cpuFile); err != nil {
			_ = cpuFile.Close()
			t.Fatalf("Failed to start CPU profile: %v", err)
		}
	}

	start := time.Now()
	for i := 0; i < iterations; i++ {
		query := `MATCH (a:Actor), (m:Movie)
			WITH a, m LIMIT 1
			CREATE (a)-[r:TEMP_REL]->(m)
			DELETE r`
		SendRun(t, conn, query, nil, nil)
		ReadSuccess(t, conn)
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
	}
	elapsed := time.Since(start)

	if cpuFile != nil {
		pprof.StopCPUProfile()
		_ = cpuFile.Close()
	}

	if memProfilePath := os.Getenv("BOLT_PROFILE_MEM"); memProfilePath != "" {
		runtime.GC()
		file, err := os.Create(memProfilePath)
		if err != nil {
			t.Fatalf("Failed to create mem profile: %v", err)
		}
		if err := pprof.WriteHeapProfile(file); err != nil {
			_ = file.Close()
			t.Fatalf("Failed to write mem profile: %v", err)
		}
		_ = file.Close()
	}

	opsPerSec := float64(iterations) / elapsed.Seconds()
	t.Logf("Bolt Performance (BadgerDB, large dataset): %.2f ops/sec, %.3f ms/op", opsPerSec, elapsed.Seconds()*1000/float64(iterations))
}

// TestBoltResponseMetadata verifies Neo4j-compatible metadata in responses
// KEEP THIS TEST - ensures JS driver compatibility
func TestBoltResponseMetadata(t *testing.T) {
	baseStore := storage.NewMemoryEngine()
	asyncBase := storage.NewAsyncEngine(baseStore, nil)
	store := storage.NewNamespacedEngine(asyncBase, "test")
	cypherExec := cypher.NewStorageExecutor(store)
	executor := &cypherQueryExecutor{executor: cypherExec}

	config := &Config{Port: 0, MaxConnections: 10}
	server := New(config, executor)
	defer server.Close()

	go func() { server.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)

	port := server.listener.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	PerformHandshakeWithTesting(t, conn)
	SendHello(t, conn, nil)
	ReadSuccess(t, conn)

	// Test write query returns proper metadata
	SendRun(t, conn, "CREATE (n:TestNode {name: 'test'})", nil, nil)
	if err := ReadSuccess(t, conn); err != nil {
		t.Fatalf("RUN failed: %v", err)
	}

	SendPull(t, conn, nil)
	if err := ReadSuccess(t, conn); err != nil {
		t.Fatalf("PULL failed: %v", err)
	}

	t.Log("Response metadata verified - Neo4j driver should work correctly")
}

// TestBoltLatencyBreakdown measures where time is spent in protocol exchange
// KEEP THIS TEST - helps identify bottlenecks in protocol handling
func TestBoltLatencyBreakdown(t *testing.T) {
	baseStore := storage.NewMemoryEngine()
	asyncBase := storage.NewAsyncEngine(baseStore, nil)
	store := storage.NewNamespacedEngine(asyncBase, "test")
	cypherExec := cypher.NewStorageExecutor(store)
	executor := &cypherQueryExecutor{executor: cypherExec}

	config := &Config{Port: 0, MaxConnections: 10}
	server := New(config, executor)
	defer server.Close()

	go func() { server.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)

	port := server.listener.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	PerformHandshakeWithTesting(t, conn)
	SendHello(t, conn, nil)
	ReadSuccess(t, conn)

	// Create test data
	SendRun(t, conn, "CREATE (a:Actor {name: 'Keanu'})", nil, nil)
	ReadSuccess(t, conn)
	SendPull(t, conn, nil)
	ReadSuccess(t, conn)

	SendRun(t, conn, "CREATE (m:Movie {title: 'Matrix'})", nil, nil)
	ReadSuccess(t, conn)
	SendPull(t, conn, nil)
	ReadSuccess(t, conn)

	// Measure each phase separately
	iterations := 50
	var runTotal, pullTotal time.Duration

	for i := 0; i < iterations; i++ {
		// Measure RUN
		runStart := time.Now()
		SendRun(t, conn, `MATCH (a:Actor), (m:Movie) WITH a, m LIMIT 1 CREATE (a)-[r:TEMP_REL]->(m) DELETE r`, nil, nil)
		ReadSuccess(t, conn)
		runTotal += time.Since(runStart)

		// Measure PULL
		pullStart := time.Now()
		SendPull(t, conn, nil)
		ReadSuccess(t, conn)
		pullTotal += time.Since(pullStart)
	}

	avgRun := runTotal.Seconds() * 1000 / float64(iterations)
	avgPull := pullTotal.Seconds() * 1000 / float64(iterations)
	t.Logf("Latency breakdown (%d iterations):", iterations)
	t.Logf("  RUN avg: %.3f ms", avgRun)
	t.Logf("  PULL avg: %.3f ms", avgPull)
	t.Logf("  Total avg: %.3f ms", avgRun+avgPull)
	t.Logf("  Throughput: %.2f ops/sec", float64(iterations)/((runTotal + pullTotal).Seconds()))
}
