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
	calls     *CallLog
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
		calls:     NewCallLog(200),
	}
}

// Calls returns recent API calls, newest first, up to limit (<=0 means all).
func (c *Client) Calls(limit int) []Call {
	if c.calls == nil {
		return nil
	}
	return c.calls.List(limit)
}

// logCall records one outbound request. err == nil means status "ok".
func (c *Client) logCall(start time.Time, httpStatus int, err error, in, out int) {
	if c.calls == nil {
		return
	}
	status := "ok"
	msg := ""
	if err != nil {
		status = "error"
		msg = err.Error()
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
	}
	c.calls.Add(Call{
		Timestamp:    time.Now().UTC(),
		Model:        c.model,
		Endpoint:     c.baseURL,
		Status:       status,
		HTTPStatus:   httpStatus,
		LatencyMS:    time.Since(start).Milliseconds(),
		InputTokens:  in,
		OutputTokens: out,
		Error:        msg,
	})
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
	State     string         `json:"state"`
	Questions map[string]any `json:"questions"`
}

// stateToString encodes the structured tool state as a string because the
// /v1/systemone API expects "state" to be a string (sending the raw object
// yields "request invalid").
func stateToString(state map[string]any) string {
	if state == nil {
		return ""
	}
	b, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return string(b)
}

// questionPayload builds one question entry in the API shape:
// {"type": ..., "instructions": ...} with "criteria" omitted when empty.
// Types are passed through verbatim ("noul" stays "noul"). Score criteria
// must be a list of "level: description" strings (a map yields 422
// "Input should be a valid list"); other types pass criteria through as-is.
func questionPayload(q policy.Question) map[string]any {
	qData := map[string]any{
		"type":         q.Type,
		"instructions": q.Instructions,
	}
	if len(q.Criteria) == 0 {
		return qData
	}
	if q.Type == "score" {
		qData["criteria"] = scoreCriteriaToArray(q.Criteria)
		return qData
	}
	qData["criteria"] = q.Criteria
	return qData
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
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
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

func (c *Client) evaluateEval(ctx context.Context, state map[string]any, questions map[string]policy.Question) (result map[string]policy.Answer, err error) {
	start := time.Now()
	var in, out, httpStatus int
	defer func() { c.logCall(start, httpStatus, err, in, out) }()

	qs := make(map[string]any, len(questions))
	for k, q := range questions {
		qs[k] = questionPayload(q)
	}

	req := decisionRequest{
		Model:     c.model,
		State:     stateToString(state),
		Questions: qs,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal eval request: %w", err)
	}

	var respBody []byte
	switch c.provider {
	case "openrouter-eval":
		respBody, httpStatus, err = c.postOpenRouterEval(ctx, body)
	default:
		respBody, httpStatus, err = c.postToEndpoint(ctx, body)
	}
	if err != nil {
		return nil, err
	}

	var dr evalResponse
	if uerr := json.Unmarshal(respBody, &dr); uerr != nil {
		// Try openrouter format
		var orResp openrouterDecisionsResponse
		if err2 := json.Unmarshal(respBody, &orResp); err2 != nil {
			return nil, fmt.Errorf("decode eval response: %w (body: %.200s)", uerr, string(respBody))
		}
		in, out = orResp.Metadata.InputTokens, orResp.Metadata.OutputTokens
		result = make(map[string]policy.Answer)
		for k, ans := range orResp.Answers {
			result[k] = openrouterAnswerToPolicy(ans)
		}
		return result, nil
	}

	in, out = dr.Usage.InputTokens, dr.Usage.OutputTokens
	result = make(map[string]policy.Answer)
	for k, ans := range dr.Answers {
		result[k] = evalAnswerToPolicy(ans)
	}
	return result, nil
}

func (c *Client) postToEndpoint(ctx context.Context, body []byte) ([]byte, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("Jev HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, resp.StatusCode, fmt.Errorf("Jev returned %d: %s", resp.StatusCode, string(raw))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

func (c *Client) postOpenRouterEval(ctx context.Context, body []byte) ([]byte, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("OpenRouter HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, resp.StatusCode, fmt.Errorf("OpenRouter returned %d: %s", resp.StatusCode, string(raw))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

func (c *Client) evaluateDefault(ctx context.Context, state map[string]any, questions map[string]policy.Question) (result map[string]policy.Answer, err error) {
	start := time.Now()
	var in, out, httpStatus int
	defer func() { c.logCall(start, httpStatus, err, in, out) }()

	qs := make(map[string]any, len(questions))
	for k, q := range questions {
		qs[k] = questionPayload(q)
	}

	req := decisionRequest{
		Model:     c.model,
		State:     stateToString(state),
		Questions: qs,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal decision request: %w", err)
	}

	respBody, httpStatus, err := c.postToEndpoint(ctx, body)
	if err != nil {
		return nil, err
	}

	var dr decisionResponse
	if err := json.Unmarshal(respBody, &dr); err != nil {
		return nil, fmt.Errorf("decode Jev response: %w", err)
	}
	in, out = dr.Usage.InputTokens, dr.Usage.OutputTokens
	return dr.Answers, nil
}

func (c *Client) evaluateChat(ctx context.Context, state map[string]any, questions map[string]policy.Question) (result map[string]policy.Answer, err error) {
	start := time.Now()
	var in, out, httpStatus int
	defer func() { c.logCall(start, httpStatus, err, in, out) }()

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

	httpStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Jev returned %d: %s", resp.StatusCode, string(raw))
	}

	var chatResp chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	in, out = chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	return parseChatAnswers(chatResp.Choices[0].Message.Content, questions)
}

func evalAnswerToPolicy(ans evalAnswer) policy.Answer {
	policyAns := policy.Answer{Type: ans.Type}
	// The API returns noul answers as {"type":"noul","noul":<p>};
	// older shapes used "probability". Accept both.
	if ans.Noul != nil {
		policyAns.Noul = ans.Noul
	} else if ans.Probability != nil {
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
	policyAns.Confidence = ans.Confidence
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
