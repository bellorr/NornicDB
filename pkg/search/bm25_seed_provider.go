package search

import "github.com/orneryd/nornicdb/pkg/envutil"

type lexicalSeedProvider interface {
	LexicalSeedDocIDs(maxTerms, perTerm int) []string
}

// bm25SeedDocIDs returns lexical seed doc IDs using configured limits.
// This helper centralizes BM25 seed selection so multiple ANN builders
// (k-means, IVF/PQ, HNSW insertion order) can reuse identical seed inputs.
func bm25SeedDocIDs(fulltext lexicalSeedProvider) []string {
	if fulltext == nil {
		return nil
	}
	maxTerms := envutil.GetInt("NORNICDB_KMEANS_SEED_MAX_TERMS", 256)
	if maxTerms < 16 {
		maxTerms = 16
	}
	perTerm := envutil.GetInt("NORNICDB_KMEANS_SEED_DOCS_PER_TERM", 1)
	if perTerm < 1 {
		perTerm = 1
	}
	return fulltext.LexicalSeedDocIDs(maxTerms, perTerm)
}
