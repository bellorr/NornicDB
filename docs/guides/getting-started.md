# Getting Started with NornicDB

Get up and running with NornicDB in 5 minutes.

## Prerequisites

- Go 1.21 or later
- Docker (optional, for containerized deployment)
- 2GB RAM minimum (4GB recommended)

## Installation

### Option 1: From Source

```bash
# Clone the repository
git clone https://github.com/orneryd/nornicdb.git
cd nornicdb

# Build the binary
go build -o nornicdb ./cmd/nornicdb

# Verify installation
./nornicdb --version
```

### Option 2: Docker

```bash
# Pull the image
docker pull ghcr.io/orneryd/nornicdb:latest

# Run the container
docker run -d \
  -p 7474:7474 \
  -p 7687:7687 \
  -v nornicdb-data:/data \
  ghcr.io/orneryd/nornicdb:latest
```

### Option 3: Go Package

```go
import "github.com/orneryd/nornicdb/pkg/nornicdb"

// Use in your Go application
db, err := nornicdb.Open("./data", nil)
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

## Quick Start

### 1. Create a Database

```go
package main

import (
    "context"
    "log"
    
    "github.com/orneryd/nornicdb/pkg/nornicdb"
)

func main() {
    // Open database
    db, err := nornicdb.Open("./mydb", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()
    
    ctx := context.Background()
    
    // Store a memory
    memory := &nornicdb.Memory{
        Content: "Machine learning is a subset of AI",
        Title:   "ML Definition",
        Tier:    nornicdb.TierSemantic,
        Tags:    []string{"AI", "ML"},
    }
    
    stored, err := db.Store(ctx, memory)
    if err != nil {
        log.Fatal(err)
    }
    
    log.Printf("Stored memory: %s\n", stored.ID)
}
```

### 2. Query Data

```go
// Execute Cypher queries
result, err := db.ExecuteCypher(ctx, 
    "MATCH (n) RETURN count(n)", nil)
if err != nil {
    log.Fatal(err)
}

log.Printf("Total nodes: %v\n", result.Rows[0][0])
```

### 3. Vector Search

```go
// Search with embeddings
results, err := db.Search(ctx, "artificial intelligence", 10)
if err != nil {
    log.Fatal(err)
}

for _, result := range results {
    log.Printf("Found: %s (score: %.3f)\n", 
        result.Title, result.Score)
}
```

## Configuration

### Default Configuration

```go
config := nornicdb.DefaultConfig()
// Customization:
config.DecayEnabled = true
config.AutoLinksEnabled = true
config.BoltPort = 7687
config.HTTPPort = 7474

db, err := nornicdb.Open("./data", config)
```

### Production Configuration

```go
config := &nornicdb.Config{
    DataDir:                      "/var/lib/nornicdb",
    EmbeddingProvider:            "openai",
    EmbeddingAPIURL:              "https://api.openai.com/v1",
    EmbeddingModel:               "text-embedding-3-large",
    EmbeddingDimensions:          3072,
    DecayEnabled:                 true,
    DecayRecalculateInterval:     30 * time.Minute,
    DecayArchiveThreshold:        0.01,
    AutoLinksEnabled:             true,
    AutoLinksSimilarityThreshold: 0.85,
    AutoLinksCoAccessWindow:      60 * time.Second,
    BoltPort:                     7687,
    HTTPPort:                     7474,
}

db, err := nornicdb.Open("./data", config)
```

## Memory Tiers

NornicDB simulates human memory with three tiers:

| Tier | Half-Life | Use Case | Example |
|------|-----------|----------|---------|
| **Episodic** | 7 days | Short-term events | "I ran a test yesterday" |
| **Semantic** | 69 days | Facts and concepts | "Python is a programming language" |
| **Procedural** | 693 days | Skills and procedures | "How to deploy to production" |

```go
// Create episodic memory (short-term)
memory := &nornicdb.Memory{
    Content: "Fixed bug in authentication module",
    Tier:    nornicdb.TierEpisodic,
}

// Create semantic memory (long-term facts)
memory := &nornicdb.Memory{
    Content: "NornicDB supports Neo4j Cypher queries",
    Tier:    nornicdb.TierSemantic,
}

// Create procedural memory (skills)
memory := &nornicdb.Memory{
    Content: "Deploy using: docker-compose up -d",
    Tier:    nornicdb.TierProcedural,
}
```

## Next Steps

- **[Vector Search Guide](vector-search.md)** - Learn semantic search
- **[Cypher Queries](cypher-queries.md)** - Master Neo4j queries
- **[GPU Acceleration](gpu-acceleration.md)** - Boost performance
- **[API Reference](../api-reference.md)** - Complete API docs

## Troubleshooting

### Port Already in Use

```bash
# Change ports in configuration
config.BoltPort = 7688
config.HTTPPort = 7475
```

### Out of Memory

```go
// Reduce cache sizes
config := nornicdb.DefaultConfig()
// Adjust decay settings to archive more aggressively
config.DecayArchiveThreshold = 0.05
```

### Slow Queries

```go
// Enable GPU acceleration
// See GPU Acceleration guide
```

## Getting Help

- **[Documentation](../index.md)** - Full documentation
- **[GitHub Issues](https://github.com/orneryd/nornicdb/issues)** - Report bugs
- **[Discussions](https://github.com/orneryd/nornicdb/discussions)** - Ask questions
