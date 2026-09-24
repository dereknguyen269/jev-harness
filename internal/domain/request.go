package domain

import "time"

// ToolRequest is the central V2 object: everything flows through it.
type ToolRequest struct {
	ID        string           `json:"id"`
	Agent     AgentInfo        `json:"agent"`
	Session   SessionInfo      `json:"session"`
	Tool      ToolCall         `json:"tool"`
	Context   ExecutionContext `json:"context"`
	Timestamp time.Time        `json:"timestamp"`
}

// ToolCall is the agent-supplied tool invocation (pre-normalization).
type ToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// AgentInfo identifies the calling agent and shim.
type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Adapter string `json:"adapter,omitempty"`
}

// SessionInfo carries optional session scoping.
type SessionInfo struct {
	ID     string `json:"id,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	TurnID string `json:"turn_id,omitempty"`
}

// ExecutionContext is repo/workspace/environment info for policy matching.
type ExecutionContext struct {
	Repo        string            `json:"repo,omitempty"`
	Workspace   string            `json:"workspace,omitempty"`
	Branch      string            `json:"branch,omitempty"`
	Files       []string          `json:"files,omitempty"`
	Environment string            `json:"environment,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	// Legacy compat fields mapped from v1 RequestContext.
	UserRequest string `json:"user_request,omitempty"`
	WorkingDir  string `json:"working_dir,omitempty"`
	Platform    string `json:"platform,omitempty"`
}
