// Package search provides file-backed vector storage for memory-efficient indexing.
//
// VectorFileStore implements append-only vector storage: vectors are written to a
// .vec file and only id→offset metadata is kept in RAM. This allows BuildIndexes
// to index large datasets without holding 2–3× vector data in memory. Vectors are
// stored normalized (one copy per id, cosine-only) per the indexing-memory plan.
package search

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/orneryd/nornicdb/pkg/math/vector"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	vecFileMagic   = "NVF\n"
	vecFileVersion = 1
	vecHeaderSize  = 64
)

var (
	errVecFileClosed = errors.New("vector file store is closed")
)

// VectorFileStore is an append-only vector store backed by a file.
// Only id→offset is kept in RAM; vector data lives on disk.
// All vectors are stored normalized (one copy per id).
type VectorFileStore struct {
	dimensions int
	vecPath    string
	metaPath   string

	mu                sync.RWMutex
	file              *os.File
	idToOff           map[string]int64
	buildIndexedCount int64 // last checkpoint count; persisted in .meta for resume
	closed            bool
}

// Has reports whether id is present in the id→offset map.
func (v *VectorFileStore) Has(id string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.idToOff[id]
	return ok
}

// VectorFileStoreMeta is persisted to the .meta file (msgpack).
type VectorFileStoreMeta struct {
	Version           int              `msgpack:"v"`
	Dimensions        int              `msgpack:"dim"`
	IDToOffset        map[string]int64 `msgpack:"id2off"`
	BuildIndexedCount int64            `msgpack:"build_count,omitempty"` // last checkpoint count during BuildIndexes; used for resume
}

// NewVectorFileStore creates a new file-backed store and opens the vector file for append.
// vecBasePath is the path prefix: .vec and .meta will be appended.
// If the .vec file exists it is opened for append; otherwise it is created with a header.
func NewVectorFileStore(vecBasePath string, dimensions int) (*VectorFileStore, error) {
	if dimensions <= 0 {
		return nil, fmt.Errorf("dimensions must be > 0, got %d", dimensions)
	}
	vecPath := vecBasePath + ".vec"
	metaPath := vecBasePath + ".meta"

	v := &VectorFileStore{
		dimensions: dimensions,
		vecPath:    vecPath,
		metaPath:   metaPath,
		idToOff:    make(map[string]int64),
	}

	// Open or create .vec file
	exists := false
	if _, err := os.Stat(vecPath); err == nil {
		exists = true
	}
	if err := os.MkdirAll(filepath.Dir(vecPath), 0755); err != nil {
		return nil, err
	}

	flags := os.O_RDWR | os.O_CREATE
	var err error
	v.file, err = os.OpenFile(vecPath, flags, 0644)
	if err != nil {
		return nil, err
	}

	if !exists {
		if err := v.writeHeader(); err != nil {
			v.file.Close()
			return nil, err
		}
	} else {
		// Verify header
		if err := v.readHeader(); err != nil {
			v.file.Close()
			return nil, err
		}
	}

	return v, nil
}

func (v *VectorFileStore) writeHeader() error {
	buf := make([]byte, vecHeaderSize)
	copy(buf, vecFileMagic)
	buf[4] = vecFileVersion
	binary.LittleEndian.PutUint32(buf[5:9], uint32(v.dimensions))
	_, err := v.file.Write(buf)
	return err
}

func (v *VectorFileStore) readHeader() error {
	buf := make([]byte, vecHeaderSize)
	_, err := io.ReadFull(v.file, buf)
	if err != nil {
		return err
	}
	if string(buf[:4]) != vecFileMagic {
		return fmt.Errorf("invalid vector file magic")
	}
	if buf[4] != vecFileVersion {
		return fmt.Errorf("unsupported vector file version %d", buf[4])
	}
	dim := int(binary.LittleEndian.Uint32(buf[5:9]))
	if dim != v.dimensions {
		return fmt.Errorf("vector file dimensions %d != store dimensions %d", dim, v.dimensions)
	}
	return nil
}

// Add appends a normalized vector to the store. vec is normalized in place/copied; only one copy is stored.
func (v *VectorFileStore) Add(id string, vec []float32) error {
	if len(vec) != v.dimensions {
		return ErrDimensionMismatch
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return errVecFileClosed
	}

	normalized := vector.Normalize(vec)
	idBytes := []byte(id)
	idLen := uint32(len(idBytes))
	if int(idLen) != len(idBytes) {
		return fmt.Errorf("id too long")
	}

	offset, err := v.file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	// Record: idLen (4), id (idLen), vector (dim*4)
	buf := make([]byte, 4+len(idBytes)+v.dimensions*4)
	binary.LittleEndian.PutUint32(buf[0:4], idLen)
	copy(buf[4:4+idLen], idBytes)
	for i := 0; i < v.dimensions; i++ {
		binary.LittleEndian.PutUint32(buf[4+int(idLen)+i*4:], math.Float32bits(normalized[i]))
	}
	if _, err := v.file.Write(buf); err != nil {
		return err
	}
	v.idToOff[id] = offset
	return nil
}

// GetVector returns a copy of the stored (normalized) vector for id, or (nil, false) if not found.
func (v *VectorFileStore) GetVector(id string) ([]float32, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	off, ok := v.idToOff[id]
	if !ok || v.closed || v.file == nil {
		return nil, false
	}
	// Read record at offset: idLen (4), id (idLen), vector (dim*4)
	buf := make([]byte, 4+v.dimensions*4+256)
	if _, err := v.file.ReadAt(buf[:4], off); err != nil {
		return nil, false
	}
	idLen := binary.LittleEndian.Uint32(buf[:4])
	recSize := 4 + int(idLen) + v.dimensions*4
	if recSize > len(buf) {
		buf = make([]byte, recSize)
	}
	if _, err := v.file.ReadAt(buf[:recSize], off); err != nil {
		return nil, false
	}
	vec := make([]float32, v.dimensions)
	for i := 0; i < v.dimensions; i++ {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4+int(idLen)+i*4:]))
	}
	return vec, true
}

// Count returns the number of vectors in the store.
func (v *VectorFileStore) Count() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.idToOff)
}

// GetDimensions returns the vector dimension.
func (v *VectorFileStore) GetDimensions() int {
	return v.dimensions
}

// IterateChunked reads the vector file in chunks and calls fn(ids, vecs) for each chunk.
// Used to build HNSW without loading all vectors into memory. fn may be called with
// fewer than chunkSize vectors on the last chunk.
func (v *VectorFileStore) IterateChunked(chunkSize int, fn func(ids []string, vecs [][]float32) error) error {
	if chunkSize <= 0 {
		chunkSize = 10000
	}
	v.mu.RLock()
	if v.closed {
		v.mu.RUnlock()
		return errVecFileClosed
	}
	file := v.file
	v.mu.RUnlock()
	if file == nil {
		return errVecFileClosed
	}

	// Seek to first record (after header)
	if _, err := file.Seek(vecHeaderSize, io.SeekStart); err != nil {
		return err
	}

	ids := make([]string, 0, chunkSize)
	vecs := make([][]float32, 0, chunkSize)
	buf := make([]byte, 4+256+v.dimensions*4)
	for {
		// Read id length
		if _, err := io.ReadFull(file, buf[:4]); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		idLen := binary.LittleEndian.Uint32(buf[:4])
		recLen := 4 + int(idLen) + v.dimensions*4
		if recLen > len(buf) {
			newBuf := make([]byte, recLen)
			binary.LittleEndian.PutUint32(newBuf[0:4], idLen)
			buf = newBuf
		}
		if _, err := io.ReadFull(file, buf[4:recLen]); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		id := string(buf[4 : 4+idLen])
		vec := make([]float32, v.dimensions)
		for i := 0; i < v.dimensions; i++ {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4+int(idLen)+i*4:]))
		}
		ids = append(ids, id)
		vecs = append(vecs, vec)
		if len(ids) >= chunkSize {
			if err := fn(ids, vecs); err != nil {
				return err
			}
			ids = ids[:0]
			vecs = vecs[:0]
		}
	}
	if len(ids) > 0 {
		return fn(ids, vecs)
	}
	return nil
}

// Save writes the id→offset map to the .meta file so the store can be loaded later.
// Copies idToOff under a short lock so the (potentially slow) encode doesn't block Add().
func (v *VectorFileStore) Save() error {
	v.mu.RLock()
	if v.closed {
		v.mu.RUnlock()
		return errVecFileClosed
	}
	dim := v.dimensions
	buildCount := v.buildIndexedCount
	idToOffCopy := make(map[string]int64, len(v.idToOff))
	for k, o := range v.idToOff {
		idToOffCopy[k] = o
	}
	v.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(v.metaPath), 0755); err != nil {
		return err
	}
	f, err := os.Create(v.metaPath)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := msgpack.NewEncoder(f)
	return enc.Encode(&VectorFileStoreMeta{
		Version:           vecFileVersion,
		Dimensions:        dim,
		IDToOffset:        idToOffCopy,
		BuildIndexedCount: buildCount,
	})
}

// Load populates the store from an existing .vec + .meta. The store must be created with
// NewVectorFileStore(vecBasePath, dimensions); Load then reads the .meta file to populate
// idToOff. The .vec file is already open from NewVectorFileStore.
func (v *VectorFileStore) Load() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return errVecFileClosed
	}
	f, err := os.Open(v.metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No meta; rebuild idToOff from .vec if present.
			return v.rebuildIndexFromVecLocked()
		}
		return err
	}
	defer f.Close()
	var meta VectorFileStoreMeta
	if err := msgpack.NewDecoder(f).Decode(&meta); err != nil {
		// Corrupt meta; rebuild idToOff from .vec.
		return v.rebuildIndexFromVecLocked()
	}
	if meta.Dimensions != v.dimensions {
		return fmt.Errorf("meta dimensions %d != store dimensions %d", meta.Dimensions, v.dimensions)
	}
	if meta.IDToOffset != nil {
		v.idToOff = meta.IDToOffset
	}
	if meta.BuildIndexedCount > 0 {
		v.buildIndexedCount = meta.BuildIndexedCount
	}
	// Rebuild id→offset from .vec so resume is accurate even if meta is stale.
	return v.rebuildIndexFromVecLocked()
}

// rebuildIndexFromVecLocked rebuilds id→offset by scanning the .vec file.
// Caller must hold v.mu.
func (v *VectorFileStore) rebuildIndexFromVecLocked() error {
	if v.closed || v.file == nil {
		return errVecFileClosed
	}
	if _, err := v.file.Seek(vecHeaderSize, io.SeekStart); err != nil {
		return err
	}
	idToOff := make(map[string]int64)
	buf := make([]byte, 4+256+v.dimensions*4)
	for {
		offset, err := v.file.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		if _, err := io.ReadFull(v.file, buf[:4]); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		idLen := binary.LittleEndian.Uint32(buf[:4])
		recLen := 4 + int(idLen) + v.dimensions*4
		if recLen > len(buf) {
			newBuf := make([]byte, recLen)
			binary.LittleEndian.PutUint32(newBuf[0:4], idLen)
			buf = newBuf
		}
		if _, err := io.ReadFull(v.file, buf[4:recLen]); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		id := string(buf[4 : 4+idLen])
		idToOff[id] = offset
	}
	v.idToOff = idToOff
	v.buildIndexedCount = int64(len(idToOff))
	return nil
}

// SetBuildIndexedCount sets the last checkpoint count from BuildIndexes (for resume).
// Call before Save() when persisting after a checkpoint so the next run can skip already-indexed nodes.
func (v *VectorFileStore) SetBuildIndexedCount(n int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.buildIndexedCount = n
}

// GetBuildIndexedCount returns the last persisted checkpoint count (0 if none).
// Used at start of BuildIndexes to skip the first N nodes when resuming.
func (v *VectorFileStore) GetBuildIndexedCount() int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.buildIndexedCount
}

// Sync flushes the .vec file to disk so progress is visible and durable.
func (v *VectorFileStore) Sync() error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.closed || v.file == nil {
		return nil
	}
	return v.file.Sync()
}

// Close closes the underlying file. The store must not be used after Close.
func (v *VectorFileStore) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	v.closed = true
	if v.file != nil {
		err := v.file.Close()
		v.file = nil
		return err
	}
	return nil
}
