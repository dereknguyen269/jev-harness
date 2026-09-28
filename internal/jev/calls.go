package jev

import (
	"sync"
	"time"
)

// Call is one outbound AI API request made by the client.
type Call struct {
	Timestamp    time.Time `json:"timestamp"`
	Model        string    `json:"model"`
	Endpoint     string    `json:"endpoint"`
	Status       string    `json:"status"` // "ok" or "error"
	HTTPStatus   int       `json:"http_status"`
	LatencyMS    int64     `json:"latency_ms"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	Error        string    `json:"error,omitempty"`
}

// CallPersistence is the SQLite backend for AI call records (implemented
// by internal/store.Store). Interface-only, so jev stays storage-free.
type CallPersistence interface {
	InsertJevCall(Call) error
}

// CallLog is a bounded in-memory ring of recent API calls. The client
// additionally write-throughs every call to SQLite when persistence is
// attached, so history survives restarts.
type CallLog struct {
	mu    sync.Mutex
	calls []Call
	cap   int
}

func NewCallLog(capacity int) *CallLog {
	if capacity <= 0 {
		capacity = 200
	}
	return &CallLog{cap: capacity}
}

func (l *CallLog) Add(c Call) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, c)
	if len(l.calls) > l.cap {
		l.calls = append([]Call{}, l.calls[len(l.calls)-l.cap:]...)
	}
}

// List returns calls newest-first, up to limit (<=0 means all).
func (l *CallLog) List(limit int) []Call {
	out := []Call{}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.calls) - 1; i >= 0; i-- {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, l.calls[i])
	}
	return out
}
