package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/policy"
)

const defaultBaseURL = "https://api.typesafe.ai/v1/systemone"

type Client struct {
	apiKey    string
	model     string
	modelType string
	client    *http.Client
	baseURL   string
	provider  string
}

func NewClient(apiKey, baseURL, model, provider string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if model == "" {
		model = "jev-latest"
	}
	if provider == "" {
		switch baseURL {
		case "https://api.typesafe.ai/v1/systemone":
			provider = "typesafe"
		case "https://openrouter.ai/api/v1/chat/completions":
			provider = "openrouter"
		case "https://openrouter.ai/api/v1/decisions":
			provider = "openrouter-eval"
		case "https://ai-gateway.vercel.sh/v1/chat/completions", "https://ai-gateway.vercel.app/v1/chat/completions":
			provider = "vercel"
		case "https://ai-gateway.vercel.sh/v1/evaluate", "https://ai-gateway.vercel.app/v1/evaluate":
			provider = "vercel-eval"
		default:
			provider = "openrouter"
		}
	}
	modelType := detectModelType(model, provider)
	return &Client{
		apiKey:    apiKey,
		model:     model,
		modelType: modelType,
		baseURL:   baseURL,
		provider:  provider,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func detectModelType(model, provider string) string {
	if provider == "typesafe" || strings.HasPrefix(model, "typesafe-ai/") || strings.Contains(model, "jev") {
		return "evaluation"
	}
	if provider == "openrouter-eval" || provider == "vercel-eval" {
		return "evaluation"
	}
	return "chat"
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

type evalResponse struct {
	Answers map[string]evalAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type evalAnswer struct {
	Type          string             `json:"type"`
	Probability   *float64           `json:"probability,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Score         *float64           `json:"score,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type openrouterDecisionsResponse struct {
	Model    string                      `json:"model"`
	Answers  map[string]openrouterAnswer `json:"answers"`
	Metadata struct {
		Provider     string `json:"provider"`
		InputTokens  int    `json:"input_tokens"`
		OutputTokens int    `json:"output_tokens"`
	} `json:"metadata"`
}

type openrouterAnswer struct {
	Type          string             `json:"type"`
	Probability   *float64           `json:"probability,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
}

func (c *Client) Evaluate(ctx context.Context, state map[string]any, questions map[string]policy.Question) (map[string]policy.Answer, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("API key not set")
	}

	switch c.modelType {
	case "evaluation":
		return c.evaluateEval(ctx, state, questions)
	default:
		switch c.provider {
		case "vercel", "openrouter":
			return c.evaluateChat(ctx, state, questions)
		default:
			return c.evaluateDefault(ctx, state, questions)
		}
	}
}

func (c *Client) evaluateEval(ctx context.Context, state map[string]any, questions map[string]policy.Question) (map[string]policy.Answer, error) {
	qs := make(map[string]any, len(questions))
	for k, q := range questions {
		qData := map[string]any{
			"type":         normalizeEvalType(q.Type),
			"instructions": q.Instructions,
		}
		if q.Type == "score" && q.Criteria != nil {
			qData["criteria"] = scoreCriteriaToArray(q.Criteria)
		} else {
			qData["criteria"] = q.Criteria
		}
		qs[k] = qData
	}

	req := decisionRequest{
		Model:     c.model,
		State:     state,
		Questions: qs,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal eval request: %w", err)
	}

	var respBody []byte
	switch c.provider {
	case "openrouter-eval":
		respBody, err = c.postOpenRouterEval(ctx, body)
	default:
		respBody, err = c.postToEndpoint(ctx, body)
	}
	if err != nil {
		return nil, err
	}

	var dr evalResponse
	if err := json.Unmarshal(respBody, &dr); err != nil {
		// Try openrouter format
		var orResp openrouterDecisionsResponse
		if err2 := json.Unmarshal(respBody, &orResp); err2 != nil {
			return nil, fmt.Errorf("decode eval response: %w (body: %.200s)", err, string(respBody))
		}
		result := make(map[string]policy.Answer)
		for k, ans := range orResp.Answers {
			result[k] = openrouterAnswerToPolicy(ans)
		}
		return result, nil
	}

	result := make(map[string]policy.Answer)
	for k, ans := range dr.Answers {
		result[k] = evalAnswerToPolicy(ans)
	}
	return result, nil
}

func (c *Client) postToEndpoint(ctx context.Context, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Jev HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Jev returned %d: %s", resp.StatusCode, string(raw))
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) postOpenRouterEval(ctx context.Context, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenRouter returned %d: %s", resp.StatusCode, string(raw))
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) evaluateDefault(ctx context.Context, state map[string]any, questions map[string]policy.Question) (map[string]policy.Answer, error) {
	qs := make(map[string]any, len(questions))
	for k, q := range questions {
		qs[k] = map[string]any{
			"type":         normalizeEvalType(q.Type),
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

	respBody, err := c.postToEndpoint(ctx, body)
	if err != nil {
		return nil, err
	}

	var dr decisionResponse
	if err := json.Unmarshal(respBody, &dr); err != nil {
		return nil, fmt.Errorf("decode Jev response: %w", err)
	}
	return dr.Answers, nil
}

func (c *Client) evaluateChat(ctx context.Context, state map[string]any, questions map[string]policy.Question) (map[string]policy.Answer, error) {
	prompt := buildPrompt(state, questions)

	reqBody, err := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Jev HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Jev returned %d: %s", resp.StatusCode, string(raw))
	}

	var chatResp chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	return parseChatAnswers(chatResp.Choices[0].Message.Content, questions)
}

func normalizeEvalType(t string) string {
	switch t {
	case "noul":
		return "boolean"
	default:
		return t
	}
}

func scoreCriteriaToArray(criteria map[string]any) []string {
	keys := make([]string, 0, len(criteria))
	for k := range criteria {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, fmt.Sprintf("%s: %v", k, criteria[k]))
	}
	return result
}

func evalAnswerToPolicy(ans evalAnswer) policy.Answer {
	policyAns := policy.Answer{Type: ans.Type}
	if ans.Probability != nil {
		policyAns.Noul = ans.Probability
	}
	if ans.Choice != "" {
		policyAns.Choice = ans.Choice
	}
	if ans.Probabilities != nil {
		policyAns.Probabilities = ans.Probabilities
	}
	if ans.Score != nil {
		policyAns.Score = ans.Score
	}
	return policyAns
}

func openrouterAnswerToPolicy(ans openrouterAnswer) policy.Answer {
	policyAns := policy.Answer{Type: ans.Type}
	if ans.Probability != nil {
		policyAns.Noul = ans.Probability
	}
	if ans.Noul != nil {
		policyAns.Noul = ans.Noul
	}
	if ans.Choice != "" {
		policyAns.Choice = ans.Choice
	}
	if ans.Probabilities != nil {
		policyAns.Probabilities = ans.Probabilities
	}
	if ans.Score != nil {
		policyAns.Score = ans.Score
	}
	return policyAns
}

func buildPrompt(state map[string]any, questions map[string]policy.Question) string {
	var sb strings.Builder
	sb.WriteString("You are a security evaluation system. Answer in JSON format only.\n\n")
	sb.WriteString("State:\n")
	b, _ := json.MarshalIndent(state, "", "  ")
	sb.WriteString(string(b))
	sb.WriteString("\n\nQuestions:\n")
	for k, q := range questions {
		sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", k, q.Type, q.Instructions))
	}
	sb.WriteString("\nRespond with a JSON object where each key is the question name and the value contains 'type', 'confidence', and the appropriate answer field ('noul' for noul type, 'score' for score type).\n")
	return sb.String()
}

func parseChatAnswers(content string, questions map[string]policy.Question) (map[string]policy.Answer, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		var filtered []string
		inBlock := false
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock || !strings.HasPrefix(strings.TrimSpace(line), "```") {
				filtered = append(filtered, line)
			}
		}
		content = strings.Join(filtered, "\n")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, fmt.Errorf("parse response JSON: %w (body: %.200s)", err, content)
	}

	answers := make(map[string]policy.Answer)
	for k := range questions {
		data, ok := raw[k]
		if !ok {
			continue
		}
		var ans policy.Answer
		if err := json.Unmarshal(data, &ans); err != nil {
			continue
		}
		answers[k] = ans
	}

	if len(answers) == 0 {
		return nil, fmt.Errorf("no matching answers found in response")
	}
	return answers, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if c.provider == "typesafe" {
		req.Header.Set("HTTP-Referer", "https://hermes-agent.nousresearch.com")
		req.Header.Set("X-Title", "hermes-guard")
	}
	if c.provider == "openrouter" || c.provider == "openrouter-eval" {
		req.Header.Set("HTTP-Referer", "https://openrouter.ai")
		req.Header.Set("X-Title", "jev-guard")
	}
}
