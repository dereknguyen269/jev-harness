package harness

import (
	"context"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

// ttlIssuer captures the TTL it is asked to mint.
type ttlIssuer struct {
	ttl time.Duration
	n   int
}

func (f *ttlIssuer) Create(requestID, tool string, args map[string]any, risk float64, reason string, ttl time.Duration) string {
	f.ttl = ttl
	f.n++
	return "aid"
}

func timeoutHarness(issuer ApprovalIssuer) *Harness {
	return &Harness{
		Approvals:   issuer,
		Thresholds:  domain.DefaultThresholds(),
		ApprovalTTL: 30 * time.Second,
	}
}

func timeoutReq() domain.ToolRequest {
	return domain.ToolRequest{
		ID:    "req-t",
		Agent: domain.AgentInfo{Name: "test"},
		Tool:  domain.ToolCall{Name: "terminal", Args: map[string]any{"command": "x"}},
	}
}

func TestPerRuleTimeoutWins(t *testing.T) {
	iss := &ttlIssuer{}
	h := timeoutHarness(iss)
	h.Policy = stubPolicy{res: domain.PolicyResult{Matched: true, Final: true, Decision: domain.DecisionResult{
		Decision: domain.ApprovalRequired, ApprovalTimeoutSecs: 120,
	}}}
	out := h.Evaluate(context.Background(), timeoutReq())
	if iss.ttl != 120*time.Second || out.ExpiresIn != 120 || out.ApprovalID != "aid" {
		t.Fatalf("ttl=%v out=%+v", iss.ttl, out)
	}
}

func TestZeroHintUsesDefault(t *testing.T) {
	iss := &ttlIssuer{}
	h := timeoutHarness(iss)
	h.SetApprovalTTLSeconds(45)
	h.Policy = stubPolicy{res: domain.PolicyResult{Matched: true, Final: true, Decision: domain.DecisionResult{
		Decision: domain.ApprovalRequired,
	}}}
	out := h.Evaluate(context.Background(), timeoutReq())
	if iss.ttl != 45*time.Second || out.ExpiresIn != 45 {
		t.Fatalf("ttl=%v out=%+v", iss.ttl, out)
	}
}

func TestHintClampedToBounds(t *testing.T) {
	iss := &ttlIssuer{}
	h := timeoutHarness(iss)
	h.Policy = stubPolicy{res: domain.PolicyResult{Matched: true, Final: true, Decision: domain.DecisionResult{
		Decision: domain.ApprovalRequired, ApprovalTimeoutSecs: 99999,
	}}}
	h.Evaluate(context.Background(), timeoutReq())
	if iss.ttl != time.Duration(domain.MaxApprovalTTLSeconds)*time.Second {
		t.Fatalf("ttl=%v", iss.ttl)
	}
}

func TestSetApprovalTTLClamps(t *testing.T) {
	h := timeoutHarness(&ttlIssuer{})
	h.SetApprovalTTLSeconds(3)
	if got := h.ApprovalTTLSeconds(); got != domain.MinApprovalTTLSeconds {
		t.Fatalf("low=%d", got)
	}
	h.SetApprovalTTLSeconds(0)
	if got := h.ApprovalTTLSeconds(); got != domain.DefaultApprovalTTLSeconds {
		t.Fatalf("zero=%d", got)
	}
	h.SetApprovalTTLSeconds(60)
	if got := h.ApprovalTTLSeconds(); got != 60 {
		t.Fatalf("got=%d", got)
	}
}

type stubPolicy struct {
	res domain.PolicyResult
}

func (s stubPolicy) Evaluate(_ context.Context, _ domain.NormalizedRequest) domain.PolicyResult {
	return s.res
}
