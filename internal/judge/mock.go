package judge

import "context"

// Mock returns a fixed judgment for tests and offline evals.
type Mock struct {
	Risk       float64
	Confidence float64
	Action     string
	Category   string
	Err        error
}

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
