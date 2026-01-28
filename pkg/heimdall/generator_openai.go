// Package heimdall - OpenAI-backed Generator for chat completions.
// When Heimdall provider is "openai", NewManager uses this implementation.
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

const defaultOpenAIBaseURL = "https://api.openai.com"
const defaultOpenAIModel = "gpt-4o-mini"
const openAIChatPath = "/v1/chat/completions"

// looksLikeLocalModel returns true if the model name is clearly a local/GGUF model
// (e.g. from config default or YAML) so we do not send it to the OpenAI API.
func looksLikeLocalModel(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return true
	}
	if strings.Contains(s, ".gguf") {
		return true
	}
	return false
}

// openAIGenerator implements Generator by calling OpenAI (or compatible) chat API.
// Supports both non-streaming (Generate) and streaming (GenerateStream) via stream: true and SSE.
type openAIGenerator struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// openAIChatRequest is the request body for OpenAI chat completions.
type openAIChatRequest struct {
	Model       string      `json:"model"`
	Messages    []openAIMsg `json:"messages"`
	Stream      bool        `json:"stream,omitempty"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	Temperature float32     `json:"temperature,omitempty"`
}

type openAIMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIChatResponse is the non-streaming response.
type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// openAIChatChunk is one SSE chunk for streaming.
type openAIChatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// newOpenAIGenerator creates a Generator that uses the OpenAI chat completions API.
func newOpenAIGenerator(cfg Config) (Generator, error) {
	baseURL := cfg.APIURL
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	model := strings.TrimSpace(cfg.Model)
	if looksLikeLocalModel(model) {
		model = defaultOpenAIModel
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai provider requires NORNICDB_HEIMDALL_API_KEY")
	}
	return &openAIGenerator{
		baseURL: baseURL,
		apiKey:  cfg.APIKey,
		model:   model,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}, nil
}

// Generate implements Generator.
func (g *openAIGenerator) Generate(ctx context.Context, prompt string, params GenerateParams) (string, error) {
	reqBody := openAIChatRequest{
		Model: g.model,
		Messages: []openAIMsg{
			{Role: "user", Content: prompt},
		},
		Stream:      false,
		MaxTokens:   params.MaxTokens,
		Temperature: params.Temperature,
	}
	if reqBody.MaxTokens == 0 {
		reqBody.MaxTokens = 1024
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}

	url := g.baseURL + openAIChatPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("openai decode: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices in response")
	}
	return chatResp.Choices[0].Message.Content, nil
}

// GenerateStream implements Generator.
func (g *openAIGenerator) GenerateStream(ctx context.Context, prompt string, params GenerateParams, callback func(token string) error) error {
	reqBody := openAIChatRequest{
		Model: g.model,
		Messages: []openAIMsg{
			{Role: "user", Content: prompt},
		},
		Stream:      true,
		MaxTokens:   params.MaxTokens,
		Temperature: params.Temperature,
	}
	if reqBody.MaxTokens == 0 {
		reqBody.MaxTokens = 1024
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("openai request: %w", err)
	}

	url := g.baseURL + openAIChatPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("openai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk openAIChatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			if err := callback(chunk.Choices[0].Delta.Content); err != nil {
				return err
			}
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
			break
		}
	}
	return scanner.Err()
}

// Close implements Generator (no-op for HTTP client).
func (g *openAIGenerator) Close() error {
	return nil
}

// ModelPath implements Generator (returns display name for logging).
func (g *openAIGenerator) ModelPath() string {
	return "openai:" + g.model
}
