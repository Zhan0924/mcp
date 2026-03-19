package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// QueryTransformer defines the interface for query extension and rewriting
type QueryTransformer interface {
	Transform(ctx context.Context, query string) (string, error)
}

// HyDETransformer is an implementation of QueryTransformer using the HyDE approach.
// It generates a hypothetical answer to the query using an LLM to improve retrieval recall.
type HyDETransformer struct {
	baseURL     string
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
}

// NewHyDETransformer creates a new HyDETransformer from HyDEConfig.
func NewHyDETransformer(cfg HyDEConfig) *HyDETransformer {
	model := cfg.Model
	if model == "" {
		model = "gpt-3.5-turbo"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	} else {
		baseURL = strings.TrimSuffix(baseURL, "/")
		baseURL = strings.TrimSuffix(baseURL, "/embeddings")
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 256
	}
	temperature := cfg.Temperature
	if temperature <= 0 {
		temperature = 0.3
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &HyDETransformer{
		baseURL:     baseURL,
		apiKey:      cfg.APIKey,
		model:       model,
		maxTokens:   maxTokens,
		temperature: temperature,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func (t *HyDETransformer) Transform(ctx context.Context, query string) (string, error) {
	url := fmt.Sprintf("%s/chat/completions", t.baseURL)

	reqBody := map[string]interface{}{
		"model": t.model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful expert. Please write a short, hypothetical document or code snippet that answers the user's query. Only provide the hypothetical content, no intro or concluding remarks."},
			{"role": "user", "content": query},
		},
		"temperature": t.temperature,
		"max_tokens":  t.maxTokens,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return query, err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return query, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return query, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return query, err
	}

	if len(result.Choices) > 0 {
		// Use original query plus the hypothetical document for the best embedding recall
		return query + "\n" + strings.TrimSpace(result.Choices[0].Message.Content), nil
	}

	return query, nil
}
