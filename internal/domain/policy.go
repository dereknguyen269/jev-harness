package domain

// CanonicalTool is the normalized tool namespace. Policy never sees
// agent-specific names.
type CanonicalTool string

const (
	ToolTerminal   CanonicalTool = "terminal"
	ToolReadFile   CanonicalTool = "read_file"
	ToolWriteFile  CanonicalTool = "write_file"
	ToolDeleteFile CanonicalTool = "delete_file"
	ToolNetwork    CanonicalTool = "network"
	ToolPatch      CanonicalTool = "patch"
	ToolUnknown    CanonicalTool = "unknown"
)

// NormalizedRequest is a ToolRequest with canonical tool + extracted signals.
type NormalizedRequest struct {
	Request   ToolRequest
	Canonical CanonicalTool
	Command   string
	Path      string
	URL       string
	Resource  string

	Destructive bool
	Network     bool
	Sensitive   bool
}

// PolicyRule is a V2 match → decision rule.
type PolicyRule struct {
	ID          string
	Tool        CanonicalTool `json:"tool,omitempty"`
	CommandRe   string        `json:"command_regex,omitempty"`
	PathRe      string        `json:"path_regex,omitempty"`
	Environment string        `json:"environment,omitempty"`
	Action      Decision      `json:"action"`
	Risk        float64       `json:"risk"`
	ReasonCode  string        `json:"reason_code,omitempty"`
}

// PolicyResult is the outcome of policy evaluation.
type PolicyResult struct {
	Matched  bool
	Final    bool // true → skip Jev, return Decision immediately
	Decision DecisionResult
}

// ConfidenceThresholds gate Jev judgments per risk level.
type ConfidenceThresholds map[RiskLevel]float64

// DefaultThresholds per spec §8.
func DefaultThresholds() ConfidenceThresholds {
	return ConfidenceThresholds{
		RiskRead:       0.5,
		RiskLow:        0.7,
		RiskMutation:   0.85,
		RiskPrivileged: 0.95,
		RiskCritical:   1.01, // L4 always blocks regardless of confidence
	}
}
