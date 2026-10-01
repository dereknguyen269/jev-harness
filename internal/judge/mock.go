package judge

import (
	"context"

	"github.com/dereknguyen269/jev-harness/internal/jev"
)

// Mock returns a fixed judgment for tests and offline evals.
type Mock struct {
	Risk       float64
	Confidence float64
	Action     string
	Category   string
	Err        error
	// Logged fakes recorded API calls for /v1/jev/calls tests.
	Logged []jev.Call
}

// Calls satisfies the same shape as Jev.Calls for the dashboard endpoint.
func (m *Mock) Calls() []jev.Call { return m.Logged }

func (m *Mock) Evaluate(_ context.Context, _ JudgeRequest) (JudgeResult, error) {
	if m.Err != nil {
		return JudgeResult{}, m.Err
	}
	action := m.Action
	if action == "" {
		action = "allow"
	}
	return JudgeResult{
		Risk:       m.Risk,
		Confidence: m.Confidence,
		Action:     action,
		Category:   m.Category,
		Reasons:    []string{"mock"},
	}, nil
}
