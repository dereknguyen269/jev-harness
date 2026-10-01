package jev

import (
	"testing"
	"time"
)

func TestCallLogOrderAndLimit(t *testing.T) {
	l := NewCallLog(5)
	for i := 0; i < 3; i++ {
		l.Add(Call{Model: "m", Status: "ok", LatencyMS: int64(i)})
	}
	got := l.List(0) // all
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	// Newest first.
	if got[0].LatencyMS != 2 || got[2].LatencyMS != 0 {
		t.Fatalf("order=%+v", got)
	}
	got = l.List(2)
	if len(got) != 2 || got[0].LatencyMS != 2 {
		t.Fatalf("limit=%+v", got)
	}
}

func TestCallLogEvictsOldest(t *testing.T) {
	l := NewCallLog(3)
	for i := 0; i < 5; i++ {
		l.Add(Call{Model: "m", Timestamp: time.Now(), LatencyMS: int64(i)})
	}
	got := l.List(0)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].LatencyMS != 4 || got[2].LatencyMS != 2 {
		t.Fatalf("evict=%+v", got)
	}
}

func TestClientCallsEmpty(t *testing.T) {
	c := NewClient("k", "", "", "")
	if got := c.Calls(20); len(got) != 0 {
		t.Fatalf("calls=%+v", got)
	}
	var zero Client
	if got := zero.Calls(20); got != nil {
		t.Fatalf("zero client calls=%+v", got)
	}
}
