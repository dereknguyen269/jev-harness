package judge

import (
	"context"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

// JudgeRequest is what the policy engine sends to Jev. No arbitrary
// commands or actions — only judgment over a canonical tool call.
type JudgeRequest struct {
	Tool      domain.CanonicalTool
	Arguments map[string]any
	Context   domain.ExecutionContext

	Command     string
	Path        string
	Destructive bool
	Network     bool
	Sensitive   bool

	CandidateActions []string
}

// JudgeResult is Jev's structured judgment. The Go resolver — not Jev —
// turns this into a final Decision.
type JudgeResult struct {
	Risk       float64
	Confidence float64

	Category string
	Action   string // allow | approval_required | block (advisory only)

	Reasons []string
}

// Judge evaluates a normalized request.
type Judge interface {
	Evaluate(ctx context.Context, req JudgeRequest) (JudgeResult, error)
}
