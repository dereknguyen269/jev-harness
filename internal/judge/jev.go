package judge

import (
	"context"
	"fmt"

	"github.com/dereknguyen269/jev-harness/internal/jev"
	"github.com/dereknguyen269/jev-harness/internal/policy"
)

// Jev adapts the existing *jev.Client (state+questions wire format) to the
// V2 Judge interface. Question framing stays here; thresholds stay in the
// harness resolver.
type Jev struct {
	client policy.JevClient
}

func NewJev(c policy.JevClient) *Jev { return &Jev{client: c} }

func NewJevFromClient(apiKey, endpoint, model string) *Jev {
	return &Jev{client: jev.NewClient(apiKey, endpoint, model, "")}
}

func (j *Jev) Evaluate(ctx context.Context, req JudgeRequest) (JudgeResult, error) {
	if j.client == nil {
		return JudgeResult{}, fmt.Errorf("no Jev client configured")
	}
	state := map[string]any{
		"tool":         string(req.Tool),
		"command":      req.Command,
		"path":         req.Path,
		"destructive":  req.Destructive,
		"network":      req.Network,
		"sensitive":    req.Sensitive,
		"environment":  req.Context.Environment,
		"repo":         req.Context.Repo,
		"working_dir":  req.Context.WorkingDir,
		"user_request": req.Context.UserRequest,
	}
	questions := map[string]policy.Question{
		"should_allow": {
			Type: "noul",
			Instructions: fmt.Sprintf(
				"Should the tool call %s be allowed? command=%q path=%q env=%s destructive=%v network=%v sensitive=%v",
				req.Tool, req.Command, req.Path, req.Context.Environment,
				req.Destructive, req.Network, req.Sensitive,
			),
			Criteria: map[string]any{
				"true":  "Allow - the action is safe, reversible, or clearly intended",
				"false": "Block - the action is dangerous, irreversible, or likely unintended",
			},
		},
		"risk_level": {
			Type:         "score",
			Instructions: "Risk level 1=minimal 2=low 3=moderate 4=high 5=critical",
			Criteria:     map[string]any{"1": "minimal", "2": "low", "3": "moderate", "4": "high", "5": "critical"},
		},
	}
	answers, err := j.client.Evaluate(ctx, state, questions)
	if err != nil {
		return JudgeResult{}, err
	}
	allowAns := answers["should_allow"]
	riskAns := answers["risk_level"]

	shouldAllow := false
	if allowAns.Noul != nil {
		shouldAllow = *allowAns.Noul > 0.5
	}
	risk := 0.5
	if riskAns.Score != nil {
		risk = *riskAns.Score / 5.0
	}
	conf := allowAns.Confidence
	if riskAns.Confidence > conf {
		conf = riskAns.Confidence
	}
	action := "approval_required"
	if !shouldAllow || risk >= 0.95 {
		action = "block"
	} else if shouldAllow && risk < 0.4 {
		action = "allow"
	}
	return JudgeResult{
		Risk:       risk,
		Confidence: conf,
		Action:     action,
		Category:   string(req.Tool),
		Reasons:    []string{fmt.Sprintf("allow=%v risk=%.2f conf=%.2f", shouldAllow, risk, conf)},
	}, nil
}
