package approval

import (
	"sync"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/google/uuid"
)

// Store is an in-memory approval store with TTL expiry.
type Store struct {
	mu   sync.RWMutex
	data map[string]*domain.Approval
}

func NewStore() *Store { return &Store{data: make(map[string]*domain.Approval)} }

func (s *Store) Create(requestID, tool string, args map[string]any, risk float64, reason string, ttl time.Duration) string {
	id := uuid.NewString()
	now := time.Now()
	a := &domain.Approval{
		ID: id, RequestID: requestID, Tool: tool, Arguments: args,
		Risk: risk, Reason: reason,
		Status:    domain.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	s.mu.Lock()
	s.data[id] = a
	s.mu.Unlock()
	return id
}

func (s *Store) Get(id string) (*domain.Approval, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.data[id]
	if !ok {
		return nil, false
	}
	cpy := *a
	return &cpy, true
}

func (s *Store) List() []domain.Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Approval, 0, len(s.data))
	for _, a := range s.data {
		cpy := *a
		out = append(out, cpy)
	}
	return out
}

// Decide marks pending→approved|denied. Expired approvals → expired.
func (s *Store) Decide(id string, approve bool) (*domain.Approval, bool) {
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
