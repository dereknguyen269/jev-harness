package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/dereknguyen269/jev-harness/internal/approval"
	"github.com/dereknguyen269/jev-harness/internal/audit"
	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/harness"
	"github.com/dereknguyen269/jev-harness/internal/jev"
	"github.com/dereknguyen269/jev-harness/internal/policy"
	"github.com/dereknguyen269/jev-harness/internal/store"
)

// webDist embeds the built React dashboard (web/dist, produced by
// `make ui-build`). The directory must exist at compile time.
var (
	//go:embed web/dist
	webDist embed.FS
	// webFiles is web/dist rooted for http.FileServer.
	webFiles = mustSub(webDist, "web/dist")
)

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// Unreachable when the embed above succeeded, but a nil FS
		// would panic the file server — fail loudly instead.
		panic("server: embed web/dist: " + err.Error())
	}
	return sub
}

type Gateway struct {
	Harness   *harness.Harness
	Approvals *approval.Store
	Store     *store.Store
	AuditPath string
	Timeout   time.Duration
	JevOn     bool
	Version   string
	Groups    []map[string]any
	// SeedPolicy is the bundled YAML policy the DB was seeded from. Set at
	// serve time; the reseed endpoint merges it into the store so running
	// installs adopt restructured defaults. Nil = unknown source.
	SeedPolicy *policy.PolicyConfig
	// AuthToken gates the dashboard (HTML + management API) via
	// Authorization: Bearer. Empty = auth disabled (local-dev default).
	// The agent surface (/v1/check, /v1/policies) always stays open.
	AuthToken string
}

func (g *Gateway) timeout() time.Duration {
	if g.Timeout > 0 {
		return g.Timeout
	}
	return 10 * time.Second
}

func (g *Gateway) Router() *mux.Router {
	r := mux.NewRouter()
	r.HandleFunc("/health", g.handleHealth).Methods("GET")
	r.HandleFunc("/v1/health", g.handleHealth).Methods("GET")
	r.HandleFunc("/v1/check", g.handleCheck).Methods("POST")
	r.HandleFunc("/v1/policies", g.handlePolicies).Methods("GET")
	r.HandleFunc("/v1/auth/status", g.handleAuthStatus).Methods("GET")
	r.HandleFunc("/v1/auth/login", g.handleAuthLogin).Methods("POST")
	r.HandleFunc("/v1/auth/me", g.requireAuth(g.handleAuthMe)).Methods("GET")
	// Everything below is dashboard surface: gated by requireAuth when
	// an auth token is configured. Agent plugins only need /v1/check.
	// Role model: viewer reads; operator also approves + reloads;
	// admin also manages users/rules/groups. Unknown roles read like viewer.
	r.HandleFunc("/v1/approvals", g.requireAuth(g.handleListApprovals)).Methods("GET")
	r.HandleFunc("/v1/approvals/{id}/approve", g.requireRole(g.handleDecide(true), "admin", "operator")).Methods("POST")
	r.HandleFunc("/v1/approvals/{id}/deny", g.requireRole(g.handleDecide(false), "admin", "operator")).Methods("POST")
	r.HandleFunc("/v1/audit", g.requireAuth(g.handleAudit)).Methods("GET")
	r.HandleFunc("/v1/stats", g.requireAuth(g.handleStats)).Methods("GET")
	// Dashboard management API (503 without the SQLite store).
	r.HandleFunc("/v1/users", g.requireAuth(g.handleListUsers)).Methods("GET")
	r.HandleFunc("/v1/users", g.requireRole(g.handleCreateUser, "admin")).Methods("POST")
	r.HandleFunc("/v1/users/{id}", g.requireRole(g.handleUpdateUser, "admin")).Methods("PUT")
	r.HandleFunc("/v1/users/{id}", g.requireRole(g.handleDeleteUser, "admin")).Methods("DELETE")
	r.HandleFunc("/v1/rules", g.requireAuth(g.handleListRules)).Methods("GET")
	r.HandleFunc("/v1/rules", g.requireRole(g.handleUpsertRule, "admin")).Methods("POST")
	r.HandleFunc("/v1/rules/{id}", g.requireRole(g.handleUpsertRuleByID, "admin")).Methods("PUT")
	r.HandleFunc("/v1/rules/{id}", g.requireRole(g.handleDeleteRule, "admin")).Methods("DELETE")
	r.HandleFunc("/v1/policy/reload", g.requireRole(g.handlePolicyReload, "admin", "operator")).Methods("POST")
	r.HandleFunc("/v1/policy/reseed", g.requireRole(g.handlePolicyReseed, "admin")).Methods("POST")
	r.HandleFunc("/v1/jev/calls", g.requireAuth(g.handleJevCalls)).Methods("GET")
	r.HandleFunc("/v1/groups", g.requireAuth(g.handleListGroups)).Methods("GET")
	r.HandleFunc("/v1/groups", g.requireRole(g.handleCreateGroup, "admin")).Methods("POST")
	r.HandleFunc("/v1/groups/{name}", g.requireRole(g.handleUpdateGroup, "admin")).Methods("PUT")
	r.HandleFunc("/v1/groups/{name}", g.requireRole(g.handleDeleteGroup, "admin")).Methods("DELETE")
	r.HandleFunc("/v1/categories", g.requireAuth(g.handleListCategories)).Methods("GET")
	r.HandleFunc("/v1/categories", g.requireRole(g.handleCreateCategory, "admin")).Methods("POST")
	r.HandleFunc("/v1/categories/{name}", g.requireRole(g.handleUpdateCategory, "admin")).Methods("PUT")
	r.HandleFunc("/v1/categories/{name}", g.requireRole(g.handleDeleteCategory, "admin")).Methods("DELETE")
	// Built dashboard assets. The HTML/JS shell is intentionally OPEN
	// (it holds no secrets) so the login screen can load; every data
	// endpoint above enforces auth. Registered last so nothing shadows /v1/*.
	r.PathPrefix("/assets/").Handler(http.StripPrefix("/", http.FileServer(http.FS(webFiles)))).Methods("GET")
	r.HandleFunc("/", g.handleDashboard).Methods("GET")
	return r
}

// requireAuth gates dashboard handlers when AuthToken is set.
// Empty token = auth disabled. Comparison is constant-time.
func (g *Gateway) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.AuthToken == "" {
			next(w, r)
			return
		}
		if _, _, ok := g.resolveBearer(bearerToken(r)); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (g *Gateway) handleAuthStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"auth": g.AuthToken != ""})
}

// requireRole layers role checks over requireAuth: 401 when unauthenticated,
// 403 when authenticated but lacking one of the allowed roles. Auth disabled
// means full access (local-dev default, unchanged).
func (g *Gateway) requireRole(next http.HandlerFunc, allowed ...string) http.HandlerFunc {
	return g.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if g.AuthToken == "" {
			next(w, r)
			return
		}
		_, role, _ := g.resolveBearer(bearerToken(r))
		for _, a := range allowed {
			if role == a {
				next(w, r)
				return
			}
		}
		http.Error(w, "forbidden: requires "+strings.Join(allowed, " or "), http.StatusForbidden)
	})
}

// bearerToken extracts the Bearer credential, or "" when absent.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// resolveBearer identifies the caller: the master token maps to the admin
// identity; otherwise the first active user whose API key matches (constant
// time). No users table hit when the store is absent.
func (g *Gateway) resolveBearer(token string) (name, role string, ok bool) {
	if token == "" {
		return "", "", false
	}
	if g.AuthToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(g.AuthToken)) == 1 {
		return "admin", "admin", true
	}
	if g.Store == nil {
		return "", "", false
	}
	users, err := g.Store.ListUsers()
	if err != nil {
		return "", "", false
	}
	for _, u := range users {
		if !u.Active || u.APIKey == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(u.APIKey)) == 1 {
			return u.Name, u.Role, true
		}
	}
	return "", "", false
}

// resolveLogin verifies a dashboard credential: the master secret or any
// active user's API key. Keys are unique, so no name or email is needed.
func (g *Gateway) resolveLogin(secret string) (string, string, bool) {
	return g.resolveBearer(secret)
}

func (g *Gateway) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	name, role, ok := g.resolveLogin(body.Secret)
	if !ok {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "role": role})
}

func (g *Gateway) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	name, role, _ := g.resolveBearer(bearerToken(r))
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "role": role})
}

func (g *Gateway) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	html, err := fs.ReadFile(webFiles, "index.html")
	if err != nil {
		http.Error(w, "dashboard not built (run make ui-build)", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

// requireStore guards DB-backed handlers: without a store the gateway is
// YAML-only and management writes are unavailable.
func (g *Gateway) requireStore(w http.ResponseWriter) *store.Store {
	if g.Store == nil {
		http.Error(w, "policy store unavailable (YAML-only mode)", http.StatusServiceUnavailable)
		return nil
	}
	return g.Store
}

func (g *Gateway) handleListUsers(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	users, err := s.ListUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if users == nil {
		users = []store.User{}
	}
	// API keys are credentials: only admins may list them.
	// Everyone else sees who exists, not how to sign in as them.
	if g.AuthToken != "" {
		if _, role, _ := g.resolveBearer(bearerToken(r)); role != "admin" {
			for i := range users {
				users[i].APIKey = ""
			}
		}
	}
	writeJSON(w, http.StatusOK, users)
}

func (g *Gateway) handleListGroups(w http.ResponseWriter, _ *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	groups, err := s.ListGroups()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	writeJSON(w, http.StatusOK, groups)
}

func (g *Gateway) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var gdef store.Group
	if err := json.NewDecoder(r.Body).Decode(&gdef); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	saved, err := s.UpsertGroup(gdef)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	g.reloadEngineFromStore()
	writeJSON(w, http.StatusCreated, saved)
}

func (g *Gateway) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var gdef store.Group
	if err := json.NewDecoder(r.Body).Decode(&gdef); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// The name is the key: path wins (rename = create + delete).
	gdef.Name = mux.Vars(r)["name"]
	if _, err := s.UpsertGroup(gdef); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	g.reloadEngineFromStore()
	writeJSON(w, http.StatusOK, gdef)
}

func (g *Gateway) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	if err := s.DeleteGroup(mux.Vars(r)["name"]); err != nil {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}
	g.reloadEngineFromStore()
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleListCategories(w http.ResponseWriter, _ *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	cats, err := s.ListCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cats == nil {
		cats = []store.Category{}
	}
	writeJSON(w, http.StatusOK, cats)
}

func (g *Gateway) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var c store.Category
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	saved, err := s.UpsertCategory(c)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (g *Gateway) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var c store.Category
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// The name is the key: path wins (rename = create + delete).
	c.Name = mux.Vars(r)["name"]
	if _, err := s.UpsertCategory(c); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (g *Gateway) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	if err := s.DeleteCategory(mux.Vars(r)["name"]); err != nil {
		http.Error(w, "category not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	u, err := s.CreateUser(body.Name, body.Email, body.Role)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (g *Gateway) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var u store.User
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	u.ID = mux.Vars(r)["id"]
	if strings.TrimSpace(u.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := s.UpdateUser(u); err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (g *Gateway) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	if err := s.DeleteUser(mux.Vars(r)["id"]); err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleListRules(w http.ResponseWriter, _ *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	rules, err := s.ListRules()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rules == nil {
		rules = []store.Rule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func validRule(r store.Rule) error {
	switch strings.ToLower(strings.TrimSpace(r.Action)) {
	case "allow", "block", "approval_required":
	default:
		return fmt.Errorf("action must be allow, block, or approval_required")
	}
	if r.Pattern != "" {
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("invalid pattern regex: %w", err)
		}
	}
	return nil
}

func (g *Gateway) handleUpsertRule(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var rule store.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := validRule(rule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	saved, err := s.UpsertRule(rule)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	g.reloadEngineFromStore()
	writeJSON(w, http.StatusOK, saved)
}

func (g *Gateway) handleUpsertRuleByID(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	var rule store.Rule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	rule.ID = mux.Vars(r)["id"]
	if err := validRule(rule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	saved, err := s.UpsertRule(rule)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	g.reloadEngineFromStore()
	writeJSON(w, http.StatusOK, saved)
}

func (g *Gateway) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	if err := s.DeleteRule(mux.Vars(r)["id"]); err != nil {
		http.Error(w, "rule not found", http.StatusNotFound)
		return
	}
	g.reloadEngineFromStore()
	w.WriteHeader(http.StatusNoContent)
}

// handlePolicyReload re-reads the DB into the live engine. Failures leave
// the running rule set untouched.
func (g *Gateway) handlePolicyReload(w http.ResponseWriter, _ *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	n, err := g.reloadEngineFromStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reloaded", "rules": n})
}

// handlePolicyReseed merges bundled YAML defaults into the store so the
// dashboard can adopt restructured policies without losing custom rules.
// Body: {"mode": "merge"|"replace", "force": bool}. Replace wipes all policy
// tables and requires force=true. Only admins.
func (g *Gateway) handlePolicyReseed(w http.ResponseWriter, r *http.Request) {
	s := g.requireStore(w)
	if s == nil {
		return
	}
	if g.SeedPolicy == nil {
		http.Error(w, "seed policy unknown (restart with --policy/--profile)", http.StatusConflict)
		return
	}
	var body struct {
		Mode  string `json:"mode"`
		Force bool   `json:"force"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch body.Mode {
	case "", "merge":
		rn, gn, cn, err := s.SyncDefaults(g.SeedPolicy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pn, err := s.PruneStaleDefaults(g.SeedPolicy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n, err := g.reloadEngineFromStore()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "reseeded", "rules": rn, "groups": gn,
			"categories": cn, "pruned": pn, "live": n,
		})
	case "replace":
		if !body.Force {
			http.Error(w, "replace destroys custom rules; confirm with force=true", http.StatusBadRequest)
			return
		}
		n, err := s.ReplaceWith(g.SeedPolicy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		live, err := g.reloadEngineFromStore()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "replaced", "rules": n, "live": live})
	default:
		http.Error(w, "unknown mode (want merge|replace)", http.StatusBadRequest)
	}
}

// reloadEngineFromStore loads the DB rule set into the live policy engine.
// Returns the rule count. A nil/empty DB keeps the current engine as-is.
func (g *Gateway) reloadEngineFromStore() (int, error) {
	if g.Store == nil || g.Harness == nil {
		return 0, nil
	}
	pc, err := g.Store.LoadPolicyConfig()
	if err != nil {
		return 0, err
	}
	if pc == nil {
		return 0, nil
	}
	eng, ok := g.Harness.Policy.(*policy.EngineV2)
	if !ok || eng == nil {
		return 0, fmt.Errorf("policy engine is not reloadable")
	}
	eng.Reload(pc, nil)
	g.Groups = groupMetadataFromStore(pc)
	return len(pc.Rules), nil
}

func groupMetadataFromStore(pc *policy.PolicyConfig) []map[string]any {
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, gd := range pc.Groups {
		if !seen[gd.Name] {
			seen[gd.Name] = true
			m := map[string]any{"name": gd.Name}
			if gd.Description != "" {
				m["description"] = gd.Description
			}
			out = append(out, m)
		}
	}
	return out
}

func (g *Gateway) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": g.Version, "jev": g.JevOn,
	})
}

// handleJevCalls serves recent outbound AI API calls, newest first.
// Default 20, cap 100. Empty array (not an error) when Jev never ran.
func (g *Gateway) handleJevCalls(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = min(n, 100)
		}
	}
	out := []jev.Call{}
	if g.Harness != nil {
		if cl, ok := g.Harness.Judge.(interface{ Calls() []jev.Call }); ok && cl != nil {
			out = cl.Calls()
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, out)
}

type CheckRequest struct {
	Agent struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"agent"`
	Session map[string]string `json:"session"`
	Tool    struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"tool"`
	Context domain.ExecutionContext `json:"context"`
}

func (g *Gateway) handleCheck(w http.ResponseWriter, r *http.Request) {
	var req CheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Tool.Args == nil {
		req.Tool.Args = map[string]any{}
	}
	toolReq := domain.ToolRequest{
		ID:        uuid.NewString(),
		Agent:     domain.AgentInfo{Name: req.Agent.Name, Version: req.Agent.Version, Adapter: req.Agent.Name},
		Tool:      domain.ToolCall{Name: req.Tool.Name, Args: req.Tool.Args},
		Context:   req.Context,
		Timestamp: time.Now(),
	}
	if req.Session != nil {
		toolReq.Session = domain.SessionInfo{
			ID: req.Session["id"], TaskID: req.Session["task_id"], TurnID: req.Session["turn_id"],
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), g.timeout())
	defer cancel()
	writeJSON(w, http.StatusOK, g.Harness.Evaluate(ctx, toolReq))
}

func (g *Gateway) handleListApprovals(w http.ResponseWriter, _ *http.Request) {
	if g.Approvals == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, g.Approvals.List())
}

func (g *Gateway) handleDecide(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g.Approvals == nil {
			http.Error(w, "approvals disabled", http.StatusServiceUnavailable)
			return
		}
		a, ok := g.Approvals.Decide(mux.Vars(r)["id"], approve)
		if !ok {
			http.Error(w, "approval not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

func (g *Gateway) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	events, err := audit.NewReader(g.AuditPath).Filter(
		q.Get("decision"), parseRisk(q.Get("min_risk")),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []domain.AuditEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (g *Gateway) handleStats(w http.ResponseWriter, _ *http.Request) {
	events, err := audit.NewReader(g.AuditPath).All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats := map[string]int{"total": len(events)}
	for _, e := range events {
		stats[e.FinalDecision]++
	}
	writeJSON(w, http.StatusOK, stats)
}

func (g *Gateway) handlePolicies(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")
	groups := g.Groups
	if group != "" {
		filtered := []map[string]any{}
		for _, gd := range groups {
			if gd["name"] == group {
				filtered = append(filtered, gd)
			}
		}
		groups = filtered
	}
	if groups == nil {
		groups = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": g.Version, "groups": groups})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func parseRisk(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
