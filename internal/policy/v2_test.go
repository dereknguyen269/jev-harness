package policy

import (
	"context"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

func v2TestConfig() *V2Config {
	return &V2Config{Policies: []V2Rule{
		{ID: "production", Match: V2Match{Context: struct {
			Environment string `yaml:"environment"`
		}{Environment: "production"}},
			Decision: V2Decision{Action: "approval_required", Risk: 0.9}},
		{ID: "git-read", Match: V2Match{Tool: "terminal", Command: struct {
			Regex string `yaml:"regex"`
		}{Regex: "^git\\s+(status|diff|log)"}},
			Decision: V2Decision{Action: "allow", Risk: 0.05}},
	}}
}

func v2Req(tool, command, env string) domain.NormalizedRequest {
	return domain.NormalizedRequest{
		Request: domain.ToolRequest{
			Tool:    domain.ToolCall{Name: tool, Args: map[string]any{"command": command}},
			Context: domain.ExecutionContext{Environment: env},
		},
		Canonical: domain.CanonicalTool(tool),
		Command:   command,
	}
}

func TestEngineV2_EnvRuleMatchesOnlyWhenEnvEquals(t *testing.T) {
	e := NewEngineV2(nil, v2TestConfig())

	// Production env → approval, even for reads.
	res := e.Evaluate(context.Background(), v2Req("terminal", "git status", "production"))
	if !res.Matched || res.Decision.PolicyID != "production" ||
		res.Decision.Decision != domain.ApprovalRequired {
		t.Fatalf("expected production approval, got %+v", res)
	}

	// Non-prod env → env rule must NOT match; git-read allows.
	res = e.Evaluate(context.Background(), v2Req("terminal", "git status", ""))
	if !res.Matched || res.Decision.PolicyID != "git-read" ||
		res.Decision.Decision != domain.Allow {
		t.Fatalf("expected git-read allow, got %+v", res)
	}
}

func TestEngineV2_FirstMatchWins(t *testing.T) {
	e := NewEngineV2(nil, v2TestConfig())
	res := e.Evaluate(context.Background(), v2Req("terminal", "uptime", "production"))
	if res.Decision.PolicyID != "production" {
		t.Fatalf("expected production, got %+v", res)
	}
}

func TestLoadV2_LegacyFileReturnsNil(t *testing.T) {
	cfg, err := LoadV2("../../configs/policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Fatalf("expected nil for legacy file, got %+v", cfg)
	}
}

func TestLoadV2_ProfileParses(t *testing.T) {
	cfg, err := LoadV2("../../configs/profiles/strict.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || len(cfg.Policies) == 0 {
		t.Fatalf("expected policies, got %+v", cfg)
	}
	if cfg.Policies[0].ID == "" || cfg.Policies[0].Decision.Action == "" {
		t.Fatalf("malformed first rule: %+v", cfg.Policies[0])
	}
}

var _ = time.Minute
