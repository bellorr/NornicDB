//go:build cgo && (darwin || linux)

// Package localllm provides CGO bindings to llama.cpp for local GGUF model inference.
//
// This package enables NornicDB to run embedding models directly without external
// services like Ollama. It uses llama.cpp compiled as a static library with
// GPU acceleration (CUDA on Linux/Windows, Metal on macOS) and CPU fallback.
//
// Features:
//   - GPU-first with automatic CPU fallback
//   - Memory-mapped model loading for low memory footprint
//   - Thread-safe embedding generation
//   - Batch embedding support
//
// Example:
//
//	opts := localllm.DefaultOptions("/models/bge-m3.gguf")
//	model, err := localllm.LoadModel(opts)
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer model.Close()
//
//	embedding, err := model.Embed(ctx, "hello world")
//	// embedding is a normalized []float32
package localllm

/*
#cgo CFLAGS: -I${SRCDIR}/../../lib/llama

// Linux with CUDA (GPU primary)
#cgo linux,amd64,cuda LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_linux_amd64_cuda -lcudart -lcublas -lm -lstdc++ -lpthread
// Linux CPU fallback
#cgo linux,amd64,!cuda LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_linux_amd64 -lm -lstdc++ -lpthread

#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_linux_arm64 -lm -lstdc++ -lpthread

// macOS with Metal (GPU primary on Apple Silicon)
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_darwin_arm64 -lm -lc++ -framework Accelerate -framework Metal -framework MetalPerformanceShaders
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_darwin_amd64 -lm -lc++ -framework Accelerate

// Windows with CUDA
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_windows_amd64 -lcudart -lcublas -lm -lstdc++

#include <stdlib.h>
#include <string.h>
#include "llama.h"

// Initialize backend once (handles GPU detection)
static int initialized = 0;
void init_backend() {
    if (!initialized) {
        llama_backend_init();
        initialized = 1;
    }
}

// Load model with mmap for low memory usage
struct llama_model* load_model(const char* path, int n_gpu_layers) {
    init_backend();
    struct llama_model_params params = llama_model_default_params();
    params.n_gpu_layers = n_gpu_layers;
    params.use_mmap = 1;
    return llama_model_load_from_file(path, params);
}

// Create embedding context (minimal memory)
struct llama_context* create_context(struct llama_model* model, int n_ctx, int n_batch, int n_threads) {
    struct llama_context_params params = llama_context_default_params();
    params.n_ctx = n_ctx;
    params.n_batch = n_batch;
    params.n_threads = n_threads;
    params.n_threads_batch = n_threads;
    params.embeddings = 1;
    params.pooling_type = LLAMA_POOLING_TYPE_MEAN;
    return llama_init_from_model(model, params);
}

// Tokenize using model's vocab
int tokenize(struct llama_model* model, const char* text, int text_len, int32_t* tokens, int max_tokens) {
    const struct llama_vocab* vocab = llama_model_get_vocab(model);
    return llama_tokenize(vocab, text, text_len, tokens, max_tokens, 1, 1);
}

// Generate embedding
int embed(struct llama_context* ctx, int32_t* tokens, int n_tokens, float* out, int n_embd) {
    llama_kv_cache_clear(ctx);

    struct llama_batch batch = llama_batch_init(n_tokens, 0, 1);
    for (int i = 0; i < n_tokens; i++) {
        batch.token[i] = tokens[i];
        batch.pos[i] = i;
        batch.n_seq_id[i] = 1;
        batch.seq_id[i][0] = 0;
        batch.logits[i] = 1;  // Need logits for embeddings
    }
    batch.n_tokens = n_tokens;

    if (llama_decode(ctx, batch) != 0) {
        llama_batch_free(batch);
        return -1;
    }

    float* embd = llama_get_embeddings_seq(ctx, 0);
    if (!embd) {
        llama_batch_free(batch);
        return -2;
    }

    memcpy(out, embd, n_embd * sizeof(float));
    llama_batch_free(batch);
    return 0;
}

int get_n_embd(struct llama_model* model) { return llama_model_n_embd(model); }
void free_ctx(struct llama_context* ctx) { if (ctx) llama_free(ctx); }
void free_model(struct llama_model* model) { if (model) llama_model_free(model); }
*/
import "C"

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"unsafe"
)

// Model wraps a GGUF model for embedding generation.
//
// Thread-safe: The Embed and EmbedBatch methods can be called concurrently,
// but operations are serialized internally via mutex to prevent race conditions
// with the underlying C context.
type Model struct {
	model *C.struct_llama_model
	ctx   *C.struct_llama_context
	dims  int
	mu    sync.Mutex
}

// Options configures model loading and inference.
//
// Fields:
//   - ModelPath: Path to .gguf model file
//   - ContextSize: Max context size for tokenization (default: 512)
//   - BatchSize: Batch size for processing (default: 512)
//   - Threads: CPU threads for inference (default: NumCPU/2, max 8)
//   - GPULayers: GPU layer offload (-1=auto, 0=CPU only, N=N layers)
type Options struct {
	ModelPath   string
	ContextSize int
	BatchSize   int
	Threads     int
	GPULayers   int
}

// DefaultOptions returns options optimized for embedding generation.
//
// GPU is enabled by default (-1 = auto-detect and use all layers).
// Set GPULayers to 0 to force CPU-only mode.
//
// Example:
//
//	opts := localllm.DefaultOptions("/models/bge-m3.gguf")
//	opts.GPULayers = 0 // Force CPU mode
//	model, err := localllm.LoadModel(opts)
func DefaultOptions(modelPath string) Options {
	threads := runtime.NumCPU() / 2
	if threads < 1 {
		threads = 1
	}
	if threads > 8 {
		threads = 8
	}
	return Options{
		ModelPath:   modelPath,
		ContextSize: 512,
		BatchSize:   512,
		Threads:     threads,
		GPULayers:   -1, // Auto: use GPU if available, fallback to CPU
	}
}

// LoadModel loads a GGUF model for embedding generation.
//
// The model is memory-mapped for low memory footprint. GPU layers are
// automatically offloaded based on Options.GPULayers:
//   - -1: Auto-detect GPU and offload all layers
//   - 0: CPU only (no GPU offload)
//   - N: Offload N layers to GPU
//
// Example:
//
//	opts := localllm.DefaultOptions("/models/bge-m3.gguf")
//	model, err := localllm.LoadModel(opts)
//	if err != nil {
//		log.Fatalf("Failed to load model: %v", err)
//	}
//	defer model.Close()
//
//	fmt.Printf("Model loaded: %d dimensions\n", model.Dimensions())
func LoadModel(opts Options) (*Model, error) {
	cPath := C.CString(opts.ModelPath)
	defer C.free(unsafe.Pointer(cPath))

	model := C.load_model(cPath, C.int(opts.GPULayers))
	if model == nil {
		return nil, fmt.Errorf("failed to load model: %s", opts.ModelPath)
	}

	ctx := C.create_context(model, C.int(opts.ContextSize), C.int(opts.BatchSize), C.int(opts.Threads))
	if ctx == nil {
		C.free_model(model)
		return nil, fmt.Errorf("failed to create context for: %s", opts.ModelPath)
	}

	return &Model{
		model: model,
		ctx:   ctx,
		dims:  int(C.get_n_embd(model)),
	}, nil
}

// Embed generates a normalized embedding vector for the given text.
//
// The returned vector is L2-normalized (unit length), suitable for
// cosine similarity calculations.
//
// Example:
//
//	vec, err := model.Embed(ctx, "graph database")
//	if err != nil {
//		return err
//	}
//	fmt.Printf("Embedding: %d dimensions\n", len(vec))
func (m *Model) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Tokenize
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	tokens := make([]C.int, 512)
	n := C.tokenize(m.model, cText, C.int(len(text)), &tokens[0], 512)
	if n < 0 {
		return nil, fmt.Errorf("tokenization failed for text of length %d", len(text))
	}

	// Generate embedding
	emb := make([]float32, m.dims)
	result := C.embed(m.ctx, (*C.int)(&tokens[0]), n, (*C.float)(&emb[0]), C.int(m.dims))
	if result != 0 {
		return nil, fmt.Errorf("embedding generation failed (code: %d)", result)
	}

	// Normalize to unit vector
	normalize(emb)
	return emb, nil
}

// EmbedBatch generates normalized embeddings for multiple texts.
//
// Each text is processed sequentially. For true parallel batch processing,
// consider creating multiple Model instances.
//
// Example:
//
//	texts := []string{"hello", "world", "test"}
//	vecs, err := model.EmbedBatch(ctx, texts)
//	if err != nil {
//		return err
//	}
//	for i, vec := range vecs {
//		fmt.Printf("Text %d: %d dims\n", i, len(vec))
//	}
func (m *Model) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, t := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		emb, err := m.Embed(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("text %d: %w", i, err)
		}
		results[i] = emb
	}
	return results, nil
}

// Dimensions returns the embedding vector size.
//
// This is determined by the model architecture:
//   - BGE-M3: 1024 dimensions
//   - E5-large: 1024 dimensions
//   - Jina-v2-base-code: 768 dimensions
func (m *Model) Dimensions() int { return m.dims }

// Close releases all resources associated with the model.
//
// After Close is called, the Model must not be used.
func (m *Model) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	C.free_ctx(m.ctx)
	C.free_model(m.model)
	m.ctx = nil
	m.model = nil
	return nil
}

// normalize applies L2 normalization to a vector in-place.
func normalize(v []float32) {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return
	}
	norm := float32(1.0 / math.Sqrt(float64(sum)))
	for i := range v {
		v[i] *= norm
	}
}
