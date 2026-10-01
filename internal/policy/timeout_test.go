package policy

import (
	"context"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

func timeoutEng(t *testing.T) *Engine {
	t.Helper()
	pc := &PolicyConfig{Rules: []Rule{
		{ID: "slow-ask", Tool: "terminal", Pattern: "slow-probe", Action: "approval_required", ApprovalTimeout: 120},
		{ID: "plain-block", Tool: "terminal", Pattern: "plain-probe", Action: "block"},
	}}
	return NewEngine(pc, nil, NewCache(5*time.Minute))
}

func TestLegacyRuleTimeoutHint(t *testing.T) {
	e := timeoutEng(t)
	dr, ok := e.CheckWithContext(ToolAction{Tool: "terminal", Command: "run slow-probe now"}, RequestContext{})
	if !ok || dr.Decision != ApprovalRequired {
		t.Fatalf("got %+v ok=%v", dr, ok)
	}
	if dr.ApprovalTimeout != 120 {
		t.Fatalf("timeout=%d", dr.ApprovalTimeout)
	}
	dr2, ok := e.CheckWithContext(ToolAction{Tool: "terminal", Command: "run plain-probe now"}, RequestContext{})
	if !ok || dr2.ApprovalTimeout != 0 {
		t.Fatalf("non-timeout rule leaked hint: %+v", dr2)
	}
}

func v2TimeoutReq(cmd string) domain.ToolRequest {
	return domain.ToolRequest{
		ID:    "req-t",
		Agent: domain.AgentInfo{Name: "test"},
		Tool:  domain.ToolCall{Name: "terminal", Args: map[string]any{"command": cmd}},
	}
}

func TestV2TimeoutHintAndConversion(t *testing.T) {
	cfg := &V2Config{Policies: []V2Rule{
		{
			ID: "v2-slow", Description: "slow",
			Match:    V2Match{Tool: "terminal"},
			Decision: V2Decision{Action: "approval_required", Risk: 0.8, ReasonCode: "SLOW"},
		},
	}}
	cfg.Policies[0].Match.Command.Regex = "v2-slow-probe"
	cfg.Policies[0].Decision.ApprovalTimeout = 45
	e := NewEngineV2(timeoutEng(t), cfg)
	res := e.Evaluate(context.Background(), domain.NormalizedRequest{
		Request:   v2TimeoutReq("run v2-slow-probe now"),
		Canonical: domain.ToolTerminal,
		Command:   "run v2-slow-probe now",
	})
	if !res.Matched || res.Decision.ApprovalTimeoutSecs != 45 {
		t.Fatalf("got %+v", res)
	}
	if res.Decision.PolicyID != "v2-slow" {
		t.Fatalf("policy=%s", res.Decision.PolicyID)
	}
	rules := V2ToRules(cfg)
	if len(rules) != 1 || rules[0].ApprovalTimeout != 45 {
		t.Fatalf("conversion lost timeout: %+v", rules)
	}
}

func TestTimeoutBounds(t *testing.T) {
	if got := domain.ClampApprovalTTLSeconds(0); got != domain.DefaultApprovalTTLSeconds {
		t.Fatalf("zero=%d", got)
	}
	if got := domain.ClampApprovalTTLSeconds(3); got != domain.MinApprovalTTLSeconds {
		t.Fatalf("low=%d", got)
	}
	if got := domain.ClampApprovalTTLSeconds(99999); got != domain.MaxApprovalTTLSeconds {
		t.Fatalf("high=%d", got)
	}
	if got := domain.ResolveApprovalTTLSeconds(120, 30); got != 120 {
		t.Fatalf("rule=%d", got)
	}
	if got := domain.ResolveApprovalTTLSeconds(0, 45); got != 45 {
		t.Fatalf("default=%d", got)
	}
}
