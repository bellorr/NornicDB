package search

import (
	"sync"
	"time"
)

const (
	ivfpqBundleFormatVersion = 1
)

// IVFPQProfile is the concrete runtime profile used to build/query compressed ANN.
type IVFPQProfile struct {
	Dimensions          int
	IVFLists            int
	PQSegments          int
	PQBits              int
	NProbe              int
	RerankTopK          int
	TrainingSampleMax   int
	KMeansMaxIterations int
}

// IVFPQBuildStats captures build observability for acceptance gates.
type IVFPQBuildStats struct {
	VectorCount         int
	TrainingSampleCount int
	ListCount           int
	AvgListSize         float64
	MaxListSize         int
	BytesPerVector      float64
	BuildDuration       time.Duration
}

type ivfpqCodebook struct {
	SubDim   int
	Codeword [][]float32
}

type ivfpqList struct {
	IDs   []string
	Codes [][]byte
}

// IVFPQIndex stores a compressed IVF/PQ ANN structure.
type IVFPQIndex struct {
	profile         IVFPQProfile
	centroids       [][]float32
	centroidNorm    [][]float32
	codebooks       []ivfpqCodebook
	lists           []ivfpqList
	formatVersion   int
	builtAtUnixNano int64
	scratchPool     sync.Pool
}

type ivfpqScratch struct {
	lut      [][]float32
	heapData []Candidate
}

func (i *IVFPQIndex) Profile() IVFPQProfile {
	if i == nil {
		return IVFPQProfile{}
	}
	return i.profile
}

func (i *IVFPQIndex) Count() int {
	if i == nil {
		return 0
	}
	total := 0
	for idx := range i.lists {
		total += len(i.lists[idx].IDs)
	}
	return total
}

func (i *IVFPQIndex) compatibleProfile(want IVFPQProfile) bool {
	if i == nil {
		return false
	}
	have := i.profile
	return have.Dimensions == want.Dimensions &&
		have.IVFLists == want.IVFLists &&
		have.PQSegments == want.PQSegments &&
		have.PQBits == want.PQBits
}
