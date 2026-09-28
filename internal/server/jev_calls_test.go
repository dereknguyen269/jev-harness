package server

import (
	"encoding/json"
	"testing"

	"github.com/dereknguyen269/jev-harness/internal/jev"
)

func TestJevCalls_FromStore(t *testing.T) {
	gw := testGatewayWithStore(t)
	for i, m := range []string{"m0", "m1", "m2"} {
		if err := gw.Store.InsertJevCall(jev.Call{Model: m, Status: "ok", LatencyMS: int64(i)}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// Default limit (20): all three, newest first.
	code, body := doReq(t, gw, "GET", "/v1/jev/calls", "")
	if code != 200 {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var calls []jev.Call
	if err := json.Unmarshal(body, &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0].Model != "m2" || calls[2].Model != "m0" {
		t.Fatalf("calls=%s", body)
	}
	// Explicit limit honored.
	code, body = doReq(t, gw, "GET", "/v1/jev/calls?limit=2", "")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if err := json.Unmarshal(body, &calls); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Model != "m2" {
		t.Fatalf("calls=%s", body)
	}
}
