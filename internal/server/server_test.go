package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/approval"
	"github.com/dereknguyen269/jev-harness/internal/cache"
	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/dereknguyen269/jev-harness/internal/harness"
	"github.com/dereknguyen269/jev-harness/internal/judge"
	"github.com/dereknguyen269/jev-harness/internal/policy"
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

func TestV2Check_Block(t *testing.T) {
	gw := testGateway(t)
	body := `{"agent":{"name":"claude-code"},"tool":{"name":"Bash","args":{"command":"rm -rf /"}},"context":{}}`
	req := httptest.NewRequest("POST", "/v2/check", bytes.NewBufferString(body))
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

func TestV2Check_JevAllowShape(t *testing.T) {
	gw := testGateway(t)
	body := `{"agent":{"name":"codex"},"tool":{"name":"shell","args":{"command":"frobnicate --all"}},"context":{"environment":"dev"}}`
	req := httptest.NewRequest("POST", "/v2/check", bytes.NewBufferString(body))
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

func TestV1Check_Compat(t *testing.T) {
	gw := testGateway(t)
	body := `{"tool":"terminal","args":{"command":"git status"},"context":{"agent":"opencode"}}`
	req := httptest.NewRequest("POST", "/v1/check", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	gw.Router().ServeHTTP(rec, req)
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["decision"] != "allow" {
		t.Fatalf("got %v", res)
	}
	if _, ok := res["policy"]; !ok {
		t.Fatalf("expected policy key, got %v", res)
	}
}

func TestApprovals_Flow(t *testing.T) {
	gw := testGateway(t)
	gw.Harness.Judge = &judge.Mock{Risk: 0.8, Confidence: 0.99, Action: "approval_required"}
	body := `{"agent":{"name":"x"},"tool":{"name":"terminal","args":{"command":"do-something-risky"}},"context":{}}`
	req := httptest.NewRequest("POST", "/v2/check", bytes.NewBufferString(body))
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
	req2 := httptest.NewRequest("POST", "/v2/approvals/"+res.ApprovalID+"/approve", nil)
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
