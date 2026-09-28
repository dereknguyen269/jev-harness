package domain

// Approval TTL bounds (seconds). The default applies to every approval;
// a rule may override it per approval_required decision via
// approval_timeout (0/blank = default).
const (
	DefaultApprovalTTLSeconds = 30
	MinApprovalTTLSeconds     = 5
	MaxApprovalTTLSeconds     = 3600
)

// ClampApprovalTTLSeconds forces secs into [Min, Max]; non-positive
// values fall back to the default.
func ClampApprovalTTLSeconds(secs int) int {
	if secs <= 0 {
		return DefaultApprovalTTLSeconds
	}
	if secs < MinApprovalTTLSeconds {
		return MinApprovalTTLSeconds
	}
	if secs > MaxApprovalTTLSeconds {
		return MaxApprovalTTLSeconds
	}
	return secs
}

// ResolveApprovalTTLSeconds picks the effective TTL: a positive per-rule
// timeout wins, otherwise the default. Both sides are clamped.
func ResolveApprovalTTLSeconds(ruleTimeoutSecs, defaultSecs int) int {
	if ruleTimeoutSecs > 0 {
		return ClampApprovalTTLSeconds(ruleTimeoutSecs)
	}
	return ClampApprovalTTLSeconds(defaultSecs)
}
