// Package search provides HNSW vector indexing for fast approximate nearest neighbor search.
//
// HNSW Delete/Update Policy:
//
// Delete:
//   - Remove() tombstones a vector via a dense `deleted []bool` flag
//   - Neighbor lists are not eagerly rewired (tombstones keep deletes cheap)
//   - Entry point is re-selected if the removed node was the entry point
//
// Update:
//   - Current policy: Remove() + Add() pattern
//   - Call Remove(id) then Add(id, newVector) to update a vector
//   - This ensures the graph structure is correctly maintained
//   - Future: A dedicated Update() method may be added for efficiency
//
// Graph Quality:
//   - High-churn workloads (many updates/deletes) can degrade graph quality
//   - Periodic rebuilds are recommended (see NORNICDB_VECTOR_ANN_REBUILD_INTERVAL)
//   - Rebuilds restore optimal graph structure and improve recall
package search

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sort"
	"sync"

	"github.com/orneryd/nornicdb/pkg/math/vector"
)

var errHNSWIndexFull = errors.New("hnsw index full")

// HNSWConfig contains configuration parameters for the HNSW index.
type HNSWConfig struct {
	M               int     // Max connections per node per layer (default: 16)
	EfConstruction  int     // Candidate list size during construction (default: 200)
	EfSearch        int     // Candidate list size during search (default: 100)
	LevelMultiplier float64 // Level multiplier = 1/ln(M)
}

// DefaultHNSWConfig returns sensible defaults for HNSW index.
func DefaultHNSWConfig() HNSWConfig {
	return HNSWConfig{
		M:               16,
		EfConstruction:  200,
		EfSearch:        100,
		LevelMultiplier: 1.0 / math.Log(16.0),
	}
}

// NOTE: We intentionally avoid a per-node struct/slices in favor of a
// struct-of-arrays layout in HNSWIndex to reduce pointer chasing and improve
// cache locality in the hot search loop.

// ANNResult is a minimal search result from the ANN index (HNSW).
//
// This intentionally stays small (ID + float32 score) to keep per-request
// allocations and copy costs low. Higher-level layers can enrich results as
// needed (labels, properties, etc.).
type ANNResult struct {
	ID    string
	Score float32
}

// HNSWIndex provides fast approximate nearest neighbor search using HNSW algorithm.
type HNSWIndex struct {
	config     HNSWConfig
	dimensions int
	mu         sync.RWMutex

	// Per-node metadata, indexed by internal ID.
	nodeLevel []uint16
	vecOff    []int32

	// Neighbor links stored in one arena to keep iteration cache-friendly.
	// For node i:
	//   - neighborsOff[i] points to (level+1)*M slots in neighborsArena
	//   - neighborCountsOff[i] points to (level+1) counts in neighborCountsArena
	neighborsArena      []uint32
	neighborsOff        []int32
	neighborCountsArena []uint16
	neighborCountsOff   []int32

	idToInternal map[string]uint32
	internalToID []string
	deleted      []bool
	liveCount    int
	vectors      []float32

	entryPoint    uint32
	hasEntryPoint bool
	maxLevel      int

	queryBufPool sync.Pool
	visitedPool  sync.Pool
	heapPool     sync.Pool
	idsPool      sync.Pool
}

type visitedGenState struct {
	gen []uint16
	cur uint16
}

// NewHNSWIndex creates a new HNSW index with the given dimensions and config.
func NewHNSWIndex(dimensions int, config HNSWConfig) *HNSWIndex {
	if config.M == 0 {
		config = DefaultHNSWConfig()
	}
	h := &HNSWIndex{
		config:              config,
		dimensions:          dimensions,
		nodeLevel:           make([]uint16, 0, 1024),
		vecOff:              make([]int32, 0, 1024),
		neighborsArena:      make([]uint32, 0, 1024*config.M),
		neighborsOff:        make([]int32, 0, 1024),
		neighborCountsArena: make([]uint16, 0, 1024),
		neighborCountsOff:   make([]int32, 0, 1024),
		idToInternal:        make(map[string]uint32, 1024),
		internalToID:        make([]string, 0, 1024),
		deleted:             make([]bool, 0, 1024),
		liveCount:           0,
		vectors:             make([]float32, 0, 1024*dimensions),
		maxLevel:            0,
	}
	h.queryBufPool.New = func() any {
		return make([]float32, dimensions)
	}
	h.visitedPool.New = func() any {
		return &visitedGenState{}
	}
	h.heapPool.New = func() any {
		return &distHeap{items: make([]hnswDistItem, 0, config.EfSearch*2)}
	}
	h.idsPool.New = func() any {
		return make([]uint32, 0, config.EfSearch*2)
	}
	return h
}

// Add inserts a vector into the index.
func (h *HNSWIndex) Add(id string, vec []float32) error {
	if len(vec) != h.dimensions {
		return ErrDimensionMismatch
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if id == "" {
		return nil
	}
	if internalID, ok := h.idToInternal[id]; ok && int(internalID) < len(h.deleted) && !h.deleted[internalID] {
		// In-place update: overwrite the stored vector without changing the graph
		// topology. This avoids tombstone growth from hot upsert workloads.
		//
		// Note: Neighbor links were created for the old vector, so large vector
		// changes can reduce recall until a rebuild. This is the same tradeoff
		// most production systems make to keep updates cheap.
		off := int(h.vecOff[internalID])
		if off >= 0 && off+h.dimensions <= len(h.vectors) {
			dst := h.vectors[off : off+h.dimensions]
			copy(dst, vec)
			vector.NormalizeInPlace(dst)
			return nil
		}

		// Fallback: inconsistent internal state; degrade to remove+add.
		h.removeLocked(internalID)
	}

	level := h.randomLevel()

	if len(h.nodeLevel) >= int(^uint32(0)) {
		return errHNSWIndexFull
	}
	internalID := uint32(len(h.nodeLevel))
	m := h.config.M
	if m <= 0 {
		return nil
	}

	vecOff := len(h.vectors)
	h.vectors = append(h.vectors, vec...)
	normalized := h.vectors[vecOff : vecOff+h.dimensions]
	vector.NormalizeInPlace(normalized)

	h.nodeLevel = append(h.nodeLevel, uint16(level))
	h.vecOff = append(h.vecOff, int32(vecOff))

	neighborsOff := len(h.neighborsArena)
	h.neighborsArena = append(h.neighborsArena, make([]uint32, (level+1)*m)...)
	h.neighborsOff = append(h.neighborsOff, int32(neighborsOff))

	countsOff := len(h.neighborCountsArena)
	h.neighborCountsArena = append(h.neighborCountsArena, make([]uint16, level+1)...)
	h.neighborCountsOff = append(h.neighborCountsOff, int32(countsOff))
	h.internalToID = append(h.internalToID, id)
	h.idToInternal[id] = internalID
	h.deleted = append(h.deleted, false)
	h.liveCount++

	if !h.hasEntryPoint {
		h.entryPoint = internalID
		h.hasEntryPoint = true
		h.maxLevel = level
		return nil
	}

	ep := h.entryPoint
	epLevel := int(h.nodeLevel[ep])

	for l := epLevel; l > level; l-- {
		ep = h.searchLayerSingle(normalized, ep, l)
	}

	for l := min(level, epLevel); l >= 0; l-- {
		candidates := h.searchLayer(normalized, ep, h.config.EfConstruction, l)
		neighbors := h.selectNeighbors(normalized, candidates, h.config.M)
		h.setNeighborsAtLevelLocked(internalID, l, neighbors)

		for _, neighborID := range neighbors {
			if int(neighborID) >= len(h.nodeLevel) || h.deleted[neighborID] {
				continue
			}
			h.insertNeighborAtLevelLocked(neighborID, l, internalID)
		}

		if len(candidates) > 0 {
			ep = candidates[0]
		}
	}

	if level > h.maxLevel {
		h.entryPoint = internalID
		h.hasEntryPoint = true
		h.maxLevel = level
	}

	return nil
}

// Update updates an existing vector in the index.
//
// Update policy: Remove + Add pattern
//   - Removes the old vector and all its connections
//   - Adds the new vector with fresh connections
//   - This ensures graph structure is correctly maintained
//
// If the vector doesn't exist, this is equivalent to Add().
//
// Performance: O(M * log(N)) where M is max connections, N is dataset size
// For high-churn workloads, consider periodic rebuilds to restore graph quality.
func (h *HNSWIndex) Update(id string, vec []float32) error {
	h.Remove(id)
	return h.Add(id, vec)
}

// Remove removes a vector from the index by ID.
func (h *HNSWIndex) Remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	internalID, ok := h.idToInternal[id]
	if !ok || int(internalID) >= len(h.nodeLevel) || h.deleted[internalID] {
		return
	}
	h.removeLocked(internalID)
}

// Clear removes all vectors from the index and resets it to an empty state.
// This frees memory by clearing all internal arrays and maps.
// Use this when you need to completely reset the index (e.g., after deleting a collection).
func (h *HNSWIndex) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Reset all internal state
	h.nodeLevel = make([]uint16, 0, 1024)
	h.vecOff = make([]int32, 0, 1024)
	h.neighborsArena = make([]uint32, 0, 1024*h.config.M)
	h.neighborsOff = make([]int32, 0, 1024)
	h.neighborCountsArena = make([]uint16, 0, 1024)
	h.neighborCountsOff = make([]int32, 0, 1024)
	h.idToInternal = make(map[string]uint32, 1024)
	h.internalToID = make([]string, 0, 1024)
	h.deleted = make([]bool, 0, 1024)
	h.liveCount = 0
	h.vectors = make([]float32, 0, 1024*h.dimensions)
	h.entryPoint = 0
	h.hasEntryPoint = false
	h.maxLevel = 0
}

// Search finds the k nearest neighbors to the query vector.
func (h *HNSWIndex) Search(ctx context.Context, query []float32, k int, minSimilarity float64) ([]ANNResult, error) {
	return h.searchWithEf(ctx, query, k, minSimilarity, h.config.EfSearch)
}

// SearchWithEf finds the k nearest neighbors using a caller-provided `ef`.
//
// In Qdrant terms, `ef` is the beam size for HNSW search: larger values improve
// recall and usually increase latency. If `ef <= 0`, this falls back to the
// index's configured `EfSearch`.
func (h *HNSWIndex) SearchWithEf(ctx context.Context, query []float32, k int, minSimilarity float64, ef int) ([]ANNResult, error) {
	if ef <= 0 {
		ef = h.config.EfSearch
	}
	return h.searchWithEf(ctx, query, k, minSimilarity, ef)
}

func (h *HNSWIndex) searchWithEf(ctx context.Context, query []float32, k int, minSimilarity float64, ef int) ([]ANNResult, error) {
	if len(query) != h.dimensions {
		return nil, ErrDimensionMismatch
	}
	if ef <= 0 {
		ef = h.config.EfSearch
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.hasEntryPoint || len(h.nodeLevel) == 0 {
		return []ANNResult{}, nil
	}

	var (
		normalized []float32
		pooledBuf  []float32
	)
	if h.dimensions <= 256 {
		var qbuf [256]float32
		copy(qbuf[:h.dimensions], query)
		normalized = qbuf[:h.dimensions]
		vector.NormalizeInPlace(normalized)
	} else {
		bufAny := h.queryBufPool.Get()
		buf := bufAny.([]float32)
		if cap(buf) < h.dimensions {
			buf = make([]float32, h.dimensions)
		}
		pooledBuf = buf
		normalized = buf[:h.dimensions]
		copy(normalized, query)
		vector.NormalizeInPlace(normalized)
		defer h.queryBufPool.Put(pooledBuf)
	}

	minSim32 := float32(minSimilarity)
	ep := h.entryPoint

	for l := h.maxLevel; l > 0; l-- {
		ep = h.searchLayerSingle(normalized, ep, l)
	}

	candidates := h.searchLayerHeapPooled(normalized, ep, ef, 0)

	results := make([]ANNResult, 0, k)
	var ctxErr error
	for _, candidateID := range candidates {
		if err := ctx.Err(); err != nil {
			ctxErr = err
			break
		}

		if int(candidateID) >= len(h.nodeLevel) || h.deleted[candidateID] {
			continue
		}
		similarity := vector.DotProductSIMD(normalized, h.vectorAtLocked(candidateID))

		if similarity >= minSim32 {
			results = append(results, ANNResult{
				ID:    h.internalToID[candidateID],
				Score: similarity,
			})
		}

		if len(results) >= k {
			break
		}
	}

	h.idsPool.Put(candidates[:0])
	if ctxErr != nil {
		return results, ctxErr
	}
	return results, nil
}

// Size returns the number of vectors in the index.
func (h *HNSWIndex) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.liveCount
}

// TombstoneRatio returns the ratio of deleted vectors to total vectors.
// Returns 0.0 if there are no vectors. A high ratio (>0.5) indicates
// the index should be rebuilt to free memory.
func (h *HNSWIndex) TombstoneRatio() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	total := len(h.nodeLevel)
	if total == 0 {
		return 0.0
	}
	deleted := total - h.liveCount
	return float64(deleted) / float64(total)
}

// ShouldRebuild returns true if the index has accumulated too many tombstones
// and should be rebuilt to free memory. Threshold is 50% deleted vectors.
func (h *HNSWIndex) ShouldRebuild() bool {
	return h.TombstoneRatio() > 0.5
}

func (h *HNSWIndex) removeLocked(internalID uint32) {
	if int(internalID) >= len(h.nodeLevel) || h.deleted[internalID] {
		return
	}

	h.deleted[internalID] = true
	h.liveCount--

	if int(internalID) < len(h.internalToID) {
		delete(h.idToInternal, h.internalToID[internalID])
	}

	// If index becomes empty, clear entry point.
	if h.liveCount <= 0 {
		h.entryPoint = 0
		h.hasEntryPoint = false
		h.maxLevel = 0
		return
	}

	// Re-select entry point if we deleted it, or if it may have carried maxLevel.
	if h.hasEntryPoint && (internalID == h.entryPoint || int(h.nodeLevel[internalID]) == h.maxLevel) {
		h.reselectEntryPointLocked()
	}
}

func (h *HNSWIndex) reselectEntryPointLocked() {
	var (
		bestID    uint32
		bestLevel = -1
		found     = false
	)

	for id := range h.nodeLevel {
		internalID := uint32(id)
		if int(internalID) < len(h.deleted) && h.deleted[internalID] {
			continue
		}
		lvl := int(h.nodeLevel[internalID])
		if !found || lvl > bestLevel {
			bestID = internalID
			bestLevel = lvl
			found = true
		}
	}

	if !found {
		h.entryPoint = 0
		h.hasEntryPoint = false
		h.maxLevel = 0
		return
	}

	h.entryPoint = bestID
	h.hasEntryPoint = true
	h.maxLevel = bestLevel
}

func (h *HNSWIndex) searchLayerSingle(query []float32, entryID uint32, level int) uint32 {
	current := entryID
	currentDist := float32(1.0) - vector.DotProductSIMD(query, h.vectorAtLocked(current))

	for {
		changed := false
		neighbors, ok := h.neighborsAtLevelLocked(current, level)
		if !ok {
			break
		}

		// Reverse iteration: order doesn't matter when finding closest neighbor
		for i := len(neighbors) - 1; i >= 0; i-- {
			neighborID := neighbors[i]
			if int(neighborID) >= len(h.nodeLevel) {
				continue
			}
			dist := float32(1.0) - vector.DotProductSIMD(query, h.vectorAtLocked(neighborID))
			if dist < currentDist {
				current = neighborID
				currentDist = dist
				changed = true
			}
		}

		if !changed {
			break
		}
	}

	return current
}

func (h *HNSWIndex) searchLayer(query []float32, entryID uint32, ef int, level int) []uint32 {
	if ef <= 0 {
		return nil
	}
	return h.searchLayerHeap(query, entryID, ef, level)
}

func (h *HNSWIndex) searchLayerHeap(query []float32, entryID uint32, ef int, level int) []uint32 {
	visited := h.visitedPool.Get().(*visitedGenState)
	defer h.visitedPool.Put(visited)
	if len(visited.gen) < len(h.nodeLevel) {
		oldLen := len(visited.gen)
		if cap(visited.gen) < len(h.nodeLevel) {
			next := make([]uint16, len(h.nodeLevel))
			copy(next, visited.gen)
			visited.gen = next
		} else {
			visited.gen = visited.gen[:len(h.nodeLevel)]
			clear(visited.gen[oldLen:])
		}
	}
	visited.cur++
	if visited.cur == 0 {
		clear(visited.gen)
		visited.cur = 1
	}
	curGen := visited.cur
	visited.gen[entryID] = curGen

	candidates := h.heapPool.Get().(*distHeap)
	candidates.Reset(false, ef*2)
	defer h.heapPool.Put(candidates)

	results := h.heapPool.Get().(*distHeap)
	results.Reset(true, ef*2)
	defer h.heapPool.Put(results)

	entryDist := float32(1.0) - vector.DotProductSIMD(query, h.vectorAtLocked(entryID))
	candidates.Push(hnswDistItem{id: entryID, dist: entryDist})
	results.Push(hnswDistItem{id: entryID, dist: entryDist})

	for candidates.Len() > 0 {
		closest := candidates.Pop()

		if results.Len() >= ef {
			furthest := results.Peek()
			if closest.dist > furthest.dist {
				break
			}
		}

		nodeID := closest.id
		if int(nodeID) >= len(h.nodeLevel) || h.deleted[nodeID] {
			continue
		}
		neighbors, ok := h.neighborsAtLevelLocked(nodeID, level)
		if !ok {
			continue
		}

		// Reverse iteration: order doesn't matter when checking all neighbors
		for i := len(neighbors) - 1; i >= 0; i-- {
			neighborID := neighbors[i]
			if int(neighborID) >= len(h.nodeLevel) || h.deleted[neighborID] {
				continue
			}
			if visited.gen[neighborID] == curGen {
				continue
			}
			visited.gen[neighborID] = curGen

			dist := float32(1.0) - vector.DotProductSIMD(query, h.vectorAtLocked(neighborID))

			if results.Len() < ef || dist < results.Peek().dist {
				candidates.Push(hnswDistItem{id: neighborID, dist: dist})
				results.Push(hnswDistItem{id: neighborID, dist: dist})

				if results.Len() > ef {
					_ = results.Pop()
				}
			}
		}
	}

	resultList := make([]uint32, results.Len())
	for i := results.Len() - 1; i >= 0; i-- {
		item := results.Pop()
		resultList[i] = item.id
	}

	return resultList
}

func (h *HNSWIndex) searchLayerHeapPooled(query []float32, entryID uint32, ef int, level int) []uint32 {
	visited := h.visitedPool.Get().(*visitedGenState)
	defer h.visitedPool.Put(visited)
	if len(visited.gen) < len(h.nodeLevel) {
		oldLen := len(visited.gen)
		if cap(visited.gen) < len(h.nodeLevel) {
			next := make([]uint16, len(h.nodeLevel))
			copy(next, visited.gen)
			visited.gen = next
		} else {
			visited.gen = visited.gen[:len(h.nodeLevel)]
			clear(visited.gen[oldLen:])
		}
	}
	visited.cur++
	if visited.cur == 0 {
		clear(visited.gen)
		visited.cur = 1
	}
	curGen := visited.cur
	visited.gen[entryID] = curGen

	candidates := h.heapPool.Get().(*distHeap)
	candidates.Reset(false, ef*2)
	defer h.heapPool.Put(candidates)

	results := h.heapPool.Get().(*distHeap)
	results.Reset(true, ef*2)
	defer h.heapPool.Put(results)

	entryDist := float32(1.0) - vector.DotProductSIMD(query, h.vectorAtLocked(entryID))
	candidates.Push(hnswDistItem{id: entryID, dist: entryDist})
	results.Push(hnswDistItem{id: entryID, dist: entryDist})

	for candidates.Len() > 0 {
		closest := candidates.Pop()

		if results.Len() >= ef {
			furthest := results.Peek()
			if closest.dist > furthest.dist {
				break
			}
		}

		nodeID := closest.id
		if int(nodeID) >= len(h.nodeLevel) || h.deleted[nodeID] {
			continue
		}
		neighbors, ok := h.neighborsAtLevelLocked(nodeID, level)
		if !ok {
			continue
		}

		// Reverse iteration: order doesn't matter when checking all neighbors
		for i := len(neighbors) - 1; i >= 0; i-- {
			neighborID := neighbors[i]
			if int(neighborID) >= len(h.nodeLevel) || h.deleted[neighborID] {
				continue
			}
			if visited.gen[neighborID] == curGen {
				continue
			}
			visited.gen[neighborID] = curGen

			dist := float32(1.0) - vector.DotProductSIMD(query, h.vectorAtLocked(neighborID))

			if results.Len() < ef || dist < results.Peek().dist {
				candidates.Push(hnswDistItem{id: neighborID, dist: dist})
				results.Push(hnswDistItem{id: neighborID, dist: dist})

				if results.Len() > ef {
					_ = results.Pop()
				}
			}
		}
	}

	n := results.Len()
	bufAny := h.idsPool.Get()
	buf := bufAny.([]uint32)
	if cap(buf) < n {
		buf = make([]uint32, n)
	} else {
		buf = buf[:n]
	}
	for i := n - 1; i >= 0; i-- {
		item := results.Pop()
		buf[i] = item.id
	}
	return buf
}

func (h *HNSWIndex) selectNeighbors(query []float32, candidates []uint32, m int) []uint32 {
	if m <= 0 || len(candidates) == 0 {
		return nil
	}

	type distNode struct {
		id   uint32
		dist float32
	}
	dists := make([]distNode, 0, min(len(candidates), m*2))
	for _, cid := range candidates {
		if int(cid) >= len(h.nodeLevel) || h.deleted[cid] {
			continue
		}
		dists = append(dists, distNode{
			id:   cid,
			dist: float32(1.0) - vector.DotProductSIMD(query, h.vectorAtLocked(cid)),
		})
	}

	if len(dists) <= m {
		out := make([]uint32, len(dists))
		for i := range dists {
			out[i] = dists[i].id
		}
		return out
	}

	sort.Slice(dists, func(i, j int) bool {
		return dists[i].dist < dists[j].dist
	})

	result := make([]uint32, m)
	for i := 0; i < m; i++ {
		result[i] = dists[i].id
	}
	return result
}

func (h *HNSWIndex) randomLevel() int {
	r := rand.Float64()
	return int(-math.Log(r) * h.config.LevelMultiplier)
}

func (h *HNSWIndex) vectorAtLocked(internalID uint32) []float32 {
	if int(internalID) >= len(h.vecOff) {
		return nil
	}
	off := int(h.vecOff[internalID])
	if off < 0 || off+h.dimensions > len(h.vectors) {
		return nil
	}
	return h.vectors[off : off+h.dimensions]
}

func (h *HNSWIndex) neighborsAtLevelLocked(nodeID uint32, level int) ([]uint32, bool) {
	if int(nodeID) >= len(h.neighborsOff) || int(nodeID) >= len(h.neighborCountsOff) {
		return nil, false
	}
	if level < 0 || level > int(h.nodeLevel[nodeID]) {
		return nil, false
	}
	m := h.config.M
	if m <= 0 {
		return nil, false
	}

	neighborsBase := int(h.neighborsOff[nodeID]) + level*m
	countsBase := int(h.neighborCountsOff[nodeID]) + level
	if countsBase < 0 || countsBase >= len(h.neighborCountsArena) {
		return nil, false
	}
	cnt := int(h.neighborCountsArena[countsBase])
	if cnt == 0 {
		return nil, true
	}
	end := neighborsBase + cnt
	if neighborsBase < 0 || end > len(h.neighborsArena) {
		return nil, false
	}
	return h.neighborsArena[neighborsBase:end], true
}

func (h *HNSWIndex) setNeighborsAtLevelLocked(nodeID uint32, level int, neighbors []uint32) {
	if int(nodeID) >= len(h.neighborsOff) || int(nodeID) >= len(h.neighborCountsOff) {
		return
	}
	if level < 0 || level > int(h.nodeLevel[nodeID]) {
		return
	}

	m := h.config.M
	if m <= 0 {
		return
	}
	if len(neighbors) > m {
		neighbors = neighbors[:m]
	}

	neighborsBase := int(h.neighborsOff[nodeID]) + level*m
	countsBase := int(h.neighborCountsOff[nodeID]) + level
	if neighborsBase < 0 || neighborsBase+m > len(h.neighborsArena) {
		return
	}
	if countsBase < 0 || countsBase >= len(h.neighborCountsArena) {
		return
	}

	copy(h.neighborsArena[neighborsBase:neighborsBase+len(neighbors)], neighbors)
	h.neighborCountsArena[countsBase] = uint16(len(neighbors))
}

func (h *HNSWIndex) insertNeighborAtLevelLocked(neighborID uint32, level int, newNeighborID uint32) {
	if h.deleted[neighborID] {
		return
	}
	if level < 0 || level > int(h.nodeLevel[neighborID]) {
		return
	}

	m := h.config.M
	if m <= 0 {
		return
	}

	neighborsBase := int(h.neighborsOff[neighborID]) + level*m
	countsBase := int(h.neighborCountsOff[neighborID]) + level
	if neighborsBase < 0 || neighborsBase+m > len(h.neighborsArena) {
		return
	}
	if countsBase < 0 || countsBase >= len(h.neighborCountsArena) {
		return
	}

	cnt := int(h.neighborCountsArena[countsBase])
	if cnt < m {
		h.neighborsArena[neighborsBase+cnt] = newNeighborID
		h.neighborCountsArena[countsBase] = uint16(cnt + 1)
		return
	}

	// Full: select best M among existing + new.
	all := make([]uint32, 0, m+1)
	all = append(all, h.neighborsArena[neighborsBase:neighborsBase+m]...)
	all = append(all, newNeighborID)
	best := h.selectNeighbors(h.vectorAtLocked(neighborID), all, m)
	copy(h.neighborsArena[neighborsBase:neighborsBase+m], best)
	h.neighborCountsArena[countsBase] = uint16(min(len(best), m))
}

// Heap types for HNSW search
type hnswDistItem struct {
	id   uint32
	dist float32
}

type distHeap struct {
	max   bool
	items []hnswDistItem
}

func newDistHeap(max bool, capHint int) *distHeap {
	if capHint < 0 {
		capHint = 0
	}
	return &distHeap{
		max:   max,
		items: make([]hnswDistItem, 0, capHint),
	}
}

func (h *distHeap) Reset(max bool, capHint int) {
	h.max = max
	h.items = h.items[:0]
	if capHint > cap(h.items) {
		h.items = make([]hnswDistItem, 0, capHint)
	}
}

func (h *distHeap) Len() int { return len(h.items) }

func (h *distHeap) Peek() hnswDistItem {
	return h.items[0]
}

func (h *distHeap) Push(item hnswDistItem) {
	h.items = append(h.items, item)
	h.siftUp(len(h.items) - 1)
}

func (h *distHeap) Pop() hnswDistItem {
	n := len(h.items)
	out := h.items[0]
	last := h.items[n-1]
	h.items = h.items[:n-1]
	if len(h.items) > 0 {
		h.items[0] = last
		h.siftDown(0)
	}
	return out
}

func (h *distHeap) less(i, j int) bool {
	if h.max {
		return h.items[i].dist > h.items[j].dist
	}
	return h.items[i].dist < h.items[j].dist
}

func (h *distHeap) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			return
		}
		h.items[i], h.items[p] = h.items[p], h.items[i]
		i = p
	}
}

func (h *distHeap) siftDown(i int) {
	n := len(h.items)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		best := l
		r := l + 1
		if r < n && h.less(r, l) {
			best = r
		}
		if !h.less(best, i) {
			return
		}
		h.items[i], h.items[best] = h.items[best], h.items[i]
		i = best
	}
}
