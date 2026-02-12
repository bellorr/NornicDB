// Package search provides full-text indexing with BM25 scoring.
package search

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/vmihailenco/msgpack/v5"
)

// BM25 parameters (standard values)
const (
	bm25K1 = 1.2  // Term frequency saturation
	bm25B  = 0.75 // Length normalization
)

// FulltextIndex provides BM25-based full-text search.
// It indexes documents and supports keyword search with TF-IDF scoring.
type FulltextIndex struct {
	mu sync.RWMutex

	// Document storage: docID -> original text
	documents map[string]string

	// Inverted index: term -> docID -> term frequency
	invertedIndex map[string]map[string]int

	// Document lengths: docID -> word count
	docLengths map[string]int

	// Average document length (for BM25)
	avgDocLength float64

	// Total document count
	docCount int
}

// NewFulltextIndex creates a new full-text search index.
func NewFulltextIndex() *FulltextIndex {
	return &FulltextIndex{
		documents:     make(map[string]string),
		invertedIndex: make(map[string]map[string]int),
		docLengths:    make(map[string]int),
	}
}

// fulltextIndexFormatVersion is the semver written into saved index files (Qdrant-style).
// On load, if the file's version is not equal to this, the file is not loaded (caller rebuilds).
// If the file was written by a newer version, we log and skip so the user can upgrade.
const fulltextIndexFormatVersion = "1.0.0"

// fulltextIndexSnapshot is the serializable form of the BM25 index (no mutex).
type fulltextIndexSnapshot struct {
	Version       string
	Documents     map[string]string
	InvertedIndex map[string]map[string]int
	DocLengths    map[string]int
	AvgDocLength  float64
	DocCount      int
}

// Save writes the fulltext index to path (msgpack format). Dir is created if needed.
// Caller should not mutate the index concurrently.
func (f *FulltextIndex) Save(path string) error {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	snap := fulltextIndexSnapshot{
		Version:       fulltextIndexFormatVersion,
		Documents:     f.documents,
		InvertedIndex: f.invertedIndex,
		DocLengths:    f.docLengths,
		AvgDocLength:  f.avgDocLength,
		DocCount:      f.docCount,
	}
	return msgpack.NewEncoder(file).Encode(&snap)
}

// Load replaces the fulltext index with the one stored at path (msgpack format).
// If the file does not exist or decode fails, the index is left empty (or unchanged on
// missing file) and no error is returned, so the caller can proceed with a full rebuild.
// Returns an error only for unexpected I/O (e.g. permission denied).
func (f *FulltextIndex) Load(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // No saved index; caller will rebuild
		}
		return err
	}
	defer file.Close()

	var snap fulltextIndexSnapshot
	if err := msgpack.NewDecoder(file).Decode(&snap); err != nil {
		// Corrupt or wrong format; clear index so caller rebuilds
		f.mu.Lock()
		f.documents = make(map[string]string)
		f.invertedIndex = make(map[string]map[string]int)
		f.docLengths = make(map[string]int)
		f.avgDocLength = 0
		f.docCount = 0
		f.mu.Unlock()
		return nil
	}
	if !searchIndexVersionCompatible(snap.Version, fulltextIndexFormatVersion, "BM25") {
		f.mu.Lock()
		f.documents = make(map[string]string)
		f.invertedIndex = make(map[string]map[string]int)
		f.docLengths = make(map[string]int)
		f.avgDocLength = 0
		f.docCount = 0
		f.mu.Unlock()
		return nil
	}
	if snap.Documents == nil {
		snap.Documents = make(map[string]string)
	}
	if snap.InvertedIndex == nil {
		snap.InvertedIndex = make(map[string]map[string]int)
	}
	if snap.DocLengths == nil {
		snap.DocLengths = make(map[string]int)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.documents = snap.Documents
	f.invertedIndex = snap.InvertedIndex
	f.docLengths = snap.DocLengths
	f.avgDocLength = snap.AvgDocLength
	f.docCount = snap.DocCount
	return nil
}

// Index adds or updates a document in the index.
func (f *FulltextIndex) Index(id string, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Remove old entry if exists
	f.removeInternal(id)

	// Tokenize and normalize
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return
	}

	// Store document
	f.documents[id] = text
	f.docLengths[id] = len(tokens)
	f.docCount++

	// Update inverted index
	termFreq := make(map[string]int)
	for _, token := range tokens {
		termFreq[token]++
	}

	for term, freq := range termFreq {
		if f.invertedIndex[term] == nil {
			f.invertedIndex[term] = make(map[string]int)
		}
		f.invertedIndex[term][id] = freq
	}

	// Update average document length
	f.updateAvgDocLength()
}

// Remove removes a document from the index.
func (f *FulltextIndex) Remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeInternal(id)
}

// removeInternal removes a document without locking.
func (f *FulltextIndex) removeInternal(id string) {
	if _, exists := f.documents[id]; !exists {
		return
	}

	// Get the document's terms
	text := f.documents[id]
	tokens := tokenize(text)

	// Count term frequencies
	termFreq := make(map[string]int)
	for _, token := range tokens {
		termFreq[token]++
	}

	// Remove from inverted index
	for term := range termFreq {
		if docs, ok := f.invertedIndex[term]; ok {
			delete(docs, id)
			if len(docs) == 0 {
				delete(f.invertedIndex, term)
			}
		}
	}

	delete(f.documents, id)
	delete(f.docLengths, id)
	f.docCount--
	f.updateAvgDocLength()
}

// Search performs BM25 keyword search.
// Returns results sorted by BM25 score (highest first).
func (f *FulltextIndex) Search(query string, limit int) []indexResult {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.docCount == 0 {
		return nil
	}

	// Tokenize query
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil
	}

	// Calculate BM25 scores for all documents containing query terms
	scores := make(map[string]float64)

	for _, term := range queryTerms {
		// First try exact match
		docs, exists := f.invertedIndex[term]
		if exists {
			idf := f.calculateIDF(term)
			for docID, termFreq := range docs {
				docLen := float64(f.docLengths[docID])
				tf := float64(termFreq)
				numerator := tf * (bm25K1 + 1)
				denominator := tf + bm25K1*(1-bm25B+bm25B*(docLen/f.avgDocLength))
				scores[docID] += idf * (numerator / denominator)
			}
		}

		// Also try prefix matching for better search experience
		// (matches "searchable" to "searchablealice")
		for indexedTerm, termDocs := range f.invertedIndex {
			if indexedTerm != term && strings.HasPrefix(indexedTerm, term) {
				// Use reduced IDF for prefix matches (not as strong as exact)
				idf := f.calculateIDF(indexedTerm) * 0.8
				for docID, termFreq := range termDocs {
					docLen := float64(f.docLengths[docID])
					tf := float64(termFreq)
					numerator := tf * (bm25K1 + 1)
					denominator := tf + bm25K1*(1-bm25B+bm25B*(docLen/f.avgDocLength))
					scores[docID] += idf * (numerator / denominator)
				}
			}
		}
	}

	// Convert to sorted results
	type scored struct {
		id    string
		score float64
	}
	var results []scored
	for id, score := range scores {
		results = append(results, scored{id: id, score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	// Convert to indexResult
	output := make([]indexResult, len(results))
	for i, r := range results {
		output[i] = indexResult{ID: r.id, Score: r.score}
	}

	return output
}

// calculateIDF calculates the Inverse Document Frequency for a term.
// Uses the BM25 IDF formula: log((N - df + 0.5) / (df + 0.5) + 1)
// where N = total docs, df = docs containing term
// The +1 inside the log ensures non-negative IDF for common terms.
func (f *FulltextIndex) calculateIDF(term string) float64 {
	df := float64(len(f.invertedIndex[term]))
	n := float64(f.docCount)

	// BM25 IDF formula with +1 smoothing to prevent negative values
	// This is the Lucene/Elasticsearch variant that ensures non-negative IDF
	idf := math.Log(1 + (n-df+0.5)/(df+0.5))
	if idf < 0 {
		idf = 0 // Safety floor
	}
	return idf
}

// updateAvgDocLength recalculates average document length.
func (f *FulltextIndex) updateAvgDocLength() {
	if f.docCount == 0 {
		f.avgDocLength = 0
		return
	}

	var total int
	for _, length := range f.docLengths {
		total += length
	}
	f.avgDocLength = float64(total) / float64(f.docCount)
}

// Count returns the number of indexed documents.
func (f *FulltextIndex) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.docCount
}

// GetDocument retrieves the original text for a document.
func (f *FulltextIndex) GetDocument(id string) (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	text, exists := f.documents[id]
	return text, exists
}

// tokenize splits text into lowercase tokens.
// Removes punctuation and common stop words.
func tokenize(text string) []string {
	// Convert to lowercase
	text = strings.ToLower(text)

	// Split on non-alphanumeric characters
	words := strings.FieldsFunc(text, func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsDigit(c)
	})

	// Filter stop words and short tokens
	var tokens []string
	for _, word := range words {
		if len(word) < 2 {
			continue
		}
		if isStopWord(word) {
			continue
		}
		tokens = append(tokens, word)
	}

	return tokens
}

// isStopWord checks if a word is a common stop word.
// This is a minimal list focused on truly generic words.
// Technical terms like "learning", "query", etc. are deliberately NOT filtered.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "by": true, "for": true, "from": true,
	"has": true, "have": true, "he": true, "in": true, "is": true,
	"it": true, "its": true, "of": true, "on": true, "or": true,
	"that": true, "the": true, "to": true, "was": true, "were": true,
	"with": true, "this": true, "but": true, "they": true,
	"we": true, "you": true, "your": true, "my": true, "their": true,
	"been": true, "do": true, "does": true, "did": true,
}

func isStopWord(word string) bool {
	return stopWords[word]
}

// PhraseSearch searches for an exact phrase match.
// Returns documents containing the exact phrase.
func (f *FulltextIndex) PhraseSearch(phrase string, limit int) []indexResult {
	f.mu.RLock()
	defer f.mu.RUnlock()

	phrase = strings.ToLower(phrase)
	var results []indexResult

	for id, text := range f.documents {
		if strings.Contains(strings.ToLower(text), phrase) {
			// Score based on how early the phrase appears
			idx := strings.Index(strings.ToLower(text), phrase)
			score := 1.0 / (1.0 + float64(idx)/100.0)
			results = append(results, indexResult{ID: id, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results
}
