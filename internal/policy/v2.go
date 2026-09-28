package policy

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"gopkg.in/yaml.v3"
)

// V2Rule is the new match → decision format (spec §4). Legacy
// configs/policy.yaml keeps working; new files use this shape.
// Grouping fields (group/category/business/task) are optional:
// empty = match all. They serve two purposes: organization
// (GET /v2/policies groups rules) and scope filtering
// (--group filter + request context matching).
type V2Rule struct {
	ID          string     `yaml:"id"`
	Group       string     `yaml:"group,omitempty"`
	Category    string     `yaml:"category,omitempty"`
	Business    string     `yaml:"business,omitempty"`
	Task        string     `yaml:"task,omitempty"`
	Description string     `yaml:"description,omitempty"`
	Match       V2Match    `yaml:"match"`
	Decision    V2Decision `yaml:"decision"`
}

type V2Context struct {
	Environment string `yaml:"environment"`
	Business    string `yaml:"business,omitempty"`
	Task        string `yaml:"task,omitempty"`
	Category    string `yaml:"category,omitempty"`
}

type V2Match struct {
	Tool    string `yaml:"tool"`
	Command struct {
		Regex string `yaml:"regex"`
	} `yaml:"command"`
	Path struct {
		Regex string `yaml:"regex"`
	} `yaml:"path"`
	Context V2Context `yaml:"context"`
}

type V2Decision struct {
	Action     string  `yaml:"action"`
	Risk       float64 `yaml:"risk"`
	ReasonCode string  `yaml:"reason_code"`
	// ApprovalTimeout is the per-policy approval TTL in seconds, honoured
	// only when action is approval_required (0 = default).
	ApprovalTimeout int `yaml:"approval_timeout,omitempty"`
}

type V2Config struct {
	Groups   []GroupDef `yaml:"groups,omitempty"`
	Policies []V2Rule   `yaml:"policies"`
}

// LoadV2 loads the new policies: format. Returns nil, nil when the file
// uses the legacy rules: shape (detected by missing policies: key).
func LoadV2(path string) (*V2Config, error) {
	return loadV2File(path)
}

// LoadV2Merged loads V2 policies from a file or, when path is a directory,
// merges every *.yaml/*.yml inside it (sorted, policies: sections only).
func LoadV2Merged(path string) (*V2Config, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return loadV2File(path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	merged := &V2Config{}
	seenGroup := map[string]bool{}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		cfg, err := loadV2File(filepath.Join(path, name))
		if err != nil || cfg == nil {
			if err != nil {
				log.Printf("warning: load %s: %v", name, err)
			}
			continue
		}
		for _, g := range cfg.Groups {
			if !seenGroup[g.Name] {
				seenGroup[g.Name] = true
				merged.Groups = append(merged.Groups, g)
			}
		}
		merged.Policies = append(merged.Policies, cfg.Policies...)
	}
	if len(merged.Policies) == 0 && len(merged.Groups) == 0 {
		return nil, nil
	}
	return merged, nil
}

func loadV2File(path string) (*V2Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var raw map[string]any
	if err := yaml.NewDecoder(f).Decode(&raw); err != nil {
		return nil, err
	}
	if _, ok := raw["policies"]; !ok {
		return nil, nil
	}
	f2, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f2.Close()
	var cfg V2Config
	if err := yaml.NewDecoder(f2).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type compiledV2Rule struct {
	rule V2Rule
	cmd  *regexp.Regexp
	path *regexp.Regexp
}

// EngineV2 adapts deterministic rules (legacy + V2 format) to the
// harness.PolicyEngine interface. First match wins (file order).
type EngineV2 struct {
	legacy *Engine
	v2     []compiledV2Rule
	groups []GroupDef
	active map[string]bool // nil = all groups active
}

// SetActiveGroups restricts V2 + legacy evaluation to the named
// groups/categories/businesses/tasks. Propagates to the legacy engine.
func (e *EngineV2) SetActiveGroups(names []string) {
	if len(names) == 0 {
		e.active = nil
	} else {
		m := make(map[string]bool, len(names))
		for _, n := range names {
			if n != "" {
				m[n] = true
			}
		}
		e.active = m
	}
	if e.legacy != nil {
		e.legacy.SetActiveGroups(names)
	}
}

// Groups returns declared + derived group metadata.
func (e *EngineV2) Groups() []GroupDef {
	if len(e.groups) > 0 {
		return e.groups
	}
	seen := map[string]bool{}
	var out []GroupDef
	for _, c := range e.v2 {
		if c.rule.Group != "" && !seen[c.rule.Group] {
			seen[c.rule.Group] = true
			out = append(out, GroupDef{Name: c.rule.Group})
		}
	}
	if e.legacy != nil {
		out = append(out, e.legacy.Groups()...)
	}
	return out
}

func (e *EngineV2) isActive(rule V2Rule) bool {
	if e.active == nil {
		return true
	}
	if rule.Group == "" && rule.Category == "" && rule.Business == "" && rule.Task == "" {
		return true
	}
	return e.active[rule.Group] || e.active[rule.Category] || e.active[rule.Business] || e.active[rule.Task]
}

func NewEngineV2(legacy *Engine, cfg *V2Config) *EngineV2 {
	e := &EngineV2{legacy: legacy}
	if cfg != nil {
		e.groups = cfg.Groups
		for _, r := range cfg.Policies {
			c := compiledV2Rule{rule: r}
			// Invalid regex must not panic the gateway: skip the rule
			// with a warning so one typo can't disable all policy.
			if r.Match.Command.Regex != "" {
				re, err := regexp.Compile(r.Match.Command.Regex)
				if err != nil {
					log.Printf("warning: rule %q has invalid command regex, skipped: %v", r.ID, err)
					continue
				}
				c.cmd = re
			}
			if r.Match.Path.Regex != "" {
				re, err := regexp.Compile(r.Match.Path.Regex)
				if err != nil {
					log.Printf("warning: rule %q has invalid path regex, skipped: %v", r.ID, err)
					continue
				}
				c.path = re
			}
			e.v2 = append(e.v2, c)
		}
	}
	return e
}

// Reload swaps the V2 + legacy rule sets in place so a running gateway
// picks up dashboard edits without restart. The active group filter and
// the legacy Jev client / cache are kept.
func (e *EngineV2) Reload(pc *PolicyConfig, cfg *V2Config) {
	if e.legacy != nil && pc != nil {
		e.legacy.Reload(pc)
	}
	fresh := NewEngineV2(e.legacy, cfg)
	e.v2 = fresh.v2
	e.groups = fresh.groups
}

func (e *EngineV2) Evaluate(_ context.Context, req domain.NormalizedRequest) domain.PolicyResult {
	// V2 rules first (explicit gateway policy), then legacy engine.
	for _, c := range e.v2 {
		if c.rule.Match.Tool != "" && c.rule.Match.Tool != string(req.Canonical) && c.rule.Match.Tool != "*" {
			continue
		}
		if !e.isActive(c.rule) {
			continue
		}
		// Rule-declared scope: only match when the request carries it.
		// Category is taxonomy, not scope (adapters send none) — see
		// matchesScopeLocked in the legacy engine.
		if c.rule.Business != "" && c.rule.Business != req.Request.Context.Business {
			continue
		}
		if c.rule.Task != "" && c.rule.Task != req.Request.Context.Task {
			continue
		}
		if c.rule.Match.Context.Environment != "" &&
			c.rule.Match.Context.Environment != req.Request.Context.Environment {
			continue
		}
		if c.rule.Match.Context.Business != "" &&
			c.rule.Match.Context.Business != req.Request.Context.Business {
			continue
		}
		if c.rule.Match.Context.Task != "" &&
			c.rule.Match.Context.Task != req.Request.Context.Task {
			continue
		}
		if c.cmd != nil && !c.cmd.MatchString(req.Command) {
			continue
		}
		if c.path != nil && !c.path.MatchString(req.Path) && !c.path.MatchString(req.Resource) {
			continue
		}
		target := req.Command
		if target == "" {
			target = req.Path
		}
		return v2Result(c.rule, c.rule.Decision.Risk, target)
	}
	if e.legacy != nil {
		// Normalize via the canonical name so agent-specific variants
		// (Bash, shell, Write, Edit, ...) hit the same legacy rules.
		// The legacy extractor switches on tool family for arg extraction.
		name := string(req.Canonical)
		if req.Canonical == domain.ToolUnknown || req.Canonical == domain.ToolNetwork || req.Canonical == domain.ToolPatch {
			name = req.Request.Tool.Name
		}
		// Note: normalize collapses delete-family tools to write_file, so
		// they match write_file rules with Destructive=true. There is no
		// separate ToolDeleteFile path here by design.
		raw := Normalize(name, req.Request.Tool.Args)
		// Scope-aware: legacy group/business/task/category filters apply
		// here too, using the V2 request context.
		lctx := RequestContext{
			Business: req.Request.Context.Business,
			Task:     req.Request.Context.Task,
			Category: req.Request.Context.Category,
		}
		if dr, ok := e.legacy.CheckWithContext(raw, lctx); ok {
			return domain.PolicyResult{
				Matched: true,
				Final:   true,
				Decision: domain.DecisionResult{
					Decision:            domain.Decision(dr.Decision),
					Risk:                dr.Risk,
					Confidence:          dr.Confidence,
					Source:              "policy",
					PolicyID:            dr.Policy.RuleID,
					Reason:              dr.Reason,
					RequestApproval:     dr.RequestApproval,
					ApprovalTimeoutSecs: dr.ApprovalTimeout,
				},
			}
		}
	}
	return domain.PolicyResult{}
}

// V2ToRules converts V2 policies into legacy Rules so YAML-seeded stores
// (which only understand the legacy shape) can adopt profile files that use
// the policies: format. Command regex wins over path regex when both are set;
// tool-only rules seed as .*; context-only rules (no tool, no regex) are
// skipped (no legacy match).
func V2ToRules(cfg *V2Config) []Rule {
	if cfg == nil {
		return nil
	}
	var out []Rule
	for _, r := range cfg.Policies {
		pattern := r.Match.Command.Regex
		if pattern == "" {
			pattern = r.Match.Path.Regex
		}
		tool := r.Match.Tool
		if tool == "" {
			tool = "*"
		}
		if pattern == "" {
			// Tool-only V2 rules (e.g. strict write-any) match any target
			// of that tool; context-only rules (no tool) have no legacy
			// match and are skipped.
			if r.Match.Tool == "" {
				continue
			}
			pattern = ".*"
		}
		out = append(out, Rule{
			ID:              r.ID,
			Tool:            tool,
			Pattern:         pattern,
			Action:          r.Decision.Action,
			Group:           r.Group,
			Category:        r.Category,
			Business:        firstNonEmpty(r.Business, r.Match.Context.Business),
			Task:            firstNonEmpty(r.Task, r.Match.Context.Task),
			Description:     r.Description,
			ApprovalTimeout: r.Decision.ApprovalTimeout,
		})
		// Category from match context when the top-level tag is empty.
		if out[len(out)-1].Category == "" {
			out[len(out)-1].Category = r.Match.Context.Category
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func v2Result(r V2Rule, risk float64, target string) domain.PolicyResult {
	action := domain.Decision(r.Decision.Action)
	rc := r.Decision.ReasonCode
	if rc == "" {
		rc = r.ID
	}
	reason := "Matched policy " + r.ID
	if target != "" {
		reason += ": " + target
	}
	return domain.PolicyResult{
		Matched: true,
		Final:   true,
		Decision: domain.DecisionResult{
			Decision:            action,
			Risk:                risk,
			Confidence:          1.0,
			Source:              "policy",
			PolicyID:            r.ID,
			ReasonCode:          rc,
			Reason:              reason,
			RequestApproval:     action == domain.ApprovalRequired,
			ApprovalTimeoutSecs: r.Decision.ApprovalTimeout,
		},
	}
}
