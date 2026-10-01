package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/jev"
)

func TestJevCallRoundTrip(t *testing.T) {
	s := openTest(t)
	in := jev.Call{
		Timestamp: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		Model:     "m1", Endpoint: "https://example.test/eval", Status: "ok",
		HTTPStatus: 200, LatencyMS: 321, InputTokens: 11, OutputTokens: 7,
	}
	if err := s.InsertJevCall(in); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.ListJevCalls(0)
	if err != nil || len(got) != 1 {
		t.Fatalf("list=%v err=%v", got, err)
	}
	g := got[0]
	if !g.Timestamp.Equal(in.Timestamp) || g.Model != "m1" || g.Endpoint != in.Endpoint ||
		g.Status != "ok" || g.HTTPStatus != 200 || g.LatencyMS != 321 ||
		g.InputTokens != 11 || g.OutputTokens != 7 || g.Error != "" {
		t.Fatalf("round-trip mismatch: %+v", g)
	}
}

func TestJevCallErrorAndOrder(t *testing.T) {
	s := openTest(t)
	for i := 0; i < 3; i++ {
		status := "ok"
		errMsg := ""
		if i == 2 {
			status = "error"
			errMsg = "boom"
		}
		if err := s.InsertJevCall(jev.Call{Model: fmt.Sprintf("m%d", i), Status: status, Error: errMsg}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	got, err := s.ListJevCalls(2)
	if err != nil || len(got) != 2 {
		t.Fatalf("list=%v err=%v", got, err)
	}
	// Newest first.
	if got[0].Model != "m2" || got[0].Status != "error" || got[0].Error != "boom" {
		t.Fatalf("order=%+v", got)
	}
	if got[1].Model != "m1" {
		t.Fatalf("order=%+v", got)
	}
	all, err := s.ListJevCalls(0)
	if err != nil || len(all) != 3 {
		t.Fatalf("all=%v err=%v", all, err)
	}
}

func TestJevCallEmpty(t *testing.T) {
	s := openTest(t)
	got, err := s.ListJevCalls(20)
	if err != nil || len(got) != 0 {
		t.Fatalf("list=%v err=%v", got, err)
	}
}

func TestJevCallPruneCap(t *testing.T) {
	s := openTest(t)
	for i := 0; i < MaxJevCalls+10; i++ {
		if err := s.InsertJevCall(jev.Call{Model: fmt.Sprintf("m%d", i), Status: "ok"}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got, err := s.ListJevCalls(0)
	if err != nil || len(got) != MaxJevCalls {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	// Oldest evicted, newest kept.
	if got[len(got)-1].Model != "m10" || got[0].Model != fmt.Sprintf("m%d", MaxJevCalls+9) {
		t.Fatalf("evict=[%s..%s]", got[len(got)-1].Model, got[0].Model)
	}
}
