package llm

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

// ─── Request / Response types ────────────────────────────────────────────────

// GenerateRequest mirrors the Ollama /api/generate payload.
type GenerateRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options,omitempty"`
	System  string                 `json:"system,omitempty"`
}

// GenerateResponse is one SSE chunk from /api/generate.
type GenerateResponse struct {
	Model     string `json:"model"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
	CreatedAt string `json:"created_at"`
}

// ChatMessage mirrors Ollama's chat message object.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest mirrors the Ollama /api/chat payload.
type ChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ChatMessage          `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

// ChatResponse is one SSE chunk from /api/chat.
type ChatResponse struct {
	Model   string      `json:"model"`
	Message ChatMessage `json:"message"`
	Done    bool        `json:"done"`
}

// TagsResponse is returned by /api/tags (list local models).
type TagsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo contains metadata for one locally available Ollama model.
type ModelInfo struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
}

// ─── Client ──────────────────────────────────────────────────────────────────

// Client is a thread-safe Ollama REST client.
type Client struct {
	baseURL        string
	defaultModel   string
	fallbackModels []string
	httpClient     *http.Client
}

// NewClient creates a new Client. Call ProbeModels to verify connectivity.
func NewClient(baseURL, defaultModel string, fallbacks []string, timeout time.Duration) *Client {
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		defaultModel:   defaultModel,
		fallbackModels: fallbacks,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// ActiveModel returns the model name that will be used for generation.
func (c *Client) ActiveModel() string { return c.defaultModel }

// ProbeModels checks the Ollama server and picks an available model from the
// preferred list. Returns an error if no suitable model is found.
func (c *Client) ProbeModels(ctx context.Context) error {
	available, err := c.listModels(ctx)
	if err != nil {
		return fmt.Errorf("ollama probe: %w", err)
	}

	avMap := make(map[string]bool, len(available))
	for _, m := range available {
		// Strip tag suffix for fuzzy matching ("qwen2.5-coder:latest" → "qwen2.5-coder")
		name := strings.Split(m.Name, ":")[0]
		avMap[name] = true
		avMap[m.Name] = true
	}

	candidates := append([]string{c.defaultModel}, c.fallbackModels...)
	for _, candidate := range candidates {
		base := strings.Split(candidate, ":")[0]
		if avMap[candidate] || avMap[base] {
			c.defaultModel = candidate
			return nil
		}
	}

	if len(available) > 0 {
		// Use whatever is there as last resort
		c.defaultModel = available[0].Name
		return nil
	}

	return fmt.Errorf("no models available on Ollama server at %s", c.baseURL)
}

// GenerateStream calls /api/generate with stream=true.
// Tokens are delivered one-by-one to the provided callback until the model
// signals completion or ctx is cancelled. The function blocks until done.
func (c *Client) GenerateStream(ctx context.Context, req GenerateRequest, onToken func(token string)) error {
	req.Stream = true
	if req.Model == "" {
		req.Model = c.defaultModel
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal generate request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama generate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk GenerateResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue // tolerate malformed lines
		}
		if chunk.Response != "" {
			onToken(chunk.Response)
		}
		if chunk.Done {
			break
		}
	}
	return scanner.Err()
}

// GenerateSync calls /api/generate without streaming and returns the full text.
func (c *Client) GenerateSync(ctx context.Context, req GenerateRequest) (string, error) {
	req.Stream = false
	if req.Model == "" {
		req.Model = c.defaultModel
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama generate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, string(b))
	}

	var result GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.Response, nil
}

// listModels calls GET /api/tags.
func (c *Client) listModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var tags TagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	return tags.Models, nil
}
