package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

// Writer appends JSONL audit events. Path defaults to
// $HOME/.hermes/guard/audit.jsonl (v1 compat) or $AUDIT_PATH.
type Writer struct {
	mu   sync.Mutex
	file *os.File
	path string
}

func ResolvePath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if p := os.Getenv("AUDIT_PATH"); p != "" {
		return p
	}
	return filepath.Join(os.Getenv("HOME"), ".hermes", "guard", "audit.jsonl")
}

func NewWriter(path string) (*Writer, error) {
	path = ResolvePath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{file: f, path: path}, nil
}

func (w *Writer) Path() string { return w.path }

// Record satisfies harness.Auditor.
func (w *Writer) Record(e domain.AuditEvent) {
	if e.ID == "" {
		e.ID = time.Now().Format("20060102150405.000000000")
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	data, _ := json.Marshal(e)
	w.file.Write(append(data, '\n'))
}

// Reader scans the JSONL log for CLI/API queries.
type Reader struct{ path string }

func NewReader(path string) *Reader { return &Reader{path: ResolvePath(path)} }

func (r *Reader) All() ([]domain.AuditEvent, error) {
	f, err := os.Open(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []domain.AuditEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var e domain.AuditEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Filter returns events matching decision and/or minimum risk.
func (r *Reader) Filter(decision string, minRisk float64) ([]domain.AuditEvent, error) {
	all, err := r.All()
	if err != nil {
		return nil, err
	}
	var out []domain.AuditEvent
	for _, e := range all {
		if decision != "" && e.FinalDecision != decision {
			continue
		}
		if e.Risk < minRisk {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
