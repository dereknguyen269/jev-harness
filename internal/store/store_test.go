package store

import (
	"path/filepath"
	"testing"

	"github.com/dereknguyen269/jev-harness/internal/policy"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSeedAndLoad(t *testing.T) {
	s := openTest(t)
	pc := &policy.PolicyConfig{
		Groups: []policy.GroupDef{{Name: "g1", Description: "first"}},
		Rules: []policy.Rule{
			{ID: "r1", Tool: "terminal", Pattern: "rm", Action: "block", Priority: 100, Group: "g1"},
			{ID: "r2", Tool: "read_file", Pattern: ".*", Action: "allow", Priority: 10},
		},
	}
	n, err := s.Seed(pc)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n != 2 {
		t.Fatalf("seeded=%d want 2", n)
	}
	// Second seed is a no-op on a non-empty table.
	if n, _ := s.Seed(pc); n != 0 {
		t.Fatalf("re-seed=%d want 0", n)
	}
	loaded, err := s.LoadPolicyConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil || len(loaded.Rules) != 2 {
		t.Fatalf("rules=%+v", loaded)
	}
	if loaded.Rules[0].ID != "r1" || loaded.Rules[0].Action != "block" || loaded.Rules[0].Group != "g1" {
		t.Fatalf("round-trip mismatch: %+v", loaded.Rules[0])
	}
	if len(loaded.Groups) != 1 || loaded.Groups[0].Name != "g1" {
		t.Fatalf("groups=%+v", loaded.Groups)
	}
}

func TestLoadEmptyReturnsNil(t *testing.T) {
	s := openTest(t)
	pc, err := s.LoadPolicyConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if pc != nil {
		t.Fatalf("expected nil config for empty table, got %+v", pc)
	}
}

func TestRuleCRUD(t *testing.T) {
	s := openTest(t)
	r, err := s.UpsertRule(Rule{Tool: "terminal", Pattern: "sudo", Action: "approval_required", Priority: 85})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if r.ID == "" || r.Source != "db" {
		t.Fatalf("got %+v", r)
	}
	got, err := s.GetRule(r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Pattern != "sudo" {
		t.Fatalf("got %+v", got)
	}
	r.Action = "block"
	if _, err := s.UpsertRule(r); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.GetRule(r.ID)
	if got.Action != "block" {
		t.Fatalf("got %+v", got)
	}
	if err := s.DeleteRule(r.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetRule(r.ID); err == nil {
		t.Fatal("expected error for deleted rule")
	}
	if err := s.DeleteRule("missing"); err == nil {
		t.Fatal("expected error for missing rule")
	}
}

func TestUserCRUD(t *testing.T) {
	s := openTest(t)
	u, err := s.CreateUser("ada", "ada@example.com", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == "" || u.APIKey == "" || !u.Active || u.Role != "admin" {
		t.Fatalf("got %+v", u)
	}
	if _, err := s.CreateUser("", "", ""); err == nil {
		t.Fatal("expected error for blank name")
	}
	users, err := s.ListUsers()
	if err != nil || len(users) != 1 {
		t.Fatalf("users=%v err=%v", users, err)
	}
	u.Active = false
	u.Role = "viewer"
	if err := s.UpdateUser(u); err != nil {
		t.Fatalf("update: %v", err)
	}
	users, _ = s.ListUsers()
	if users[0].Active || users[0].Role != "viewer" {
		t.Fatalf("got %+v", users[0])
	}
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	users, _ = s.ListUsers()
	if len(users) != 0 {
		t.Fatalf("users=%v", users)
	}
}

func TestGroupCRUD(t *testing.T) {
	s := openTest(t)
	g, err := s.UpsertGroup(Group{Name: "  safety  ", Description: " critical stuff "})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if g.Name != "safety" || g.Description != "critical stuff" {
		t.Fatalf("not trimmed: %+v", g)
	}
	if _, err := s.UpsertGroup(Group{Name: "   "}); err == nil {
		t.Fatal("expected error for blank name")
	}
	if _, err := s.UpsertGroup(Group{Name: "safety", Description: "updated"}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	groups, err := s.ListGroups()
	if err != nil || len(groups) != 1 || groups[0].Description != "updated" {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
	if err := s.DeleteGroup("safety"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteGroup("safety"); err == nil {
		t.Fatal("expected error for missing group")
	}
}

func TestSeedGroupsAndDescriptions(t *testing.T) {
	s := openTest(t)
	pc := &policy.PolicyConfig{
		Groups: []policy.GroupDef{
			{Name: "g1", Description: "first group"},
			{Name: "g2"},
		},
		Rules: []policy.Rule{
			{ID: "r1", Tool: "terminal", Pattern: "rm", Action: "block", Group: "g1"},
			{ID: "r9", Tool: "terminal", Pattern: "x", Action: "allow", Group: "ungrouped-in-yaml"},
		},
	}
	if n, err := s.SeedGroups(pc); err != nil || n != 2 {
		t.Fatalf("seed=%d err=%v", n, err)
	}
	if n, _ := s.SeedGroups(pc); n != 0 {
		t.Fatalf("re-seed=%d want 0", n)
	}
	if _, err := s.Seed(pc); err != nil {
		t.Fatalf("rule seed: %v", err)
	}
	loaded, err := s.LoadPolicyConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	byName := map[string]string{}
	for _, g := range loaded.Groups {
		byName[g.Name] = g.Description
	}
	if byName["g1"] != "first group" {
		t.Fatalf("description lost: %+v", loaded.Groups)
	}
	if _, ok := byName["g2"]; !ok {
		t.Fatalf("rule-less group missing: %+v", loaded.Groups)
	}
	if _, ok := byName["ungrouped-in-yaml"]; !ok {
		t.Fatalf("rule-only group missing: %+v", loaded.Groups)
	}
}

func TestUserEmailUnique(t *testing.T) {
	s := openTest(t)
	if _, err := s.CreateUser("ada", "ada@example.com", "admin"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Duplicate email (case-insensitive) rejected.
	if _, err := s.CreateUser("ada2", "ADA@EXAMPLE.COM", "viewer"); err == nil {
		t.Fatal("expected error for duplicate email")
	}
	// Blank email stays optional (login is key-only).
	if _, err := s.CreateUser("bob", "", "viewer"); err != nil {
		t.Fatalf("blank email create: %v", err)
	}
	if _, err := s.CreateUser("bob2", "", "viewer"); err != nil {
		t.Fatalf("second blank email create: %v", err)
	}
}

func TestCategoryCRUD(t *testing.T) {
	s := openTest(t)
	c, err := s.UpsertCategory(Category{Name: "  safety  ", Description: " scope "})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if c.Name != "safety" || c.Description != "scope" {
		t.Fatalf("not trimmed: %+v", c)
	}
	if _, err := s.UpsertCategory(Category{Name: "   "}); err == nil {
		t.Fatal("expected error for blank name")
	}
	got, err := s.GetCategory("safety")
	if err != nil || got.Description != "scope" {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	if err := s.DeleteCategory("safety"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteCategory("safety"); err == nil {
		t.Fatal("expected error for missing category")
	}
}

func TestSyncDefaultsMergeKeepsCustom(t *testing.T) {
	s := openTest(t)
	pc := &policy.PolicyConfig{
		Groups: []policy.GroupDef{{Name: "g1", Description: "first"}},
		Rules: []policy.Rule{
			{ID: "r1", Tool: "terminal", Pattern: "rm", Action: "block", Group: "g1", Category: "safety"},
		},
	}
	if _, err := s.Seed(pc); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.SeedGroups(pc); err != nil {
		t.Fatalf("seed groups: %v", err)
	}
	if _, err := s.SeedCategories(pc); err != nil {
		t.Fatalf("seed cats: %v", err)
	}
	// Dashboard custom: becomes source='db', must survive sync+prune.
	custom, err := s.UpsertRule(Rule{Tool: "terminal", Pattern: "custom", Action: "allow"})
	if err != nil {
		t.Fatalf("custom upsert: %v", err)
	}
	// Dashboard edit of a yaml rule flips it to source='db' (edits win).
	edited, err := s.GetRule("r1")
	if err != nil {
		t.Fatalf("get r1: %v", err)
	}
	edited.Pattern = "rm-edited"
	if _, err := s.UpsertRule(Rule{ID: edited.ID, Tool: edited.Tool, Pattern: "rm-edited",
		Action: edited.Action, Group: edited.Group, Category: edited.Category}); err != nil {
		t.Fatalf("edit r1: %v", err)
	}
	newPC := &policy.PolicyConfig{
		Groups: []policy.GroupDef{{Name: "g1", Description: "first v2"}, {Name: "g2"}},
		Rules: []policy.Rule{
			{ID: "r1", Tool: "terminal", Pattern: "rm-v2", Action: "block", Group: "g1", Category: "safety"},
			{ID: "r2", Tool: "terminal", Pattern: "sudo", Action: "approval_required", Group: "g2", Category: "safety"},
		},
	}
	rn, gn, cn, err := s.SyncDefaults(newPC)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if rn != 2 || gn != 2 || cn != 1 {
		t.Fatalf("sync=%d,%d,%d", rn, gn, cn)
	}
	// Edited yaml rule keeps the dashboard pattern (source=db wins).
	got, _ := s.GetRule("r1")
	if got.Pattern != "rm-edited" {
		t.Fatalf("edit lost: %+v", got)
	}
	// Custom survives.
	if _, err := s.GetRule(custom.ID); err != nil {
		t.Fatalf("custom lost: %v", err)
	}
	// Fresh yaml rule applied.
	got2, _ := s.GetRule("r2")
	if got2.Pattern != "sudo" || got2.Source != "yaml" {
		t.Fatalf("r2 not synced: %+v", got2)
	}
	// Group description refreshed.
	groups, _ := s.ListGroups()
	byName := map[string]string{}
	for _, g := range groups {
		byName[g.Name] = g.Description
	}
	if byName["g1"] != "first v2" || byName["g2"] != "" {
		t.Fatalf("groups=%v", byName)
	}
}

func TestPruneStaleDefaultsOnlyYaml(t *testing.T) {
	s := openTest(t)
	pc := &policy.PolicyConfig{
		Rules: []policy.Rule{
			{ID: "keep", Tool: "terminal", Pattern: "a", Action: "allow"},
			{ID: "stale", Tool: "terminal", Pattern: "b", Action: "allow"},
		},
	}
	if _, err := s.Seed(pc); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Dashboard upsert over id "stale" flips it to source='db'.
	if _, err := s.UpsertRule(Rule{ID: "stale", Tool: "terminal", Pattern: "mine", Action: "block"}); err != nil {
		t.Fatalf("custom: %v", err)
	}
	// Re-add a pure yaml stale row to prove only yaml rows prune.
	if _, err := s.db.Exec(`INSERT INTO rules (id, tool, pattern, action, source)
		VALUES ('stale-yaml', 'terminal', 'b', 'allow', 'yaml')`); err != nil {
		t.Fatalf("insert stale: %v", err)
	}
	n, err := s.PruneStaleDefaults(&policy.PolicyConfig{
		Rules: []policy.Rule{{ID: "keep", Tool: "terminal", Pattern: "a", Action: "allow"}},
	})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned=%d want 1", n)
	}
	if _, err := s.GetRule("stale-yaml"); err == nil {
		t.Fatal("stale-yaml should be gone")
	}
	// source=db row with colliding id survives.
	got, err := s.GetRule("stale")
	if err != nil || got.Pattern != "mine" {
		t.Fatalf("custom overwritten: %+v err=%v", got, err)
	}
	if _, err := s.GetRule("keep"); err != nil {
		t.Fatalf("keep lost: %v", err)
	}
}

func TestReplaceWithWipesPolicyKeepsUsers(t *testing.T) {
	s := openTest(t)
	pc := &policy.PolicyConfig{
		Groups: []policy.GroupDef{{Name: "g1"}},
		Rules: []policy.Rule{
			{ID: "r1", Tool: "terminal", Pattern: "a", Action: "allow", Category: "safety"},
		},
	}
	if _, err := s.Seed(pc); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.UpsertRule(Rule{Tool: "terminal", Pattern: "custom", Action: "block"}); err != nil {
		t.Fatalf("custom: %v", err)
	}
	if _, err := s.CreateUser("ada", "ada@example.com", "admin"); err != nil {
		t.Fatalf("user: %v", err)
	}
	newPC := &policy.PolicyConfig{
		Groups: []policy.GroupDef{{Name: "g9", Description: "nine"}},
		Rules: []policy.Rule{
			{ID: "n1", Tool: "terminal", Pattern: "z", Action: "block", Group: "g9", Category: "productivity"},
		},
	}
	n, err := s.ReplaceWith(newPC)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if n != 1 {
		t.Fatalf("n=%d want 1", n)
	}
	rules, _ := s.ListRules()
	if len(rules) != 1 || rules[0].ID != "n1" || rules[0].Source != "yaml" {
		t.Fatalf("rules=%+v", rules)
	}
	groups, _ := s.ListGroups()
	if len(groups) != 1 || groups[0].Name != "g9" {
		t.Fatalf("groups=%+v", groups)
	}
	cats, _ := s.ListCategories()
	if len(cats) != 1 || cats[0].Name != "productivity" || cats[0].Description == "" {
		t.Fatalf("cats=%+v", cats)
	}
	users, _ := s.ListUsers()
	if len(users) != 1 {
		t.Fatalf("users=%v", users)
	}
}

func TestSeedCategoriesDefaultDescriptions(t *testing.T) {
	s := openTest(t)
	pc := &policy.PolicyConfig{
		Rules: []policy.Rule{
			{ID: "r1", Tool: "terminal", Pattern: "a", Action: "block", Category: "safety"},
			{ID: "r2", Tool: "terminal", Pattern: "b", Action: "allow", Category: "mycustom"},
		},
	}
	if n, err := s.SeedCategories(pc); err != nil || n != 2 {
		t.Fatalf("seed=%d err=%v", n, err)
	}
	got, err := s.GetCategory("safety")
	if err != nil || got.Description == "" {
		t.Fatalf("safety description missing: %+v err=%v", got, err)
	}
	got2, err := s.GetCategory("mycustom")
	if err != nil || got2.Description != "" {
		t.Fatalf("custom should stay blank: %+v err=%v", got2, err)
	}
}

func TestSeedCategoriesFromRules(t *testing.T) {
	s := openTest(t)
	pc := &policy.PolicyConfig{
		Rules: []policy.Rule{
			{ID: "r1", Tool: "terminal", Pattern: "rm", Action: "block", Category: "safety"},
			{ID: "r2", Tool: "terminal", Pattern: "x", Action: "allow", Category: "safety"},
			{ID: "r3", Tool: "terminal", Pattern: "y", Action: "allow"},
		},
	}
	if n, err := s.SeedCategories(pc); err != nil || n != 1 {
		t.Fatalf("seed=%d err=%v", n, err)
	}
	if n, _ := s.SeedCategories(pc); n != 0 {
		t.Fatalf("re-seed=%d want 0", n)
	}
	cats, err := s.ListCategories()
	if err != nil || len(cats) != 1 || cats[0].Name != "safety" ||
		cats[0].Description != DefaultCategoryDescriptions["safety"] {
		t.Fatalf("cats=%v err=%v", cats, err)
	}
}
