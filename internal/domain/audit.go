package domain

import "time"

// AuditEvent is the V2 audit record.
type AuditEvent struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`

	Agent string `json:"agent"`
	Tool  string `json:"tool"`

	PolicyDecision string `json:"policy_decision,omitempty"`
	JevDecision    string `json:"jev_decision,omitempty"`
	FinalDecision  string `json:"final_decision"`

	PolicyID   string `json:"policy_id,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`

	Risk       float64 `json:"risk"`
	Confidence float64 `json:"confidence"`

	LatencyMS int64 `json:"latency_ms"`
}

// ApprovalStatus tracks human approval lifecycle.
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalDenied   ApprovalStatus = "denied"
	ApprovalExpired  ApprovalStatus = "expired"
)

// Approval is a pending human decision.
type Approval struct {
	ID        string         `json:"id"`
	RequestID string         `json:"request_id"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`

	Risk   float64 `json:"risk"`
	Reason string  `json:"reason,omitempty"`

	Status    ApprovalStatus `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`
}
