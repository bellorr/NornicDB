// Package embed provides embedding generation clients for vector search.
//
// This package supports multiple embedding providers:
//   - Ollama: Local open-source models (mxbai-embed-large, nomic-embed-text)
//   - OpenAI: Cloud API (text-embedding-3-small, text-embedding-3-large)
//
// Embeddings convert text into high-dimensional vectors that capture semantic meaning.
// Similar texts have similar vectors, enabling semantic search.
//
// Example Usage:
//
//	// Use local Ollama
//	config := embed.DefaultOllamaConfig()
//	embedder := embed.NewOllama(config)
//
//	embedding, err := embedder.Embed(ctx, "graph database")
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Printf("Embedding dimensions: %d\n", len(embedding))
//	// Output: 1024 (for mxbai-embed-large)
//
//	// Or use OpenAI
//	config := embed.DefaultOpenAIConfig("sk-...")
//	embedder := embed.NewOpenAI(config)
//
//	// Batch processing for efficiency
//	texts := []string{"memory", "storage", "database"}
//	embeddings, err := embedder.EmbedBatch(ctx, texts)
//
// ELI12 (Explain Like I'm 12):
//
// Embeddings are like a "smell" or "vibe" for text. Similar things have similar
// smells. "Cat" and "kitten" smell similar. "Cat" and "car" smell different.
//
// The computer represents each text as a list of 1024 numbers (a vector).
// When you search, it finds texts with similar number patterns (similar vibes).
//
// Think of it like this:
//   - "Happy" might be [0.8, 0.2, 0.1, ...] (lots of positive vibes)
//   - "Joyful" might be [0.7, 0.3, 0.1, ...] (similar vibes!)
//   - "Sad" might be [0.1, 0.1, 0.9, ...] (very different vibes)
//
// The search system measures how close these number lists are to find similar meanings!
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Embedder generates vector embeddings from text.
//
// Implementations must be safe for concurrent use from multiple goroutines.
//
// Example:
//
//	var embedder embed.Embedder
//	embedder = embed.NewOllama(nil) // Uses defaults
//
//	// Single embedding
//	vec, err := embedder.Embed(ctx, "hello world")
//
//	// Batch for efficiency
//	vecs, err := embedder.EmbedBatch(ctx, []string{"one", "two", "three"})
type Embedder interface {
	// Embed generates embedding for single text
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embeddings for multiple texts
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the embedding vector dimension
	Dimensions() int

	// Model returns the model name
	Model() string
}

// Config holds embedding provider configuration.
//
// Fields:
//   - Provider: "ollama" or "openai"
//   - APIURL: Base URL for API (e.g., http://localhost:11434)
//   - APIPath: Endpoint path (e.g., /api/embeddings)
//   - APIKey: Authentication key (OpenAI only)
//   - Model: Model name (e.g., mxbai-embed-large)
//   - Dimensions: Expected vector size for validation
//   - Timeout: HTTP request timeout
//
// Example:
//
//	// Custom Ollama config
//	config := &embed.Config{
//		Provider:   "ollama",
//		APIURL:     "http://192.168.1.100:11434",
//		Model:      "nomic-embed-text",
//		Dimensions: 768,
//		Timeout:    60 * time.Second,
//	}
type Config struct {
	Provider   string        // ollama, openai
	APIURL     string        // e.g., http://localhost:11434
	APIPath    string        // e.g., /api/embeddings or /v1/embeddings
	APIKey     string        // For OpenAI
	Model      string        // e.g., mxbai-embed-large
	Dimensions int           // Expected dimensions (for validation)
	Timeout    time.Duration // Request timeout
}

// DefaultOllamaConfig returns configuration for local Ollama with mxbai-embed-large.
//
// Default settings:
//   - Provider: ollama
//   - API URL: http://localhost:11434
//   - Model: mxbai-embed-large (1024 dimensions)
//   - Timeout: 30 seconds
//
// This assumes Ollama is running locally. To start Ollama:
//
//	$ ollama pull mxbai-embed-large
//	$ ollama serve
//
// Example:
//
//	config := embed.DefaultOllamaConfig()
//	embedder := embed.NewOllama(config)
//
//	embedding, err := embedder.Embed(ctx, "test")
//	if err != nil {
//		log.Fatal(err)
//	}
func DefaultOllamaConfig() *Config {
	return &Config{
		Provider:   "ollama",
		APIURL:     "http://localhost:11434",
		APIPath:    "/api/embeddings",
		Model:      "mxbai-embed-large",
		Dimensions: 1024,
		Timeout:    30 * time.Second,
	}
}

// DefaultOpenAIConfig returns configuration for OpenAI's text-embedding-3-small.
//
// Default settings:
//   - Provider: openai
//   - API URL: https://api.openai.com
//   - Model: text-embedding-3-small (1536 dimensions)
//   - Timeout: 30 seconds
//
// Requires an OpenAI API key. Get one at: https://platform.openai.com/api-keys
//
// Example:
//
//	apiKey := os.Getenv("OPENAI_API_KEY")
//	config := embed.DefaultOpenAIConfig(apiKey)
//	embedder := embed.NewOpenAI(config)
//
//	embedding, err := embedder.Embed(ctx, "test")
//	if err != nil {
//		log.Fatal(err)
//	}
//
// Cost:
//   - text-embedding-3-small: $0.02 per 1M tokens (~750k words)
//   - text-embedding-3-large: $0.13 per 1M tokens (3072 dimensions)
func DefaultOpenAIConfig(apiKey string) *Config {
	return &Config{
		Provider:   "openai",
		APIURL:     "https://api.openai.com",
		APIPath:    "/v1/embeddings",
		APIKey:     apiKey,
		Model:      "text-embedding-3-small",
		Dimensions: 1536,
		Timeout:    30 * time.Second,
	}
}

// OllamaEmbedder implements Embedder for local Ollama models.
//
// Ollama runs open-source embedding models locally:
//   - mxbai-embed-large: 1024 dimensions, excellent quality
//   - nomic-embed-text: 768 dimensions, faster
//   - all-minilm: 384 dimensions, very fast but lower quality
//
// Install models:
//
//	$ ollama pull mxbai-embed-large
//	$ ollama pull nomic-embed-text
//
// Thread-safe: Can be used concurrently from multiple goroutines.
//
// Example:
//
//	embedder := embed.NewOllama(nil) // Uses defaults
//
//	vec, err := embedder.Embed(ctx, "hello world")
//	if err != nil {
//		return err
//	}
//
//	fmt.Printf("Model: %s, Dimensions: %d\n",
//		embedder.Model(), embedder.Dimensions())
type OllamaEmbedder struct {
	config *Config
	client *http.Client
}

// NewOllama creates a new Ollama embedder.
//
// If config is nil, DefaultOllamaConfig() is used.
//
// Example:
//
//	// Use defaults (localhost:11434, mxbai-embed-large)
//	embedder := embed.NewOllama(nil)
//
//	// Custom config
//	config := &embed.Config{
//		Provider:   "ollama",
//		APIURL:     "http://192.168.1.100:11434",
//		Model:      "nomic-embed-text",
//		Dimensions: 768,
//	}
//	embedder = embed.NewOllama(config)
//
// Returns an embedder ready to generate embeddings.
func NewOllama(config *Config) *OllamaEmbedder {
	if config == nil {
		config = DefaultOllamaConfig()
	}

	return &OllamaEmbedder{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// ollamaRequest is the request format for Ollama.
type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ollamaResponse is the response format from Ollama.
type ollamaResponse struct {
	Embedding []float32 `json:"embedding"`
}

// Embed generates a vector embedding for a single text string.
//
// The embedding is a float32 slice of length specified by Dimensions().
// Empty or very short text may produce low-quality embeddings.
//
// Example:
//
//	embedder := embed.NewOllama(nil)
//
//	vec, err := embedder.Embed(ctx, "machine learning")
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	fmt.Printf("Generated %d-dimensional vector\n", len(vec))
//	// Output: Generated 1024-dimensional vector
//
// Returns the embedding vector, or an error if the API request fails.
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	req := ollamaRequest{
		Model:  e.config.Model,
		Prompt: text,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := e.config.APIURL + e.config.APIPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return ollamaResp.Embedding, nil
}

// EmbedBatch generates embeddings for multiple texts efficiently.
//
// For Ollama, this currently makes one request per text. Future versions
// may support true batch processing for better performance.
//
// Example:
//
//	embedder := embed.NewOllama(nil)
//
//	texts := []string{
//		"graph database",
//		"vector search",
//		"semantic similarity",
//	}
//
//	vecs, err := embedder.EmbedBatch(ctx, texts)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	for i, vec := range vecs {
//		fmt.Printf("Text %d: %d dimensions\n", i, len(vec))
//	}
//
// Returns a slice of embeddings (one per input text), or an error if any request fails.
func (e *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		embedding, err := e.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("failed to embed text %d: %w", i, err)
		}
		results[i] = embedding
	}
	return results, nil
}

// Dimensions returns the expected embedding dimensions.
func (e *OllamaEmbedder) Dimensions() int {
	return e.config.Dimensions
}

// Model returns the model name.
func (e *OllamaEmbedder) Model() string {
	return e.config.Model
}

// OpenAIEmbedder implements Embedder for OpenAI's embedding API.
//
// Supported models:
//   - text-embedding-3-small: 1536 dimensions, $0.02/1M tokens
//   - text-embedding-3-large: 3072 dimensions, $0.13/1M tokens
//   - text-embedding-ada-002: 1536 dimensions (legacy)
//
// Thread-safe: Can be used concurrently from multiple goroutines.
//
// Example:
//
//	apiKey := os.Getenv("OPENAI_API_KEY")
//	embedder := embed.NewOpenAI(embed.DefaultOpenAIConfig(apiKey))
//
//	vec, err := embedder.Embed(ctx, "hello world")
//	if err != nil {
//		return err
//	}
type OpenAIEmbedder struct {
	config *Config
	client *http.Client
}

// NewOpenAI creates a new OpenAI embedder.
//
// If config is nil, DefaultOpenAIConfig("") is used (will fail without API key).
//
// Example:
//
//	apiKey := os.Getenv("OPENAI_API_KEY")
//	config := embed.DefaultOpenAIConfig(apiKey)
//	embedder := embed.NewOpenAI(config)
//
//	// Or use custom model
//	config.Model = "text-embedding-3-large"
//	config.Dimensions = 3072
//	embedder = embed.NewOpenAI(config)
//
// Returns an embedder ready to generate embeddings.
func NewOpenAI(config *Config) *OpenAIEmbedder {
	if config == nil {
		config = DefaultOpenAIConfig("")
	}

	return &OpenAIEmbedder{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
	}
}

// openaiRequest is the request format for OpenAI.
type openaiRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// openaiResponse is the response format from OpenAI.
type openaiResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed generates a vector embedding for a single text string.
//
// Internally calls EmbedBatch with a single-element slice for API consistency.
//
// Example:
//
//	apiKey := os.Getenv("OPENAI_API_KEY")
//	embedder := embed.NewOpenAI(embed.DefaultOpenAIConfig(apiKey))
//
//	vec, err := embedder.Embed(ctx, "artificial intelligence")
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	fmt.Printf("Generated %d-dimensional OpenAI embedding\n", len(vec))
//	// Output: Generated 1536-dimensional OpenAI embedding
//
// Returns the embedding vector, or an error if the API request fails.
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

// EmbedBatch generates embeddings for multiple texts in a single API call.
//
// OpenAI's API supports true batch processing, making this more efficient
// than calling Embed() multiple times.
//
// Maximum batch size: 2048 texts per request.
//
// Example:
//
//	apiKey := os.Getenv("OPENAI_API_KEY")
//	embedder := embed.NewOpenAI(embed.DefaultOpenAIConfig(apiKey))
//
//	texts := []string{
//		"First document about machine learning",
//		"Second document about neural networks",
//		"Third document about deep learning",
//	}
//
//	vecs, err := embedder.EmbedBatch(ctx, texts)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	for i, vec := range vecs {
//		fmt.Printf("Document %d: %d dimensions\n", i+1, len(vec))
//	}
//
// Returns a slice of embeddings (one per input text), or an error if the API request fails.
func (e *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	req := openaiRequest{
		Model: e.config.Model,
		Input: texts,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := e.config.APIURL + e.config.APIPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+e.config.APIKey)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var openaiResp openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	results := make([][]float32, len(openaiResp.Data))
	for _, data := range openaiResp.Data {
		results[data.Index] = data.Embedding
	}

	return results, nil
}

// Dimensions returns the expected embedding dimensions.
func (e *OpenAIEmbedder) Dimensions() int {
	return e.config.Dimensions
}

// Model returns the model name.
func (e *OpenAIEmbedder) Model() string {
	return e.config.Model
}

// NewEmbedder creates an embedder based on the provider specified in config.
//
// Supported providers:
//   - "ollama": Local open-source models
//   - "openai": OpenAI cloud API
//
// This is a convenience function for dynamic provider selection.
//
// Example:
//
//	// Dynamic provider selection
//	provider := os.Getenv("EMBEDDING_PROVIDER") // "ollama" or "openai"
//
//	var config *embed.Config
//	if provider == "openai" {
//		apiKey := os.Getenv("OPENAI_API_KEY")
//		config = embed.DefaultOpenAIConfig(apiKey)
//	} else {
//		config = embed.DefaultOllamaConfig()
//	}
//
//	embedder, err := embed.NewEmbedder(config)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Use embedder regardless of provider
//	vec, err := embedder.Embed(ctx, "test")
//
// Returns an Embedder interface, or an error if the provider is unknown or
// configuration is invalid (e.g., OpenAI without API key).
func NewEmbedder(config *Config) (Embedder, error) {
	switch config.Provider {
	case "ollama":
		return NewOllama(config), nil
	case "openai":
		if config.APIKey == "" {
			return nil, fmt.Errorf("OpenAI requires an API key")
		}
		return NewOpenAI(config), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", config.Provider)
	}
}
