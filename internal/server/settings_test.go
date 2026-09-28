package server

import (
	"encoding/json"
	"testing"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

func TestSettings_GetPut(t *testing.T) {
	gw := testGatewayWithStore(t)
	// Default comes from the harness seed (30s).
	code, body := doReq(t, gw, "GET", "/v1/settings", "")
	if code != 200 {
		t.Fatalf("get status=%d body=%s", code, body)
	}
	var cur map[string]int
	if err := json.Unmarshal(body, &cur); err != nil || cur["approval_ttl_seconds"] != 30 {
		t.Fatalf("get=%s", body)
	}
	// Update applies live to the harness and persists.
	code, body = doReq(t, gw, "PUT", "/v1/settings", `{"approval_ttl_seconds":90}`)
	if code != 200 {
		t.Fatalf("put status=%d body=%s", code, body)
	}
	if err := json.Unmarshal(body, &cur); err != nil || cur["approval_ttl_seconds"] != 90 {
		t.Fatalf("put=%s", body)
	}
	if got := gw.Harness.ApprovalTTLSeconds(); got != 90 {
		t.Fatalf("harness=%d", got)
	}
	code, body = doReq(t, gw, "GET", "/v1/settings", "")
	if err := json.Unmarshal(body, &cur); err != nil || cur["approval_ttl_seconds"] != 90 {
		t.Fatalf("re-get=%s", body)
	}
	// Bounds enforced.
	for _, bad := range []string{`{"approval_ttl_seconds":3}`, `{"approval_ttl_seconds":9999}`,
		`{"approval_ttl_seconds":-1}`, `{}`, `{"approval_ttl_seconds":"x"}`, `not-json`} {
		if code, body := doReq(t, gw, "PUT", "/v1/settings", bad); code != 400 {
			t.Fatalf("put %s status=%d body=%s want 400", bad, code, body)
		}
	}
}

func TestSettings_YAMLOnly(t *testing.T) {
	gw := testGateway(t) // no store
	if code, _ := doReq(t, gw, "GET", "/v1/settings", ""); code != 503 {
		t.Fatalf("get status=%d want 503", code)
	}
	if code, _ := doReq(t, gw, "PUT", "/v1/settings", `{"approval_ttl_seconds":60}`); code != 503 {
		t.Fatalf("put status=%d want 503", code)
	}
}

func TestSettings_RBAC(t *testing.T) {
	gw, viewer := rbacGateway(t, "viewer")
	if code, _ := authReq(t, gw, viewer, "GET", "/v1/settings", ""); code != 200 {
		t.Fatalf("viewer get status=%d want 200", code)
	}
	if code, _ := authReq(t, gw, viewer, "PUT", "/v1/settings", `{"approval_ttl_seconds":60}`); code != 403 {
		t.Fatalf("viewer put status=%d want 403", code)
	}
	gw2, op := rbacGateway(t, "operator")
	if code, _ := authReq(t, gw2, op, "PUT", "/v1/settings", `{"approval_ttl_seconds":60}`); code != 403 {
		t.Fatalf("operator put status=%d want 403", code)
	}
}

func TestRules_TimeoutValidation(t *testing.T) {
	gw := testGatewayWithStore(t)
	// Valid per-rule timeout on approval_required.
	code, body := doReq(t, gw, "POST", "/v1/rules",
		`{"id":"ttl-ok","tool":"terminal","pattern":"ttl-probe-ok","action":"approval_required","approval_timeout":120}`)
	if code != 200 {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var saved map[string]any
	if err := json.Unmarshal(body, &saved); err != nil || saved["approval_timeout"] != 120.0 {
		t.Fatalf("saved=%s", body)
	}
	// Too small / too big / negative rejected.
	for _, bad := range []string{`3`, `9999`, `-10`} {
		code, body := doReq(t, gw, "POST", "/v1/rules",
			`{"id":"ttl-bad","tool":"terminal","pattern":"x","action":"approval_required","approval_timeout":`+bad+`}`)
		if code != 400 {
			t.Fatalf("timeout %s status=%d body=%s want 400", bad, code, body)
		}
	}
	// Timeout on a non-approval rule rejected.
	code, body = doReq(t, gw, "POST", "/v1/rules",
		`{"id":"ttl-wrong","tool":"terminal","pattern":"x","action":"block","approval_timeout":60}`)
	if code != 400 {
		t.Fatalf("status=%d body=%s want 400", code, body)
	}
	// Zero timeout is the default and always legal.
	code, _ = doReq(t, gw, "POST", "/v1/rules",
		`{"id":"ttl-zero","tool":"terminal","pattern":"x","action":"block","approval_timeout":0}`)
	if code != 200 {
		t.Fatalf("zero status=%d want 200", code)
	}
}

func TestRules_TimeoutFlowsToApproval(t *testing.T) {
	gw := testGatewayWithStore(t)
	if code, _ := doReq(t, gw, "POST", "/v1/rules",
		`{"id":"ttl-flow","tool":"terminal","pattern":"ttl-flow-probe","action":"approval_required","approval_timeout":100}`); code != 200 {
		t.Fatal("rule create failed")
	}
	code, body := doReq(t, gw, "POST", "/v1/check",
		`{"agent":{"name":"t"},"tool":{"name":"terminal","args":{"command":"ttl-flow-probe"}},"context":{}}`)
	if code != 200 {
		t.Fatalf("check status=%d", code)
	}
	var res domain.DecisionResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != domain.ApprovalRequired || res.ExpiresIn != 100 || res.ApprovalID == "" {
		t.Fatalf("got %+v", res)
	}
}
