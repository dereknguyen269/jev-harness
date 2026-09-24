package cache

import (
	"sync"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

// Cache is a TTL decision cache. Only read/low-risk decisions should be
// stored — the harness enforces that; this store is dumb + safe.
type Cache struct {
	mu   sync.RWMutex
	data map[string]entry
}

type entry struct {
	value     domain.DecisionResult
	expiresAt time.Time
}

func New() *Cache { return &Cache{data: make(map[string]entry)} }

func (c *Cache) Get(key string) (domain.DecisionResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.data[key]
	if !ok || time.Now().After(e.expiresAt) {
		return domain.DecisionResult{}, false
	}
	return e.value, true
}

func (c *Cache) Set(key string, value domain.DecisionResult, ttl time.Duration) {
	if ttl <= 0 {
		return // mutation/critical: never cache
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = entry{value: value, expiresAt: time.Now().Add(ttl)}
}
