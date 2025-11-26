// Command nornicdb-bolt starts a NornicDB Bolt protocol server.
//
// This command launches a Neo4j-compatible Bolt server that can be accessed
// by any Neo4j driver (Python, JavaScript, Java, Go, etc.).
//
// Usage:
//
//	nornicdb-bolt [flags]
//
// Flags:
//
//	-port int
//	    Bolt server port (default: 7687)
//	-data string
//	    Data directory for storage (default: "./data")
//
// Example:
//
//	# Start server with defaults
//	nornicdb-bolt
//
//	# Start on custom port
//	nornicdb-bolt -port 7688
//
//	# Connect with Neo4j driver
//	from neo4j import GraphDatabase
//	driver = GraphDatabase.driver("bolt://localhost:7687")
//	with driver.session() as session:
//	    result = session.run("CREATE (n:Person {name: 'Alice'}) RETURN n")
//	    print(result.single()[0])
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/orneryd/nornicdb/pkg/bolt"
	"github.com/orneryd/nornicdb/pkg/cypher"
	"github.com/orneryd/nornicdb/pkg/storage"
)

func main() {
	// Parse flags
	port := flag.Int("port", 7687, "Bolt server port")
	dataDir := flag.String("data", "./data", "Data directory")
	flag.Parse()

	fmt.Printf("🚀 NornicDB Bolt Server Starting\n\n")
	fmt.Printf("Configuration:\n")
	fmt.Printf("  Port:     %d\n", *port)
	fmt.Printf("  Data Dir: %s\n\n", *dataDir)

	// Create storage engine
	fmt.Printf("📦 Initializing storage engine...\n")
	store := storage.NewMemoryEngine()

	// Create Cypher executor
	fmt.Printf("⚙️  Creating Cypher executor...\n")
	executor := cypher.NewStorageExecutor(store)

	// Wrap executor for Bolt server
	boltExecutor := &BoltQueryExecutor{
		cypherExecutor: executor,
	}

	// Create Bolt server
	config := &bolt.Config{
		Port:            *port,
		MaxConnections:  100,
		ReadBufferSize:  8192,
		WriteBufferSize: 8192,
	}

	server := bolt.New(config, boltExecutor)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		fmt.Printf("\n🔌 Bolt server starting on bolt://localhost:%d\n\n", *port)
		fmt.Printf("Ready to accept Neo4j driver connections!\n\n")
		fmt.Printf("Example usage:\n")
		fmt.Printf("  Python:     driver = GraphDatabase.driver('bolt://localhost:%d')\n", *port)
		fmt.Printf("  JavaScript: driver = neo4j.driver('bolt://localhost:%d', neo4j.auth.basic('', ''))\n", *port)
		fmt.Printf("  Go:         driver, _ := neo4j.NewDriver('bolt://localhost:%d', neo4j.NoAuth())\n\n", *port)
		
		if err := server.ListenAndServe(); err != nil {
			errChan <- err
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errChan:
		log.Fatalf("❌ Server error: %v", err)
	case <-sigChan:
		fmt.Printf("\n\n🛑 Shutting down gracefully...\n")
		if err := server.Close(); err != nil {
			log.Printf("Error closing server: %v", err)
		}
		fmt.Printf("✅ Server stopped\n")
	}
}

// BoltQueryExecutor adapts the Cypher executor to the Bolt server interface.
type BoltQueryExecutor struct {
	cypherExecutor *cypher.StorageExecutor
}

// Execute implements bolt.QueryExecutor interface.
func (b *BoltQueryExecutor) Execute(ctx context.Context, query string, params map[string]any) (*bolt.QueryResult, error) {
	// Execute Cypher query
	result, err := b.cypherExecutor.Execute(ctx, query, params)
	if err != nil {
		return nil, err
	}

	// Convert to Bolt format
	return &bolt.QueryResult{
		Columns: result.Columns,
		Rows:    result.Rows,
	}, nil
}
