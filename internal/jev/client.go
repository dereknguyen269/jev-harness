package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/policy"
)

const openRouterDecisionsURL = "https://api.typesafe.ai/v1/systemone"

type Client struct {
	apiKey  string
	model   string
	client  *http.Client
	baseURL string
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		model:   "jev-latest",
		baseURL: openRouterDecisionsURL,
		client:  &http.Client{Timeout: 500 * time.Millisecond},
	}
}

type decisionRequest struct {
	Model     string         `json:"model"`
	State     map[string]any `json:"state"`
	Questions map[string]any `json:"questions"`
}

type decisionResponse struct {
	Answers map[string]policy.Answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    float64            `json:"confidence"`
}

func (c *Client) Evaluate(ctx context.Context, state map[string]any, questions map[string]policy.Question) (map[string]policy.Answer, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("TYPESAFE_API_KEY not set")
	}

	qs := make(map[string]any, len(questions))
	for k, q := range questions {
		qs[k] = map[string]any{
			"type":         q.Type,
			"instructions": q.Instructions,
			"criteria":     q.Criteria,
		}
	}

	req := decisionRequest{
		Model:     c.model,
		State:     state,
		Questions: qs,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal decision request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("HTTP-Referer", "https://hermes-agent.nousresearch.com")
	httpReq.Header.Set("X-Title", "hermes-guard")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Jev HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Jev returned %d: %s", resp.StatusCode, string(raw))
	}

	var dr decisionResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, fmt.Errorf("decode Jev response: %w", err)
	}

	return dr.Answers, nil
}
