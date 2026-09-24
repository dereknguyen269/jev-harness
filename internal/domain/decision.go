package domain

// Decision is the final gateway verdict.
type Decision string

const (
	Allow            Decision = "allow"
	Block            Decision = "block"
	ApprovalRequired Decision = "approval_required"
)

// DecisionResult is the V2 response body.
type DecisionResult struct {
	Decision   Decision `json:"decision"`
	Risk       float64  `json:"risk"`
	Confidence float64  `json:"confidence"`

	Source     string `json:"source"`
	PolicyID   string `json:"policy_id,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
	Reason     string `json:"reason,omitempty"`

	RequestApproval bool   `json:"request_approval"`
	ApprovalID      string `json:"approval_id,omitempty"`
	ExpiresIn       int    `json:"expires_in,omitempty"`
}

// RiskLevel classifies tool calls L0..L4.
type RiskLevel int

const (
	RiskRead       RiskLevel = 0 // L0 READ: git status, cat
	RiskLow        RiskLevel = 1 // L1 LOW: npm install
	RiskMutation   RiskLevel = 2 // L2 MUTATION: edit source
	RiskPrivileged RiskLevel = 3 // L3 PRIVILEGED: sudo
	RiskCritical   RiskLevel = 4 // L4 CRITICAL: rm -rf /, prod DB mutation
)

func (r RiskLevel) String() string {
	switch r {
	case RiskRead:
		return "READ"
	case RiskLow:
		return "LOW"
	case RiskMutation:
		return "MUTATION"
	case RiskPrivileged:
		return "PRIVILEGED"
	case RiskCritical:
		return "CRITICAL"
	default:
		return "UNKNOWN"
	}
}

// RiskFromScore maps a 0..1 risk score to a level.
func RiskFromScore(risk float64) RiskLevel {
	switch {
	case risk >= 0.95:
		return RiskCritical
	case risk >= 0.70:
		return RiskPrivileged
	case risk >= 0.40:
		return RiskMutation
	case risk >= 0.15:
		return RiskLow
	default:
		return RiskRead
	}
}
