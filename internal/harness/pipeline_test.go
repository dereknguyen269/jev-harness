package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/judge"
	"github.com/dereknguyen269/jev-harness/internal/policy"
)

func testLegacyEngine(t *testing.T) *policy.Engine {
	t.Helper()
	pc, err := policy.Load("../../configs/policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return policy.NewEngine(pc, nil, policy.NewCache(5*time.Minute))
}

func v2req(tool, command string) domain.ToolRequest {
	return domain.ToolRequest{
		ID:    "req-1",
		Agent: domain.AgentInfo{Name: "test"},
		Tool:  domain.ToolCall{Name: tool, Args: map[string]any{"command": command}},
	}
}

func TestPipeline_PolicyBlockWins(t *testing.T) {
	h := &Harness{Policy: policy.NewEngineV2(testLegacyEngine(t), nil)}
	res := h.Evaluate(context.Background(), v2req("terminal", "rm -rf /"))
	if res.Decision != domain.Block {
		t.Fatalf("expected block, got %s", res.Decision)
	}
	if res.Source != "policy" || res.PolicyID != "root-delete" {
		t.Fatalf("expected policy/root-delete, got %s/%s", res.Source, res.PolicyID)
	}
}

func TestPipeline_FailClosedNoJudge(t *testing.T) {
	h := &Harness{Policy: policy.NewEngineV2(testLegacyEngine(t), nil)}
	res := h.Evaluate(context.Background(), v2req("terminal", "some-unknown-command-xyz"))
	if res.Decision != domain.Block || res.ReasonCode != "JEV_UNAVAILABLE" {
		t.Fatalf("expected fail-closed block, got %+v", res)
	}
}

func TestPipeline_JevAllow(t *testing.T) {
	h := &Harness{
		Policy: policy.NewEngineV2(testLegacyEngine(t), nil),
		Judge:  &judge.Mock{Risk: 0.05, Confidence: 0.95, Action: "allow"},
	}
	res := h.Evaluate(context.Background(), v2req("terminal", "some-unknown-command-xyz"))
	if res.Decision != domain.Allow {
		t.Fatalf("expected allow, got %+v", res)
	}
}

func TestPipeline_JevErrorFailsClosed(t *testing.T) {
	h := &Harness{
		Policy: policy.NewEngineV2(testLegacyEngine(t), nil),
		Judge:  &judge.Mock{Err: errors.New("boom")},
	}
	res := h.Evaluate(context.Background(), v2req("terminal", "some-unknown-command-xyz"))
	if res.Decision != domain.Block || res.ReasonCode != "JEV_UNAVAILABLE" {
		t.Fatalf("expected fail-closed, got %+v", res)
	}
}

func TestResolver_LowConfidenceAsks(t *testing.T) {
	res := ResolveJev(judge.JudgeResult{Risk: 0.1, Confidence: 0.2, Action: "allow"}, nil)
	if res.Decision != domain.ApprovalRequired || res.ReasonCode != "LOW_CONFIDENCE" {
		t.Fatalf("expected LOW_CONFIDENCE ask, got %+v", res)
	}
}

func TestResolver_CriticalBlocks(t *testing.T) {
	res := ResolveJev(judge.JudgeResult{Risk: 0.99, Confidence: 0.99, Action: "approval_required"}, nil)
	if res.Decision != domain.Block {
		t.Fatalf("expected block, got %+v", res)
	}
}

func TestCanonicalMapping(t *testing.T) {
	h := &Harness{Policy: policy.NewEngineV2(testLegacyEngine(t), nil)}
	// Claude "Bash" and Codex "shell" must hit the terminal root-delete rule.
	for _, name := range []string{"Bash", "shell", "execute"} {
		res := h.Evaluate(context.Background(), v2req(name, "rm -rf /"))
		if res.Decision != domain.Block {
			t.Fatalf("%s: expected block, got %+v", name, res)
		}
	}
}
