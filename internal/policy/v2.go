package policy

import (
	"context"
	"log"
	"os"
	"regexp"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"gopkg.in/yaml.v3"
)

// V2Rule is the new match → decision format (spec §4). Legacy
// configs/policy.yaml keeps working; new files use this shape.
type V2Rule struct {
	ID       string     `yaml:"id"`
	Match    V2Match    `yaml:"match"`
	Decision V2Decision `yaml:"decision"`
}

type V2Match struct {
	Tool    string `yaml:"tool"`
	Command struct {
		Regex string `yaml:"regex"`
	} `yaml:"command"`
	Path struct {
		Regex string `yaml:"regex"`
	} `yaml:"path"`
	Context struct {
		Environment string `yaml:"environment"`
	} `yaml:"context"`
}

type V2Decision struct {
	Action     string  `yaml:"action"`
	Risk       float64 `yaml:"risk"`
	ReasonCode string  `yaml:"reason_code"`
}

type V2Config struct {
	Policies []V2Rule `yaml:"policies"`
}

// LoadV2 loads the new policies: format. Returns nil, nil when the file
// uses the legacy rules: shape (detected by missing policies: key).
func LoadV2(path string) (*V2Config, error) {
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
}

func NewEngineV2(legacy *Engine, cfg *V2Config) *EngineV2 {
	e := &EngineV2{legacy: legacy}
	if cfg != nil {
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

func (e *EngineV2) Evaluate(_ context.Context, req domain.NormalizedRequest) domain.PolicyResult {
	// V2 rules first (explicit gateway policy), then legacy engine.
	for _, c := range e.v2 {
		if c.rule.Match.Tool != "" && c.rule.Match.Tool != string(req.Canonical) && c.rule.Match.Tool != "*" {
			continue
		}
		if c.rule.Match.Context.Environment != "" &&
			c.rule.Match.Context.Environment != req.Request.Context.Environment {
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
		if dr, ok := e.legacy.Check(raw); ok {
			return domain.PolicyResult{
				Matched: true,
				Final:   true,
				Decision: domain.DecisionResult{
					Decision:        domain.Decision(dr.Decision),
					Risk:            dr.Risk,
					Confidence:      dr.Confidence,
					Source:          "policy",
					PolicyID:        dr.Policy.RuleID,
					Reason:          dr.Reason,
					RequestApproval: dr.RequestApproval,
				},
			}
		}
	}
	return domain.PolicyResult{}
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
			Decision:        action,
			Risk:            risk,
			Confidence:      1.0,
			Source:          "policy",
			PolicyID:        r.ID,
			ReasonCode:      rc,
			Reason:          reason,
			RequestApproval: action == domain.ApprovalRequired,
		},
	}
}
