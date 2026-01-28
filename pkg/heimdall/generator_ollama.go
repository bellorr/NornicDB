// Package heimdall - Ollama-backed Generator for chat completions.
// When Heimdall provider is "ollama", NewManager uses this implementation.
package heimdall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultOllamaAPIURL = "http://localhost:11434"
const ollamaChatPath = "/api/chat"

// ollamaGenerator implements Generator by calling Ollama's /api/chat API.
type ollamaGenerator struct {
	apiURL string
	model  string
	client *http.Client
}

// ollamaChatRequest is the request body for Ollama /api/chat.
type ollamaChatRequest struct {
	Model       string          `json:"model"`
	Messages    []ollamaMessage  `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	Options     *ollamaOptions   `json:"options,omitempty"`
	NumPredict  *int            `json:"num_predict,omitempty"`
	Temperature *float32        `json:"temperature,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float32 `json:"temperature,omitempty"`
}

// ollamaChatResponse is the non-streaming response.
type ollamaChatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// ollamaChatChunk is one line of NDJSON for streaming.
type ollamaChatChunk struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// newOllamaGenerator creates a Generator that uses the Ollama /api/chat API.
func newOllamaGenerator(cfg Config) (Generator, error) {
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = defaultOllamaAPIURL
	}
	apiURL = strings.TrimSuffix(apiURL, "/")
	model := cfg.Model
	if model == "" {
		model = "llama3.2"
	}
	return &ollamaGenerator{
		apiURL: apiURL,
		model:  model,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}, nil
}

// Generate implements Generator.
func (g *ollamaGenerator) Generate(ctx context.Context, prompt string, params GenerateParams) (string, error) {
	numPredict := params.MaxTokens
	if numPredict == 0 {
		numPredict = 1024
	}
	temp := params.Temperature

	reqBody := ollamaChatRequest{
		Model: g.model,
		Messages: []ollamaMessage{
			{Role: "user", Content: prompt},
		},
		Stream:      false,
		NumPredict:  &numPredict,
		Temperature: &temp,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}

	url := g.apiURL + ollamaChatPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("ollama decode: %w", err)
	}
	return chatResp.Message.Content, nil
}

// GenerateStream implements Generator.
func (g *ollamaGenerator) GenerateStream(ctx context.Context, prompt string, params GenerateParams, callback func(token string) error) error {
	numPredict := params.MaxTokens
	if numPredict == 0 {
		numPredict = 1024
	}
	temp := params.Temperature

	reqBody := ollamaChatRequest{
		Model: g.model,
		Messages: []ollamaMessage{
			{Role: "user", Content: prompt},
		},
		Stream:      true,
		NumPredict:  &numPredict,
		Temperature: &temp,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("ollama request: %w", err)
	}

	url := g.apiURL + ollamaChatPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk ollamaChatChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}
		if chunk.Message.Content != "" {
			if err := callback(chunk.Message.Content); err != nil {
				return err
			}
		}
		if chunk.Done {
			break
		}
	}
	return scanner.Err()
}

// Close implements Generator (no-op for HTTP client).
func (g *ollamaGenerator) Close() error {
	return nil
}

// ModelPath implements Generator (returns display name for logging).
func (g *ollamaGenerator) ModelPath() string {
	return "ollama:" + g.model
}
