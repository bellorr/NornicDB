//go:build !darwin || !cgo || nometal

package search

// batchScoreCandidatesMetal is a stub that always falls back to CPU.
func (h *HNSWIndex) batchScoreCandidatesMetal(normalizedQuery []float32, candidateIDs []uint32, minSim32 float32) ([]ANNResult, error) {
	return h.batchScoreCandidatesCPU(normalizedQuery, candidateIDs, minSim32)
}

