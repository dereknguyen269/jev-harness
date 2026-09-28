package approval

import (
	"sort"
	"sync"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/google/uuid"
)

// Persistence is the SQLite backend for approvals (implemented by
// internal/store.Store). It is an interface — not an import — so the
// approval package stays free of storage dependencies and YAML-only
// mode (nil persistence) keeps working purely in memory.
type Persistence interface {
	CreateApproval(requestID, tool string, args map[string]any, risk float64, reason string, ttl time.Duration) (domain.Approval, error)
	GetApproval(id string) (domain.Approval, error)
	ListApprovals() ([]domain.Approval, error)
	ListApprovalsPage(limit, offset int) ([]domain.Approval, int, error)
	PendingCount() (int, error)
	DecideApproval(id string, approve bool) (domain.Approval, error)
}

// Store is an approval store with TTL expiry. Without persistence it is
// purely in-memory; with SetPersistence it write-throughs to SQLite so
// approvals survive restarts and show in the dashboard. The in-memory map
// is always kept as a fallback so a DB outage never breaks the pipeline:
// entries created while the DB is down stay visible via List/Get/Decide.
type Store struct {
	mu      sync.RWMutex
	data    map[string]*domain.Approval
	persist Persistence
	// unpersisted tracks IDs minted in memory while the DB was down. Those
	// rows never reach the DB, so paged reads merge exactly this set —
	// never the whole memory map (which also caches DB rows).
	unpersisted map[string]bool
}

func NewStore() *Store {
	return &Store{data: make(map[string]*domain.Approval), unpersisted: make(map[string]bool)}
}

// SetPersistence attaches (or, with nil, detaches) the SQLite backend.
func (s *Store) SetPersistence(p Persistence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persist = p
}

func (s *Store) Create(requestID, tool string, args map[string]any, risk float64, reason string, ttl time.Duration) string {
	now := time.Now()
	a := &domain.Approval{
		ID: uuid.NewString(), RequestID: requestID, Tool: tool, Arguments: args,
		Risk: risk, Reason: reason,
		Status:    domain.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	s.mu.Lock()
	p := s.persist
	s.mu.Unlock()
	unpersisted := false
	if p != nil {
		if saved, err := p.CreateApproval(requestID, tool, args, risk, reason, ttl); err == nil {
			a = &saved
		} else {
			// DB down — keep the in-memory entry so the flow still works.
			unpersisted = true
		}
	}
	s.mu.Lock()
	s.data[a.ID] = a
	if unpersisted {
		s.unpersisted[a.ID] = true
	} else {
		delete(s.unpersisted, a.ID)
	}
	s.mu.Unlock()
	return a.ID
}

func (s *Store) Get(id string) (*domain.Approval, bool) {
	s.mu.RLock()
	p := s.persist
	a, ok := s.data[id]
	s.mu.RUnlock()
	if p != nil {
		if saved, err := p.GetApproval(id); err == nil {
			a = &saved
			ok = true
			s.mu.Lock()
			s.data[id] = a
			s.mu.Unlock()
		}
		// else: DB miss or outage — fall through to the memory copy.
	}
	if !ok {
		return nil, false
	}
	cpy := *a
	return &cpy, true
}

func (s *Store) List() []domain.Approval {
	s.mu.RLock()
	p := s.persist
	mem := make([]domain.Approval, 0, len(s.data))
	for _, a := range s.data {
		mem = append(mem, *a)
	}
	s.mu.RUnlock()
	if p == nil {
		return mem
	}
	saved, err := p.ListApprovals()
	if err != nil {
		return mem // DB outage — memory still answers the dashboard.
	}
	seen := make(map[string]bool, len(saved))
	for _, a := range saved {
		seen[a.ID] = true
	}
	for _, a := range mem {
		if !seen[a.ID] {
			saved = append(saved, a)
		}
	}
	return saved
}

// ListPage returns one page of approvals, newest first, plus the total
// count. limit <= 0 means all rows from offset. Memory-only rows (created
// while the DB was down) are merged in and counted in the total so a
// transient outage never hides a pending human decision from the page.
func (s *Store) ListPage(limit, offset int) ([]domain.Approval, int, error) {
	s.mu.RLock()
	p := s.persist
	mem := make([]domain.Approval, 0, len(s.data))
	for _, a := range s.data {
		mem = append(mem, *a)
	}
	s.mu.RUnlock()
	if p == nil {
		sortApprovalsNewestFirst(mem)
		return slicePage(mem, limit, offset), len(mem), nil
	}
	items, total, err := func() ([]domain.Approval, int, error) {
		only := s.unpersistedSnapshot()
		if len(only) == 0 {
			return p.ListApprovalsPage(limit, offset)
		}
		// Unpersisted rows sort into global rank order and shift every DB
		// row after them: widen the DB window to cover the offset, then
		// merge/sort/slice below. limit <= 0 means all rows.
		if limit <= 0 {
			return p.ListApprovalsPage(0, 0)
		}
		return p.ListApprovalsPage(limit+offset+len(only), 0)
	}()
	if err != nil {
		// DB outage — the memory fallback answers the dashboard.
		sortApprovalsNewestFirst(mem)
		return slicePage(mem, limit, offset), len(mem), nil
	}
	// Merge exactly the rows the DB never saw (created during an outage).
	only := s.unpersistedSnapshot()
	merged := false
	for _, a := range mem {
		if only[a.ID] {
			items = append(items, a)
			total++
			merged = true
		}
	}
	if merged {
		// Memory-only rows were appended past the DB page: re-sort and
		// re-slice so the page window still holds.
		sortApprovalsNewestFirst(items)
		items = slicePage(items, limit, offset)
	}
	return items, total, nil
}

// unpersistedSnapshot copies the unpersisted ID set for lock-free merging.
func (s *Store) unpersistedSnapshot() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.unpersisted))
	for id := range s.unpersisted {
		out[id] = true
	}
	return out
}

func sortApprovalsNewestFirst(items []domain.Approval) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
}

// slicePage returns items[offset:offset+limit], newest-first order
// preserved. limit <= 0 means all rows from offset.
func slicePage(items []domain.Approval, limit, offset int) []domain.Approval {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []domain.Approval{}
	}
	items = items[offset:]
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}

// PendingCount returns the live pending count: the DB count plus any
// unpersisted rows still pending (never double-counted — unpersisted rows
// are absent from the DB by construction). YAML-only mode counts memory.
func (s *Store) PendingCount() (int, error) {
	s.mu.RLock()
	p := s.persist
	var unpersistedPending int
	for id := range s.unpersisted {
		if a, ok := s.data[id]; ok && a.Status == domain.ApprovalPending {
			unpersistedPending++
		}
	}
	var memPending int
	if p == nil {
		for _, a := range s.data {
			if a.Status == domain.ApprovalPending {
				memPending++
			}
		}
	}
	s.mu.RUnlock()
	if p == nil {
		return memPending, nil
	}
	n, err := p.PendingCount()
	if err != nil {
		return 0, err
	}
	return n + unpersistedPending, nil
}

// Decide marks pending→approved|denied. Expired approvals → expired.
func (s *Store) Decide(id string, approve bool) (*domain.Approval, bool) {
	s.mu.RLock()
	p := s.persist
	s.mu.RUnlock()
	if p != nil {
		if saved, err := p.DecideApproval(id, approve); err == nil {
			s.mu.Lock()
			s.data[id] = &saved
			s.mu.Unlock()
			cpy := saved
			return &cpy, true
		}
		// else: unknown ID or DB outage — fall through to memory.
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.data[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(a.ExpiresAt) {
		a.Status = domain.ApprovalExpired
		cpy := *a
		return &cpy, true
	}
	if a.Status != domain.ApprovalPending {
		cpy := *a
		return &cpy, true
	}
	if approve {
		a.Status = domain.ApprovalApproved
	} else {
		a.Status = domain.ApprovalDenied
	}
	cpy := *a
	return &cpy, true
}
