package approval

import (
	"errors"
	"testing"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/google/uuid"
)

// fakePersist is an in-memory Persistence double with an error switch to
// simulate a DB outage.
type fakePersist struct {
	data map[string]domain.Approval
	err  error
}

func newFakePersist() *fakePersist { return &fakePersist{data: map[string]domain.Approval{}} }

func (f *fakePersist) CreateApproval(requestID, tool string, args map[string]any, risk float64, reason string, ttl time.Duration) (domain.Approval, error) {
	if f.err != nil {
		return domain.Approval{}, f.err
	}
	now := time.Now()
	a := domain.Approval{ID: uuid.NewString(), RequestID: requestID, Tool: tool,
		Arguments: args, Risk: risk, Reason: reason,
		Status: domain.ApprovalPending, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	f.data[a.ID] = a
	return a, nil
}

func (f *fakePersist) GetApproval(id string) (domain.Approval, error) {
	if f.err != nil {
		return domain.Approval{}, f.err
	}
	a, ok := f.data[id]
	if !ok {
		return domain.Approval{}, errFakeNoRow
	}
	return a, nil
}

func (f *fakePersist) ListApprovals() ([]domain.Approval, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := []domain.Approval{}
	for _, a := range f.data {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakePersist) ListApprovalsPage(limit, offset int) ([]domain.Approval, int, error) {
	all, err := f.ListApprovals()
	if err != nil {
		return nil, 0, err
	}
	// Mirror the store contract: newest-first, so widened windows are
	// stable prefixes across calls (map iteration order is not).
	sortApprovalsNewestFirst(all)
	return slicePage(all, limit, offset), len(all), nil
}

func (f *fakePersist) PendingCount() (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	n := 0
	for _, a := range f.data {
		if a.Status == domain.ApprovalPending {
			n++
		}
	}
	return n, nil
}

func (f *fakePersist) DecideApproval(id string, approve bool) (domain.Approval, error) {
	if f.err != nil {
		return domain.Approval{}, f.err
	}
	a, ok := f.data[id]
	if !ok {
		return domain.Approval{}, errFakeNoRow
	}
	if approve {
		a.Status = domain.ApprovalApproved
	} else {
		a.Status = domain.ApprovalDenied
	}
	f.data[id] = a
	return a, nil
}

var errFakeNoRow = errors.New("no such row")

func TestPersistWriteThrough(t *testing.T) {
	s, fp := NewStore(), newFakePersist()
	s.SetPersistence(fp)
	id := s.Create("req-1", "terminal", nil, 0.5, "x", time.Minute)
	if _, ok := fp.data[id]; !ok {
		t.Fatalf("backend missing %s", id)
	}
	got, ok := s.Get(id)
	if !ok || got.RequestID != "req-1" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if list := s.List(); len(list) != 1 || list[0].ID != id {
		t.Fatalf("list=%+v", list)
	}
	dec, ok := s.Decide(id, true)
	if !ok || dec.Status != domain.ApprovalApproved {
		t.Fatalf("decide=%+v ok=%v", dec, ok)
	}
	if fp.data[id].Status != domain.ApprovalApproved {
		t.Fatal("backend not updated")
	}
}

func TestPersistOutageFallsBackToMemory(t *testing.T) {
	s, fp := NewStore(), newFakePersist()
	s.SetPersistence(fp)
	fp.err = errors.New("disk gone")
	id := s.Create("req-1", "terminal", nil, 0.5, "x", time.Minute)
	if id == "" {
		t.Fatal("create must still mint an ID during outage")
	}
	if _, ok := s.Get(id); !ok {
		t.Fatal("memory fallback missing on Get")
	}
	if list := s.List(); len(list) != 1 {
		t.Fatalf("memory fallback missing on List: %+v", list)
	}
	dec, ok := s.Decide(id, false)
	if !ok || dec.Status != domain.ApprovalDenied {
		t.Fatalf("decide=%+v ok=%v", dec, ok)
	}
	// Unknown IDs still report not-found.
	if _, ok := s.Get("nope"); ok {
		t.Fatal("expected miss for unknown ID")
	}
	if _, ok := s.Decide("nope", true); ok {
		t.Fatal("expected miss on decide for unknown ID")
	}
}

func TestPersistListMergesMemoryOnly(t *testing.T) {
	s, fp := NewStore(), newFakePersist()
	s.SetPersistence(fp)
	fp.err = errors.New("disk gone")
	memID := s.Create("mem", "terminal", nil, 0.1, "", time.Minute)
	fp.err = nil
	dbID := s.Create("db", "terminal", nil, 0.1, "", time.Minute)
	list := s.List()
	seen := map[string]bool{}
	for _, a := range list {
		seen[a.ID] = true
	}
	if !seen[memID] || !seen[dbID] || len(list) != 2 {
		t.Fatalf("merge failed: %+v", list)
	}
}

func TestPersistDetach(t *testing.T) {
	s := NewStore()
	s.SetPersistence(newFakePersist())
	s.SetPersistence(nil)
	id := s.Create("r", "terminal", nil, 0.5, "x", time.Minute)
	if _, ok := s.Get(id); !ok {
		t.Fatal("memory store broken after detach")
	}
}

func TestListPageMemoryOnly(t *testing.T) {
	s := NewStore()
	for i := 0; i < 5; i++ {
		s.Create("r", "terminal", nil, 0.1, "", time.Hour)
		time.Sleep(time.Millisecond)
	}
	p1, total, err := s.ListPage(2, 0)
	if err != nil || total != 5 || len(p1) != 2 {
		t.Fatalf("p1=%d total=%d err=%v", len(p1), total, err)
	}
	p2, _, err := s.ListPage(2, 2)
	if err != nil || len(p2) != 2 {
		t.Fatalf("p2=%d err=%v", len(p2), err)
	}
	if !p1[0].CreatedAt.After(p2[0].CreatedAt) {
		t.Fatal("not newest-first")
	}
	seen := map[string]bool{}
	for _, a := range append(p1, p2...) {
		if seen[a.ID] {
			t.Fatalf("overlap %s", a.ID)
		}
		seen[a.ID] = true
	}
}

func TestListPageMergesMemoryOnly(t *testing.T) {
	s, fp := NewStore(), newFakePersist()
	s.SetPersistence(fp)
	fp.err = errors.New("disk gone")
	memID := s.Create("mem", "terminal", nil, 0.1, "", time.Hour)
	fp.err = nil
	dbID := s.Create("db", "terminal", nil, 0.1, "", time.Hour)
	items, total, err := s.ListPage(10, 0)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("items=%d total=%d err=%v", len(items), total, err)
	}
	seen := map[string]bool{}
	for _, a := range items {
		seen[a.ID] = true
	}
	if !seen[memID] || !seen[dbID] {
		t.Fatalf("merge failed: %+v", items)
	}
}

func TestListPageOutageFallsBackToMemory(t *testing.T) {
	s, fp := NewStore(), newFakePersist()
	s.SetPersistence(fp)
	fp.err = errors.New("disk gone")
	id := s.Create("r", "terminal", nil, 0.1, "", time.Hour)
	items, total, err := s.ListPage(10, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].ID != id {
		t.Fatalf("items=%+v total=%d err=%v", items, total, err)
	}
}

// TestListPageLaterPagesWithUnpersisted is the rank-shift regression: one
// outage-created row (newest) plus N DB rows must paginate to the same
// global order on every page, not just page 1.
func TestListPageLaterPagesWithUnpersisted(t *testing.T) {
	s, fp := NewStore(), newFakePersist()
	s.SetPersistence(fp)
	fp.err = errors.New("disk gone")
	memID := s.Create("mem", "terminal", nil, 0.1, "", time.Hour)
	fp.err = nil
	var dbIDs []string
	for i := 0; i < 4; i++ {
		dbIDs = append(dbIDs, s.Create("db", "terminal", nil, 0.1, "", time.Hour))
		time.Sleep(time.Millisecond)
	}
	// Global newest-first: mem, db3, db2, db1, db0 (mem created first but
	// the merge re-sorts; force mem newest by bumping its timestamp).
	s.mu.Lock()
	s.data[memID].CreatedAt = time.Now().Add(time.Hour)
	s.data[memID].ExpiresAt = time.Now().Add(2 * time.Hour)
	s.mu.Unlock()
	p1, total, err := s.ListPage(2, 0)
	if err != nil || total != 5 || len(p1) != 2 {
		t.Fatalf("p1=%d total=%d err=%v", len(p1), total, err)
	}
	if p1[0].ID != memID {
		t.Fatalf("p1[0]=%s want mem %s", p1[0].ID, memID)
	}
	p2, _, err := s.ListPage(2, 2)
	if err != nil || len(p2) != 2 {
		t.Fatalf("p2=%d err=%v", len(p2), err)
	}
	p3, _, err := s.ListPage(2, 4)
	if err != nil || len(p3) != 1 {
		t.Fatalf("p3=%d err=%v", len(p3), err)
	}
	seen := map[string]bool{}
	for _, a := range append(append(p1, p2...), p3...) {
		if seen[a.ID] {
			t.Fatalf("duplicate %s across pages", a.ID)
		}
		seen[a.ID] = true
	}
	if len(seen) != 5 {
		t.Fatalf("covered %d want 5", len(seen))
	}
}

func TestPendingCountIncludesUnpersisted(t *testing.T) {
	s, fp := NewStore(), newFakePersist()
	s.SetPersistence(fp)
	fp.err = errors.New("disk gone")
	s.Create("mem", "terminal", nil, 0.1, "", time.Hour)
	fp.err = nil
	dbID := s.Create("db", "terminal", nil, 0.1, "", time.Hour)
	n, err := s.PendingCount()
	if err != nil || n != 2 {
		t.Fatalf("pending=%d err=%v", n, err)
	}
	if _, ok := s.Decide(dbID, true); !ok {
		t.Fatal("decide failed")
	}
	n, err = s.PendingCount()
	if err != nil || n != 1 {
		t.Fatalf("after decide pending=%d err=%v", n, err)
	}
}

func TestPendingCountMemoryOnly(t *testing.T) {
	s := NewStore()
	s.Create("r", "terminal", nil, 0.1, "", time.Hour)
	id2 := s.Create("r", "terminal", nil, 0.1, "", time.Hour)
	if n, err := s.PendingCount(); err != nil || n != 2 {
		t.Fatalf("pending=%d err=%v", n, err)
	}
	s.Decide(id2, false)
	if n, err := s.PendingCount(); err != nil || n != 1 {
		t.Fatalf("after decide pending=%d err=%v", n, err)
	}
}
