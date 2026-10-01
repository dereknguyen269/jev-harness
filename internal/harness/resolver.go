package harness

import (
	"fmt"
	"strings"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/judge"
)

// ResolveJev translates a Jev judgment into a final decision using
// confidence-gated thresholds. Jev is advisory; this function decides.
//
// Rules (spec §6 + §8):
//   - confidence < threshold(level) → approval_required (don't trust weak judgments)
//   - risk >= 0.95 (L4) → block
//   - risk >= 0.70 (L3) → approval_required
//   - else honor Jev's advisory action when confident, defaulting to approval
//     in the uncertain middle band.

// riskSummary builds a human-readable "why risky" prefix shared by every
// approval/block reason: risk score + level + confidence + Jev details.
// Callers see this string on the dashboard approve/deny dialog, menubar
// notification, and `approvals` CLI, so it must stand alone.
func riskSummary(result judge.JudgeResult, level domain.RiskLevel) string {
	base := fmt.Sprintf("risk=%.2f (%s) conf=%.2f", result.Risk, level.String(), result.Confidence)
	details := []string{}
	if result.Category != "" {
		details = append(details, "category="+result.Category)
	}
	if result.Action != "" {
		details = append(details, "jev="+result.Action)
	}
	for _, r := range result.Reasons {
		if strings.TrimSpace(r) != "" {
			details = append(details, r)
		}
	}
	if len(details) > 0 {
		base += " — " + strings.Join(details, "; ")
	}
	return base
}

func ResolveJev(result judge.JudgeResult, thresholds domain.ConfidenceThresholds) domain.DecisionResult {
	if thresholds == nil {
		thresholds = domain.DefaultThresholds()
	}
	level := domain.RiskFromScore(result.Risk)
	why := riskSummary(result, level)
	// L4 critical always blocks — never gated on confidence.
	if level == domain.RiskCritical || result.Risk >= 0.95 {
		return domain.DecisionResult{
			Decision: domain.Block, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "CRITICAL_RISK",
			Reason: "Blocked: critical risk — " + why,
		}
	}
	if result.Confidence < thresholds[level] {
		return domain.DecisionResult{
			Decision:        domain.ApprovalRequired,
			Risk:            result.Risk,
			Confidence:      result.Confidence,
			Source:          "jev",
			ReasonCode:      "LOW_CONFIDENCE",
			Reason:          "Approval required: low Jev confidence for " + level.String() + " — " + why,
			RequestApproval: true,
		}
	}
	switch {
	case result.Risk >= 0.70:
		return domain.DecisionResult{
			Decision: domain.ApprovalRequired, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "HIGH_RISK",
			Reason:          "Approval required: high risk — " + why,
			RequestApproval: true,
		}
	case result.Action == "block":
		return domain.DecisionResult{
			Decision: domain.Block, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "JEV_BLOCK",
			Reason: "Blocked: Jev advises block — " + why,
		}
	case result.Action == "allow" && result.Risk < 0.4:
		return domain.DecisionResult{
			Decision: domain.Allow, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "JEV_ALLOW",
			Reason: "Jev advises allow — " + why,
		}
	default:
		return domain.DecisionResult{
			Decision: domain.ApprovalRequired, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "UNCERTAIN",
			Reason:          "Approval required: uncertain — " + why,
			RequestApproval: true,
		}
	}
}
