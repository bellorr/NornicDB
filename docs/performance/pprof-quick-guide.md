# pprof Quick Guide for HNSW Profiling

## You're in pprof interactive mode - here's what to do:

### Step 1: Find Top CPU Consumers

```pprof
(pprof) top10
```

This shows the top 10 functions consuming CPU time. Look for:
- HNSW search functions
- GC-related functions (`runtime.gcBgMarkWorker`)
- Vector operations
- Lock contention

### Step 2: Focus on HNSW Functions

```pprof
(pprof) top20 -cum
```

Shows functions with cumulative time (includes called functions). This helps find the call chain.

### Step 3: Check for GC Overhead

```pprof
(pprof) top20 | grep -E "(gc|runtime)"
```

Look for:
- `runtime.gcBgMarkWorker` - GC background work
- `runtime.mallocgc` - Memory allocation
- High percentage (>10%) indicates GC pressure

### Step 4: Focus on HNSW Search

```pprof
(pprof) list searchWithEf
```

Shows line-by-line CPU time in the search function. This identifies hot spots.

### Step 5: Generate Visual Report

```pprof
(pprof) web
```

Opens a visual call graph in your browser. Requires Graphviz installed.

Or generate SVG:
```pprof
(pprof) svg > /tmp/hnsw_profile.svg
```

### Step 6: Check Specific Functions

```pprof
(pprof) list selectNeighbors
(pprof) list searchLayerHeapPooled
(pprof) list vector.DotProductSIMD
```

### Step 7: Exit and Generate HTML Report

```pprof
(pprof) exit
```

Then generate a web UI:
```bash
go tool pprof -http=:8080 cpu.prof
```

Opens interactive web UI at http://localhost:8080

## Quick Commands Reference

| Command | Purpose |
|---------|---------|
| `top10` | Top 10 CPU consumers |
| `top20 -cum` | Top 20 with cumulative time |
| `list <function>` | Line-by-line breakdown |
| `web` | Visual call graph (requires Graphviz) |
| `svg > file.svg` | Generate SVG graph |
| `png > file.png` | Generate PNG graph |
| `help` | Show all commands |
| `exit` or `quit` | Exit pprof |

## What to Look For

### GC Problems
- `runtime.gcBgMarkWorker` > 10% of total time
- `runtime.mallocgc` in top functions
- Frequent GC pauses in trace

### Allocation Hotspots
- Functions with high `flat` time that allocate
- `make()` calls in hot paths
- `append()` on slices without pre-allocation

### Lock Contention
- `sync.(*RWMutex).RLock` in top functions
- High time in lock acquisition

### Vector Operations
- `vector.DotProductSIMD` should be fast
- If slow, may need SIMD optimization

## Next Steps After Profiling

1. **If GC is the problem:**
   - Check memory profile: `go tool pprof -alloc_space mem.prof`
   - Look for allocation hotspots
   - Implement sync.Pool optimizations

2. **If allocations are the problem:**
   - Use `list <function>` to find exact lines
   - Add pools or pre-allocate buffers
   - Re-profile to verify improvements

3. **If locks are the problem:**
   - Consider lock-free reads (advanced)
   - Reduce lock scope
   - Use read-only operations where possible

