package policy

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

var policyPath = filepath.Join("..", "..", "configs", "policy.yaml")

func loadPolicy(t *testing.T) *PolicyConfig {
	t.Helper()
	pc, err := Load(policyPath)
	if err != nil {
		t.Fatalf("Load(%s) failed: %v", policyPath, err)
	}
	return pc
}

func TestEngine_DeterministicBlocksRmRfRoot(t *testing.T) {
	pc := loadPolicy(t)
	c := NewCache(5 * time.Minute)
	eng := NewEngine(pc, nil, c)

	req := DecisionRequest{
		Tool: "terminal",
		Args: map[string]any{"command": "rm -rf /"},
		Context: RequestContext{
			Platform: "test",
			Agent:    "test",
		},
	}

	resp, err := eng.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != Block {
		t.Errorf("expected Block, got %s", resp.Decision)
	}
	if resp.Risk != 1.0 {
		t.Errorf("expected risk 1.0, got %f", resp.Risk)
	}
	if resp.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", resp.Confidence)
	}
	if resp.Policy.RuleID != "root-delete" {
		t.Errorf("expected rule root-delete, got %s", resp.Policy.RuleID)
	}
}

func TestEngine_DeterministicAllowsGitStatus(t *testing.T) {
	pc := loadPolicy(t)
	c := NewCache(5 * time.Minute)
	eng := NewEngine(pc, nil, c)

	req := DecisionRequest{
		Tool: "terminal",
		Args: map[string]any{"command": "git status"},
		Context: RequestContext{
			Platform: "test",
			Agent:    "test",
		},
	}

	resp, err := eng.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != Allow {
		t.Errorf("expected Allow, got %s", resp.Decision)
	}
	if resp.Risk != 0.0 {
		t.Errorf("expected risk 0.0, got %f", resp.Risk)
	}
	if resp.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", resp.Confidence)
	}
	if resp.Policy.RuleID != "allow-git-status" {
		t.Errorf("expected rule allow-git-status, got %s", resp.Policy.RuleID)
	}
}

func TestEngine_DeterministicRequiresApprovalForSudo(t *testing.T) {
	pc := loadPolicy(t)
	c := NewCache(5 * time.Minute)
	eng := NewEngine(pc, nil, c)

	req := DecisionRequest{
		Tool: "terminal",
		Args: map[string]any{"command": "sudo apt update"},
		Context: RequestContext{
			Platform: "test",
			Agent:    "test",
		},
	}

	resp, err := eng.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != ApprovalRequired {
		t.Errorf("expected ApprovalRequired, got %s", resp.Decision)
	}
	if resp.Policy.RuleID != "sudo-command" {
		t.Errorf("expected rule sudo-command, got %s", resp.Policy.RuleID)
	}
	if !resp.RequestApproval {
		t.Errorf("expected RequestApproval true")
	}
}

func TestEngine_FailClosedWhenNoJevClient(t *testing.T) {
	pc := loadPolicy(t)
	c := NewCache(5 * time.Minute)
	eng := NewEngine(pc, nil, c)

	// Command that doesn't match any deterministic rule
	req := DecisionRequest{
		Tool: "terminal",
		Args: map[string]any{"command": "some-unknown-command"},
		Context: RequestContext{
			Platform: "test",
			Agent:    "test",
		},
	}

	resp, err := eng.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != Block {
		t.Errorf("expected Block (fail-closed), got %s", resp.Decision)
	}
	if resp.Risk != 0.9 {
		t.Errorf("expected risk 0.9 for fail-closed, got %f", resp.Risk)
	}
	if resp.Confidence != 0.8 {
		t.Errorf("expected confidence 0.8 for fail-closed, got %f", resp.Confidence)
	}
}

func TestEngine_CacheHit(t *testing.T) {
	pc := loadPolicy(t)
	c := NewCache(5 * time.Minute)
	eng := NewEngine(pc, nil, c)

	req := DecisionRequest{
		Tool: "terminal",
		Args: map[string]any{"command": "git log --oneline"},
		Context: RequestContext{
			Platform: "test",
			Agent:    "test",
		},
	}

	// First call
	resp1, err := eng.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Second call should hit cache
	resp2, err := eng.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if resp1.Decision != resp2.Decision {
		t.Errorf("cache hit returned different decision: %s vs %s", resp1.Decision, resp2.Decision)
	}
	// Cached response preserves original RuleID
	if resp2.Policy.RuleID != resp1.Policy.RuleID {
		t.Errorf("cached response RuleID changed: %s vs %s", resp2.Policy.RuleID, resp1.Policy.RuleID)
	}
}

func TestNormalize_TerminalExtractsCommand(t *testing.T) {
	ta := Normalize("terminal", map[string]any{"command": "echo hello"})
	if ta.Operation != "execute" {
		t.Errorf("expected operation=execute, got %s", ta.Operation)
	}
	if ta.Command != "echo hello" {
		t.Errorf("expected command=echo hello, got %s", ta.Command)
	}
	if ta.Destructive {
		t.Errorf("expected Destructive=false for echo, got true")
	}
}

func TestNormalize_TerminalDetectsDestructive(t *testing.T) {
	ta := Normalize("terminal", map[string]any{"command": "rm -rf /"})
	if !ta.Destructive {
		t.Errorf("expected Destructive=true for rm -rf /")
	}
}

func TestNormalize_WriteFileDetectsSensitive(t *testing.T) {
	ta := Normalize("write_file", map[string]any{"path": "/home/user/.env"})
	if !ta.Sensitive {
		t.Errorf("expected Sensitive=true for .env file")
	}
	if ta.Path != "/home/user/.env" {
		t.Errorf("expected path=/home/user/.env, got %s", ta.Path)
	}
}

func TestNormalize_PatchExtractsPath(t *testing.T) {
	ta := Normalize("patch", map[string]any{"path": "config.yaml"})
	if ta.Operation != "patch" {
		t.Errorf("expected operation=patch, got %s", ta.Operation)
	}
	if ta.Path != "config.yaml" {
		t.Errorf("expected path=config.yaml, got %s", ta.Path)
	}
}

func TestNormalize_BrowserNavigatesNetwork(t *testing.T) {
	ta := Normalize("browser_navigate", map[string]any{"url": "https://example.com"})
	if ta.Operation != "navigate" {
		t.Errorf("expected operation=navigate, got %s", ta.Operation)
	}
	if !ta.Network {
		t.Errorf("expected Network=true for browser_navigate")
	}
	if ta.URL != "https://example.com" {
		t.Errorf("expected url=https://example.com, got %s", ta.URL)
	}
}

func TestLoad_PolicyFile(t *testing.T) {
	pc, err := Load(policyPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(pc.Rules) == 0 {
		t.Error("expected rules to be loaded")
	}
	// Check a few known rules exist
	found := false
	for _, r := range pc.Rules {
		if r.ID == "root-delete" {
			found = true
			if r.Action != "block" {
				t.Errorf("expected root-delete action=block, got %s", r.Action)
			}
			break
		}
	}
	if !found {
		t.Error("root-delete rule not found")
	}
}

func TestEngine_GetJevClient(t *testing.T) {
	pc := loadPolicy(t)
	c := NewCache(5 * time.Minute)

	engNoJev := NewEngine(pc, nil, c)
	if engNoJev.GetJevClient() != nil {
		t.Error("expected nil JevClient when none provided")
	}

	// With mock JevClient
	mockJev := &mockJevClient{}
	engWithJev := NewEngine(pc, mockJev, c)
	if engWithJev.GetJevClient() != mockJev {
		t.Error("expected JevClient to be set")
	}
}

type mockJevClient struct{}

func (m *mockJevClient) Evaluate(ctx context.Context, state map[string]any, questions map[string]Question) (map[string]Answer, error) {
	return map[string]Answer{
		"should_allow": {Type: "noul", Noul: float64Ptr(0.9), Confidence: 0.9},
		"risk_level":   {Type: "score", Score: float64Ptr(1.0), Confidence: 0.9},
	}, nil
}

func float64Ptr(f float64) *float64 {
	return &f
}
