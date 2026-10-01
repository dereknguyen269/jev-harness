package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetSetting("nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get err=%v want ErrNoRows", err)
	}
	if err := s.SetSetting("k", "v"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.GetSetting("k")
	if err != nil || got != "v" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if err := s.SetSetting("k", "v2"); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	got, _ = s.GetSetting("k")
	if got != "v2" {
		t.Fatalf("got=%q", got)
	}
}

func TestApprovalTTLSetting(t *testing.T) {
	s := openTest(t)
	if _, ok := s.GetApprovalTTLSeconds(); ok {
		t.Fatal("unset should report ok=false")
	}
	if err := s.SetSetting(SettingApprovalTTLSeconds, "90"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if n, ok := s.GetApprovalTTLSeconds(); !ok || n != 90 {
		t.Fatalf("got=%d ok=%v", n, ok)
	}
	for _, bad := range []string{"abc", "", "0", "-5", "  "} {
		if err := s.SetSetting(SettingApprovalTTLSeconds, bad); err != nil {
			t.Fatalf("set %q: %v", bad, err)
		}
		if _, ok := s.GetApprovalTTLSeconds(); ok {
			t.Fatalf("%q should report ok=false", bad)
		}
	}
}

func TestRuleTimeoutRoundTrip(t *testing.T) {
	s := openTest(t)
	r, err := s.UpsertRule(Rule{Tool: "terminal", Pattern: "probe-x", Action: "approval_required", ApprovalTimeout: 120})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.GetRule(r.ID)
	if err != nil || got.ApprovalTimeout != 120 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	list, err := s.ListRules()
	if err != nil || len(list) != 1 || list[0].ApprovalTimeout != 120 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	// Seed path carries the timeout too.
	pc, err := s.LoadPolicyConfig()
	if err != nil || len(pc.Rules) != 1 || pc.Rules[0].ApprovalTimeout != 120 {
		t.Fatalf("loaded=%+v err=%v", pc, err)
	}
	// Resetting to 0 restores the default.
	r.ApprovalTimeout = 0
	if _, err := s.UpsertRule(r); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, _ = s.GetRule(r.ID)
	if got.ApprovalTimeout != 0 {
		t.Fatalf("got=%+v", got)
	}
}

func TestMigrateOldDBWithoutTimeoutColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mig.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Simulate a pre-timeout database by dropping the fresh column and
	// inserting a legacy row.
	if _, err := s.db.Exec(`ALTER TABLE rules DROP COLUMN approval_timeout`); err != nil {
		s.Close()
		t.Skipf("sqlite drop column unsupported: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO rules (id, tool, pattern, action) VALUES ('old', 'terminal', 'legacy-probe', 'block')`); err != nil {
		s.Close()
		t.Fatalf("insert legacy row: %v", err)
	}
	s.Close()
	// Reopening the same file must migrate: legacy row survives with
	// timeout 0, and timeout writes work afterwards.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	got, err := s2.GetRule("old")
	if err != nil || got.Pattern != "legacy-probe" || got.ApprovalTimeout != 0 {
		t.Fatalf("legacy row=%+v err=%v", got, err)
	}
	if _, err := s2.UpsertRule(Rule{ID: "new", Tool: "terminal", Pattern: "n", Action: "approval_required", ApprovalTimeout: 75}); err != nil {
		t.Fatalf("upsert after migrate: %v", err)
	}
	got2, err := s2.GetRule("new")
	if err != nil || got2.ApprovalTimeout != 75 {
		t.Fatalf("new row=%+v err=%v", got2, err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idem.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.UpsertRule(Rule{ID: "keep", Tool: "terminal", Pattern: "k", Action: "block"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	s.Close()
	// Reopening runs the migration again: rows and timeouts survive.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	list, err := s2.ListRules()
	if err != nil || len(list) != 1 || list[0].ID != "keep" || list[0].ApprovalTimeout != 0 {
		t.Fatalf("list=%v err=%v", list, err)
	}
}
