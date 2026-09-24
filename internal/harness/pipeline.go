package harness

import (
	"context"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/judge"
	"github.com/dereknguyen269/jev-harness/internal/normalize"
)

// Cache is the decision cache (implemented in internal/cache).
type Cache interface {
	Get(key string) (domain.DecisionResult, bool)
	Set(key string, result domain.DecisionResult, ttl time.Duration)
}

// PolicyEngine evaluates normalized requests against deterministic rules.
type PolicyEngine interface {
	Evaluate(ctx context.Context, req domain.NormalizedRequest) domain.PolicyResult
}

// Auditor records evaluated decisions (implemented in internal/audit).
type Auditor interface {
	Record(event domain.AuditEvent)
}

// ApprovalIssuer creates async approvals for approval_required outcomes.
type ApprovalIssuer interface {
	Create(requestID, tool string, args map[string]any, risk float64, reason string, ttl time.Duration) string
}

// Harness is the V2 Agent Safety Gateway pipeline:
//
//	Normalize → Policy hard rules → Cache → Jev → fail-closed → ResolveJev → Audit
type Harness struct {
	Policy      PolicyEngine
	Judge       judge.Judge
	Cache       Cache
	Audit       Auditor
	Approvals   ApprovalIssuer
	Thresholds  domain.ConfidenceThresholds
	ApprovalTTL time.Duration
}

func (h *Harness) thresholds() domain.ConfidenceThresholds {
	if h.Thresholds != nil {
		return h.Thresholds
	}
	return domain.DefaultThresholds()
}

func (h *Harness) approvalTTL() time.Duration {
	if h.ApprovalTTL > 0 {
		return h.ApprovalTTL
	}
	return 30 * time.Second
}

// Evaluate runs the full pipeline. req.Tool.Name may be agent-specific;
// normalization maps it to canonical form first.
func (h *Harness) Evaluate(ctx context.Context, req domain.ToolRequest) domain.DecisionResult {
	start := time.Now()
	normalized := normalize.Normalize(req)

	// 1. Hard policy rules (final → return immediately).
	if h.Policy != nil {
		if result := h.Policy.Evaluate(ctx, normalized); result.Matched && result.Final {
			h.record(req, normalized, "", result.Decision, start)
			return h.maybeIssueApproval(req, result.Decision)
		}
	}

	// 2. Cache (reads + low-risk only; destructive never cached — enforced by Set).
	if h.Cache != nil {
		if cached, ok := h.Cache.Get(CacheKey(normalized)); ok {
			h.record(req, normalized, "cache", cached, start)
			return h.maybeIssueApproval(req, cached)
		}
	}

	// 3. Jev judgment.
	if h.Judge == nil {
		decision := domain.DecisionResult{
			Decision: domain.Block, Risk: 0.9, Confidence: 0.8,
			Source: "system", ReasonCode: "JEV_UNAVAILABLE",
			Reason: "no policy rule matched, no Jev judge configured — fail closed",
		}
		h.record(req, normalized, "", decision, start)
		return decision
	}
	judgeResult, err := h.Judge.Evaluate(ctx, judge.JudgeRequest{
		Tool:        normalized.Canonical,
		Arguments:   req.Tool.Args,
		Context:     req.Context,
		Command:     normalized.Command,
		Path:        normalized.Path,
		Destructive: normalized.Destructive,
		Network:     normalized.Network,
		Sensitive:   normalized.Sensitive,
	})

	// 4. Fail closed on Jev errors.
	if err != nil {
		decision := domain.DecisionResult{
			Decision: domain.Block, Risk: 0.9, Confidence: 0.8,
			Source: "system", ReasonCode: "JEV_UNAVAILABLE",
			Reason: "Jev evaluation failed: " + err.Error(),
		}
		h.record(req, normalized, "", decision, start)
		return decision
	}

	// 5. Translate Jev → policy decision.
	decision := ResolveJev(judgeResult, h.thresholds())
	decision.Source = "jev"
	h.record(req, normalized, judgeResult.Action, decision, start)

	// 6. Cache + approval issuance handled by caller helpers.
	if h.Cache != nil && shouldCache(decision) {
		h.Cache.Set(CacheKey(normalized), decision, ttlFor(decision))
	}
	return h.maybeIssueApproval(req, decision)
}

func (h *Harness) record(req domain.ToolRequest, normalized domain.NormalizedRequest, jevDecision string, decision domain.DecisionResult, start time.Time) {
	if h.Audit == nil {
		return
	}
	h.Audit.Record(domain.AuditEvent{
		ID:             req.ID,
		Timestamp:      time.Now(),
		Agent:          req.Agent.Name,
		Tool:           string(normalized.Canonical),
		PolicyDecision: decision.PolicyID,
		JevDecision:    jevDecision,
		FinalDecision:  string(decision.Decision),
		PolicyID:       decision.PolicyID,
		ReasonCode:     decision.ReasonCode,
		Risk:           decision.Risk,
		Confidence:     decision.Confidence,
		LatencyMS:      time.Since(start).Milliseconds(),
	})
}

func (h *Harness) maybeIssueApproval(req domain.ToolRequest, decision domain.DecisionResult) domain.DecisionResult {
	if decision.Decision != domain.ApprovalRequired || h.Approvals == nil {
		return decision
	}
	ttl := h.approvalTTL()
	decision.ApprovalID = h.Approvals.Create(req.ID, req.Tool.Name, req.Tool.Args, decision.Risk, decision.Reason, ttl)
	decision.ExpiresIn = int(ttl.Seconds())
	decision.RequestApproval = true
	return decision
}

// shouldCache mirrors spec §11: never cache mutations/critical, and never
// cache decisions needing human attention (each must mint a fresh approval).
func shouldCache(d domain.DecisionResult) bool {
	if d.Decision != domain.Allow {
		return false
	}
	level := domain.RiskFromScore(d.Risk)
	return level == domain.RiskRead || level == domain.RiskLow
}

func ttlFor(d domain.DecisionResult) time.Duration {
	if domain.RiskFromScore(d.Risk) == domain.RiskRead {
		return 30 * time.Second
	}
	return 10 * time.Second
}
