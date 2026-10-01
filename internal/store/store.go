// Package store is the SQLite persistence layer for the dashboard.
//
// The rules table is the runtime source of truth for policy: on first run
// (empty table) it is seeded from the YAML policy file, and YAML remains the
// fallback for offline CLI commands (check, eval, policy test) which never
// open the database.
//
// The users table is attribution-only: name/email/role rows for audit
// display. There is no auth in the gateway, so nothing enforces identity.
package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/policy"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps a SQLite database. Open failure must never block the gateway:
// callers log the error and continue YAML-only.
type Store struct {
	db *sql.DB
}

// Open creates parent dirs, opens (or creates) the database, and applies
// the schema. Safe to call when the file already exists.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store: empty db path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: mkdir %s: %w", dir, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	if err := migrateRulesApprovalTimeout(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrateRulesApprovalTimeout adds rules.approval_timeout to databases
// created before the column existed (fresh DBs already carry it via the
// schema). Runs on every open; a no-op once the column is present.
func migrateRulesApprovalTimeout(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(rules)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "approval_timeout" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE rules ADD COLUMN approval_timeout INTEGER NOT NULL DEFAULT 0`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// ---- users ----

type User struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	APIKey    string `json:"api_key"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

func scanUser(scanner interface {
	Scan(dest ...any) error
}) (User, error) {
	var u User
	var active int
	var err error = scanner.Scan(&u.ID, &u.Name, &u.Email, &u.Role, &u.APIKey, &active, &u.CreatedAt)
	u.Active = active != 0
	return u, err
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, name, email, role, api_key, active, created_at
		FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateUser(name, email, role string) (User, error) {
	u := User{
		ID:        uuid.NewString(),
		Name:      strings.TrimSpace(name),
		Email:     strings.TrimSpace(email),
		Role:      strings.TrimSpace(role),
		APIKey:    uuid.NewString(),
		Active:    true,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if u.Name == "" {
		return User{}, fmt.Errorf("store: user name required")
	}
	if u.Email != "" {
		if exists, err := s.emailTaken(u.Email, ""); err != nil {
			return User{}, err
		} else if exists {
			return User{}, fmt.Errorf("store: email already taken")
		}
	}
	if u.Role == "" {
		u.Role = "viewer"
	}
	_, err := s.db.Exec(`INSERT INTO users (id, name, email, role, api_key, active, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)`,
		u.ID, u.Name, u.Email, u.Role, u.APIKey, u.CreatedAt)
	return u, err
}

func (s *Store) UpdateUser(u User) error {
	active := 0
	if u.Active {
		active = 1
	}
	res, err := s.db.Exec(`UPDATE users SET name=?, email=?, role=?, active=? WHERE id=?`,
		strings.TrimSpace(u.Name), strings.TrimSpace(u.Email), strings.TrimSpace(u.Role), active, u.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteUser(id string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// emailTaken reports whether addr is used by another user (excludeID skips
// one ID; pass "" to skip nobody).
func (s *Store) emailTaken(addr, excludeID string) (bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM users WHERE email = ? COLLATE NOCASE`, strings.TrimSpace(addr)).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return id != excludeID, nil
}

// ---- rules ----

// Rule mirrors policy.Rule 1:1 (plus Source, which records provenance).
type Rule struct {
	ID          string `json:"id"`
	Tool        string `json:"tool"`
	Pattern     string `json:"pattern"`
	Action      string `json:"action"`
	Priority    int    `json:"priority"`
	Group       string `json:"group"`
	Category    string `json:"category"`
	Business    string `json:"business"`
	Task        string `json:"task"`
	Description string `json:"description"`
	Source      string `json:"source"`
	// ApprovalTimeout is the per-rule approval TTL in seconds (0 = default).
	ApprovalTimeout int `json:"approval_timeout"`
}

func (s *Store) ListRules() ([]Rule, error) {
	rows, err := s.db.Query(`SELECT id, tool, pattern, action, priority, group_name,
		category, business, task, description, source, approval_timeout FROM rules ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Tool, &r.Pattern, &r.Action, &r.Priority,
			&r.Group, &r.Category, &r.Business, &r.Task, &r.Description, &r.Source,
			&r.ApprovalTimeout); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRule(id string) (Rule, error) {
	var r Rule
	err := s.db.QueryRow(`SELECT id, tool, pattern, action, priority, group_name,
		category, business, task, description, source, approval_timeout FROM rules WHERE id=?`, id).Scan(
		&r.ID, &r.Tool, &r.Pattern, &r.Action, &r.Priority,
		&r.Group, &r.Category, &r.Business, &r.Task, &r.Description, &r.Source,
		&r.ApprovalTimeout)
	return r, err
}

// UpsertRule inserts or replaces a rule. The caller must validate
// r.Action and r.Pattern; a blank ID mints a uuid.
func (s *Store) UpsertRule(r Rule) (Rule, error) {
	if strings.TrimSpace(r.ID) == "" {
		r.ID = uuid.NewString()
	}
	if r.Source == "" {
		r.Source = "db"
	}
	_, err := s.db.Exec(`INSERT INTO rules
		(id, tool, pattern, action, priority, group_name, category, business, task, description, source, approval_timeout)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET tool=excluded.tool, pattern=excluded.pattern,
		action=excluded.action, priority=excluded.priority, group_name=excluded.group_name,
		category=excluded.category, business=excluded.business, task=excluded.task,
		description=excluded.description, source=excluded.source,
		approval_timeout=excluded.approval_timeout`,
		r.ID, r.Tool, r.Pattern, r.Action, r.Priority, r.Group,
		r.Category, r.Business, r.Task, r.Description, r.Source, r.ApprovalTimeout)
	return r, err
}

func (s *Store) DeleteRule(id string) error {
	res, err := s.db.Exec(`DELETE FROM rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CountRules reports how many rules the DB holds. Zero means "not seeded".
func (s *Store) CountRules() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM rules`).Scan(&n)
	return n, err
}

// Seed copies YAML rules into an empty table so the dashboard shows the
// current policy on first run. It refuses to touch a non-empty table.
func (s *Store) Seed(pc *policy.PolicyConfig) (int, error) {
	n, err := s.CountRules()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck — only fires on early return
	for _, r := range pc.Rules {
		if _, err := tx.Exec(`INSERT INTO rules
			(id, tool, pattern, action, priority, group_name, category, business, task, description, source, approval_timeout)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'yaml', ?)`,
			r.ID, r.Tool, r.Pattern, r.Action, r.Priority, r.Group,
			r.Category, r.Business, r.Task, r.Description, r.ApprovalTimeout); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(pc.Rules), nil
}

// LoadPolicyConfig converts DB rows back into a PolicyConfig for the
// engine. Returns (nil, nil) when the table is empty so the caller can
// fall back to YAML.
func (s *Store) LoadPolicyConfig() (*policy.PolicyConfig, error) {
	rules, err := s.ListRules()
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}
	desc, err := s.groupDescriptions()
	if err != nil {
		return nil, err
	}
	pc := &policy.PolicyConfig{}
	seenGroup := map[string]bool{}
	addGroup := func(name string) {
		if name == "" || seenGroup[name] {
			return
		}
		seenGroup[name] = true
		pc.Groups = append(pc.Groups, policy.GroupDef{Name: name, Description: desc[name]})
	}
	for _, r := range rules {
		pc.Rules = append(pc.Rules, policy.Rule{
			ID: r.ID, Tool: r.Tool, Pattern: r.Pattern, Action: r.Action,
			Priority: r.Priority, Group: r.Group, Category: r.Category,
			Business: r.Business, Task: r.Task, Description: r.Description,
			ApprovalTimeout: r.ApprovalTimeout,
		})
		addGroup(r.Group)
	}
	// Defined groups with no rules yet still count as declared groups.
	rest := make([]string, 0, len(desc))
	for name := range desc {
		if !seenGroup[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		addGroup(name)
	}
	return pc, nil
}

// ---- groups ----

// Group is a named policy bucket with a human description. Rules reference
// groups by name; deleting a definition leaves existing rules untouched
// (their group simply becomes undeclared).
type Group struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT name, description FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.Name, &g.Description); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpsertGroup inserts or replaces a group definition. Blank names rejected.
func (s *Store) UpsertGroup(g Group) (Group, error) {
	g.Name = strings.TrimSpace(g.Name)
	g.Description = strings.TrimSpace(g.Description)
	if g.Name == "" {
		return Group{}, fmt.Errorf("store: group name required")
	}
	_, err := s.db.Exec(`INSERT INTO groups (name, description) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET description=excluded.description`,
		g.Name, g.Description)
	return g, err
}

func (s *Store) GetGroup(name string) (Group, error) {
	var g Group
	err := s.db.QueryRow(`SELECT name, description FROM groups WHERE name=?`, name).Scan(&g.Name, &g.Description)
	return g, err
}

func (s *Store) DeleteGroup(name string) error {
	res, err := s.db.Exec(`DELETE FROM groups WHERE name=?`, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SeedGroups copies YAML group declarations into an empty groups table.
// Independent of rule seeding; refuses a non-empty table.
func (s *Store) SeedGroups(pc *policy.PolicyConfig) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM groups`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck — only fires on early return
	for _, g := range pc.Groups {
		if strings.TrimSpace(g.Name) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO groups (name, description) VALUES (?, ?)`,
			strings.TrimSpace(g.Name), strings.TrimSpace(g.Description)); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(pc.Groups), nil
}

// groupDescriptions returns defined name → description for LoadPolicyConfig.
func (s *Store) groupDescriptions() (map[string]string, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(groups))
	for _, g := range groups {
		out[g.Name] = g.Description
	}
	return out, nil
}

// ---- categories ----

// Category is a named scope bucket with a human description. Rules reference
// categories by name; deleting a definition leaves existing rules untouched.
type Category struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Store) ListCategories() ([]Category, error) {
	rows, err := s.db.Query(`SELECT name, description FROM categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Name, &c.Description); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertCategory inserts or replaces a category definition. Blank names rejected.
func (s *Store) UpsertCategory(c Category) (Category, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if c.Name == "" {
		return Category{}, fmt.Errorf("store: category name required")
	}
	_, err := s.db.Exec(`INSERT INTO categories (name, description) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET description=excluded.description`,
		c.Name, c.Description)
	return c, err
}

func (s *Store) GetCategory(name string) (Category, error) {
	var c Category
	err := s.db.QueryRow(`SELECT name, description FROM categories WHERE name=?`, name).Scan(&c.Name, &c.Description)
	return c, err
}

func (s *Store) DeleteCategory(name string) error {
	res, err := s.db.Exec(`DELETE FROM categories WHERE name=?`, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DefaultCategoryDescriptions documents the 4 category values used by the
// bundled policy (configs/policy.yaml tags every rule with one of these).
// The YAML declares no categories header by design; these defaults keep the
// dashboard Categories tab useful on first seed instead of blank.
var DefaultCategoryDescriptions = map[string]string{
	"safety":       "Irreversible / privileged operations",
	"secrets":      "Credential and key material",
	"productivity": "Everyday dev reads/writes",
	"network":      "External network access",
}

// SeedCategories populates an empty categories table from the categories
// already used by rules (YAML declares no category header). Known categories
// get their default description; unknown ones start blank for the dashboard
// to fill in. Refuses a non-empty table.
func (s *Store) SeedCategories(pc *policy.PolicyConfig) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	seen := map[string]bool{}
	var names []string
	for _, r := range pc.Rules {
		name := strings.TrimSpace(r.Category)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck — only fires on early return
	for _, name := range names {
		if _, err := tx.Exec(`INSERT INTO categories (name, description) VALUES (?, ?)`,
			name, DefaultCategoryDescriptions[strings.TrimSpace(name)]); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(names), nil
}

// SyncDefaults merges bundled YAML defaults into a seeded database so
// existing installs can adopt a restructured policy without losing custom
// rules. Upsert semantics per table:
//   - rules: every YAML rule is upserted with source='yaml' (refreshes stale
//     patterns/actions/groups on restructures); rows with source='db'
//     (dashboard-created customs) are never touched.
//   - groups: every YAML group header is upserted (description refreshed).
//   - categories: rule-derived names are inserted when missing; existing
//     rows keep their description unless blank (dashboard edits win).
// Returns (rulesSynced, groupsSynced, categoriesSynced).
func (s *Store) SyncDefaults(pc *policy.PolicyConfig) (int, int, int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback() //nolint:errcheck — only fires on early return
	rules := 0
	for _, r := range pc.Rules {
		if strings.TrimSpace(r.ID) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO rules
			(id, tool, pattern, action, priority, group_name, category, business, task, description, source, approval_timeout)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'yaml', ?)
			ON CONFLICT(id) DO UPDATE SET tool=excluded.tool, pattern=excluded.pattern,
			action=excluded.action, priority=excluded.priority, group_name=excluded.group_name,
			category=excluded.category, business=excluded.business, task=excluded.task,
			description=excluded.description, source='yaml',
			approval_timeout=excluded.approval_timeout
			WHERE rules.source = 'yaml' OR rules.source = ''`,
			r.ID, r.Tool, r.Pattern, r.Action, r.Priority, r.Group,
			r.Category, r.Business, r.Task, r.Description, r.ApprovalTimeout); err != nil {
			return 0, 0, 0, err
		}
		rules++
	}
	groups := 0
	for _, g := range pc.Groups {
		if strings.TrimSpace(g.Name) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO groups (name, description) VALUES (?, ?)
			ON CONFLICT(name) DO UPDATE SET description=excluded.description`,
			strings.TrimSpace(g.Name), strings.TrimSpace(g.Description)); err != nil {
			return 0, 0, 0, err
		}
		groups++
	}
	cats := 0
	seen := map[string]bool{}
	for _, r := range pc.Rules {
		name := strings.TrimSpace(r.Category)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if _, err := tx.Exec(`INSERT INTO categories (name, description) VALUES (?, ?)
			ON CONFLICT(name) DO UPDATE SET description=excluded.description
			WHERE categories.description = ''`,
			name, DefaultCategoryDescriptions[name]); err != nil {
			return 0, 0, 0, err
		}
		cats++
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, err
	}
	return rules, groups, cats, nil
}

// ReplaceWith wipes all rules, groups, and categories and re-seeds from pc
// (rules land with source='yaml'). Users are untouched. This destroys
// dashboard-created custom rules — callers must confirm explicitly.
func (s *Store) ReplaceWith(pc *policy.PolicyConfig) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck — only fires on early return
	for _, table := range []string{"rules", "groups", "categories"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return 0, err
		}
	}
	for _, r := range pc.Rules {
		if strings.TrimSpace(r.ID) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO rules
			(id, tool, pattern, action, priority, group_name, category, business, task, description, source, approval_timeout)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'yaml', ?)`,
			r.ID, r.Tool, r.Pattern, r.Action, r.Priority, r.Group,
			r.Category, r.Business, r.Task, r.Description, r.ApprovalTimeout); err != nil {
			return 0, err
		}
	}
	for _, g := range pc.Groups {
		if strings.TrimSpace(g.Name) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO groups (name, description) VALUES (?, ?)`,
			strings.TrimSpace(g.Name), strings.TrimSpace(g.Description)); err != nil {
			return 0, err
		}
	}
	seen := map[string]bool{}
	for _, r := range pc.Rules {
		name := strings.TrimSpace(r.Category)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if _, err := tx.Exec(`INSERT INTO categories (name, description) VALUES (?, ?)`,
			name, DefaultCategoryDescriptions[name]); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(pc.Rules), nil
}

// PruneStaleDefaults deletes source='yaml' rules whose ID no longer exists
// in the bundled YAML (e.g. the 23 per-subcommand allow-git-* rules removed
// by consolidation). Custom source='db' rules are never touched. Returns the
// number of rows removed.
func (s *Store) PruneStaleDefaults(pc *policy.PolicyConfig) (int, error) {
	keep := make(map[string]bool, len(pc.Rules))
	for _, r := range pc.Rules {
		if strings.TrimSpace(r.ID) != "" {
			keep[r.ID] = true
		}
	}
	rows, err := s.db.Query(`SELECT id FROM rules WHERE source = 'yaml' OR source = ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range stale {
		if _, err := s.db.Exec(`DELETE FROM rules WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}
