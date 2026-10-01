package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

func groupedLegacyConfig() *PolicyConfig {
	return &PolicyConfig{
		Groups: []GroupDef{{Name: "git"}, {Name: "secrets"}},
		Rules: []Rule{
			{ID: "git-status", Tool: "terminal", Pattern: "git\\s+status", Action: "allow", Group: "git"},
			{ID: "env-write", Tool: "write_file", Pattern: "\\.env", Action: "approval_required", Group: "secrets"},
		},
	}
}

func TestGroups_LegacyFilter(t *testing.T) {
	eng := NewEngine(groupedLegacyConfig(), nil, NewCache(5*time.Minute))

	// No filter: both active.
	if dr, ok := eng.CheckWithContext(Normalize("terminal", map[string]any{"command": "git status"}), RequestContext{}); !ok || dr.Decision != Allow {
		t.Fatalf("expected git-status allow, got %+v ok=%v", dr, ok)
	}

	// Filter to secrets: git rule inactive → no match.
	eng.SetActiveGroups([]string{"secrets"})
	if _, ok := eng.CheckWithContext(Normalize("terminal", map[string]any{"command": "git status"}), RequestContext{}); ok {
		t.Fatal("expected git-status inactive under secrets filter")
	}
	if dr, ok := eng.CheckWithContext(Normalize("write_file", map[string]any{"path": ".env"}), RequestContext{}); !ok || dr.Decision != ApprovalRequired {
		t.Fatalf("expected env-write approval, got %+v ok=%v", dr, ok)
	}

	// Clear filter: active again.
	eng.SetActiveGroups(nil)
	if _, ok := eng.CheckWithContext(Normalize("terminal", map[string]any{"command": "git status"}), RequestContext{}); !ok {
		t.Fatal("expected git-status active after clearing filter")
	}
}

func TestGroups_LegacyBusinessScope(t *testing.T) {
	pc := &PolicyConfig{Rules: []Rule{
		{ID: "pay-deploy", Tool: "terminal", Pattern: "deploy", Action: "approval_required", Business: "payments", Task: "deploy"},
	}}
	eng := NewEngine(pc, nil, NewCache(5*time.Minute))
	ta := Normalize("terminal", map[string]any{"command": "deploy prod"})

	if _, ok := eng.CheckWithContext(ta, RequestContext{}); ok {
		t.Fatal("scoped rule must not match empty context")
	}
	if _, ok := eng.CheckWithContext(ta, RequestContext{Business: "payments"}); ok {
		t.Fatal("scoped rule must not match business-only context")
	}
	dr, ok := eng.CheckWithContext(ta, RequestContext{Business: "payments", Task: "deploy"})
	if !ok || dr.Decision != ApprovalRequired {
		t.Fatalf("expected scoped match, got %+v ok=%v", dr, ok)
	}
}

func TestGroups_V2BusinessTaskCategory(t *testing.T) {
	cfg := &V2Config{Policies: []V2Rule{
		{ID: "pay-rule", Business: "payments",
			Match:    V2Match{Tool: "terminal"},
			Decision: V2Decision{Action: "approval_required", Risk: 0.8}},
		{ID: "deploy-task",
			Match:    V2Match{Tool: "terminal", Context: V2Context{Task: "deploy"}},
			Decision: V2Decision{Action: "block", Risk: 1.0}},
	}}
	e := NewEngineV2(nil, cfg)
	mkReq := func(biz, task string) domain.NormalizedRequest {
		return domain.NormalizedRequest{
			Request: domain.ToolRequest{
				Tool:    domain.ToolCall{Name: "terminal", Args: map[string]any{"command": "run"}},
				Context: domain.ExecutionContext{Business: biz, Task: task},
			},
			Canonical: domain.ToolTerminal,
			Command:   "run",
		}
	}
	if res := e.Evaluate(context.Background(), mkReq("", "")); res.Matched {
		t.Fatalf("no scope → no match, got %+v", res)
	}
	if res := e.Evaluate(context.Background(), mkReq("payments", "")); !res.Matched || res.Decision.PolicyID != "pay-rule" {
		t.Fatalf("expected pay-rule, got %+v", res)
	}
	if res := e.Evaluate(context.Background(), mkReq("", "deploy")); !res.Matched || res.Decision.PolicyID != "deploy-task" {
		t.Fatalf("expected deploy-task, got %+v", res)
	}
}

func TestGroups_V2ActiveFilterPropagatesToLegacy(t *testing.T) {
	legacy := NewEngine(groupedLegacyConfig(), nil, NewCache(5*time.Minute))
	e := NewEngineV2(legacy, nil)
	e.SetActiveGroups([]string{"git"})
	req := domain.NormalizedRequest{
		Request: domain.ToolRequest{
			Tool: domain.ToolCall{Name: "write_file", Args: map[string]any{"path": ".env"}},
		},
		Canonical: domain.ToolWriteFile,
		Path:      ".env",
		Resource:  ".env",
	}
	if res := e.Evaluate(context.Background(), req); res.Matched {
		t.Fatalf("env-write should be filtered out, got %+v", res)
	}
}

func TestV2ToRulesConversion(t *testing.T) {
	cfg := &V2Config{
		Groups: []GroupDef{{Name: "git"}},
		Policies: []V2Rule{
			{ID: "root-delete", Group: "critical-safety", Category: "safety",
				Match:    V2Match{Tool: "terminal"},
				Decision: V2Decision{Action: "block"}},
			{ID: "production", Category: "safety",
				Match:    V2Match{Context: V2Context{Environment: "production"}},
				Decision: V2Decision{Action: "approval_required"}},
		},
	}
	// Fix up the command regexes (zero-value structs above need them).
	cfg.Policies[0].Match.Command.Regex = `rm\s+-rf\s+/`
	rules := V2ToRules(cfg)
	if len(rules) != 1 {
		t.Fatalf("expected 1 convertible rule (context-only skipped), got %+v", rules)
	}
	if rules[0].ID != "root-delete" || rules[0].Pattern != `rm\s+-rf\s+/` ||
		rules[0].Group != "critical-safety" || rules[0].Category != "safety" {
		t.Fatalf("got %+v", rules[0])
	}
	// Tool-only V2 rules seed as .*.
	cfg2 := &V2Config{Policies: []V2Rule{
		{ID: "write-any", Group: "code-allow", Match: V2Match{Tool: "write_file"},
			Decision: V2Decision{Action: "approval_required"}},
	}}
	rules2 := V2ToRules(cfg2)
	if len(rules2) != 1 || rules2[0].Pattern != ".*" {
		t.Fatalf("tool-only should seed .*: %+v", rules2)
	}
}

func TestGroups_LoadMergedDir(t *testing.T) {
	dir := t.TempDir()
	a := "groups:\n  - name: g-a\nrules:\n  - id: r-a\n    tool: terminal\n    pattern: \"aaa\"\n    action: allow\n    group: g-a\n"
	b := "groups:\n  - name: g-a\n  - name: g-b\nrules:\n  - id: r-b\n    tool: terminal\n    pattern: \"bbb\"\n    action: block\n    group: g-b\n"
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(a), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
	pc, err := LoadMerged(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Rules) != 2 || len(pc.Groups) != 2 {
		t.Fatalf("expected 2 rules 2 groups, got %+v", pc)
	}
	if pc.Rules[0].ID != "r-a" || pc.Rules[1].ID != "r-b" {
		t.Fatalf("file order not preserved: %+v", pc.Rules)
	}
}
