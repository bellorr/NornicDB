# NornicDB Clustering & Sharding Strategy

**Version:** 1.0.0 (Draft)  
**Status:** Planning  
**Last Updated:** December 2024

---

## Executive Summary

This document outlines a strategy for scaling NornicDB from single-node embedded deployments to enterprise-scale distributed clusters. The goal is to support:

- **Embedded** (~10K nodes): Single binary, in-process, no network overhead
- **Standalone** (~1M nodes): Single server, replicated for HA
- **Clustered** (~100M nodes): Multi-node, read replicas, leader election
- **Sharded** (~10B+ nodes): Horizontal partitioning, geo-distributed

We draw inspiration from **Cassandra** (consistent hashing, tunable consistency), **Dgraph** (graph-aware sharding, Raft consensus), and **Milvus** (vector index distribution).

---

## Part 1: Database Clustering Landscape

### 1.1 Key Strategies from Industry Leaders

| Database | Architecture | Sharding | Replication | Consensus | Key Insight |
|----------|-------------|----------|-------------|-----------|-------------|
| **Cassandra** | Shared-nothing | Consistent hash ring | Tunable (1-N) | Gossip protocol | Simple ops, AP-focused |
| **CockroachDB** | Shared-nothing | Range-based (auto) | Raft per range | Raft | Strong consistency, auto-rebalance |
| **TiDB** | Compute/storage split | Range-based | Raft | PD (Placement Driver) | MySQL-compatible scaling |
| **MongoDB** | Sharded + Replica sets | Hash or range | Primary-secondary | Raft (4.0+) | Flexible, good dev experience |
| **Dgraph** | Graph-native | Predicate-based | Raft per group | Raft | Graph-aware data locality |
| **Milvus** | Vector-native | Collection-based | Etcd + Pulsar | Raft (etcd) | Vector index distribution |
| **Neo4j** | Fabric + Causal | Manual sharding | Causal clustering | Raft | Enterprise-only scaling |

### 1.2 Why Cassandra's Model Appeals

Cassandra's architecture is attractive for NornicDB because:

1. **Simplicity**: No master node, all nodes are equal (shared-nothing)
2. **Tunable consistency**: Choose between AP (fast) and CP (safe) per query
3. **Linear scalability**: Add nodes → linear increase in capacity
4. **Operational simplicity**: No complex leader election visible to users
5. **Gossip protocol**: Nodes self-discover and heal
6. **Consistent hashing**: Minimal data movement when scaling

### 1.3 Graph-Specific Considerations (from Dgraph)

Graph databases have unique challenges:

1. **Traversal locality**: Related nodes should be co-located
2. **Edge distribution**: Edges can create hotspots (celebrity problem)
3. **Multi-hop queries**: Cross-shard traversals are expensive
4. **Index distribution**: Full-text and vector indexes need special handling

Dgraph solves this with **predicate-based sharding**:
- Nodes with same label/type tend to be grouped
- Predicates (properties/relationships) are sharded independently
- Alpha nodes handle queries, Zero nodes handle cluster management

### 1.4 Vector Database Considerations (from Milvus/Qdrant)

For vector search at scale:

1. **Index partitioning**: HNSW/IVF indexes are memory-intensive
2. **Segment-based storage**: Data stored in immutable segments
3. **Query routing**: Vector queries routed to relevant shards
4. **Replication**: Read replicas for query throughput
5. **GPU distribution**: Optional GPU nodes for embedding/search

---

## Part 2: NornicDB Current Architecture

### 2.1 Current Stack

```
┌─────────────────────────────────────────────────────────────┐
│                      NornicDB Server                         │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  Bolt API   │  │  HTTP API   │  │      MCP API        │  │
│  └──────┬──────┘  └──────┬──────┘  └──────────┬──────────┘  │
│         └────────────────┼────────────────────┘             │
│                          ▼                                   │
│  ┌───────────────────────────────────────────────────────┐  │
│  │                 Cypher Executor                        │  │
│  │  (Query parsing, planning, execution)                  │  │
│  └───────────────────────┬───────────────────────────────┘  │
│                          ▼                                   │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  Storage    │  │   Search    │  │    Embeddings       │  │
│  │  (BadgerDB) │  │   (HNSW)    │  │  (llama.cpp/API)    │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
│                          ▼                                   │
│  ┌───────────────────────────────────────────────────────┐  │
│  │                 BadgerDB + WAL                         │  │
│  │             (Embedded key-value store)                 │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Current Strengths

- **Fast single-node performance**: 3-52x faster than Neo4j
- **Low memory footprint**: 100-500MB vs Neo4j's 1-4GB
- **Embedded option**: Can run in-process
- **Simple deployment**: Single binary, zero configuration
- **WAL for durability**: Write-ahead logging for crash recovery

### 2.3 Current Limitations for Scaling

| Limitation | Impact | Priority |
|------------|--------|----------|
| Single-node only | Can't scale beyond one machine | Critical |
| No replication | No HA, no read scaling | High |
| HNSW in-memory | Vector index limited by RAM | High |
| No query routing | Can't distribute queries | Medium |
| No cluster discovery | Manual node configuration | Medium |

---

## Part 3: Proposed Deployment Tiers

### 3.1 Tier Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        DEPLOYMENT TIERS                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│   TIER 1: Embedded          TIER 2: Standalone                       │
│   ┌─────────────────┐       ┌─────────────────┐                     │
│   │  App + NornicDB │       │    NornicDB     │                     │
│   │   (in-process)  │       │    (server)     │                     │
│   └─────────────────┘       │   ┌─────────┐   │                     │
│   ~10K nodes, 1 app         │   │ Primary │   │                     │
│   Zero network overhead     │   └────┬────┘   │                     │
│                             │        │        │                     │
│                             │   ┌────▼────┐   │                     │
│                             │   │ Standby │   │                     │
│                             │   └─────────┘   │                     │
│                             └─────────────────┘                     │
│                             ~1M nodes, HA                           │
│                                                                      │
│   TIER 3: Replicated        TIER 4: Sharded Cluster                 │
│   ┌─────────────────┐       ┌───────────────────────────────────┐   │
│   │     Leader      │       │           Coordinator              │   │
│   │   (reads/writes)│       │   (query routing, metadata)        │   │
│   └────────┬────────┘       └───────────────┬───────────────────┘   │
│            │                                │                        │
│   ┌────────┴────────┐       ┌───────────────┴───────────────┐       │
│   ▼                 ▼       ▼               ▼               ▼       │
│ ┌─────┐         ┌─────┐   ┌─────┐       ┌─────┐       ┌─────┐      │
│ │Read │         │Read │   │Shard│       │Shard│       │Shard│      │
│ │Repl.│         │Repl.│   │ A   │       │ B   │       │ C   │      │
│ └─────┘         └─────┘   └─────┘       └─────┘       └─────┘      │
│ ~10M nodes, read scale    ~10B+ nodes, write scale               │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.2 Tier 1: Embedded (Current)

**Use Case**: Desktop apps, edge devices, single-user applications

```go
// Embedded usage (already supported)
import "nornicdb/pkg/nornicdb"

db, _ := nornicdb.Open("/data/mydb")
defer db.Close()

// Direct in-process access
result, _ := db.Execute(ctx, "MATCH (n) RETURN n LIMIT 10", nil)
```

**Characteristics**:
- Single binary, no network
- ~10,000 nodes comfortably
- Sub-millisecond latency
- ~100MB memory footprint

### 3.3 Tier 2: Standalone with Hot Standby

**Use Case**: Small teams, development, staging

```yaml
# docker-compose.yml
services:
  nornicdb-primary:
    image: nornicdb:latest
    environment:
      NORNICDB_MODE: primary
      NORNICDB_STANDBY_URL: nornicdb-standby:7687
    
  nornicdb-standby:
    image: nornicdb:latest
    environment:
      NORNICDB_MODE: standby
      NORNICDB_PRIMARY_URL: nornicdb-primary:7687
```

**Characteristics**:
- Primary handles all writes
- Standby receives WAL stream
- Automatic failover (< 30s)
- ~1M nodes capacity
- Read queries can use standby

**Implementation**: 
- Stream WAL to standby via gRPC
- Heartbeat-based failure detection
- Standby promotes on primary failure

### 3.4 Tier 3: Replicated Cluster (Read Scaling)

**Use Case**: Read-heavy workloads, production apps

```
┌──────────────────────────────────────────────────────────────┐
│                     Replicated Cluster                        │
├──────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │                    Raft Consensus                        │ │
│  │  ┌─────────┐    ┌─────────┐    ┌─────────┐             │ │
│  │  │ Leader  │◄──►│Follower │◄──►│Follower │             │ │
│  │  │(writes) │    │ (reads) │    │ (reads) │             │ │
│  │  └─────────┘    └─────────┘    └─────────┘             │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                               │
│  • Leader handles all writes (Raft log replication)          │
│  • Followers serve read queries                              │
│  • Automatic leader election on failure                      │
│  • Strong consistency (CP)                                   │
│                                                               │
└──────────────────────────────────────────────────────────────┘
```

**Characteristics**:
- 3-5 nodes recommended
- Strong consistency via Raft
- ~10M nodes per cluster
- Horizontal read scaling
- Sub-second failover

**Implementation**:
- Use **hashicorp/raft** or **dragonboat** (Go Raft libraries)
- Raft log = NornicDB WAL entries
- Each node has full data copy
- Read preference: leader, follower, nearest

### 3.5 Tier 4: Sharded Cluster (Write Scaling)

**Use Case**: Enterprise, billions of nodes, global distribution

```
┌────────────────────────────────────────────────────────────────────┐
│                      Sharded Cluster                                │
├────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌───────────────────────────────────────────────────────────────┐ │
│  │                      Coordinator Nodes                         │ │
│  │  ┌─────────┐    ┌─────────┐    ┌─────────┐                    │ │
│  │  │ Coord 1 │    │ Coord 2 │    │ Coord 3 │                    │ │
│  │  │(router) │    │(router) │    │(router) │                    │ │
│  │  └────┬────┘    └────┬────┘    └────┬────┘                    │ │
│  │       │              │              │                          │ │
│  │       └──────────────┼──────────────┘                          │ │
│  │                      │                                          │ │
│  │            ┌─────────┴─────────┐                               │ │
│  │            │   Metadata Store  │                               │ │
│  │            │   (etcd cluster)  │                               │ │
│  │            └───────────────────┘                               │ │
│  └───────────────────────────────────────────────────────────────┘ │
│                              │                                      │
│        ┌─────────────────────┼─────────────────────┐               │
│        │                     │                     │               │
│        ▼                     ▼                     ▼               │
│  ┌───────────┐        ┌───────────┐        ┌───────────┐          │
│  │  Shard A  │        │  Shard B  │        │  Shard C  │          │
│  │ (Raft 3)  │        │ (Raft 3)  │        │ (Raft 3)  │          │
│  ├───────────┤        ├───────────┤        ├───────────┤          │
│  │Labels:    │        │Labels:    │        │Labels:    │          │
│  │Person,Org │        │File,Doc   │        │Event,Task │          │
│  │Hash: 0-42 │        │Hash: 43-85│        │Hash: 86-127│         │
│  └───────────┘        └───────────┘        └───────────┘          │
│                                                                     │
│  • Each shard is a Raft cluster (3 nodes)                          │
│  • Sharding by: Label (graph-aware) + Hash (even distribution)     │
│  • Coordinator routes queries to relevant shards                    │
│  • Cross-shard queries via scatter-gather                          │
│                                                                     │
└────────────────────────────────────────────────────────────────────┘
```

**Characteristics**:
- Unlimited horizontal scaling
- Billions of nodes
- Geographic distribution
- Per-shard Raft replication
- Coordinator handles routing

---

## Part 4: Sharding Strategy

### 4.1 Shard Key Selection

The shard key determines data placement. For graph databases, we need to balance:

1. **Even distribution**: Avoid hotspots
2. **Query locality**: Minimize cross-shard hops
3. **Write distribution**: Spread writes across shards

**Proposed: Hybrid Label + Hash Strategy**

```
┌─────────────────────────────────────────────────────────────┐
│                   Shard Key Strategy                         │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Node ID: "user-123"                                         │
│  Labels: ["User", "Premium"]                                 │
│                                                              │
│  Shard Key = hash(primary_label) % num_shards               │
│            = hash("User") % 3                                │
│            = 1  → Shard B                                    │
│                                                              │
│  Benefits:                                                   │
│  • Nodes with same label co-located (traversal locality)     │
│  • Hash ensures even distribution within label               │
│  • Secondary index for cross-label queries                   │
│                                                              │
│  Label Affinity Groups (configurable):                       │
│  • Group 1: User, Profile, Session → Shard A               │
│  • Group 2: File, Chunk, Embedding → Shard B               │
│  • Group 3: Task, Decision, Note → Shard C                 │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 4.2 Analytics-Driven Shard Placement

**Key Insight**: NornicDB's existing k-means, Louvain, and TLP implementations can inform intelligent shard placement to minimize cross-shard queries.

```
┌────────────────────────────────────────────────────────────────────────┐
│            ANALYTICS-DRIVEN SHARD OPTIMIZATION                          │
├────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐        │
│  │  K-MEANS        │  │  LOUVAIN        │  │  TLP            │        │
│  │  (Semantic)     │  │  (Structural)   │  │  (Predictive)   │        │
│  ├─────────────────┤  ├─────────────────┤  ├─────────────────┤        │
│  │ Clusters nodes  │  │ Finds dense     │  │ Predicts which  │        │
│  │ by embedding    │  │ communities in  │  │ nodes will be   │        │
│  │ similarity      │  │ the graph       │  │ traversed       │        │
│  │                 │  │                 │  │ together        │        │
│  │ "These docs     │  │ "These users    │  │ "These nodes    │        │
│  │  are about      │  │  form a tight   │  │  are likely to  │        │
│  │  similar        │  │  social         │  │  be queried     │        │
│  │  topics"        │  │  cluster"       │  │  together"      │        │
│  └────────┬────────┘  └────────┬────────┘  └────────┬────────┘        │
│           │                    │                    │                  │
│           └────────────────────┼────────────────────┘                  │
│                                ▼                                        │
│           ┌──────────────────────────────────────────┐                 │
│           │        HYBRID AFFINITY SCORE             │                 │
│           │                                          │                 │
│           │  score(A,B) = w₁·semantic(A,B)          │                 │
│           │             + w₂·structural(A,B)         │                 │
│           │             + w₃·coAccess(A,B)           │                 │
│           │                                          │                 │
│           │  Default weights: w₁=0.3, w₂=0.4, w₃=0.3 │                 │
│           └──────────────────────────────────────────┘                 │
│                                │                                        │
│                                ▼                                        │
│           ┌──────────────────────────────────────────┐                 │
│           │         SHARD ASSIGNMENT                 │                 │
│           │                                          │                 │
│           │  Nodes with high affinity → Same shard   │                 │
│           │  Cross-shard edges minimized             │                 │
│           │                                          │                 │
│           └──────────────────────────────────────────┘                 │
│                                                                         │
└────────────────────────────────────────────────────────────────────────┘
```

#### 4.2.1 Using K-Means for Semantic Sharding

Your GPU-accelerated k-means can cluster nodes by embedding similarity:

```go
// Use existing ClusterIndex for shard placement
func computeSemanticShardAssignments(storage Storage, numShards int) map[NodeID]int {
    // Create cluster index with K = numShards
    kmeansConfig := &gpu.KMeansConfig{
        NumClusters:   numShards,
        MaxIterations: 100,
        InitMethod:    "kmeans++",
    }
    
    clusterIndex := gpu.NewClusterIndex(gpuManager, nil, kmeansConfig)
    
    // Add all node embeddings
    nodes := storage.GetAllNodes()
    for _, node := range nodes {
        if len(node.Embedding) > 0 {
            clusterIndex.Add(string(node.ID), node.Embedding)
        }
    }
    
    // Run k-means clustering
    clusterIndex.Cluster()
    
    // Get cluster assignments (embedding cluster → shard)
    assignments := make(map[NodeID]int)
    for nodeID, clusterID := range clusterIndex.GetAssignments() {
        assignments[NodeID(nodeID)] = clusterID
    }
    
    return assignments
}
```

**When to use**: Document/file nodes where semantic similarity = query locality

#### 4.2.2 Using Louvain for Structural Sharding

Your Louvain implementation finds densely connected communities:

```go
// Use Louvain for graph-structure-aware sharding
func computeStructuralShardAssignments(exec *StorageExecutor, numShards int) map[NodeID]int {
    // Run Louvain community detection
    result, _ := exec.Execute(ctx, `
        CALL apoc.algo.louvain([], {}) 
        YIELD node, community
        RETURN node.id as nodeId, community
    `, nil)
    
    // Map communities to shards
    communityToShard := mapCommunitiesToShards(result, numShards)
    
    assignments := make(map[NodeID]int)
    for _, row := range result.Rows {
        nodeID := row[0].(string)
        community := row[1].(int)
        assignments[NodeID(nodeID)] = communityToShard[community]
    }
    
    return assignments
}

// Balance communities across shards (bin packing)
func mapCommunitiesToShards(communities []Community, numShards int) map[int]int {
    // Sort communities by size (largest first)
    sort.Slice(communities, func(i, j int) bool {
        return communities[i].Size > communities[j].Size
    })
    
    // Greedy bin packing: assign to least-full shard
    shardSizes := make([]int, numShards)
    mapping := make(map[int]int)
    
    for _, comm := range communities {
        // Find shard with smallest size
        minShard := 0
        for i := 1; i < numShards; i++ {
            if shardSizes[i] < shardSizes[minShard] {
                minShard = i
            }
        }
        mapping[comm.ID] = minShard
        shardSizes[minShard] += comm.Size
    }
    
    return mapping
}
```

**When to use**: Social graphs, org charts, citation networks

#### 4.2.3 Using TLP for Access-Pattern Sharding

Your TLP can predict which nodes are accessed together:

```go
// Use topology link prediction to inform co-location
func computeAccessPatternAffinity(graph linkpredict.Graph) map[NodePair]float64 {
    affinities := make(map[NodePair]float64)
    
    // For each node, find nodes likely to be traversed with it
    for sourceID := range graph {
        // Adamic-Adar: weights by exclusivity of connections
        predictions := linkpredict.AdamicAdar(graph, sourceID, 50)
        
        for _, pred := range predictions {
            pair := NodePair{sourceID, pred.TargetID}
            affinities[pair] = pred.Score
        }
    }
    
    return affinities
}

// Boost shard co-location for high-affinity pairs
func adjustShardAssignmentsForAffinity(
    baseAssignments map[NodeID]int,
    affinities map[NodePair]float64,
    threshold float64,
) map[NodeID]int {
    adjusted := copyMap(baseAssignments)
    
    // Sort pairs by affinity (highest first)
    pairs := sortByAffinity(affinities)
    
    for _, pair := range pairs {
        if affinities[pair] < threshold {
            break // Below threshold, stop adjusting
        }
        
        shardA := adjusted[pair.A]
        shardB := adjusted[pair.B]
        
        if shardA != shardB {
            // Move smaller node to larger node's shard
            // (or use more sophisticated rebalancing)
            if shouldMove(pair.A, pair.B, adjusted) {
                adjusted[pair.A] = shardB
            }
        }
    }
    
    return adjusted
}
```

**When to use**: AI agent memory, knowledge graphs with predictable access patterns

#### 4.2.4 Hybrid Shard Optimizer

Combine all three signals:

```go
// ShardOptimizer combines multiple signals for optimal placement
type ShardOptimizer struct {
    storage     Storage
    clusterIdx  *gpu.ClusterIndex
    executor    *StorageExecutor
    graph       linkpredict.Graph
    
    // Configurable weights
    SemanticWeight   float64 // Default: 0.3
    StructuralWeight float64 // Default: 0.4
    AccessWeight     float64 // Default: 0.3
}

// OptimizeShardPlacement returns optimal shard assignments
func (o *ShardOptimizer) OptimizeShardPlacement(numShards int) *ShardPlan {
    // 1. Get semantic clusters (k-means on embeddings)
    semanticClusters := o.computeSemanticClusters(numShards)
    
    // 2. Get structural communities (Louvain)
    structuralCommunities := o.computeStructuralCommunities()
    
    // 3. Get access pattern affinities (TLP)
    accessAffinities := o.computeAccessAffinities()
    
    // 4. Compute hybrid affinity matrix
    affinityMatrix := o.computeHybridAffinity(
        semanticClusters,
        structuralCommunities,
        accessAffinities,
    )
    
    // 5. Partition nodes using affinity matrix
    // (spectral clustering or min-cut optimization)
    assignments := o.partitionByAffinity(affinityMatrix, numShards)
    
    // 6. Balance shards (ensure even size distribution)
    balanced := o.balanceShards(assignments, numShards)
    
    return &ShardPlan{
        Assignments: balanced,
        Stats:       o.computeStats(balanced),
    }
}

// computeHybridAffinity builds an affinity score matrix
func (o *ShardOptimizer) computeHybridAffinity(
    semantic map[NodeID]int,
    structural map[NodeID]int,
    access map[NodePair]float64,
) *AffinityMatrix {
    nodes := o.storage.GetAllNodes()
    n := len(nodes)
    
    // Affinity[i][j] = weighted combination of signals
    affinity := NewAffinityMatrix(n)
    
    for i, nodeA := range nodes {
        for j, nodeB := range nodes {
            if i >= j {
                continue // Only upper triangle
            }
            
            score := 0.0
            
            // Semantic: same k-means cluster = high affinity
            if semantic[nodeA.ID] == semantic[nodeB.ID] {
                score += o.SemanticWeight * 1.0
            }
            
            // Structural: same Louvain community = high affinity
            if structural[nodeA.ID] == structural[nodeB.ID] {
                score += o.StructuralWeight * 1.0
            }
            
            // Access: TLP prediction score
            pair := NodePair{nodeA.ID, nodeB.ID}
            if accessScore, ok := access[pair]; ok {
                score += o.AccessWeight * accessScore
            }
            
            affinity.Set(i, j, score)
        }
    }
    
    return affinity
}
```

#### 4.2.5 Multi-Purpose Analytics: One Pass, Many Optimizations

A single k-means/Louvain/TLP pass can power multiple optimizations simultaneously:

```
┌────────────────────────────────────────────────────────────────────────────┐
│              ONE ANALYTICS PASS → MANY OPTIMIZATIONS                        │
├────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│                    ┌─────────────────────┐                                 │
│                    │  K-MEANS + LOUVAIN  │                                 │
│                    │  + TLP (one pass)   │                                 │
│                    └──────────┬──────────┘                                 │
│                               │                                             │
│     ┌─────────────────────────┼─────────────────────────┐                  │
│     │           │             │             │           │                  │
│     ▼           ▼             ▼             ▼           ▼                  │
│ ┌───────┐  ┌───────┐    ┌───────┐    ┌───────┐    ┌───────┐              │
│ │Shard  │  │Search │    │Cache  │    │Auto-  │    │Anomaly│              │
│ │Place- │  │Speed  │    │Warm-  │    │Label/ │    │Detect │              │
│ │ment   │  │up     │    │ing    │    │Tag    │    │       │              │
│ └───────┘  └───────┘    └───────┘    └───────┘    └───────┘              │
│                                                                             │
│ ┌───────┐  ┌───────┐    ┌───────┐    ┌───────┐    ┌───────┐              │
│ │Dedup- │  │Recom- │    │Pre-   │    │Memory │    │Result │              │
│ │licate │  │mend-  │    │fetch  │    │Tiering│    │Divers-│              │
│ │       │  │ations │    │       │    │       │    │ify    │              │
│ └───────┘  └───────┘    └───────┘    └───────┘    └───────┘              │
│                                                                             │
└────────────────────────────────────────────────────────────────────────────┘
```

**1. Vector Search Speedup** (Already implemented!)

```go
// Your ClusterIndex already does this
results := clusterIndex.SearchWithClusters(query, topK, numClusters)
// 10-50x faster than brute force on 100K+ embeddings
```

**2. Intelligent Cache Warming**

```go
// Cache entire clusters when one member is accessed
type ClusterCache struct {
    clusters      map[int][]NodeID    // From k-means
    nodeToCluster map[NodeID]int
    cache         *LRUCache
}

func (c *ClusterCache) OnNodeAccess(nodeID NodeID) {
    clusterID := c.nodeToCluster[nodeID]
    
    // Warm cache with cluster neighbors (likely to be accessed next)
    for _, neighborID := range c.clusters[clusterID] {
        if !c.cache.Has(neighborID) {
            go c.cache.WarmAsync(neighborID)
        }
    }
}
```

**3. Auto-Labeling / Smart Tagging**

```go
// Suggest labels based on cluster membership
func SuggestLabels(node *Node, clusters *ClusterIndex) []string {
    clusterID := clusters.GetCluster(node.ID)
    clusterMembers := clusters.GetClusterMembers(clusterID)
    
    // Find most common labels in this cluster
    labelCounts := make(map[string]int)
    for _, memberID := range clusterMembers {
        member, _ := storage.GetNode(memberID)
        for _, label := range member.Labels {
            labelCounts[label]++
        }
    }
    
    // Suggest labels this node doesn't have but cluster peers do
    suggestions := []string{}
    for label, count := range labelCounts {
        if count > len(clusterMembers)/2 && !hasLabel(node, label) {
            suggestions = append(suggestions, label)
        }
    }
    return suggestions
}

// Example:
// Node: {content: "quarterly earnings report"}
// Cluster peers: 80% have label "Finance"
// Suggestion: Add label "Finance" to this node
```

**4. Anomaly Detection**

```go
// Nodes far from ALL centroids are outliers
func DetectAnomalies(clusters *ClusterIndex, threshold float32) []NodeID {
    anomalies := []NodeID{}
    
    for nodeID, embedding := range clusters.GetAllEmbeddings() {
        // Find distance to nearest centroid
        minDist := float32(math.MaxFloat32)
        for _, centroid := range clusters.GetCentroids() {
            dist := cosineDistance(embedding, centroid)
            if dist < minDist {
                minDist = dist
            }
        }
        
        // If far from all clusters → anomaly
        if minDist > threshold {
            anomalies = append(anomalies, nodeID)
        }
    }
    return anomalies
}

// Use cases:
// - Detect misclassified documents
// - Find corrupted embeddings
// - Surface unique/novel content
```

**5. Content Deduplication**

```go
// Near-duplicates cluster together with very high similarity
func FindDuplicates(clusters *ClusterIndex, similarityThreshold float32) []DuplicateGroup {
    duplicates := []DuplicateGroup{}
    
    for clusterID, members := range clusters.GetClusterMap() {
        if len(members) < 2 {
            continue
        }
        
        // Within each cluster, find very similar pairs
        for i, nodeA := range members {
            for _, nodeB := range members[i+1:] {
                sim := clusters.Similarity(nodeA, nodeB)
                if sim > similarityThreshold { // e.g., 0.98
                    duplicates = append(duplicates, DuplicateGroup{
                        Nodes:      []NodeID{nodeA, nodeB},
                        Similarity: sim,
                    })
                }
            }
        }
    }
    return duplicates
}
```

**6. Recommendations / "More Like This"**

```go
// Fast recommendations using cluster locality
func RecommendSimilar(nodeID NodeID, clusters *ClusterIndex, limit int) []NodeID {
    clusterID := clusters.GetCluster(nodeID)
    members := clusters.GetClusterMembers(clusterID)
    
    // Rank cluster members by similarity to source
    type scored struct {
        id    NodeID
        score float32
    }
    candidates := []scored{}
    
    sourceEmb := clusters.GetEmbedding(nodeID)
    for _, memberID := range members {
        if memberID == nodeID {
            continue
        }
        memberEmb := clusters.GetEmbedding(memberID)
        candidates = append(candidates, scored{
            id:    memberID,
            score: cosineSimilarity(sourceEmb, memberEmb),
        })
    }
    
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].score > candidates[j].score
    })
    
    result := make([]NodeID, min(limit, len(candidates)))
    for i := range result {
        result[i] = candidates[i].id
    }
    return result
}
```

**7. Memory Tiering (Hot/Cold)**

```go
// Keep frequently-accessed clusters in RAM, others on disk
type TieredStorage struct {
    hotClusters  map[int]bool        // In-memory
    coldClusters map[int]bool        // On disk
    accessCounts map[int]int64       // Access frequency
}

func (t *TieredStorage) OptimizeTiers(clusters *ClusterIndex) {
    // Sort clusters by access frequency
    type clusterAccess struct {
        id     int
        access int64
    }
    sorted := []clusterAccess{}
    for id, count := range t.accessCounts {
        sorted = append(sorted, clusterAccess{id, count})
    }
    sort.Slice(sorted, func(i, j int) bool {
        return sorted[i].access > sorted[j].access
    })
    
    // Top 20% → hot (RAM)
    hotCount := len(sorted) / 5
    for i, ca := range sorted {
        if i < hotCount {
            t.hotClusters[ca.id] = true
            t.loadToRAM(ca.id)
        } else {
            t.coldClusters[ca.id] = true
        }
    }
}
```

**8. Search Result Diversification**

```go
// Ensure results span multiple clusters (MMR alternative)
func DiverseSearch(query []float32, clusters *ClusterIndex, topK int) []SearchResult {
    // Get top-K from each of top-N clusters
    nearestClusters := clusters.FindNearestClusters(query, 5)
    
    allResults := []SearchResult{}
    for _, clusterID := range nearestClusters {
        clusterResults := clusters.SearchInCluster(clusterID, query, topK)
        allResults = append(allResults, clusterResults...)
    }
    
    // Interleave results from different clusters
    diverse := InterleaveByCluster(allResults, topK)
    return diverse
}
```

**9. Prefetching for Graph Traversal (TLP-enhanced)**

```go
// Combine k-means + TLP for smart prefetching
func SmartPrefetch(currentNode NodeID, clusters *ClusterIndex, tlp *LinkPredict) {
    // 1. Prefetch cluster neighbors (semantic locality)
    clusterID := clusters.GetCluster(currentNode)
    clusterNeighbors := clusters.GetClusterMembers(clusterID)
    
    // 2. Prefetch TLP-predicted next nodes (traversal locality)
    predictedEdges := tlp.AdamicAdar(currentNode, 10)
    
    // 3. Combine and prefetch
    toPrefetch := union(clusterNeighbors, predictedEdges)
    for _, nodeID := range toPrefetch {
        go cache.WarmAsync(nodeID)
    }
}
```

**10. Auto-Relationship Inference**

```go
// Nodes in same semantic cluster might deserve edges
func SuggestRelationships(clusters *ClusterIndex, storage Storage) []SuggestedEdge {
    suggestions := []SuggestedEdge{}
    
    for clusterID, members := range clusters.GetClusterMap() {
        for i, nodeA := range members {
            for _, nodeB := range members[i+1:] {
                // Check if edge already exists
                if storage.HasEdge(nodeA, nodeB) {
                    continue
                }
                
                // High similarity + same cluster + no edge = suggest
                sim := clusters.Similarity(nodeA, nodeB)
                if sim > 0.85 {
                    suggestions = append(suggestions, SuggestedEdge{
                        From:       nodeA,
                        To:         nodeB,
                        Type:       "RELATED_TO",
                        Confidence: sim,
                        Reason:     fmt.Sprintf("Same semantic cluster %d", clusterID),
                    })
                }
            }
        }
    }
    return suggestions
}
```

**11. Embedding Compression (Delta Encoding)**

```go
// Store embeddings as deltas from centroid (saves ~40% space)
type CompressedEmbeddings struct {
    centroids map[int][]float32          // Full centroids
    deltas    map[NodeID][]float32       // Node deltas from centroid
    clusters  map[NodeID]int             // Node → cluster mapping
}

func (c *CompressedEmbeddings) Compress(clusters *ClusterIndex) {
    for nodeID, embedding := range clusters.GetAllEmbeddings() {
        clusterID := clusters.GetCluster(nodeID)
        centroid := clusters.GetCentroid(clusterID)
        
        // Store delta (typically smaller values, better compression)
        delta := make([]float32, len(embedding))
        for i := range embedding {
            delta[i] = embedding[i] - centroid[i]
        }
        
        c.deltas[nodeID] = delta
        c.clusters[nodeID] = clusterID
    }
}

func (c *CompressedEmbeddings) Decompress(nodeID NodeID) []float32 {
    clusterID := c.clusters[nodeID]
    centroid := c.centroids[clusterID]
    delta := c.deltas[nodeID]
    
    embedding := make([]float32, len(centroid))
    for i := range embedding {
        embedding[i] = centroid[i] + delta[i]
    }
    return embedding
}
```

**12. Query Routing Optimization**

```go
// Route queries to optimal shard based on cluster
type SmartRouter struct {
    clusterToShard map[int]int        // Cluster → best shard
    shardCapabilities map[int][]string // Shard → capabilities
}

func (r *SmartRouter) Route(query []float32, clusters *ClusterIndex) int {
    // Find which cluster the query belongs to
    nearestCluster := clusters.FindNearestCluster(query)
    
    // Route to shard that owns this cluster
    return r.clusterToShard[nearestCluster]
}
```

**Summary: One Pass, 12+ Optimizations**

| Optimization | Uses K-Means | Uses Louvain | Uses TLP |
|--------------|--------------|--------------|----------|
| Shard Placement | ✅ | ✅ | ✅ |
| Search Speedup | ✅ | | |
| Cache Warming | ✅ | ✅ | ✅ |
| Auto-Label | ✅ | ✅ | |
| Anomaly Detection | ✅ | | |
| Deduplication | ✅ | | |
| Recommendations | ✅ | | |
| Memory Tiering | ✅ | | |
| Result Diversity | ✅ | | |
| Prefetching | ✅ | | ✅ |
| Auto-Relationships | ✅ | ✅ | ✅ |
| Compression | ✅ | | |
| Query Routing | ✅ | | |

#### 4.2.6 One Small Piece Away: Unlockable Features

These features require only **one small addition** to your existing tools:

```
┌────────────────────────────────────────────────────────────────────────────┐
│         FEATURES "ONE BRIDGE" AWAY FROM YOUR EXISTING TOOLS                 │
├────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  HAVE                          NEED                    UNLOCKS              │
│  ────                          ────                    ───────              │
│                                                                             │
│  K-Means ──────────────► + Timestamps ──────────► Temporal Clustering      │
│                                                   (trend detection,        │
│                                                    concept drift)          │
│                                                                             │
│  K-Means ──────────────► + Recursion ───────────► Hierarchical Clusters    │
│                                                   (zoom in/out, taxonomy)  │
│                                                                             │
│  K-Means + VL Model ───► + Joint Space ─────────► Cross-Modal Clustering   │
│                                                   (image + text together)  │
│                                                                             │
│  K-Means ──────────────► + User Feedback ───────► Active Learning          │
│                                                   (self-improving search)  │
│                                                                             │
│  K-Means ──────────────► + Mini-Batch ──────────► Streaming Clusters       │
│                                                   (real-time assignment)   │
│                                                                             │
│  Clusters + Storage ───► + TF-IDF ──────────────► Cluster Explanations     │
│                                                   ("this cluster is about")│
│                                                                             │
│  Louvain + K-Means ────► + Walk Sampling ───────► Graph Embeddings         │
│                                                   (Node2Vec features)      │
│                                                                             │
│  Cluster Stats ────────► + Time Series ─────────► Predictive Scaling       │
│                                                   (auto-provision)         │
│                                                                             │
│  TLP + Clusters ───────► + Inter-Cluster ───────► Bridge Node Detection    │
│                                                   (find connectors)        │
│                                                                             │
│  K-Means Centroids ────► + Query Log ───────────► Autocompletion           │
│                                                   (smart suggestions)      │
│                                                                             │
└────────────────────────────────────────────────────────────────────────────┘
```

---

**1. Temporal Clustering** (Need: `clustered_at` timestamp)

Track when nodes joined clusters to detect trends and concept drift:

```go
// Small addition: timestamp on cluster assignment
type TemporalClusterAssignment struct {
    NodeID     NodeID
    ClusterID  int
    AssignedAt time.Time
    Embedding  []float32  // Snapshot at assignment time
}

// UNLOCKS: Trend Detection
func DetectEmergingTopics(history []TemporalClusterAssignment) []Trend {
    // Find clusters growing faster than average
    clusterGrowth := make(map[int][]time.Time)
    for _, a := range history {
        clusterGrowth[a.ClusterID] = append(clusterGrowth[a.ClusterID], a.AssignedAt)
    }
    
    trends := []Trend{}
    for clusterID, timestamps := range clusterGrowth {
        rate := computeGrowthRate(timestamps)
        if rate > avgRate * 1.5 { // 50% faster than average
            trends = append(trends, Trend{
                ClusterID:  clusterID,
                GrowthRate: rate,
                Label:      "Emerging Topic",
            })
        }
    }
    return trends
}

// UNLOCKS: Concept Drift Detection
func DetectDrift(nodeID NodeID, history []TemporalClusterAssignment) *DriftEvent {
    // Find if node changed clusters
    assignments := filterByNode(history, nodeID)
    if len(assignments) < 2 {
        return nil
    }
    
    prev := assignments[len(assignments)-2]
    curr := assignments[len(assignments)-1]
    
    if prev.ClusterID != curr.ClusterID {
        return &DriftEvent{
            NodeID:      nodeID,
            FromCluster: prev.ClusterID,
            ToCluster:   curr.ClusterID,
            DriftedAt:   curr.AssignedAt,
            Reason:      "Content semantics shifted",
        }
    }
    return nil
}
```

---

**2. Hierarchical Clustering** (Need: recursive k-means call)

Run k-means on clusters to create a tree structure:

```go
// Small addition: recursive clustering
type ClusterTree struct {
    ID       int
    Centroid []float32
    Children []*ClusterTree
    Members  []NodeID  // Only at leaves
    Level    int
}

func BuildHierarchy(embeddings map[NodeID][]float32, maxLevels int) *ClusterTree {
    root := &ClusterTree{ID: 0, Level: 0}
    buildLevel(root, embeddings, 0, maxLevels)
    return root
}

func buildLevel(node *ClusterTree, embeddings map[NodeID][]float32, level, maxLevels int) {
    if level >= maxLevels || len(embeddings) < 10 {
        // Leaf: store members
        for id := range embeddings {
            node.Members = append(node.Members, id)
        }
        return
    }
    
    // K-means at this level
    k := min(10, len(embeddings)/5)
    clusters := kMeans(embeddings, k)
    
    for clusterID, memberEmbeddings := range clusters {
        child := &ClusterTree{
            ID:       clusterID,
            Centroid: computeCentroid(memberEmbeddings),
            Level:    level + 1,
        }
        node.Children = append(node.Children, child)
        buildLevel(child, memberEmbeddings, level+1, maxLevels)
    }
}

// UNLOCKS: Zoom In/Out Navigation
func (t *ClusterTree) Drill(query []float32, level int) []NodeID {
    if t.Level == level || len(t.Children) == 0 {
        return t.Members
    }
    
    // Find nearest child
    nearestChild := t.Children[0]
    maxSim := cosineSimilarity(query, nearestChild.Centroid)
    for _, child := range t.Children[1:] {
        sim := cosineSimilarity(query, child.Centroid)
        if sim > maxSim {
            maxSim = sim
            nearestChild = child
        }
    }
    
    return nearestChild.Drill(query, level)
}

// UNLOCKS: Auto-Generated Taxonomy
// Level 0: "Technology"
// Level 1: "Software", "Hardware", "AI"
// Level 2: "Databases", "Web", "ML", "DL"
// Level 3: "Graph DBs", "Vector DBs", "SQL", ...
```

---

**3. Cross-Modal Clustering** (Need: shared embedding space)

You already have VL model for image→text. One step further:

```go
// Small addition: project to shared space
type MultiModalEmbedder struct {
    textEmbedder  Embedder      // BGE-M3
    imageToText   VLService     // Your existing VL
    sharedDim     int           // Common dimension
}

// Images → text description → text embedding (already possible!)
func (m *MultiModalEmbedder) EmbedImage(imagePath string) ([]float32, error) {
    // Step 1: VL describes image (YOU HAVE THIS)
    description, _ := m.imageToText.Describe(imagePath)
    
    // Step 2: Embed description (YOU HAVE THIS)
    return m.textEmbedder.Embed(description)
}

// UNLOCKS: Mixed Media Clusters
// Images and documents about "machine learning" cluster together!

func ClusterMixedMedia(files []File, embedder *MultiModalEmbedder) map[int][]File {
    embeddings := make(map[NodeID][]float32)
    
    for _, f := range files {
        var emb []float32
        if isImage(f) {
            emb, _ = embedder.EmbedImage(f.Path)
        } else {
            emb, _ = embedder.textEmbedder.Embed(f.Content)
        }
        embeddings[NodeID(f.ID)] = emb
    }
    
    // Standard k-means works on mixed media now!
    return kMeans(embeddings, 50)
}
```

---

**4. Active Learning Loop** (Need: feedback → centroid adjustment)

User feedback improves clusters over time:

```go
// Small addition: feedback accumulator
type AdaptiveClusters struct {
    centroids     [][]float32
    positivePulls map[int][][]float32 // Cluster → "good" examples
    negativePulls map[int][][]float32 // Cluster → "bad" examples
}

// User says "this result was good for my query"
func (a *AdaptiveClusters) PositiveFeedback(query []float32, resultID NodeID) {
    clusterID := a.findNearestCluster(query)
    resultEmb := a.getEmbedding(resultID)
    a.positivePulls[clusterID] = append(a.positivePulls[clusterID], resultEmb)
}

// User says "this result was bad"
func (a *AdaptiveClusters) NegativeFeedback(query []float32, resultID NodeID) {
    clusterID := a.findNearestCluster(query)
    resultEmb := a.getEmbedding(resultID)
    a.negativePulls[clusterID] = append(a.negativePulls[clusterID], resultEmb)
}

// UNLOCKS: Self-Improving Search
func (a *AdaptiveClusters) AdaptCentroids(learningRate float32) {
    for clusterID := range a.centroids {
        centroid := a.centroids[clusterID]
        
        // Pull toward positive examples
        for _, pos := range a.positivePulls[clusterID] {
            for i := range centroid {
                centroid[i] += learningRate * (pos[i] - centroid[i])
            }
        }
        
        // Push away from negative examples
        for _, neg := range a.negativePulls[clusterID] {
            for i := range centroid {
                centroid[i] -= learningRate * 0.5 * (neg[i] - centroid[i])
            }
        }
        
        normalize(centroid)
    }
}
```

---

**5. Cluster Explanations** (Need: TF-IDF on cluster members)

Auto-generate "what is this cluster about":

```go
// Small addition: keyword extraction
type ClusterExplainer struct {
    clusters *ClusterIndex
    storage  Storage
}

func (e *ClusterExplainer) ExplainCluster(clusterID int) ClusterExplanation {
    members := e.clusters.GetClusterMembers(clusterID)
    
    // Collect all text from cluster members
    allText := []string{}
    for _, nodeID := range members {
        node, _ := e.storage.GetNode(nodeID)
        if content, ok := node.Properties["content"].(string); ok {
            allText = append(allText, content)
        }
    }
    
    // TF-IDF to find distinctive terms
    keywords := extractKeywords(allText, 10)
    
    // Find most central member (closest to centroid)
    centroid := e.clusters.GetCentroid(clusterID)
    exemplar := findClosestToPoint(members, centroid)
    
    return ClusterExplanation{
        ClusterID:   clusterID,
        Keywords:    keywords,        // ["machine learning", "neural", "training"]
        Exemplar:    exemplar,        // Most representative document
        Size:        len(members),
        Description: generateDescription(keywords), // "This cluster contains documents about machine learning and neural network training"
    }
}

// UNLOCKS: Explainable Search Results
// "We found these results because they're in the 'Machine Learning' cluster"
```

---

**6. Bridge Node Detection** (Need: inter-cluster TLP)

Find nodes that connect different communities:

```go
// Small addition: TLP across cluster boundaries
func FindBridgeNodes(clusters *ClusterIndex, tlp *LinkPredict) []BridgeNode {
    bridges := []BridgeNode{}
    
    for nodeID := range clusters.GetAllNodes() {
        myCluster := clusters.GetCluster(nodeID)
        
        // Use TLP to find predicted connections
        predictions := tlp.AdamicAdar(nodeID, 20)
        
        // Count connections to OTHER clusters
        crossClusterConnections := make(map[int]int)
        for _, pred := range predictions {
            targetCluster := clusters.GetCluster(pred.TargetID)
            if targetCluster != myCluster {
                crossClusterConnections[targetCluster]++
            }
        }
        
        // Bridge = connects to multiple other clusters
        if len(crossClusterConnections) >= 3 {
            bridges = append(bridges, BridgeNode{
                NodeID:             nodeID,
                HomeCluster:        myCluster,
                ConnectedClusters:  crossClusterConnections,
                BridgeScore:        float64(len(crossClusterConnections)),
            })
        }
    }
    
    return bridges
}

// UNLOCKS: 
// - Find "hub" documents that connect topics
// - Identify key people in organization (connect departments)
// - Discover interdisciplinary research
```

---

**7. Streaming Cluster Assignment** (Need: mini-batch update)

Assign new nodes without full re-cluster:

```go
// Small addition: nearest centroid assignment
func (c *ClusterIndex) AssignStreaming(newNode *Node) int {
    if len(newNode.Embedding) == 0 {
        return -1
    }
    
    // Find nearest existing centroid
    nearestCluster := 0
    maxSim := float32(-1)
    
    for i, centroid := range c.centroids {
        sim := cosineSimilarity(newNode.Embedding, centroid)
        if sim > maxSim {
            maxSim = sim
            nearestCluster = i
        }
    }
    
    // Assign to nearest cluster
    c.assignments[newNode.ID] = nearestCluster
    c.clusterMap[nearestCluster] = append(c.clusterMap[nearestCluster], newNode.ID)
    
    // Track drift for eventual re-clustering
    c.updatesSinceCluster++
    
    return nearestCluster
}

// UNLOCKS: Real-time cluster assignment
// New document arrives → instantly assigned → searchable in right cluster
```

---

**8. Query Autocompletion** (Need: query log + centroid matching)

Suggest queries based on cluster centroids:

```go
// Small addition: query history per cluster
type SmartAutocomplete struct {
    clusters     *ClusterIndex
    queryHistory map[int][]QueryLog // Cluster → past queries
}

type QueryLog struct {
    Query     string
    Embedding []float32
    Clicks    int       // How many results were clicked
}

func (s *SmartAutocomplete) Suggest(partialQuery string) []string {
    // Embed partial query
    partialEmb := embed(partialQuery)
    
    // Find nearest cluster
    nearestCluster := s.clusters.FindNearestCluster(partialEmb)
    
    // Return successful queries from that cluster
    suggestions := []string{}
    history := s.queryHistory[nearestCluster]
    
    // Sort by clicks (most successful first)
    sort.Slice(history, func(i, j int) bool {
        return history[i].Clicks > history[j].Clicks
    })
    
    for _, q := range history[:min(5, len(history))] {
        if strings.HasPrefix(strings.ToLower(q.Query), strings.ToLower(partialQuery)) {
            suggestions = append(suggestions, q.Query)
        }
    }
    
    return suggestions
}

// UNLOCKS: "Users searching for this often search for..."
```

---

**9. Predictive Auto-Scaling** (Need: cluster size time series)

Predict when you need more resources:

```go
// Small addition: track cluster sizes over time
type ClusterSizeHistory struct {
    snapshots []ClusterSnapshot
}

type ClusterSnapshot struct {
    Time     time.Time
    Sizes    map[int]int // Cluster → size
}

func (h *ClusterSizeHistory) PredictGrowth(clusterID int, horizon time.Duration) int {
    // Extract time series for this cluster
    times := []float64{}
    sizes := []float64{}
    
    for _, snap := range h.snapshots {
        times = append(times, float64(snap.Time.Unix()))
        sizes = append(sizes, float64(snap.Sizes[clusterID]))
    }
    
    // Linear regression
    slope, intercept := linearRegression(times, sizes)
    
    // Predict future size
    futureTime := float64(time.Now().Add(horizon).Unix())
    predicted := slope*futureTime + intercept
    
    return int(predicted)
}

// UNLOCKS: Auto-Provisioning
// "Cluster 5 will need 50% more storage in 30 days"
// "Vector index for cluster 12 approaching memory limit"
```

---

**Summary: Small Bridges → Big Features**

| Feature | What You Have | Small Addition | Effort |
|---------|---------------|----------------|--------|
| **Temporal Trends** | K-Means | Timestamp field | ~50 lines |
| **Hierarchical** | K-Means | Recursive call | ~100 lines |
| **Cross-Modal** | K-Means + VL | Already possible! | ~20 lines |
| **Active Learning** | K-Means | Feedback accumulator | ~80 lines |
| **Explanations** | Clusters + Storage | TF-IDF | ~60 lines |
| **Bridge Detection** | K-Means + TLP | Cross-cluster check | ~40 lines |
| **Streaming** | K-Means | Mini-batch assign | ~30 lines |
| **Autocomplete** | K-Means | Query log | ~70 lines |
| **Auto-Scaling** | Cluster stats | Time series | ~50 lines |

**Most bang for buck:**
1. **Cross-Modal** - You already have it, just wire it up!
2. **Streaming Assignment** - 30 lines enables real-time
3. **Bridge Detection** - 40 lines discovers knowledge connectors

#### 4.2.7 Continuous Shard Optimization

Monitor and rebalance shards over time:

```go
// ShardMonitor tracks cross-shard query patterns
type ShardMonitor struct {
    crossShardQueries map[ShardPair]int64 // Count of cross-shard hops
    withinShardQueries map[int]int64      // Count of within-shard queries
}

// SuggestRebalance returns nodes that should move shards
func (m *ShardMonitor) SuggestRebalance() []RebalanceSuggestion {
    suggestions := []RebalanceSuggestion{}
    
    // Find hot cross-shard paths
    for pair, count := range m.crossShardQueries {
        if count > m.threshold {
            // Identify which nodes to move
            nodes := m.findNodesToMove(pair)
            suggestions = append(suggestions, RebalanceSuggestion{
                Nodes:       nodes,
                FromShard:   pair.Source,
                ToShard:     pair.Target,
                Benefit:     m.estimateBenefit(nodes, pair),
            })
        }
    }
    
    return suggestions
}

// Live rebalancing (zero-downtime)
func (c *Cluster) RebalanceNode(nodeID NodeID, fromShard, toShard int) error {
    // 1. Copy node to target shard
    node, _ := c.shards[fromShard].GetNode(nodeID)
    c.shards[toShard].CreateNode(node)
    
    // 2. Update routing table (atomic)
    c.routing.UpdateNodeLocation(nodeID, toShard)
    
    // 3. Delete from source shard (background)
    go c.shards[fromShard].DeleteNode(nodeID)
    
    return nil
}
```

### 4.3 Edge Placement

Edges are tricky because they connect nodes that may be on different shards.

**Strategy: Edge follows source node**

```
┌─────────────────────────────────────────────────────────────┐
│                    Edge Placement                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Edge: (User:123)-[:OWNS]->(File:456)                       │
│                                                              │
│  Rule: Edge stored with SOURCE node                          │
│        User:123 is on Shard A                                │
│        → Edge stored on Shard A                              │
│                                                              │
│  Traversal:                                                  │
│  • Outgoing edges: Local read (fast)                         │
│  • Incoming edges: Need secondary index or scatter           │
│                                                              │
│  Optimization: Bidirectional edge index                      │
│  • Each shard maintains: outgoing_edges[node_id]            │
│  • Coordinator maintains: incoming_edges[node_id] → shard   │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 4.3 Vector Index Distribution

Vector indexes (HNSW) need special handling:

```
┌─────────────────────────────────────────────────────────────┐
│                Vector Index Strategy                         │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  Option A: Per-Shard Index (Simpler)                        │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐                     │
│  │Shard A  │  │Shard B  │  │Shard C  │                     │
│  │┌───────┐│  │┌───────┐│  │┌───────┐│                     │
│  ││ HNSW  ││  ││ HNSW  ││  ││ HNSW  ││                     │
│  │└───────┘│  │└───────┘│  │└───────┘│                     │
│  └─────────┘  └─────────┘  └─────────┘                     │
│  Query: Scatter to all → merge top-K                        │
│                                                              │
│  Option B: Dedicated Vector Shards (Scalable)               │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              Vector Index Cluster                    │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐             │   │
│  │  │Vector   │  │Vector   │  │Vector   │             │   │
│  │  │Shard 1  │  │Shard 2  │  │Shard 3  │             │   │
│  │  │(IVF)    │  │(IVF)    │  │(IVF)    │             │   │
│  │  └─────────┘  └─────────┘  └─────────┘             │   │
│  └─────────────────────────────────────────────────────┘   │
│  Query: Route to relevant partitions by IVF cluster         │
│                                                              │
│  Recommendation: Start with Option A, migrate to B at scale │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## Part 5: Consistency Model

### 5.1 Tunable Consistency (Cassandra-style)

```
┌─────────────────────────────────────────────────────────────┐
│                 Consistency Levels                           │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  WRITE Consistency:                                          │
│  • ONE:    Write to 1 node, ack immediately (fastest)       │
│  • QUORUM: Write to N/2+1 nodes before ack (balanced)       │
│  • ALL:    Write to all replicas before ack (safest)        │
│                                                              │
│  READ Consistency:                                           │
│  • ONE:    Read from 1 node (may be stale)                  │
│  • QUORUM: Read from N/2+1 nodes, return latest (balanced)  │
│  • ALL:    Read from all nodes (slowest, most consistent)   │
│                                                              │
│  Recommended Defaults:                                       │
│  • Tier 2 (Standalone):  Write=ALL, Read=ONE                │
│  • Tier 3 (Replicated):  Write=QUORUM, Read=QUORUM          │
│  • Tier 4 (Sharded):     Write=LOCAL_QUORUM, Read=LOCAL_ONE │
│                                                              │
│  Example API:                                                │
│  MATCH (n:User) RETURN n                                     │
│  /* CONSISTENCY LEVEL = ONE */                               │
│                                                              │
│  CREATE (n:User {name: 'Alice'})                            │
│  /* CONSISTENCY LEVEL = QUORUM */                           │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 Raft for Strong Consistency

Within each shard, use Raft for strong consistency:

```go
// Raft integration with BadgerDB
type RaftNode struct {
    raft     *raft.Raft        // hashicorp/raft
    storage  *BadgerEngine     // NornicDB storage
    fsm      *NornicDBFSM      // Finite state machine
}

// FSM applies Raft log entries to BadgerDB
type NornicDBFSM struct {
    db *BadgerEngine
}

func (f *NornicDBFSM) Apply(log *raft.Log) interface{} {
    // Decode WAL entry from Raft log
    entry, _ := DecodeWALEntry(log.Data)
    
    // Apply to BadgerDB
    switch entry.Op {
    case OpCreateNode:
        return f.db.CreateNode(entry.Node)
    case OpCreateEdge:
        return f.db.CreateEdge(entry.Edge)
    // ... etc
    }
}
```

---

## Part 6: Implementation Roadmap

### Phase 1: Foundation (Q1 2025)

**Goal**: Enable Tier 2 (Standalone with Hot Standby)

```
┌─────────────────────────────────────────────────────────────┐
│  Phase 1: Hot Standby Replication                           │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. WAL Streaming                                            │
│     • gRPC service for WAL transmission                      │
│     • Standby continuously receives WAL entries              │
│     • Configurable sync mode (async, semi-sync, sync)        │
│                                                              │
│  2. Health Monitoring                                        │
│     • Heartbeat between primary and standby                  │
│     • Failure detection (configurable timeout)               │
│     • Health endpoint for load balancers                     │
│                                                              │
│  3. Failover                                                 │
│     • Manual failover command                                │
│     • Automatic failover (optional)                          │
│     • Standby promotion procedure                            │
│                                                              │
│  Deliverables:                                               │
│  • nornicdb serve --mode=primary --standby-url=...          │
│  • nornicdb serve --mode=standby --primary-url=...          │
│  • POST /admin/failover                                     │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**Effort**: ~4-6 weeks

### Phase 2: Raft Cluster (Q2 2025)

**Goal**: Enable Tier 3 (Replicated Cluster)

```
┌─────────────────────────────────────────────────────────────┐
│  Phase 2: Raft-based Replication                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Integrate hashicorp/raft                                 │
│     • Raft FSM backed by BadgerDB                           │
│     • Leader election                                        │
│     • Log replication                                        │
│                                                              │
│  2. Cluster Management                                       │
│     • Node join/leave                                        │
│     • Cluster status API                                     │
│     • Configuration changes                                  │
│                                                              │
│  3. Query Routing                                            │
│     • Writes always go to leader                             │
│     • Reads can go to any node (configurable)               │
│     • Read preference: leader, follower, nearest            │
│                                                              │
│  4. Vector Index Replication                                │
│     • HNSW index built independently on each node           │
│     • Same data → same index (deterministic)                │
│                                                              │
│  Deliverables:                                               │
│  • nornicdb serve --cluster --peers=node1,node2,node3       │
│  • GET /cluster/status                                      │
│  • POST /cluster/join, /cluster/leave                       │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**Effort**: ~8-12 weeks

### Phase 3: Sharding (Q3-Q4 2025)

**Goal**: Enable Tier 4 (Sharded Cluster)

```
┌─────────────────────────────────────────────────────────────┐
│  Phase 3: Horizontal Sharding                               │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Coordinator Layer                                        │
│     • Query parser/router                                    │
│     • Shard mapping (etcd-backed)                           │
│     • Cross-shard query orchestration                        │
│                                                              │
│  2. Sharding Logic                                           │
│     • Label-based shard assignment                          │
│     • Consistent hash ring for even distribution            │
│     • Edge routing (source-node affinity)                   │
│                                                              │
│  3. Cross-Shard Queries                                      │
│     • Scatter-gather for MATCH                              │
│     • Distributed aggregation                                │
│     • Join pushdown optimization                             │
│                                                              │
│  4. Rebalancing                                              │
│     • Add/remove shards                                      │
│     • Data migration                                         │
│     • Zero-downtime resharding                               │
│                                                              │
│  5. Vector Search                                            │
│     • Per-shard HNSW indexes                                │
│     • Scatter-gather for vector queries                     │
│     • Top-K merge across shards                             │
│                                                              │
│  Deliverables:                                               │
│  • nornicdb-coordinator serve --shards=3                    │
│  • nornicdb serve --shard-id=1 --coordinator=...            │
│  • Automatic query routing                                  │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**Effort**: ~16-24 weeks

### Phase 4: Enterprise Features (2026)

```
┌─────────────────────────────────────────────────────────────┐
│  Phase 4: Enterprise Scale                                  │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Geographic Distribution                                  │
│     • Multi-region deployment                                │
│     • Read replicas per region                               │
│     • Write routing to nearest region                        │
│                                                              │
│  2. Tiered Storage                                           │
│     • Hot: SSD (recent data)                                │
│     • Warm: HDD (older data)                                │
│     • Cold: Object storage (archive)                        │
│                                                              │
│  3. Advanced Query Optimization                              │
│     • Cross-shard join optimization                         │
│     • Adaptive query routing                                │
│     • Query result caching                                  │
│                                                              │
│  4. Observability                                            │
│     • Distributed tracing                                    │
│     • Per-shard metrics                                      │
│     • Query performance analysis                             │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## Part 7: Technical Decisions

### 7.1 Consensus Protocol: Raft

**Why Raft over Paxos/Gossip**:

| Aspect | Raft | Paxos | Gossip |
|--------|------|-------|--------|
| Understandability | ✅ High | ❌ Complex | ✅ Simple |
| Go implementations | ✅ hashicorp/raft, dragonboat | ⚠️ Few | ✅ memberlist |
| Strong consistency | ✅ Yes | ✅ Yes | ❌ Eventual |
| Leader election | ✅ Built-in | ⚠️ Complex | ❌ None |
| Production proven | ✅ etcd, CockroachDB | ✅ Spanner | ✅ Cassandra |

**Recommendation**: Use **hashicorp/raft** (battle-tested, Apache 2.0 license)

### 7.2 Metadata Store: etcd

For sharded clusters, we need a metadata store:

```
┌─────────────────────────────────────────────────────────────┐
│                    Metadata in etcd                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  /nornicdb/                                                  │
│  ├── cluster/                                                │
│  │   ├── config         # Cluster configuration             │
│  │   └── nodes/         # Node registry                     │
│  │       ├── node-1     # {addr, role, status}             │
│  │       ├── node-2                                         │
│  │       └── node-3                                         │
│  ├── shards/                                                 │
│  │   ├── shard-1        # {range, nodes, leader}           │
│  │   ├── shard-2                                            │
│  │   └── shard-3                                            │
│  ├── labels/                                                 │
│  │   ├── User           # {shard: 1}                       │
│  │   ├── File           # {shard: 2}                       │
│  │   └── Task           # {shard: 3}                       │
│  └── routing/                                                │
│      └── version        # Routing table version            │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 7.3 Communication: gRPC

```protobuf
// nornicdb/proto/cluster.proto

service ClusterService {
  // Node management
  rpc JoinCluster(JoinRequest) returns (JoinResponse);
  rpc LeaveCluster(LeaveRequest) returns (LeaveResponse);
  rpc GetClusterStatus(Empty) returns (ClusterStatus);
  
  // Replication
  rpc StreamWAL(StreamRequest) returns (stream WALEntry);
  rpc AppendEntries(AppendRequest) returns (AppendResponse);
  
  // Query routing
  rpc ExecuteQuery(QueryRequest) returns (QueryResponse);
  rpc ScatterGather(ScatterRequest) returns (stream GatherResponse);
}
```

---

## Part 8: Heterogeneous Cluster Architecture

### 8.1 The Vision: Capability-Based Routing

Traditional clusters assume homogeneous nodes. But real deployments are heterogeneous:

```
┌────────────────────────────────────────────────────────────────────────┐
│              HETEROGENEOUS CAPABILITY-BASED CLUSTER                     │
├────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                      Query Coordinator                           │   │
│  │   • Parses query, identifies required capabilities               │   │
│  │   • Routes sub-queries to capable nodes                          │   │
│  │   • Merges results from parallel execution                       │   │
│  └──────────────────────────┬──────────────────────────────────────┘   │
│                             │                                           │
│         ┌───────────────────┼───────────────────┐                      │
│         │                   │                   │                      │
│         ▼                   ▼                   ▼                      │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐                │
│  │ Raspberry   │    │ Desktop PC  │    │ GPU Server  │                │
│  │ Pi 5        │    │ (16GB RAM)  │    │ (RTX 4090)  │                │
│  ├─────────────┤    ├─────────────┤    ├─────────────┤                │
│  │ Capabilities│    │ Capabilities│    │ Capabilities│                │
│  │ ┌─────────┐ │    │ ┌─────────┐ │    │ ┌─────────┐ │                │
│  │ │✅ BM25   │ │    │ │✅ BM25   │ │    │ │✅ BM25   │ │                │
│  │ │✅ Graph  │ │    │ │✅ Graph  │ │    │ │✅ Graph  │ │                │
│  │ │✅ Storage│ │    │ │✅ Storage│ │    │ │✅ Storage│ │                │
│  │ │❌ Vector │ │    │ │✅ Vector │ │    │ │✅ Vector │ │                │
│  │ │❌ Embed  │ │    │ │⚠️ Embed  │ │    │ │✅ Embed  │ │                │
│  │ └─────────┘ │    │ └─────────┘ │    │ │✅ GPU    │ │                │
│  │             │    │             │    │ └─────────┘ │                │
│  │ Role: Text  │    │ Role: Mixed │    │ Role: AI    │                │
│  │ search node │    │ general node│    │ compute node│                │
│  └─────────────┘    └─────────────┘    └─────────────┘                │
│                                                                         │
│  Query: "Find files about 'machine learning' similar to this text"     │
│                                                                         │
│  Execution Plan:                                                        │
│  1. BM25 "machine learning" → Route to ALL nodes (parallel)            │
│  2. Generate embedding → Route to GPU Server                           │
│  3. Vector search → Route to Desktop + GPU (have HNSW index)          │
│  4. Graph traversal → Route to ALL nodes (parallel scatter-gather)    │
│  5. Merge & rank → Coordinator                                         │
│                                                                         │
└────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Node Capability Advertisement

Each node advertises its capabilities on startup:

```go
// Node capability registration
type NodeCapabilities struct {
    NodeID      string            `json:"node_id"`
    Address     string            `json:"address"`      // bolt://192.168.1.50:7687
    
    // Hardware profile
    Hardware    HardwareProfile   `json:"hardware"`
    
    // Enabled capabilities
    Capabilities map[Capability]CapabilityConfig `json:"capabilities"`
}

type HardwareProfile struct {
    CPUCores    int     `json:"cpu_cores"`
    RAMBytes    int64   `json:"ram_bytes"`
    GPUType     string  `json:"gpu_type,omitempty"`    // "cuda", "metal", "none"
    GPUMemory   int64   `json:"gpu_memory,omitempty"`
    DiskType    string  `json:"disk_type"`             // "ssd", "hdd", "nvme"
    NetworkMbps int     `json:"network_mbps"`
}

type Capability string

const (
    CapStorage      Capability = "storage"       // Can store nodes/edges
    CapBM25         Capability = "bm25"          // Full-text search
    CapVector       Capability = "vector"        // HNSW vector search
    CapEmbed        Capability = "embed"         // Generate embeddings
    CapGPUCompute   Capability = "gpu_compute"   // GPU-accelerated ops
    CapGraphTraverse Capability = "graph_traverse" // Multi-hop traversal
)

type CapabilityConfig struct {
    Enabled     bool    `json:"enabled"`
    Priority    int     `json:"priority"`      // Higher = preferred for this cap
    MaxLoad     float64 `json:"max_load"`      // 0.0-1.0, current capacity
    Latency     string  `json:"latency"`       // "low", "medium", "high"
}
```

### 8.3 Example: Raspberry Pi Configuration

```yaml
# nornicdb.yaml on Raspberry Pi 5
node:
  id: "rpi5-kitchen"
  cluster: "home-lab"
  
capabilities:
  storage:
    enabled: true
    priority: 5          # Lower priority (limited storage)
    
  bm25:
    enabled: true
    priority: 10         # High priority - CPU efficient
    
  graph_traverse:
    enabled: true
    priority: 7          # Can do graph ops
    
  vector:
    enabled: false       # Not enough RAM for HNSW index
    
  embed:
    enabled: false       # No GPU, too slow on CPU

# Data partitioning - only store certain labels
partitioning:
  mode: "label_filter"
  labels:
    include: ["Task", "Note", "Decision"]  # Lightweight nodes
    exclude: ["File", "FileChunk"]          # Heavy content → other nodes
```

### 8.4 Example: GPU Server Configuration

```yaml
# nornicdb.yaml on GPU server
node:
  id: "gpu-server-1"
  cluster: "home-lab"
  
capabilities:
  storage:
    enabled: true
    priority: 10         # Fast NVMe
    
  bm25:
    enabled: true
    priority: 8
    
  graph_traverse:
    enabled: true
    priority: 10
    
  vector:
    enabled: true
    priority: 10         # Preferred for vector search
    hnsw_max_elements: 10000000
    
  embed:
    enabled: true
    priority: 10         # Preferred for embedding
    provider: "local"
    model: "bge-m3"
    gpu_layers: -1       # All layers on GPU

# Store everything, especially heavy content
partitioning:
  mode: "label_filter"
  labels:
    include: ["*"]       # Accept all labels
```

### 8.5 Query Routing Logic

```go
// QueryPlanner determines execution strategy based on capabilities
type QueryPlanner struct {
    cluster *ClusterState
}

func (p *QueryPlanner) Plan(query *ParsedQuery) (*ExecutionPlan, error) {
    plan := &ExecutionPlan{}
    
    // Analyze query requirements
    requirements := p.analyzeRequirements(query)
    
    // For each requirement, find capable nodes
    for _, req := range requirements {
        nodes := p.findCapableNodes(req.Capability, req.MinPriority)
        
        if len(nodes) == 0 {
            return nil, fmt.Errorf("no node capable of: %s", req.Capability)
        }
        
        // Decide routing strategy
        switch req.Strategy {
        case RouteSingle:
            // Pick best node (highest priority, lowest load)
            plan.AddStep(req.Operation, p.pickBestNode(nodes))
            
        case RouteScatter:
            // Send to all capable nodes, gather results
            plan.AddScatterGather(req.Operation, nodes)
            
        case RouteAffinity:
            // Route to node that has the data
            plan.AddAffinityRoute(req.Operation, req.DataKeys)
        }
    }
    
    return plan, nil
}

// Example: Hybrid search query
// "CALL db.index.fulltext.queryNodes('content', 'machine learning')
//  YIELD node, score as bm25Score
//  CALL db.index.vector.queryNodes('embedding', $queryVector, 10)
//  YIELD node, score as vecScore
//  RETURN node, bm25Score + vecScore as combinedScore"

func (p *QueryPlanner) analyzeRequirements(q *ParsedQuery) []Requirement {
    return []Requirement{
        {
            Capability: CapBM25,
            Strategy:   RouteScatter,    // BM25 on all nodes with data
            Operation:  "fulltext_query",
        },
        {
            Capability: CapVector,
            Strategy:   RouteScatter,    // Vector search on nodes with index
            Operation:  "vector_query",
        },
        {
            Capability: CapGraphTraverse,
            Strategy:   RouteAffinity,   // Traverse from result nodes
            Operation:  "merge_results",
        },
    }
}
```

### 8.6 Parallel Graph Traversal over Bolt

For graph queries that span multiple nodes:

```
┌────────────────────────────────────────────────────────────────────────┐
│           PARALLEL GRAPH TRAVERSAL (Scatter-Gather)                    │
├────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  Query: MATCH (u:User)-[:KNOWS*1..3]->(friend) WHERE u.id = 'alice'   │
│                                                                         │
│  Step 1: Coordinator parses query                                      │
│          Identifies: Start node (User:alice), traversal depth (1-3)   │
│                                                                         │
│  Step 2: Find Alice's location                                         │
│          Coordinator → Metadata store: "Where is User:alice?"          │
│          Answer: Node A (Raspberry Pi)                                 │
│                                                                         │
│  Step 3: Start traversal on Node A (via Bolt)                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ Node A (RPi)                                                     │   │
│  │                                                                   │   │
│  │ MATCH (u:User {id:'alice'})-[:KNOWS]->(f1)                      │   │
│  │ RETURN f1.id, f1._shard                                          │   │
│  │                                                                   │   │
│  │ Results: [{id: 'bob', _shard: 'A'}, {id: 'carol', _shard: 'B'}] │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  Step 4: Scatter to shards for depth 2                                 │
│  ┌──────────────────────┐    ┌──────────────────────┐                 │
│  │ Node A (parallel)     │    │ Node B (parallel)     │                 │
│  │                       │    │                       │                 │
│  │ MATCH (u:User {id:    │    │ MATCH (u:User {id:    │                 │
│  │   'bob'})-[:KNOWS]    │    │   'carol'})-[:KNOWS]  │                 │
│  │   ->(f2)              │    │   ->(f2)              │                 │
│  │                       │    │                       │                 │
│  │ Results: [dave, eve]  │    │ Results: [frank]      │                 │
│  └──────────────────────┘    └──────────────────────┘                 │
│                                                                         │
│  Step 5: Coordinator merges results                                    │
│          Depth 1: bob, carol                                           │
│          Depth 2: dave, eve, frank                                     │
│          Depth 3: ... (continue if needed)                             │
│                                                                         │
│  Step 6: Return to client                                              │
│                                                                         │
└────────────────────────────────────────────────────────────────────────┘
```

### 8.7 Workload-Based Auto-Routing

```go
// WorkloadRouter monitors node health and routes accordingly
type WorkloadRouter struct {
    nodes    map[string]*NodeStatus
    metrics  *MetricsCollector
}

type NodeStatus struct {
    Capabilities map[Capability]bool
    
    // Real-time metrics
    CPUUsage     float64   // 0.0 - 1.0
    MemoryUsage  float64   // 0.0 - 1.0
    QueueDepth   int       // Pending queries
    AvgLatencyMs float64   // Recent average
    
    // Health
    LastHeartbeat time.Time
    Healthy       bool
}

func (r *WorkloadRouter) SelectNode(cap Capability) *Node {
    candidates := r.getCapableNodes(cap)
    
    // Score each candidate
    var best *Node
    bestScore := -1.0
    
    for _, node := range candidates {
        score := r.scoreNode(node, cap)
        if score > bestScore {
            best = node
            bestScore = score
        }
    }
    
    return best
}

func (r *WorkloadRouter) scoreNode(node *NodeStatus, cap Capability) float64 {
    // Higher score = better choice
    score := 100.0
    
    // Penalize high CPU usage
    score -= node.CPUUsage * 30
    
    // Penalize high memory usage
    score -= node.MemoryUsage * 20
    
    // Penalize queue depth
    score -= float64(node.QueueDepth) * 5
    
    // Penalize high latency
    score -= node.AvgLatencyMs / 10
    
    // Bonus for capability priority
    score += float64(node.Capabilities[cap].Priority) * 2
    
    return score
}
```

### 8.8 Data Locality & Affinity

For best performance, keep related data together:

```yaml
# Affinity rules in cluster config
affinity_groups:
  # User-related nodes stay together
  - name: "user-graph"
    labels: ["User", "Profile", "Session", "Preference"]
    preferred_nodes: ["desktop-pc", "gpu-server"]
    
  # File content goes to nodes with vector search
  - name: "content-store"
    labels: ["File", "FileChunk", "Embedding"]
    preferred_nodes: ["gpu-server"]
    required_capabilities: ["vector", "embed"]
    
  # Lightweight task data can go anywhere
  - name: "task-store"
    labels: ["Task", "Decision", "Note", "Memory"]
    preferred_nodes: ["*"]  # Any node
    
  # Edges follow source node
  - name: "edges"
    follow_source: true
```

### 8.9 Deployment Scenarios

#### Home Lab (Your Scenario)

```
┌─────────────────────────────────────────────────────────────┐
│                    HOME LAB CLUSTER                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐     │
│  │ Raspberry   │    │ Old Laptop  │    │ Gaming PC   │     │
│  │ Pi 5        │    │ (8GB RAM)   │    │ (RTX 3080)  │     │
│  ├─────────────┤    ├─────────────┤    ├─────────────┤     │
│  │ BM25: ✅    │    │ BM25: ✅    │    │ BM25: ✅    │     │
│  │ Vector: ❌  │    │ Vector: ✅  │    │ Vector: ✅  │     │
│  │ Embed: ❌   │    │ Embed: ⚠️   │    │ Embed: ✅   │     │
│  │ Storage: ✅ │    │ Storage: ✅ │    │ Storage: ✅ │     │
│  ├─────────────┤    ├─────────────┤    ├─────────────┤     │
│  │ Data:       │    │ Data:       │    │ Data:       │     │
│  │ Tasks, Notes│    │ All types   │    │ Files, Vecs │     │
│  │ (50K nodes) │    │ (500K nodes)│    │ (2M nodes)  │     │
│  └─────────────┘    └─────────────┘    └─────────────┘     │
│                                                              │
│  Total cluster: 2.5M nodes, distributed by capability       │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### Small Business

```
┌─────────────────────────────────────────────────────────────┐
│                  SMALL BUSINESS CLUSTER                      │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌───────────────────────────────────────────────────────┐  │
│  │                   Load Balancer                        │  │
│  └───────────────────────┬───────────────────────────────┘  │
│                          │                                   │
│         ┌────────────────┼────────────────┐                 │
│         ▼                ▼                ▼                 │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│  │ App Server 1│  │ App Server 2│  │ GPU Worker  │        │
│  │ (VM, 4 CPU) │  │ (VM, 4 CPU) │  │ (Bare metal)│        │
│  ├─────────────┤  ├─────────────┤  ├─────────────┤        │
│  │ Coordinator │  │ Coordinator │  │ Worker only │        │
│  │ + Storage   │  │ + Storage   │  │ Embed+Vector│        │
│  │ + BM25      │  │ + BM25      │  │ No storage  │        │
│  └─────────────┘  └─────────────┘  └─────────────┘        │
│                                                              │
│  • App servers handle API + lightweight queries             │
│  • GPU worker handles all embedding + vector search         │
│  • Raft replication between app servers                     │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### Enterprise (Geo-Distributed)

```
┌─────────────────────────────────────────────────────────────┐
│                 ENTERPRISE GEO-DISTRIBUTED                   │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  US-EAST                      EU-WEST                       │
│  ┌─────────────────┐         ┌─────────────────┐           │
│  │ Shard A (Raft)  │◄───────►│ Shard A (Raft)  │           │
│  │ 3 nodes         │  async  │ 3 nodes         │           │
│  │ Primary write   │  repl   │ Read replica    │           │
│  └─────────────────┘         └─────────────────┘           │
│                                                              │
│  ┌─────────────────┐         ┌─────────────────┐           │
│  │ Shard B (Raft)  │◄───────►│ Shard B (Raft)  │           │
│  │ 3 nodes         │  async  │ 3 nodes         │           │
│  │ Read replica    │  repl   │ Primary write   │           │
│  └─────────────────┘         └─────────────────┘           │
│                                                              │
│  ┌─────────────────┐         ┌─────────────────┐           │
│  │ GPU Pool        │         │ GPU Pool        │           │
│  │ (Embed/Vector)  │         │ (Embed/Vector)  │           │
│  └─────────────────┘         └─────────────────┘           │
│                                                              │
│  • Each region has coordinators + shards + GPU workers     │
│  • Cross-region async replication for DR                    │
│  • Queries route to nearest region                          │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## Part 9: Mimir Integration

### 9.1 Mimir as Intelligence Layer

```
┌─────────────────────────────────────────────────────────────┐
│                    Mimir + NornicDB                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                    Mimir Server                      │   │
│  │  • File indexing, content extraction                 │   │
│  │  • Multi-agent orchestration                        │   │
│  │  • VL image description                              │   │
│  │  • Connection pooling                                │   │
│  └───────────────────────┬─────────────────────────────┘   │
│                          │ Bolt Protocol                    │
│                          ▼                                  │
│  ┌─────────────────────────────────────────────────────┐   │
│  │              NornicDB Cluster                        │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐             │   │
│  │  │ Shard A │  │ Shard B │  │ Shard C │             │   │
│  │  │ Files   │  │ Tasks   │  │ Search  │             │   │
│  │  └─────────┘  └─────────┘  └─────────┘             │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                              │
│  Mimir handles:                                              │
│  • Content extraction (PDF, DOCX, images)                   │
│  • Text enrichment (metadata prefix)                        │
│  • Connection management                                     │
│  • Query optimization hints                                  │
│                                                              │
│  NornicDB handles:                                           │
│  • Storage, replication, sharding                           │
│  • Embedding generation                                      │
│  • Vector search                                             │
│  • Graph traversal                                           │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 9.2 Cluster-Aware Connection

```typescript
// Mimir cluster configuration
const config = {
  nornicdb: {
    mode: 'cluster',  // 'standalone' | 'cluster' | 'sharded'
    nodes: [
      'nornicdb-1:7687',
      'nornicdb-2:7687',
      'nornicdb-3:7687',
    ],
    readPreference: 'nearest',  // 'leader' | 'follower' | 'nearest'
    writeConsistency: 'quorum', // 'one' | 'quorum' | 'all'
  }
};
```

---

## Part 10: Summary

### Recommended Architecture Evolution

```
TODAY (2024)                    NEAR-TERM (2025)                LONG-TERM (2026+)
─────────────                   ────────────────                 ─────────────────

┌───────────┐                   ┌───────────────────┐           ┌──────────────────────┐
│ Single    │                   │ Primary + Standby │           │ Sharded Cluster      │
│ Node      │         →         │ (WAL streaming)   │     →     │ (Coordinator + Shards)│
│ (BadgerDB)│                   │                   │           │                      │
└───────────┘                   └───────────────────┘           └──────────────────────┘

~10K nodes                      ~1M nodes                       ~10B+ nodes
~100MB RAM                      ~1GB RAM                        Unlimited (horizontal)
No HA                           Hot standby HA                  Full HA + Geo-dist
```

### Key Takeaways

1. **Start Simple**: Tier 2 (hot standby) covers 90% of use cases
2. **Raft for Consistency**: Use hashicorp/raft for cluster consensus
3. **Label-based Sharding**: Graph-aware data locality
4. **Cassandra-style Tuning**: Consistency levels per query
5. **etcd for Metadata**: Proven, reliable, Go-native
6. **Incremental Migration**: Each tier builds on previous

### Next Steps

1. **Review this document** with stakeholders
2. **Prototype Phase 1** (WAL streaming to standby)
3. **Benchmark** single-node vs replicated performance
4. **Design API** for cluster management
5. **Document** operational procedures

---

## Appendix A: Go Libraries for Clustering

| Library | Purpose | License | Notes |
|---------|---------|---------|-------|
| **hashicorp/raft** | Consensus | MPL-2.0 | Battle-tested, used by Consul |
| **dragonboat** | Consensus | Apache-2.0 | Higher performance, more complex |
| **hashicorp/memberlist** | Gossip/discovery | MPL-2.0 | Node discovery, failure detection |
| **etcd/clientv3** | Metadata store | Apache-2.0 | Official Go client |
| **grpc-go** | RPC | Apache-2.0 | Standard for inter-node comms |

## Appendix B: Estimated Resource Requirements

| Tier | Nodes | RAM/Node | Storage/Node | Network |
|------|-------|----------|--------------|---------|
| Embedded | 1 | 100MB | 1GB | None |
| Standalone | 2 | 1GB | 10GB | 1Gbps |
| Replicated | 3-5 | 4GB | 50GB | 10Gbps |
| Sharded | 10+ | 8GB | 100GB | 10Gbps |

## Appendix C: References

- [Cassandra Architecture](https://cassandra.apache.org/doc/latest/cassandra/architecture/)
- [Dgraph Design](https://dgraph.io/docs/design-concepts/)
- [Milvus Architecture](https://milvus.io/docs/architecture_overview.md)
- [Raft Consensus](https://raft.github.io/)
- [CockroachDB Design](https://www.cockroachlabs.com/docs/stable/architecture/overview.html)
