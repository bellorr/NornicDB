// Package inference provides automatic relationship detection for NornicDB.
//
// This package implements multiple methods for detecting implicit relationships
// between nodes in the graph:
//   - Similarity-based: Nodes with similar embeddings are likely related
//   - Co-access patterns: Nodes accessed together frequently are likely related
//   - Temporal proximity: Nodes accessed in the same session are likely related
//   - Transitive inference: If A→B and B→C, then A→C (with confidence)
//
// Example Usage:
//
//	// Create inference engine
//	config := inference.DefaultConfig()
//	config.SimilarityThreshold = 0.85 // Higher threshold = more confidence
//	engine := inference.New(config)
//
//	// Hook up vector search
//	engine.SetSimilaritySearch(func(ctx context.Context, embedding []float32, k int) ([]inference.SimilarityResult, error) {
//		return vectorIndex.Search(ctx, embedding, k)
//	})
//
//	// When storing a new memory
//	node := createMemoryNode("Remember to buy milk")
//	suggestions, _ := engine.OnStore(ctx, node.ID, node.Embedding)
//
//	fmt.Printf("Found %d suggested relationships:\n", len(suggestions))
//	for _, sug := range suggestions {
//		fmt.Printf("  %s -> %s (%.2f confidence): %s\n",
//			sug.SourceID, sug.TargetID, sug.Confidence, sug.Reason)
//		
//		if sug.Confidence > 0.7 {
//			// High confidence - auto-create the edge
//			createEdge(sug.SourceID, sug.TargetID, sug.Type)
//		}
//	}
//
//	// When accessing a memory
//	suggestions = engine.OnAccess(ctx, "memory-123")
//	for _, sug := range suggestions {
//		if sug.Method == "co_access" {
//			fmt.Printf("Frequently accessed with: %s\n", sug.TargetID)
//		}
//	}
//
// How Each Method Works:
//
// 1. Similarity-Based Linking:
//    Uses vector embeddings to find semantically similar nodes.
//    Example: "Buy milk" and "Purchase dairy products" have similar embeddings.
//
// 2. Co-Access Patterns:
//    Tracks which nodes are accessed within a short time window.
//    Example: If you always access "Project Plan" and "Budget" together,
//    they're probably related.
//
// 3. Temporal Proximity:
//    Nodes accessed in the same session (within 30 minutes) are linked.
//    Example: All memories from a single conversation thread.
//
// 4. Transitive Inference:
//    If A relates to B and B relates to C, then A might relate to C.
//    Example: "Python" → "Programming" → "Computers" suggests "Python" → "Computers"
//
// ELI12 (Explain Like I'm 12):
//
// Imagine you're organizing your school notebooks:
//
// 1. **Similarity**: Your math and science notebooks go together because they're
//    both about numbers and formulas (similar content).
//
// 2. **Co-access**: Your English notebook and dictionary always get used together,
//    so they should be near each other on your shelf.
//
// 3. **Temporal**: All homework from Monday night was done at the same time,
//    so those papers are related.
//
// 4. **Transitive**: If Math relates to Science, and Science relates to Biology,
//    then Math probably relates to Biology too (they're all STEM subjects).
//
// The inference engine is like a smart librarian who notices these patterns
// and suggests: "Hey, these two things seem related - want me to connect them?"
package inference

import (
	"context"
	"sync"
	"time"
)

// EdgeSuggestion represents a suggested edge.
type EdgeSuggestion struct {
	SourceID   string
	TargetID   string
	Type       string
	Confidence float64
	Reason     string
	Method     string // similarity, co_access, temporal, transitive
}

// Config holds inference engine configuration options.
//
// All thresholds and parameters can be tuned based on your use case:
//   - Higher thresholds = fewer but more confident suggestions
//   - Lower thresholds = more suggestions but potentially noisier
//
// Example:
//
//	// Conservative: Only suggest very confident relationships
//	config := &inference.Config{
//		SimilarityThreshold: 0.90, // Very high bar
//		SimilarityTopK:      5,    // Only check top 5
//		CoAccessMinCount:    5,    // Need 5 co-accesses
//		TransitiveMinConf:   0.7,  // High confidence for transitive
//	}
//
//	// Aggressive: Suggest many potential relationships
//	config = &inference.Config{
//		SimilarityThreshold: 0.75, // Lower bar
//		SimilarityTopK:      20,   // Check top 20
//		CoAccessMinCount:    2,    // Just 2 co-accesses
//		TransitiveMinConf:   0.3,  // Lower confidence OK
//	}
type Config struct {
	// Similarity-based linking
	SimilarityThreshold float64 // Default: 0.82
	SimilarityTopK      int     // How many similar nodes to check

	// Co-access pattern detection
	CoAccessEnabled     bool
	CoAccessWindow      time.Duration // Time window for co-access
	CoAccessMinCount    int           // Minimum co-accesses to suggest edge

	// Temporal proximity
	TemporalEnabled     bool
	TemporalWindow      time.Duration // Window for "same session"

	// Transitive inference
	TransitiveEnabled   bool
	TransitiveMinConf   float64 // Minimum confidence for transitive edges
}

// DefaultConfig returns balanced default configuration suitable for most use cases.
//
// Defaults:
//   - SimilarityThreshold: 0.82 (fairly confident)
//   - SimilarityTopK: 10 (check 10 most similar)
//   - CoAccessWindow: 30 seconds
//   - CoAccessMinCount: 3 (need 3 co-accesses before suggesting)
//   - TemporalWindow: 30 minutes (same "session")
//   - TransitiveMinConf: 0.5 (moderate confidence)
//
// Example:
//
//	config := inference.DefaultConfig()
//	engine := inference.New(config)
//
//	// Or customize
//	config = inference.DefaultConfig()
//	config.SimilarityThreshold = 0.90 // Stricter
//	engine = inference.New(config)
func DefaultConfig() *Config {
	return &Config{
		SimilarityThreshold: 0.82,
		SimilarityTopK:      10,
		CoAccessEnabled:     true,
		CoAccessWindow:      30 * time.Second,
		CoAccessMinCount:    3,
		TemporalEnabled:     true,
		TemporalWindow:      30 * time.Minute,
		TransitiveEnabled:   true,
		TransitiveMinConf:   0.5,
	}
}

// Engine handles automatic relationship inference using multiple detection methods.
//
// The Engine is thread-safe and can be used concurrently. It maintains
// internal state for co-access tracking and temporal pattern detection.
//
// Lifecycle:
//  1. Create with New()
//  2. Configure similarity search with SetSimilaritySearch()
//  3. Call OnStore() when creating nodes
//  4. Call OnAccess() when accessing nodes
//  5. Periodically call SuggestTransitive() to find indirect relationships
//
// Example:
//
//	engine := inference.New(inference.DefaultConfig())
//
//	// Connect to vector index
//	engine.SetSimilaritySearch(vectorIndex.Search)
//
//	// Use in your storage layer
//	func StoreMemory(mem *Memory) error {
//		// Store the memory
//		if err := db.Store(mem); err != nil {
//			return err
//		}
//		
//		// Get relationship suggestions
//		suggestions, _ := engine.OnStore(ctx, mem.ID, mem.Embedding)
//		
//		// Auto-create high-confidence edges
//		for _, sug := range suggestions {
//			if sug.Confidence >= 0.7 {
//				db.CreateEdge(sug.SourceID, sug.TargetID, sug.Type, sug.Confidence)
//			}
//		}
//		
//		return nil
//	}
type Engine struct {
	config *Config
	mu     sync.RWMutex

	// Co-access tracking
	accessHistory []accessRecord
	coAccessCounts map[coAccessKey]int

	// For similarity lookups (injected dependency)
	similaritySearch func(ctx context.Context, embedding []float32, k int) ([]SimilarityResult, error)
}

type accessRecord struct {
	NodeID    string
	Timestamp time.Time
}

type coAccessKey struct {
	NodeA string
	NodeB string
}

// SimilarityResult from vector search.
type SimilarityResult struct {
	ID    string
	Score float64
}

// New creates a new inference Engine with the given configuration.
//
// If config is nil, DefaultConfig() is used.
//
// The engine starts with empty co-access tracking. Call SetSimilaritySearch()
// to enable similarity-based inference.
//
// Example:
//
//	// With defaults
//	engine := inference.New(nil)
//
//	// With custom config
//	config := &inference.Config{
//		SimilarityThreshold: 0.85,
//		SimilarityTopK:      15,
//		CoAccessEnabled:     true,
//	}
//	engine = inference.New(config)
//
// Returns a new Engine ready for use.
func New(config *Config) *Engine {
	if config == nil {
		config = DefaultConfig()
	}

	return &Engine{
		config:         config,
		accessHistory:  make([]accessRecord, 0),
		coAccessCounts: make(map[coAccessKey]int),
	}
}

// SetSimilaritySearch sets the similarity search function.
func (e *Engine) SetSimilaritySearch(fn func(ctx context.Context, embedding []float32, k int) ([]SimilarityResult, error)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.similaritySearch = fn
}

// OnStore is called when a new node is stored in the graph.
//
// This method analyzes the new node and suggests relationships based on
// vector similarity. High-confidence suggestions can be automatically
// created as edges.
//
// Parameters:
//   - ctx: Context for cancellation
//   - nodeID: ID of the newly created node
//   - embedding: Vector embedding of the node's content
//
// Returns:
//   - Slice of EdgeSuggestion with confidence scores and reasons
//   - Error if similarity search fails
//
// Example:
//
//	// User creates a new note
//	note := &Note{
//		ID:      "note-456",
//		Content: "Machine learning algorithms",
//		Embedding: embedder.Embed("Machine learning algorithms"),
//	}
//
//	// Get suggestions
//	suggestions, err := engine.OnStore(ctx, note.ID, note.Embedding)
//	if err != nil {
//		return err
//	}
//
//	fmt.Printf("Found %d related notes:\n", len(suggestions))
//	for _, sug := range suggestions {
//		relatedNote := getNote(sug.TargetID)
//		fmt.Printf("  - %s (%.0f%% confident): %s\n",
//			relatedNote.Title, sug.Confidence*100, sug.Reason)
//		
//		// Auto-link if very confident
//		if sug.Confidence >= 0.8 {
//			createEdge(sug)
//			log.Printf("Auto-linked: %s -> %s", note.ID, relatedNote.ID)
//		}
//	}
//
// Typical confidence levels:
//   - 0.9+: Very confident, safe to auto-create
//   - 0.7-0.9: Confident, suggest to user
//   - 0.5-0.7: Possible, show as "related"
//   - <0.5: Weak, ignore
func (e *Engine) OnStore(ctx context.Context, nodeID string, embedding []float32) ([]EdgeSuggestion, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	suggestions := make([]EdgeSuggestion, 0)

	// 1. Similarity-based suggestions
	if e.similaritySearch != nil && len(embedding) > 0 {
		similar, err := e.similaritySearch(ctx, embedding, e.config.SimilarityTopK)
		if err == nil {
			for _, result := range similar {
				if result.ID == nodeID {
					continue // Skip self
				}
				if result.Score >= e.config.SimilarityThreshold {
					conf := e.scoreToConfidence(result.Score)
					suggestions = append(suggestions, EdgeSuggestion{
						SourceID:   nodeID,
						TargetID:   result.ID,
						Type:       "RELATES_TO",
						Confidence: conf,
						Reason:     "High embedding similarity",
						Method:     "similarity",
					})
				}
			}
		}
	}

	return suggestions, nil
}

// OnAccess is called when a node is accessed (read).
//
// This method tracks co-access patterns - nodes that are accessed close together
// in time are likely related. After seeing the same pair accessed together
// multiple times, it suggests creating a relationship.
//
// Parameters:
//   - ctx: Context (currently unused, reserved for future)
//   - nodeID: ID of the accessed node
//
// Returns:
//   - Slice of EdgeSuggestion based on co-access patterns
//
// Example:
//
//	func GetMemory(id string) (*Memory, error) {
//		// Retrieve memory
//		mem, err := db.Get(id)
//		if err != nil {
//			return nil, err
//		}
//		
//		// Track access for inference
//		suggestions := engine.OnAccess(ctx, id)
//		
//		// Log co-access patterns
//		for _, sug := range suggestions {
//			if sug.Method == "co_access" {
//				log.Printf("Co-accessed with %s (%d times)",
//					sug.TargetID, sug.Confidence*10)
//				
//				// Create edge if frequently co-accessed
//				if sug.Confidence >= 0.6 {
//					createEdge(sug)
//				}
//			}
//		}
//		
//		return mem, nil
//	}
//
// How It Works:
//   The engine maintains a sliding window of recent accesses.
//   When you access node A, it checks what other nodes were accessed
//   in the last 30 seconds (configurable). If the same pair appears
//   multiple times, it suggests they're related.
//
// Use Case:
//   In a note-taking app, if you always view "Project Plan" and "Budget"
//   together, the engine suggests: "These seem related - want to link them?"
func (e *Engine) OnAccess(ctx context.Context, nodeID string) []EdgeSuggestion {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	suggestions := make([]EdgeSuggestion, 0)

	if !e.config.CoAccessEnabled {
		return suggestions
	}

	// Find recent accesses within the window
	windowStart := now.Add(-e.config.CoAccessWindow)
	recentNodes := make([]string, 0)
	
	for _, record := range e.accessHistory {
		if record.Timestamp.After(windowStart) && record.NodeID != nodeID {
			recentNodes = append(recentNodes, record.NodeID)
		}
	}

	// Update co-access counts
	for _, otherID := range recentNodes {
		key := e.makeCoAccessKey(nodeID, otherID)
		e.coAccessCounts[key]++

		// Check if we should suggest an edge
		if e.coAccessCounts[key] >= e.config.CoAccessMinCount {
			conf := float64(e.coAccessCounts[key]) / 10.0
			if conf > 0.8 {
				conf = 0.8 // Cap at 0.8 for co-access
			}
			suggestions = append(suggestions, EdgeSuggestion{
				SourceID:   nodeID,
				TargetID:   otherID,
				Type:       "RELATES_TO",
				Confidence: conf,
				Reason:     "Frequently accessed together",
				Method:     "co_access",
			})
		}
	}

	// Add to history
	e.accessHistory = append(e.accessHistory, accessRecord{
		NodeID:    nodeID,
		Timestamp: now,
	})

	// Prune old history
	e.pruneHistory(windowStart)

	return suggestions
}

// SuggestTransitive suggests edges based on transitive relationships.
// If A->B and B->C with sufficient confidence, suggest A->C.
func (e *Engine) SuggestTransitive(ctx context.Context, edges []ExistingEdge) []EdgeSuggestion {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.config.TransitiveEnabled {
		return nil
	}

	suggestions := make([]EdgeSuggestion, 0)
	
	// Build adjacency map
	outgoing := make(map[string][]ExistingEdge)
	for _, edge := range edges {
		outgoing[edge.SourceID] = append(outgoing[edge.SourceID], edge)
	}

	// For each A->B, look for B->C
	for _, ab := range edges {
		for _, bc := range outgoing[ab.TargetID] {
			if ab.SourceID == bc.TargetID {
				continue // Skip cycles back to origin
			}

			// Calculate transitive confidence
			conf := ab.Confidence * bc.Confidence
			if conf >= e.config.TransitiveMinConf {
				suggestions = append(suggestions, EdgeSuggestion{
					SourceID:   ab.SourceID,
					TargetID:   bc.TargetID,
					Type:       "RELATES_TO",
					Confidence: conf,
					Reason:     "Transitive via " + ab.TargetID,
					Method:     "transitive",
				})
			}
		}
	}

	return suggestions
}

// ExistingEdge represents an edge in the graph.
type ExistingEdge struct {
	SourceID   string
	TargetID   string
	Confidence float64
}

// scoreToConfidence converts similarity score to edge confidence.
func (e *Engine) scoreToConfidence(score float64) float64 {
	// Map similarity score ranges to confidence levels
	switch {
	case score >= 0.95:
		return 0.9
	case score >= 0.90:
		return 0.7
	case score >= 0.85:
		return 0.5
	default:
		return 0.3
	}
}

// makeCoAccessKey creates a consistent key for co-access tracking.
func (e *Engine) makeCoAccessKey(a, b string) coAccessKey {
	// Ensure consistent ordering
	if a < b {
		return coAccessKey{NodeA: a, NodeB: b}
	}
	return coAccessKey{NodeA: b, NodeB: a}
}

// pruneHistory removes old access records.
func (e *Engine) pruneHistory(before time.Time) {
	// Keep records newer than 'before'
	newHistory := make([]accessRecord, 0, len(e.accessHistory))
	for _, record := range e.accessHistory {
		if record.Timestamp.After(before) {
			newHistory = append(newHistory, record)
		}
	}
	e.accessHistory = newHistory
}

// Stats returns inference statistics.
type Stats struct {
	TotalSuggestions   int64
	BySimilarity       int64
	ByCoAccess         int64
	ByTransitive       int64
	TrackedCoAccesses  int
}

// GetStats returns current inference statistics.
func (e *Engine) GetStats() Stats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return Stats{
		TrackedCoAccesses: len(e.coAccessCounts),
	}
}
