package harness

import (
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
func ResolveJev(result judge.JudgeResult, thresholds domain.ConfidenceThresholds) domain.DecisionResult {
	if thresholds == nil {
		thresholds = domain.DefaultThresholds()
	}
	level := domain.RiskFromScore(result.Risk)
	// L4 critical always blocks — never gated on confidence.
	if level == domain.RiskCritical || result.Risk >= 0.95 {
		return domain.DecisionResult{
			Decision: domain.Block, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "CRITICAL_RISK",
			Reason: "Risk at critical level",
		}
	}
	if result.Confidence < thresholds[level] {
		return domain.DecisionResult{
			Decision:        domain.ApprovalRequired,
			Risk:            result.Risk,
			Confidence:      result.Confidence,
			Source:          "jev",
			ReasonCode:      "LOW_CONFIDENCE",
			Reason:          "Jev confidence below threshold for risk level " + level.String(),
			RequestApproval: true,
		}
	}
	switch {
	case result.Risk >= 0.70:
		return domain.DecisionResult{
			Decision: domain.ApprovalRequired, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "HIGH_RISK",
			Reason:          "Risk requires human approval",
			RequestApproval: true,
		}
	case result.Action == "block":
		return domain.DecisionResult{
			Decision: domain.Block, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "JEV_BLOCK",
			Reason: "Jev advises block",
		}
	case result.Action == "allow" && result.Risk < 0.4:
		return domain.DecisionResult{
			Decision: domain.Allow, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "JEV_ALLOW",
			Reason: "Jev advises allow",
		}
	default:
		return domain.DecisionResult{
			Decision: domain.ApprovalRequired, Risk: result.Risk, Confidence: result.Confidence,
			Source: "jev", ReasonCode: "UNCERTAIN",
			Reason:          "Uncertain middle band — require approval",
			RequestApproval: true,
		}
	}
}
