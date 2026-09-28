package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

func TestApprovalCRUD(t *testing.T) {
	s := openTest(t)
	a, err := s.CreateApproval("req-1", "terminal",
		map[string]any{"command": "deploy-preview-xyz"}, 0.8, "risky", time.Minute)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ID == "" || a.Status != domain.ApprovalPending {
		t.Fatalf("got %+v", a)
	}
	got, err := s.GetApproval(a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RequestID != "req-1" || got.Tool != "terminal" || got.Risk != 0.8 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Arguments["command"] != "deploy-preview-xyz" {
		t.Fatalf("args lost: %+v", got.Arguments)
	}
	if got.CreatedAt.IsZero() || !got.ExpiresAt.After(got.CreatedAt) {
		t.Fatalf("timestamps wrong: %+v", got)
	}
	list, err := s.ListApprovals()
	if err != nil || len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list=%v err=%v", list, err)
	}
	dec, err := s.DecideApproval(a.ID, true)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if dec.Status != domain.ApprovalApproved {
		t.Fatalf("got %+v", dec)
	}
	dec2, err := s.DecideApproval(a.ID, false)
	if err != nil || dec2.Status != domain.ApprovalApproved {
		t.Fatalf("re-decide=%+v err=%v", dec2, err)
	}
}

func TestApprovalDeny(t *testing.T) {
	s := openTest(t)
	a, err := s.CreateApproval("r", "terminal", nil, 0.5, "x", time.Minute)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dec, err := s.DecideApproval(a.ID, false)
	if err != nil || dec.Status != domain.ApprovalDenied {
		t.Fatalf("got %+v err=%v", dec, err)
	}
}

func TestApprovalUnknown(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetApproval("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get err=%v want ErrNoRows", err)
	}
	if _, err := s.DecideApproval("missing", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("decide err=%v want ErrNoRows", err)
	}
	if list, err := s.ListApprovals(); err != nil || len(list) != 0 {
		t.Fatalf("list=%v err=%v", list, err)
	}
}

func TestApprovalExpiry(t *testing.T) {
	s := openTest(t)
	a, err := s.CreateApproval("r", "terminal", nil, 0.5, "x", -time.Second)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetApproval(a.ID)
	if err != nil || got.Status != domain.ApprovalExpired {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	dec, err := s.DecideApproval(a.ID, true)
	if err != nil || dec.Status != domain.ApprovalExpired {
		t.Fatalf("decide=%+v err=%v", dec, err)
	}
	list, err := s.ListApprovals()
	if err != nil || len(list) != 1 || list[0].Status != domain.ApprovalExpired {
		t.Fatalf("list=%v err=%v", list, err)
	}
}

func TestApprovalPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reopen.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	a, err := s.CreateApproval("req-9", "write_file",
		map[string]any{"path": "/tmp/note"}, 0.4, "needs eyes", time.Hour)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	got, err := s2.GetApproval(a.ID)
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if got.RequestID != "req-9" || got.Status != domain.ApprovalPending {
		t.Fatalf("got %+v", got)
	}
	dec, err := s2.DecideApproval(a.ID, true)
	if err != nil || dec.Status != domain.ApprovalApproved {
		t.Fatalf("decide after reopen=%+v err=%v", dec, err)
	}
}

func TestApprovalNilArgsRoundTrip(t *testing.T) {
	s := openTest(t)
	a, err := s.CreateApproval("r", "terminal", nil, 0.1, "", time.Minute)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetApproval(a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Arguments == nil || len(got.Arguments) != 0 {
		t.Fatalf("args=%v", got.Arguments)
	}
}

func TestApprovalPage(t *testing.T) {
	s := openTest(t)
	for i := 0; i < 5; i++ {
		if _, err := s.CreateApproval("r", "terminal", nil, 0.1, "", time.Hour); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}
	items, total, err := s.ListApprovalsPage(2, 0)
	if err != nil || total != 5 || len(items) != 2 {
		t.Fatalf("page1 items=%d total=%d err=%v", len(items), total, err)
	}
	items2, total2, err := s.ListApprovalsPage(2, 2)
	if err != nil || total2 != 5 || len(items2) != 2 {
		t.Fatalf("page2 items=%d total=%d err=%v", len(items2), total2, err)
	}
	// Newest first, no overlap between pages.
	if !items[0].CreatedAt.After(items2[0].CreatedAt) {
		t.Fatalf("order wrong: %v vs %v", items[0].CreatedAt, items2[0].CreatedAt)
	}
	seen := map[string]bool{}
	for _, a := range append(items, items2...) {
		if seen[a.ID] {
			t.Fatalf("overlap on %s", a.ID)
		}
		seen[a.ID] = true
	}
	last, _, err := s.ListApprovalsPage(2, 4)
	if err != nil || len(last) != 1 {
		t.Fatalf("last page=%d err=%v", len(last), err)
	}
	beyond, _, err := s.ListApprovalsPage(2, 99)
	if err != nil || len(beyond) != 0 {
		t.Fatalf("beyond=%d err=%v", len(beyond), err)
	}
	all, totalAll, err := s.ListApprovalsPage(0, 0)
	if err != nil || totalAll != 5 || len(all) != 5 {
		t.Fatalf("unlimited=%d total=%d err=%v", len(all), totalAll, err)
	}
	if n, err := s.CountApprovals(); err != nil || n != 5 {
		t.Fatalf("count=%d err=%v", n, err)
	}
}

func TestPendingCount(t *testing.T) {
	s := openTest(t)
	if n, err := s.PendingCount(); err != nil || n != 0 {
		t.Fatalf("empty=%d err=%v", n, err)
	}
	a, err := s.CreateApproval("r", "terminal", nil, 0.1, "", time.Hour)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n, err := s.PendingCount(); err != nil || n != 1 {
		t.Fatalf("one pending=%d err=%v", n, err)
	}
	if _, err := s.DecideApproval(a.ID, true); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if n, err := s.PendingCount(); err != nil || n != 0 {
		t.Fatalf("after decide=%d err=%v", n, err)
	}
	// Already-expired rows never count as pending.
	if _, err := s.CreateApproval("r", "terminal", nil, 0.1, "", -time.Second); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if n, err := s.PendingCount(); err != nil || n != 0 {
		t.Fatalf("expired counted=%d err=%v", n, err)
	}
}

// TestApprovalSubSecondOrdering locks the fixed-width timestamp format:
// 0.100s ("...:00.1Z" in RFC3339Nano) must sort before 0.120s
// ("...:00.12Z"), which plain string order gets backwards.
func TestApprovalSubSecondOrdering(t *testing.T) {
	s := openTest(t)
	base := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	insert := func(id string, frac time.Duration) {
		t.Helper()
		ts := base.Add(frac)
		if _, err := s.db.Exec(`INSERT INTO approvals
			(id, request_id, tool, arguments, risk, reason, status, created_at, expires_at)
			VALUES (?, 'r', 'terminal', '{}', 0, '', 'pending', ?, ?)`,
			id,
			ts.UTC().Format(approvalTimeFormat),
			ts.Add(time.Hour).UTC().Format(approvalTimeFormat)); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("older", 100*time.Millisecond)
	insert("newer", 120*time.Millisecond)
	got, err := s.ListApprovals()
	if err != nil || len(got) != 2 {
		t.Fatalf("list=%v err=%v", got, err)
	}
	if got[0].ID != "newer" || got[1].ID != "older" {
		t.Fatalf("order=%s,%s", got[0].ID, got[1].ID)
	}
}

// TestApprovalLegacyTimestampFormat proves rows written by older binaries
// (RFC3339Nano variable fraction, or no fraction) still read back.
func TestApprovalLegacyTimestampFormat(t *testing.T) {
	s := openTest(t)
	legacy := []string{
		"2026-09-27T12:00:00.1Z",
		"2026-09-27T12:00:00.12Z",
		"2026-09-27T12:00:00Z",
	}
	for i, ts := range legacy {
		id := "legacy-" + string(rune('a'+i))
		if _, err := s.db.Exec(`INSERT INTO approvals
			(id, request_id, tool, arguments, risk, reason, status, created_at, expires_at)
			VALUES (?, 'r', 'terminal', '{}', 0, '', 'pending', ?, ?)`,
			id, ts, "2026-09-27T13:00:00Z"); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		if _, err := s.GetApproval(id); err != nil {
			t.Fatalf("read legacy %s (%s): %v", id, ts, err)
		}
	}
	list, err := s.ListApprovals()
	if err != nil || len(list) != len(legacy) {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
}
