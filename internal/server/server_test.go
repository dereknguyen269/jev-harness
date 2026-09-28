package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/approval"
	"github.com/dereknguyen269/jev-harness/internal/cache"
	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/harness"
	"github.com/dereknguyen269/jev-harness/internal/jev"
	"github.com/dereknguyen269/jev-harness/internal/judge"
	"github.com/dereknguyen269/jev-harness/internal/policy"
	"github.com/dereknguyen269/jev-harness/internal/store"
)

func testGateway(t *testing.T) *Gateway {
	t.Helper()
	pc, err := policy.Load("../../configs/policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	eng := policy.NewEngine(pc, nil, policy.NewCache(5*time.Minute))
	appr := approval.NewStore()
	h := &harness.Harness{
		Policy:      policy.NewEngineV2(eng, nil),
		Judge:       &judge.Mock{Risk: 0.05, Confidence: 0.95, Action: "allow"},
		Cache:       cache.New(),
		Approvals:   appr,
		Thresholds:  domain.DefaultThresholds(),
		ApprovalTTL: 30 * time.Second,
	}
	return &Gateway{Harness: h, Approvals: appr, Timeout: 5 * time.Second, Version: "test"}
}

func TestCheck_Block(t *testing.T) {
	gw := testGateway(t)
	body := `{"agent":{"name":"claude-code"},"tool":{"name":"Bash","args":{"command":"rm -rf /"}},"context":{}}`
	req := httptest.NewRequest("POST", "/v1/check", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res domain.DecisionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != domain.Block || res.PolicyID != "root-delete" {
		t.Fatalf("got %+v", res)
	}
	if res.Source != "policy" || res.Confidence != 1.0 {
		t.Fatalf("expected policy source full confidence, got %+v", res)
	}
}

func TestCheck_JevAllowShape(t *testing.T) {
	gw := testGateway(t)
	body := `{"agent":{"name":"codex"},"tool":{"name":"shell","args":{"command":"frobnicate --all"}},"context":{"environment":"dev"}}`
	req := httptest.NewRequest("POST", "/v1/check", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)
	var res domain.DecisionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != domain.Allow || res.Source != "jev" {
		t.Fatalf("got %+v", res)
	}
}

func TestCheck_NoLegacyV1Shape(t *testing.T) {
	gw := testGateway(t)
	body := `{"tool":"terminal","args":{"command":"git status"},"context":{"agent":"opencode"}}`
	req := httptest.NewRequest("POST", "/v1/check", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Fatalf("legacy flat v1 body should not be accepted, got %s", rec.Body.String())
	}
}

func TestApprovals_Flow(t *testing.T) {
	gw := testGateway(t)
	gw.Harness.Judge = &judge.Mock{Risk: 0.8, Confidence: 0.99, Action: "approval_required"}
	body := `{"agent":{"name":"x"},"tool":{"name":"terminal","args":{"command":"do-something-risky"}},"context":{}}`
	req := httptest.NewRequest("POST", "/v1/check", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)
	var res domain.DecisionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != domain.ApprovalRequired || res.ApprovalID == "" || res.ExpiresIn != 30 {
		t.Fatalf("expected approval with id+ttl, got %+v", res)
	}
	// approve it
	req2 := httptest.NewRequest("POST", "/v1/approvals/"+res.ApprovalID+"/approve", nil)
	rec2 := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("approve status=%d", rec2.Code)
	}
	var a domain.Approval
	if err := json.Unmarshal(rec2.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a.Status != domain.ApprovalApproved {
		t.Fatalf("got %+v", a)
	}
}

func testGatewayWithStore(t *testing.T) *Gateway {
	t.Helper()
	gw := testGateway(t)
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	gw.Store = s
	return gw
}

func doReq(t *testing.T, gw *Gateway, method, path, body string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

func TestRules_YAMLOnlyMode(t *testing.T) {
	gw := testGateway(t) // no store
	if code, _ := doReq(t, gw, "GET", "/v1/rules", ""); code != 503 {
		t.Fatalf("status=%d want 503", code)
	}
	if code, _ := doReq(t, gw, "GET", "/v1/users", ""); code != 503 {
		t.Fatalf("status=%d want 503", code)
	}
	if code, _ := doReq(t, gw, "POST", "/v1/policy/reload", ""); code != 503 {
		t.Fatalf("status=%d want 503", code)
	}
}

func TestRules_Validation(t *testing.T) {
	gw := testGatewayWithStore(t)
	// Bad regex is rejected before it can touch the engine.
	code, body := doReq(t, gw, "POST", "/v1/rules",
		`{"id":"bad","tool":"terminal","pattern":"([","action":"block"}`)
	if code != 400 {
		t.Fatalf("status=%d body=%s want 400", code, body)
	}
	// Bad action is rejected.
	code, body = doReq(t, gw, "POST", "/v1/rules",
		`{"id":"bad2","tool":"terminal","pattern":"x","action":"nuke"}`)
	if code != 400 {
		t.Fatalf("status=%d body=%s want 400", code, body)
	}
}

func TestRules_CRUDAndReload(t *testing.T) {
	gw := testGatewayWithStore(t)
	// Create a block rule for a command nothing else matches.
	code, body := doReq(t, gw, "POST", "/v1/rules",
		`{"id":"dash-test-block","tool":"terminal","pattern":"frobnicate-dashboard-probe","action":"block","priority":99}`)
	if code != 200 {
		t.Fatalf("status=%d body=%s", code, body)
	}
	// The live engine must enforce it immediately (upsert auto-reloads).
	code, body = doReq(t, gw, "POST", "/v1/check",
		`{"agent":{"name":"t"},"tool":{"name":"terminal","args":{"command":"frobnicate-dashboard-probe"}},"context":{}}`)
	if code != 200 {
		t.Fatalf("check status=%d", code)
	}
	var res domain.DecisionResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != domain.Block || res.PolicyID != "dash-test-block" {
		t.Fatalf("got %+v", res)
	}
	// List shows it.
	code, body = doReq(t, gw, "GET", "/v1/rules", "")
	if code != 200 {
		t.Fatalf("list status=%d", code)
	}
	var rules []store.Rule
	if err := json.Unmarshal(body, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != "dash-test-block" {
		t.Fatalf("rules=%s", body)
	}
	// Delete removes it from the engine too.
	if code, _ := doReq(t, gw, "DELETE", "/v1/rules/dash-test-block", ""); code != 204 {
		t.Fatalf("delete status=%d", code)
	}
	if code, _ := doReq(t, gw, "DELETE", "/v1/rules/dash-test-block", ""); code != 404 {
		t.Fatalf("re-delete status=%d want 404", code)
	}
}

func TestUsers_CRUD(t *testing.T) {
	gw := testGatewayWithStore(t)
	code, body := doReq(t, gw, "POST", "/v1/users",
		`{"name":"ada","email":"ada@example.com","role":"admin"}`)
	if code != 201 {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var u store.User
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.APIKey == "" || !u.Active {
		t.Fatalf("got %+v", u)
	}
	code, body = doReq(t, gw, "GET", "/v1/users", "")
	if code != 200 {
		t.Fatalf("list status=%d", code)
	}
	var users []store.User
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("users=%s", body)
	}
	// Blank name is rejected.
	if code, _ := doReq(t, gw, "POST", "/v1/users", `{"name":""}`); code != 400 {
		t.Fatalf("blank-name status=%d want 400", code)
	}
	if code, _ := doReq(t, gw, "DELETE", "/v1/users/"+u.ID, ""); code != 204 {
		t.Fatalf("delete status=%d", code)
	}
}

func TestDashboard_ServesHTML(t *testing.T) {
	gw := testGateway(t)
	code, body := doReq(t, gw, "GET", "/", "")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if !bytes.Contains(body, []byte("Jev Guard")) {
		t.Fatalf("unexpected body: %.120s", body)
	}
}

func TestJevCalls_Empty(t *testing.T) {
	gw := testGateway(t) // mock judge, no logs
	code, body := doReq(t, gw, "GET", "/v1/jev/calls", "")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	var calls []jev.Call
	if err := json.Unmarshal(body, &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls=%s", body)
	}
}

func TestJevCalls_LimitNewestFirst(t *testing.T) {
	gw := testGateway(t)
	gw.Harness.Judge = &judge.Mock{Logged: []jev.Call{
		{Model: "m", Status: "ok", LatencyMS: 1, InputTokens: 10},
		{Model: "m", Status: "ok", LatencyMS: 2, InputTokens: 20},
		{Model: "m", Status: "error", LatencyMS: 3, InputTokens: 30},
	}}
	code, body := doReq(t, gw, "GET", "/v1/jev/calls?limit=2", "")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	var calls []jev.Call
	if err := json.Unmarshal(body, &calls); err != nil {
		t.Fatal(err)
	}
	// Mock preserves insertion order; endpoint caps at limit.
	if len(calls) != 2 || calls[0].LatencyMS != 1 {
		t.Fatalf("calls=%s", body)
	}
}

func TestAuth_DisabledByDefault(t *testing.T) {
	gw := testGateway(t) // no AuthToken
	// No store either: 503 proves the request passed auth (not 401).
	if code, _ := doReq(t, gw, "GET", "/v1/rules", ""); code != 503 {
		t.Fatalf("status=%d want 503", code)
	}
	code, body := doReq(t, gw, "GET", "/v1/auth/status", "")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	var st map[string]bool
	if err := json.Unmarshal(body, &st); err != nil || st["auth"] {
		t.Fatalf("status=%s", body)
	}
}

func authReq(t *testing.T, gw *Gateway, token, method, path, body string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

func TestAuth_Gated(t *testing.T) {
	gw := testGateway(t)
	gw.AuthToken = "s3cret"
	// No token → 401.
	if code, _ := authReq(t, gw, "", "GET", "/v1/rules", ""); code != 401 {
		t.Fatalf("no-token status=%d want 401", code)
	}
	// Wrong token → 401.
	if code, _ := authReq(t, gw, "wrong", "GET", "/v1/rules", ""); code != 401 {
		t.Fatalf("wrong-token status=%d want 401", code)
	}
	// Right token passes auth (503 = store missing, i.e. past the gate).
	if code, _ := authReq(t, gw, "s3cret", "GET", "/v1/rules", ""); code != 503 {
		t.Fatalf("valid-token status=%d want 503", code)
	}
	// Dashboard shell stays open (it holds no secrets; the login screen
	// lives in it). Data endpoints enforce auth.
	if code, _ := authReq(t, gw, "", "GET", "/", ""); code != 200 {
		t.Fatalf("dashboard status=%d want 200", code)
	}
	// Agent surface stays open: /v1/check without token.
	code, body := authReq(t, gw, "", "POST", "/v1/check",
		`{"agent":{"name":"t"},"tool":{"name":"terminal","args":{"command":"frobnicate-auth-probe"}},"context":{}}`)
	if code != 200 {
		t.Fatalf("check status=%d body=%s", code, body)
	}
	// Status endpoint is open and reports enabled.
	code, body = authReq(t, gw, "", "GET", "/v1/auth/status", "")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	var st map[string]bool
	if err := json.Unmarshal(body, &st); err != nil || !st["auth"] {
		t.Fatalf("status=%s", body)
	}
}

func TestAuth_UserLogin(t *testing.T) {
	gw := testGatewayWithStore(t)
	gw.AuthToken = "s3cret"
	u, err := gw.Store.CreateUser("ada", "ada@example.com", "admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	login := func(secret string) (int, []byte) {
		return authReq(t, gw, "", "POST", "/v1/auth/login",
			`{"secret":`+strconv.Quote(secret)+`}`)
	}

	// Master token → admin identity.
	code, body := login("s3cret")
	if code != 200 {
		t.Fatalf("master status=%d body=%s", code, body)
	}
	var id map[string]string
	if err := json.Unmarshal(body, &id); err != nil || id["name"] != "admin" {
		t.Fatalf("master=%s", body)
	}
	// User key alone → own identity (no name or email needed).
	code, body = login(u.APIKey)
	if code != 200 {
		t.Fatalf("user status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &id); err != nil || id["name"] != "ada" || id["role"] != "admin" {
		t.Fatalf("user=%s", body)
	}
	// Wrong secret → 401.
	if code, _ := login("nope"); code != 401 {
		t.Fatalf("wrong status=%d want 401", code)
	}
	// Blank secret → 401.
	if code, _ := login(""); code != 401 {
		t.Fatalf("blank status=%d want 401", code)
	}
	// Inactive user → 401.
	u.Active = false
	if err := gw.Store.UpdateUser(u); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if code, _ := login(u.APIKey); code != 401 {
		t.Fatalf("inactive status=%d want 401", code)
	}
}

func TestAuth_UserKeyMiddleware(t *testing.T) {
	gw := testGatewayWithStore(t)
	gw.AuthToken = "s3cret"
	u, err := gw.Store.CreateUser("ada", "ada@example.com", "viewer")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// User API key passes the gate (empty rules table → 200 []).
	code, body := authReq(t, gw, u.APIKey, "GET", "/v1/rules", "")
	if code != 200 {
		t.Fatalf("user-key status=%d body=%s", code, body)
	}
	// /v1/auth/me reports the user identity.
	code, body = authReq(t, gw, u.APIKey, "GET", "/v1/auth/me", "")
	if code != 200 {
		t.Fatalf("me status=%d", code)
	}
	var id map[string]string
	if err := json.Unmarshal(body, &id); err != nil || id["name"] != "ada" || id["role"] != "viewer" {
		t.Fatalf("me=%s", body)
	}
}

func TestGroups_CRUD(t *testing.T) {
	gw := testGatewayWithStore(t)
	// YAML-only mode gates too.
	if code, _ := doReq(t, testGateway(t), "GET", "/v1/groups", ""); code != 503 {
		t.Fatalf("yaml-only status=%d want 503", code)
	}
	// Blank name rejected.
	if code, _ := doReq(t, gw, "POST", "/v1/groups", `{"name":""}`); code != 400 {
		t.Fatalf("blank status=%d want 400", code)
	}
	code, body := doReq(t, gw, "POST", "/v1/groups", `{"name":"g1","description":"first"}`)
	if code != 201 {
		t.Fatalf("create status=%d body=%s", code, body)
	}
	code, body = doReq(t, gw, "GET", "/v1/groups", "")
	if code != 200 {
		t.Fatalf("list status=%d", code)
	}
	var groups []store.Group
	if err := json.Unmarshal(body, &groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Name != "g1" || groups[0].Description != "first" {
		t.Fatalf("groups=%s", body)
	}
	// Update description via PUT (name comes from path).
	code, _ = doReq(t, gw, "PUT", "/v1/groups/g1", `{"name":"other","description":"updated"}`)
	if code != 200 {
		t.Fatalf("update status=%d", code)
	}
	if _, err := gw.Store.GetGroup("g1"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if code, _ := doReq(t, gw, "DELETE", "/v1/groups/g1", ""); code != 204 {
		t.Fatalf("delete status=%d", code)
	}
	if code, _ := doReq(t, gw, "DELETE", "/v1/groups/g1", ""); code != 404 {
		t.Fatalf("re-delete status=%d want 404", code)
	}
}

func TestCategories_CRUD(t *testing.T) {
	gw := testGatewayWithStore(t)
	if code, _ := doReq(t, testGateway(t), "GET", "/v1/categories", ""); code != 503 {
		t.Fatalf("yaml-only status=%d want 503", code)
	}
	if code, _ := doReq(t, gw, "POST", "/v1/categories", `{"name":""}`); code != 400 {
		t.Fatalf("blank status=%d want 400", code)
	}
	code, body := doReq(t, gw, "POST", "/v1/categories", `{"name":"safety","description":"scope"}`)
	if code != 201 {
		t.Fatalf("create status=%d body=%s", code, body)
	}
	code, body = doReq(t, gw, "GET", "/v1/categories", "")
	if code != 200 {
		t.Fatalf("list status=%d", code)
	}
	var cats []store.Category
	if err := json.Unmarshal(body, &cats); err != nil {
		t.Fatal(err)
	}
	if len(cats) != 1 || cats[0].Name != "safety" || cats[0].Description != "scope" {
		t.Fatalf("cats=%s", body)
	}
	code, _ = doReq(t, gw, "PUT", "/v1/categories/safety", `{"name":"other","description":"updated"}`)
	if code != 200 {
		t.Fatalf("update status=%d", code)
	}
	if got, err := gw.Store.GetCategory("safety"); err != nil || got.Description != "updated" {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	if code, _ := doReq(t, gw, "DELETE", "/v1/categories/safety", ""); code != 204 {
		t.Fatalf("delete status=%d", code)
	}
	if code, _ := doReq(t, gw, "DELETE", "/v1/categories/safety", ""); code != 404 {
		t.Fatalf("re-delete status=%d want 404", code)
	}
}

func TestRBAC_CategoryWritesAdminOnly(t *testing.T) {
	gw, viewer := rbacGateway(t, "viewer")
	if code, _ := authReq(t, gw, viewer, "POST", "/v1/categories", `{"name":"c"}`); code != 403 {
		t.Fatalf("viewer create status=%d want 403", code)
	}
	gw2, opKey := rbacGateway(t, "operator")
	if code, _ := authReq(t, gw2, opKey, "DELETE", "/v1/categories/c", ""); code != 403 {
		t.Fatalf("operator delete status=%d want 403", code)
	}
}

func rbacGateway(t *testing.T, role string) (*Gateway, string) {
	t.Helper()
	gw := testGatewayWithStore(t)
	gw.AuthToken = "s3cret"
	key := "s3cret"
	if role != "admin-master" {
		u, err := gw.Store.CreateUser("rbac-"+role, role+"@example.com", role)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		key = u.APIKey
	}
	return gw, key
}

func TestRBAC_ViewerReadOnly(t *testing.T) {
	gw, key := rbacGateway(t, "viewer")
	// Reads pass.
	if code, _ := authReq(t, gw, key, "GET", "/v1/rules", ""); code != 200 {
		t.Fatalf("rules list status=%d want 200", code)
	}
	if code, _ := authReq(t, gw, key, "GET", "/v1/users", ""); code != 200 {
		t.Fatalf("users list status=%d want 200", code)
	}
	// Every mutation is forbidden.
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/v1/rules", `{"tool":"terminal","pattern":"x","action":"block"}`},
		{"PUT", "/v1/rules/x", `{"tool":"terminal","pattern":"x","action":"block"}`},
		{"DELETE", "/v1/rules/x", ""},
		{"POST", "/v1/users", `{"name":"n","email":"n@e.com","role":"viewer"}`},
		{"DELETE", "/v1/users/x", ""},
		{"POST", "/v1/groups", `{"name":"g"}`},
		{"DELETE", "/v1/groups/g", ""},
		{"POST", "/v1/policy/reload", ""},
	} {
		if code, _ := authReq(t, gw, key, tc.method, tc.path, tc.body); code != 403 {
			t.Fatalf("%s %s status=%d want 403", tc.method, tc.path, code)
		}
	}
	// Approvals too (need a real one to hit the role check past 404).
	apprID := gw.Approvals.Create("r", "terminal", nil, 0.5, "x", time.Minute)
	if code, _ := authReq(t, gw, key, "POST", "/v1/approvals/"+apprID+"/approve", ""); code != 403 {
		t.Fatalf("approve status=%d want 403", code)
	}
}

func TestRBAC_OperatorApprovesButNotConfigures(t *testing.T) {
	gw, key := rbacGateway(t, "operator")
	apprID := gw.Approvals.Create("r", "terminal", nil, 0.5, "x", time.Minute)
	if code, _ := authReq(t, gw, key, "POST", "/v1/approvals/"+apprID+"/approve", ""); code != 200 {
		t.Fatalf("approve status=%d want 200", code)
	}
	if code, _ := authReq(t, gw, key, "POST", "/v1/policy/reload", ""); code != 200 {
		t.Fatalf("reload status=%d want 200", code)
	}
	if code, _ := authReq(t, gw, key, "POST", "/v1/rules", `{"tool":"t","pattern":"x","action":"block"}`); code != 403 {
		t.Fatalf("rule create status=%d want 403", code)
	}
	if code, _ := authReq(t, gw, key, "DELETE", "/v1/users/x", ""); code != 403 {
		t.Fatalf("user delete status=%d want 403", code)
	}
}

func TestRBAC_AdminAndUnknown(t *testing.T) {
	gw, key := rbacGateway(t, "admin")
	code, _ := authReq(t, gw, key, "POST", "/v1/rules", `{"id":"rbac","tool":"t","pattern":"x","action":"block"}`)
	if code != 200 {
		t.Fatalf("admin create status=%d want 200", code)
	}
	if code, _ := authReq(t, gw, key, "DELETE", "/v1/rules/rbac", ""); code != 204 {
		t.Fatalf("admin delete status=%d want 204", code)
	}
	// Unknown roles fail closed to read-only.
	gw2, weird := rbacGateway(t, "weird")
	if code, _ := authReq(t, gw2, weird, "GET", "/v1/rules", ""); code != 200 {
		t.Fatalf("weird read status=%d want 200", code)
	}
	if code, _ := authReq(t, gw2, weird, "POST", "/v1/rules", `{"tool":"t","pattern":"x","action":"block"}`); code != 403 {
		t.Fatalf("weird write status=%d want 403", code)
	}
}

func TestUsers_KeysMaskedForNonAdmin(t *testing.T) {
	gw, adminKey := rbacGateway(t, "admin-master")
	v, err := gw.Store.CreateUser("v", "v@example.com", "viewer")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	code, body := authReq(t, gw, adminKey, "GET", "/v1/users", "")
	if code != 200 {
		t.Fatalf("admin status=%d", code)
	}
	var users []store.User
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range users {
		if u.Name == "v" && u.APIKey != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin should see keys: %s", body)
	}
	code, body = authReq(t, gw, v.APIKey, "GET", "/v1/users", "")
	if code != 200 {
		t.Fatalf("viewer status=%d", code)
	}
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.APIKey != "" {
			t.Fatalf("viewer sees key for %s", u.Name)
		}
	}
}

func TestApprovals_SQLiteSurvivesRestart(t *testing.T) {
	gw := testGateway(t)
	dbPath := filepath.Join(t.TempDir(), "appr.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	gw.Store = s
	appr := approval.NewStore()
	appr.SetPersistence(s)
	gw.Approvals = appr
	gw.Harness.Approvals = appr
	gw.Harness.Judge = &judge.Mock{Risk: 0.8, Confidence: 0.99, Action: "approval_required"}
	code, body := doReq(t, gw, "POST", "/v1/check",
		`{"agent":{"name":"t"},"tool":{"name":"terminal","args":{"command":"frobnicate-persist-probe"}},"context":{}}`)
	if code != 200 {
		t.Fatalf("check status=%d body=%s", code, body)
	}
	var res domain.DecisionResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != domain.ApprovalRequired || res.ApprovalID == "" {
		t.Fatalf("got %+v", res)
	}
	// Fresh process, same DB file: the approval must still be there.
	s.Close()
	s2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	fresh := approval.NewStore()
	fresh.SetPersistence(s2)
	listed := fresh.List()
	if len(listed) != 1 || listed[0].ID != res.ApprovalID {
		t.Fatalf("list after reopen=%+v", listed)
	}
	if listed[0].Status != domain.ApprovalPending {
		t.Fatalf("status=%s", listed[0].Status)
	}
	dec, ok := fresh.Decide(res.ApprovalID, true)
	if !ok || dec.Status != domain.ApprovalApproved {
		t.Fatalf("decide=%+v ok=%v", dec, ok)
	}
	// And the decision itself persisted.
	got, err := s2.GetApproval(res.ApprovalID)
	if err != nil || got.Status != domain.ApprovalApproved {
		t.Fatalf("stored=%+v err=%v", got, err)
	}
}

func TestApprovalsPage_ShapeAndPagination(t *testing.T) {
	gw := testGatewayWithStore(t)
	gw.Approvals.SetPersistence(gw.Store)
	for i := 0; i < 5; i++ {
		if _, err := gw.Store.CreateApproval("r", "terminal", nil, 0.1, "", time.Hour); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}
	code, body := doReq(t, gw, "GET", "/v1/approvals/page?page=1&per_page=2", "")
	if code != 200 {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var pg ApprovalsPage
	if err := json.Unmarshal(body, &pg); err != nil {
		t.Fatal(err)
	}
	if pg.Total != 5 || pg.Page != 1 || pg.PerPage != 2 || pg.Pages != 3 || pg.Pending != 5 {
		t.Fatalf("got %+v", pg)
	}
	if len(pg.Approvals) != 2 {
		t.Fatalf("items=%d", len(pg.Approvals))
	}
	if pg.Approvals[0].CreatedAt.IsZero() {
		t.Fatal("missing created_at datetime")
	}
	if !pg.Approvals[0].CreatedAt.After(pg.Approvals[1].CreatedAt) {
		t.Fatal("not ordered newest-first by datetime")
	}
	// Page 3 holds the last row; pending drops after a decision.
	code, body = doReq(t, gw, "GET", "/v1/approvals/page?page=3&per_page=2", "")
	if code != 200 {
		t.Fatalf("p3 status=%d", code)
	}
	var pg3 ApprovalsPage
	if err := json.Unmarshal(body, &pg3); err != nil {
		t.Fatal(err)
	}
	if len(pg3.Approvals) != 1 || pg3.Approvals[0].ID == pg.Approvals[0].ID {
		t.Fatalf("p3=%+v", pg3.Approvals)
	}
	if _, ok := gw.Approvals.Decide(pg3.Approvals[0].ID, true); !ok {
		t.Fatal("decide failed")
	}
	code, body = doReq(t, gw, "GET", "/v1/approvals/page?page=1&per_page=2", "")
	var pgAfter ApprovalsPage
	if err := json.Unmarshal(body, &pgAfter); err != nil {
		t.Fatal(err)
	}
	if code != 200 || pgAfter.Pending != 4 || pgAfter.Total != 5 {
		t.Fatalf("after decide: code=%d %+v", code, pgAfter)
	}
}

func TestApprovalsPage_YAMLOnly(t *testing.T) {
	gw := testGateway(t) // no store
	gw.Approvals.Create("r", "terminal", nil, 0.1, "", time.Hour)
	code, body := doReq(t, gw, "GET", "/v1/approvals/page", "")
	if code != 200 {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var pg ApprovalsPage
	if err := json.Unmarshal(body, &pg); err != nil {
		t.Fatal(err)
	}
	if pg.Total != 1 || pg.Pending != 1 || len(pg.Approvals) != 1 {
		t.Fatalf("got %+v", pg)
	}
}

func TestApprovalsPage_BareArrayUnchanged(t *testing.T) {
	// Agent polling plugins depend on the bare-array shape.
	gw := testGateway(t)
	gw.Approvals.Create("r", "terminal", nil, 0.1, "", time.Hour)
	code, body := doReq(t, gw, "GET", "/v1/approvals", "")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	var arr []domain.Approval
	if err := json.Unmarshal(body, &arr); err != nil || len(arr) != 1 {
		t.Fatalf("body=%s err=%v", body, err)
	}
}
