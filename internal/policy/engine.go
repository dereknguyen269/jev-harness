package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type Decision string

const (
	Allow            Decision = "allow"
	Block            Decision = "block"
	ApprovalRequired Decision = "approval_required"
)

type RequestContext struct {
	UserRequest string `json:"user_request"`
	WorkingDir  string `json:"working_dir"`
	Platform    string `json:"platform"`
	Agent       string `json:"agent"`
	// Scope for policy grouping. Empty = match all.
	Business string `json:"business,omitempty"`
	Task     string `json:"task,omitempty"`
	Category string `json:"category,omitempty"`
}

type DecisionRequest struct {
	SessionID string         `json:"session_id"`
	TaskID    string         `json:"task_id"`
	TurnID    string         `json:"turn_id"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Context   RequestContext `json:"context"`
}

type PolicyInfo struct {
	RuleID string `json:"rule_id,omitempty"`
}

type DecisionResponse struct {
	Decision        Decision   `json:"decision"`
	Risk            float64    `json:"risk"`
	Confidence      float64    `json:"confidence"`
	Reason          string     `json:"reason"`
	Policy          PolicyInfo `json:"policy"`
	RequestApproval bool       `json:"request_approval"`
	// ApprovalTimeout carries the matched rule's approval_timeout (seconds,
	// 0 = default) toward the harness TTL resolution.
	ApprovalTimeout int `json:"approval_timeout,omitempty"`
}

type ToolAction struct {
	Tool        string `json:"tool"`
	Operation   string `json:"operation"`
	Resource    string `json:"resource,omitempty"`
	Command     string `json:"command,omitempty"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
	Destructive bool   `json:"destructive"`
	Network     bool   `json:"network"`
	Sensitive   bool   `json:"sensitive"`
}

type Rule struct {
	ID       string `yaml:"id"`
	Tool     string `yaml:"tool"`
	Pattern  string `yaml:"pattern"`
	Action   string `yaml:"action"`
	Priority int    `yaml:"priority"`
	// Grouping: all optional, empty = match all. Group is the primary
	// bucket (e.g. "critical-safety", "secrets", "code-allow");
	// category/business/task are free-form scopes for filtering
	// (e.g. category: "safety", business: "payments", task: "deploy").
	Group       string `yaml:"group,omitempty"`
	Category    string `yaml:"category,omitempty"`
	Business    string `yaml:"business,omitempty"`
	Task        string `yaml:"task,omitempty"`
	Description string `yaml:"description,omitempty"`
	// ApprovalTimeout is the per-rule approval TTL in seconds, honoured
	// only when action is approval_required (0 = default).
	ApprovalTimeout int `yaml:"approval_timeout,omitempty"`
}

// GroupDef declares a policy group in the YAML header.
type GroupDef struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

type PolicyConfig struct {
	Groups []GroupDef `yaml:"groups,omitempty"`
	Rules  []Rule     `yaml:"rules"`
}

type RuleEngine interface {
	Check(ta ToolAction) (DecisionResponse, bool)
}

type Engine struct {
	rules []Rule
	// groups is the declared header metadata (may be empty).
	groups   []GroupDef
	mu       sync.RWMutex
	jev      JevClient
	cache    Cache
	compiled map[string]*regexp.Regexp
	// activeGroups nil = all groups active. Non-nil = only listed groups
	// (matched by group, category, business, or task name) are evaluated.
	active map[string]bool
}

// SetActiveGroups restricts evaluation to the named groups/categories/
// businesses/tasks. Empty/nil clears the filter (all rules active).
// Names match against Rule.Group, Rule.Category, Rule.Business, Rule.Task.
func (e *Engine) SetActiveGroups(names []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(names) == 0 {
		e.active = nil
		return
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			m[n] = true
		}
	}
	e.active = m
}

// ActiveGroups returns the current filter (nil = all).
func (e *Engine) ActiveGroups() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.active == nil {
		return nil
	}
	out := make([]string, 0, len(e.active))
	for k := range e.active {
		out = append(out, k)
	}
	return out
}

func (e *Engine) isActiveLocked(r Rule) bool {
	if e.active == nil {
		return true
	}
	// Ungrouped/untagged rules always stay active so a filter can never
	// silently disable the whole policy.
	if r.Group == "" && r.Category == "" && r.Business == "" && r.Task == "" {
		return true
	}
	return e.active[r.Group] || e.active[r.Category] || e.active[r.Business] || e.active[r.Task]
}

// matchesScopeLocked enforces rule-declared business/task scope against
// the request context. Empty rule scope = match all.
//
// Category (and group) are taxonomy for the dashboard, NOT request scope:
// adapters never send a category, so gating on it would silently disable
// every tagged rule. Business/task remain genuine scopes.
func matchesScopeLocked(r Rule, ctx RequestContext) bool {
	if r.Business != "" && r.Business != ctx.Business {
		return false
	}
	if r.Task != "" && r.Task != ctx.Task {
		return false
	}
	return true
}

type JevClient interface {
	Evaluate(ctx context.Context, state map[string]any, questions map[string]Question) (map[string]Answer, error)
}

type Question struct {
	Type         string         `json:"type"`
	Instructions string         `json:"instructions"`
	Criteria     map[string]any `json:"criteria,omitempty"`
}

type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    float64            `json:"confidence"`
}

func Load(path string) (*PolicyConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pc PolicyConfig
	if err := yaml.NewDecoder(f).Decode(&pc); err != nil {
		return nil, err
	}
	return &pc, nil
}

// LoadMerged loads a single policy file or, when path is a directory,
// merges every *.yaml/*.yml inside it (sorted). Groups with the same name
// are de-duplicated; rules are concatenated in file order (first match wins).
func LoadMerged(path string) (*PolicyConfig, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return Load(path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	merged := &PolicyConfig{}
	seenGroup := map[string]bool{}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		pc, err := Load(filepath.Join(path, name))
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", name, err)
		}
		for _, g := range pc.Groups {
			if !seenGroup[g.Name] {
				seenGroup[g.Name] = true
				merged.Groups = append(merged.Groups, g)
			}
		}
		merged.Rules = append(merged.Rules, pc.Rules...)
	}
	return merged, nil
}

// ParseActiveGroups splits a comma/space-separated group filter
// (flag or POLICY_GROUPS env) into names.
func ParseActiveGroups(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Fields(strings.ReplaceAll(s, ",", " "))
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Groups returns the declared group metadata.
func (e *Engine) Groups() []GroupDef {
	e.mu.RLock()
	defer e.mu.RUnlock()
	// Derive from rules when no explicit header exists.
	if len(e.groups) > 0 {
		return e.groups
	}
	seen := map[string]bool{}
	var out []GroupDef
	for _, r := range e.rules {
		if r.Group != "" && !seen[r.Group] {
			seen[r.Group] = true
			out = append(out, GroupDef{Name: r.Group})
		}
	}
	return out
}

func NewEngine(pc *PolicyConfig, jevClient JevClient, c Cache) *Engine {
	e := &Engine{
		rules:    pc.Rules,
		groups:   pc.Groups,
		jev:      jevClient,
		cache:    c,
		compiled: make(map[string]*regexp.Regexp),
	}
	for _, r := range pc.Rules {
		if r.Pattern != "" {
			e.compiled[r.ID] = regexp.MustCompile(r.Pattern)
		}
	}
	return e
}

func (e *Engine) GetJevClient() JevClient {
	return e.jev
}

// Reload swaps the rule set in place so a running gateway picks up
// dashboard edits without restart. The active group filter is kept.
// Unlike NewEngine it never panics: a bad pattern is skipped with a
// warning so one typo can't disable the whole policy.
func (e *Engine) Reload(pc *PolicyConfig) {
	compiled := make(map[string]*regexp.Regexp, len(pc.Rules))
	kept := make([]Rule, 0, len(pc.Rules))
	for _, r := range pc.Rules {
		if r.Pattern == "" {
			continue
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			log.Printf("warning: rule %q has invalid pattern, skipped: %v", r.ID, err)
			continue
		}
		compiled[r.ID] = re
		kept = append(kept, r)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = kept
	e.groups = pc.Groups
	e.compiled = compiled
}

func (e *Engine) Evaluate(ctx context.Context, req DecisionRequest) (DecisionResponse, error) {
	ta := Normalize(req.Tool, req.Args)

	if dr, ok := e.CheckWithContext(ta, req.Context); ok {
		return dr, nil
	}

	if e.jev == nil {
		return DecisionResponse{
			Decision:        Block,
			Risk:            0.9,
			Confidence:      0.8,
			Reason:          "no policy rule matched, no Jev client configured — fail closed",
			RequestApproval: false,
		}, nil
	}

	cacheKey := hashKey(req.Tool, ta, req.Context)
	if cached, ok := e.cache.Get(cacheKey); ok {
		return cached, nil
	}

	state := map[string]any{
		"tool":         req.Tool,
		"operation":    ta.Operation,
		"resource":     ta.Resource,
		"command":      ta.Command,
		"path":         ta.Path,
		"url":          ta.URL,
		"destructive":  ta.Destructive,
		"network":      ta.Network,
		"sensitive":    ta.Sensitive,
		"user_request": req.Context.UserRequest,
		"working_dir":  req.Context.WorkingDir,
		"platform":     req.Context.Platform,
		"agent":        req.Context.Agent,
	}

	questions := map[string]Question{
		"should_allow": {
			Type: "noul",
			Instructions: fmt.Sprintf(
				"Should the tool call %s (%s) be allowed? Context: user wants to %s in %s. Destructive=%v, Network=%v, Sensitive=%v",
				req.Tool, ta.Operation, req.Context.UserRequest, req.Context.WorkingDir,
				ta.Destructive, ta.Network, ta.Sensitive,
			),
			Criteria: map[string]any{
				"true":  "Allow - the action is safe, reversible, or clearly intended by the user",
				"false": "Block - the action is dangerous, irreversible, or likely unintended",
			},
		},
		"risk_level": {
			Type:         "score",
			Instructions: "What is the risk level of this tool call? 1=minimal, 2=low, 3=moderate, 4=high, 5=critical",
			Criteria: map[string]any{
				"1": "minimal risk",
				"2": "low risk",
				"3": "moderate risk",
				"4": "high risk",
				"5": "critical risk",
			},
		},
	}

	answers, err := e.jev.Evaluate(ctx, state, questions)
	if err != nil {
		return DecisionResponse{
			Decision:        Block,
			Risk:            0.9,
			Confidence:      0.8,
			Reason:          fmt.Sprintf("Jev evaluation failed: %v", err),
			RequestApproval: false,
		}, nil
	}

	allowAns := answers["should_allow"]
	riskAns := answers["risk_level"]

	shouldAllow := false
	if allowAns.Noul != nil {
		shouldAllow = *allowAns.Noul > 0.5
	}

	risk := 0.5
	if riskAns.Score != nil {
		// The API returns score as a 0-based index into the criteria
		// list (0..4 for our 5 levels) — normalize by the max index.
		risk = *riskAns.Score / 4.0
		if risk < 0 {
			risk = 0
		}
		if risk > 1 {
			risk = 1
		}
	}

	confidence := allowAns.Confidence
	if confidence == 0 && allowAns.Noul != nil {
		// Noul answers carry no confidence field; derive it from the
		// distance to the decision boundary (0.5).
		confidence = math.Abs(*allowAns.Noul-0.5) * 2
	}
	if riskAns.Confidence > confidence {
		confidence = riskAns.Confidence
	}

	decision := Allow
	if !shouldAllow {
		decision = Block
	} else if risk > 0.6 {
		decision = ApprovalRequired
	}

	resp := DecisionResponse{
		Decision:        decision,
		Risk:            risk,
		Confidence:      confidence,
		Reason:          fmt.Sprintf("Jev: allow=%v risk=%.2f conf=%.2f", shouldAllow, risk, confidence),
		RequestApproval: decision == ApprovalRequired,
	}

	if decision != ApprovalRequired {
		e.cache.Set(cacheKey, resp, 5*time.Minute)
	}

	return resp, nil
}

func (e *Engine) Check(ta ToolAction) (DecisionResponse, bool) {
	return e.CheckWithContext(ta, RequestContext{})
}

// CheckWithContext is group- and scope-aware: rules outside the active
// group filter or whose business/task/category scope doesn't match ctx
// are skipped. First match wins (file order).
func (e *Engine) CheckWithContext(ta ToolAction, ctx RequestContext) (DecisionResponse, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, r := range e.rules {
		if r.Tool != "" && r.Tool != ta.Tool && r.Tool != "*" {
			continue
		}
		if !e.isActiveLocked(r) {
			continue
		}
		if !matchesScopeLocked(r, ctx) {
			continue
		}
		re, ok := e.compiled[r.ID]
		if !ok {
			continue
		}
		target := ""
		switch {
		case ta.Command != "":
			target = ta.Command
		case ta.Path != "":
			target = ta.Path
		case ta.URL != "":
			target = ta.URL
		case ta.Resource != "":
			target = ta.Resource
		}
		// Also test Resource when it carries information beyond the primary
		// target (e.g. smart_relocate stores "src -> dst" while Path is dst
		// only), so file rules see both ends of the operation.
		targets := []string{target}
		if ta.Resource != "" && ta.Resource != target {
			targets = append(targets, ta.Resource)
		}
		matched := false
		for _, t := range targets {
			if re.MatchString(t) {
				matched = true
				target = t
				break
			}
		}
		if matched {
			desc := ""
			if r.Description != "" {
				desc = ": " + r.Description
			}
			switch strings.ToLower(r.Action) {
			case "block":
				return DecisionResponse{
					Decision:        Block,
					Risk:            1.0,
					Confidence:      1.0,
					Reason:          fmt.Sprintf("Blocked by rule %s [risk=1.00 CRITICAL]%s — matched: %s", r.ID, desc, target),
					Policy:          PolicyInfo{RuleID: r.ID},
					RequestApproval: false,
				}, true
			case "approval_required":
				return DecisionResponse{
					Decision:        ApprovalRequired,
					Risk:            0.8,
					Confidence:      0.9,
					Reason:          fmt.Sprintf("Approval required by rule %s [risk=0.80 PRIVILEGED]%s — matched: %s", r.ID, desc, target),
					Policy:          PolicyInfo{RuleID: r.ID},
					RequestApproval: true,
					ApprovalTimeout: r.ApprovalTimeout,
				}, true
			case "allow":
				return DecisionResponse{
					Decision:        Allow,
					Risk:            0.0,
					Confidence:      1.0,
					Reason:          fmt.Sprintf("Allowed by rule %s%s", r.ID, desc),
					Policy:          PolicyInfo{RuleID: r.ID},
					RequestApproval: false,
				}, true
			}
		}
	}
	return DecisionResponse{}, false
}

func Normalize(tool string, args map[string]any) ToolAction {
	ta := ToolAction{Tool: tool}
	switch tool {
	case "terminal", "bash", "Bash", "execute", "shell", "execute_bash", "executeBash":
		ta.Tool = "terminal"
		ta.Operation = "execute"
		if c := firstString(args, "command", "cmd", "commandLine"); c != "" {
			ta.Command = c
			ta.Destructive = isDestructiveCommand(c)
		}
		if w, ok := args["workdir"].(string); ok {
			ta.Resource = w
		}
	case "write_file", "write", "Write", "Edit", "edit", "apply_patch", "fs_write", "fs_append", "str_replace":
		ta.Tool = "write_file"
		ta.Operation = "write"
		if p := firstString(args, "path", "file", "file_path", "filePath", "filename"); p != "" {
			ta.Path = p
			ta.Resource = p
			ta.Sensitive = isSensitivePath(p)
		}
	case "delete_file", "deleteFile":
		ta.Tool = "write_file"
		ta.Operation = "write"
		ta.Destructive = true
		if p := firstString(args, "path", "file", "file_path", "filePath", "filename"); p != "" {
			ta.Path = p
			ta.Resource = p
			ta.Sensitive = isSensitivePath(p)
		}
	case "smart_relocate", "move", "rename":
		ta.Tool = "write_file"
		ta.Operation = "write"
		ta.Destructive = true
		dst := firstString(args, "destination", "to", "dest", "newPath", "path")
		src := firstString(args, "source", "from", "oldPath")
		if dst != "" {
			ta.Path = dst
			ta.Sensitive = isSensitivePath(dst)
		}
		if src != "" && dst != "" {
			ta.Resource = src + " -> " + dst
		} else if dst != "" {
			ta.Resource = dst
		} else if src != "" {
			ta.Resource = src
		}
	case "patch":
		ta.Operation = "patch"
		if p, ok := args["path"].(string); ok {
			ta.Path = p
			ta.Resource = p
		}
	case "read_file", "read", "Read", "cat":
		ta.Tool = "read_file"
		ta.Operation = "read"
		if p := firstString(args, "path", "file", "file_path", "filePath", "filename"); p != "" {
			ta.Path = p
			ta.Resource = p
			ta.Sensitive = isSensitivePath(p)
		}
	case "browser_navigate", "browser":
		ta.Operation = "navigate"
		ta.Network = true
		if u, ok := args["url"].(string); ok {
			ta.URL = u
			ta.Resource = u
		}
	case "browser_extract":
		ta.Operation = "extract"
		ta.Network = true
		if u, ok := args["url"].(string); ok {
			ta.URL = u
			ta.Resource = u
		}
	default:
		ta.Operation = "unknown"
		if c := firstString(args, "command", "cmd", "commandLine"); c != "" {
			ta.Command = c
		}
		if p := firstString(args, "path", "file", "file_path", "filePath", "filename"); p != "" {
			ta.Path = p
		}
		if u, ok := args["url"].(string); ok && u != "" {
			ta.URL = u
		}
		// Resource prefers command > path > url so rule matching is
		// deterministic regardless of Go map iteration order.
		switch {
		case ta.Command != "":
			ta.Resource = ta.Command
		case ta.Path != "":
			ta.Resource = ta.Path
		case ta.URL != "":
			ta.Resource = ta.URL
		default:
			for _, v := range args {
				ta.Resource = fmt.Sprint(v)
				break
			}
		}
	}
	return ta
}

func firstString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func isDestructiveCommand(cmd string) bool {
	patterns := []string{
		`rm\s+-rf\s+/`,
		`rm\s+-rf\s+\*`,
		`rm\s+-rf\s+~`,
		`sudo\s+`,
		`systemctl\s+(stop|disable|restart|kill)`,
		`docker\s+(rm|rmi|kill|stop|system\s+prune)`,
		`kubectl\s+delete`,
		`git\s+push\s+--force`,
		`git\s+reset\s+--hard`,
		`mkfs`,
		`dd\s+if=`,
		`chmod\s+777\s+/`,
		`chown\s+-R\s+root\s+/`,
	}
	for _, p := range patterns {
		if matched, _ := regexp.MatchString(p, cmd); matched {
			return true
		}
	}
	return false
}

func isSensitivePath(path string) bool {
	sensitive := []string{
		".env", ".env.local", ".env.production",
		"id_rsa", "id_ed25519", "authorized_keys",
		".ssh/", ".aws/", ".config/gcloud/",
		"secrets", "credentials", ".pem", ".key",
	}
	lower := strings.ToLower(path)
	for _, s := range sensitive {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func hashKey(tool string, ta ToolAction, ctx RequestContext) string {
	var sb strings.Builder
	sb.WriteString(tool)
	sb.WriteString("|")
	sb.WriteString(ta.Operation)
	sb.WriteString("|")
	if ta.Command != "" {
		sb.WriteString("cmd:" + ta.Command)
	}
	if ta.Path != "" {
		sb.WriteString("path:" + ta.Path)
	}
	if ta.URL != "" {
		sb.WriteString("url:" + ta.URL)
	}
	sb.WriteString("|")
	sb.WriteString(ctx.WorkingDir)
	sb.WriteString("|")
	sb.WriteString(ctx.Business)
	sb.WriteString("|")
	sb.WriteString(ctx.Task)
	sb.WriteString("|")
	sb.WriteString(ctx.Category)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(sb.String())).String()
}

type Cache interface {
	Get(key string) (DecisionResponse, bool)
	Set(key string, value DecisionResponse, ttl time.Duration)
}

type inMemoryCache struct {
	mu   sync.RWMutex
	data map[string]cacheEntry
}

type cacheEntry struct {
	value     DecisionResponse
	expiresAt time.Time
}

func NewCache(ttl time.Duration) Cache {
	c := &inMemoryCache{data: make(map[string]cacheEntry)}
	go c.cleanup(ttl)
	return c
}

func (c *inMemoryCache) Get(key string) (DecisionResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.data[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return DecisionResponse{}, false
	}
	return entry.value, true
}

func (c *inMemoryCache) Set(key string, value DecisionResponse, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = cacheEntry{value: value, expiresAt: time.Now().Add(ttl)}
}

func (c *inMemoryCache) cleanup(ttl time.Duration) {
	ticker := time.NewTicker(ttl)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.data {
			if now.After(v.expiresAt) {
				delete(c.data, k)
			}
		}
		c.mu.Unlock()
	}
}

type AuditLogger interface {
	Log(event AuditEvent)
}

type AuditEvent struct {
	ID         string         `json:"id"`
	Timestamp  time.Time      `json:"timestamp"`
	SessionID  string         `json:"session_id"`
	TurnID     string         `json:"turn_id"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args"`
	Decision   Decision       `json:"decision"`
	Risk       float64        `json:"risk"`
	Confidence float64        `json:"confidence"`
	RuleID     string         `json:"rule_id,omitempty"`
	Reason     string         `json:"reason"`
	DurationMS int64          `json:"duration_ms"`
}

func NewFileLogger(path string) (AuditLogger, error) {
	if path == "" {
		path = filepath.Join(os.Getenv("HOME"), ".hermes", "guard", "audit.jsonl")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &fileLogger{file: f}, nil
}

type fileLogger struct {
	file *os.File
	mu   sync.Mutex
}

func (l *fileLogger) Log(event AuditEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, _ := json.Marshal(event)
	l.file.Write(append(data, '\n'))
}
