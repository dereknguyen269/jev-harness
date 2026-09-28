package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/domain"
	"github.com/google/uuid"
)

// Approval persistence.
//
// The approvals table is runtime data (like users): it is never seeded,
// reseeded, or pruned by policy sync - SyncDefaults/ReplaceWith/
// PruneStaleDefaults leave it untouched. When the gateway runs YAML-only
// (no store) approvals live in the in-memory approval.Store only.

// approvalTimeFormat keeps timestamps lexicographically sortable so
// expiry comparisons and ORDER BY work as plain string compares in SQLite.
//
// Fixed 9-digit fraction, NOT time.RFC3339Nano: RFC3339Nano's ".999999999"
// layout strips trailing zeros, so 0.100s formats as "...:00.1Z" and 0.120s
// as "...:00.12Z" - and "Z" (0x5A) > "2" (0x32), so the shorter string sorts
// LAST. Ordering and expiry would be wrong within any one-second bucket.
const approvalTimeFormat = "2006-01-02T15:04:05.000000000Z"

func encodeApprovalArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func decodeApprovalArgs(raw string) map[string]any {
	out := map[string]any{}
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func scanApproval(scanner interface {
	Scan(dest ...any) error
}) (domain.Approval, error) {
	var a domain.Approval
	var argsJSON, createdAt, expiresAt string
	err := scanner.Scan(
		&a.ID, &a.RequestID, &a.Tool, &argsJSON,
		&a.Risk, &a.Reason, &a.Status, &createdAt, &expiresAt,
	)
	if err != nil {
		return domain.Approval{}, err
	}
	a.Arguments = decodeApprovalArgs(argsJSON)
	// Parse leniently: rows written before the fixed-width format carry
	// RFC3339Nano's variable-length fraction (or none). RFC3339Nano
	// parses every variant, fixed included.
	if a.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return domain.Approval{}, err
	}
	if a.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return domain.Approval{}, err
	}
	return a, nil
}

// expireDueApprovals flips pending rows past their TTL to expired. It runs
// at the top of every read/decide so the dashboard never shows a stale
// pending row. RFC3339Nano UTC strings sort chronologically, so a plain
// string comparison is exact.
func (s *Store) expireDueApprovals() error {
	_, err := s.db.Exec(`UPDATE approvals SET status = 'expired'
		WHERE status = 'pending' AND expires_at < ?`,
		time.Now().UTC().Format(approvalTimeFormat))
	return err
}

// CreateApproval inserts a pending approval with a fresh UUID and TTL.
func (s *Store) CreateApproval(requestID, tool string, args map[string]any, risk float64, reason string, ttl time.Duration) (domain.Approval, error) {
	now := time.Now()
	a := domain.Approval{
		ID:        uuid.NewString(),
		RequestID: requestID, Tool: tool, Arguments: args,
		Risk: risk, Reason: reason,
		Status:    domain.ApprovalPending,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	_, err := s.db.Exec(`INSERT INTO approvals
		(id, request_id, tool, arguments, risk, reason, status, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.RequestID, a.Tool, encodeApprovalArgs(args), a.Risk, a.Reason,
		string(a.Status),
		a.CreatedAt.UTC().Format(approvalTimeFormat),
		a.ExpiresAt.UTC().Format(approvalTimeFormat))
	if err != nil {
		return domain.Approval{}, err
	}
	return a, nil
}

// GetApproval fetches one row by ID. sql.ErrNoRows when unknown.
func (s *Store) GetApproval(id string) (domain.Approval, error) {
	if err := s.expireDueApprovals(); err != nil {
		return domain.Approval{}, err
	}
	return scanApproval(s.db.QueryRow(`SELECT id, request_id, tool, arguments,
		risk, reason, status, created_at, expires_at FROM approvals WHERE id = ?`, id))
}

// ListApprovals returns every row, newest first.
func (s *Store) ListApprovals() ([]domain.Approval, error) {
	items, _, err := s.ListApprovalsPage(0, 0)
	return items, err
}

// CountApprovals returns the total row count (after expiring due rows).
func (s *Store) CountApprovals() (int, error) {
	if err := s.expireDueApprovals(); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM approvals`).Scan(&n)
	return n, err
}

// PendingCount returns the number of still-pending rows (after expiring
// due rows). Powers the dashboard tab badge without pulling the history.
func (s *Store) PendingCount() (int, error) {
	if err := s.expireDueApprovals(); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM approvals WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// ListApprovalsPage returns rows newest-first with LIMIT/OFFSET pagination
// plus the total row count. limit <= 0 means no limit (all rows from
// offset); offset < 0 is treated as 0.
func (s *Store) ListApprovalsPage(limit, offset int) ([]domain.Approval, int, error) {
	if err := s.expireDueApprovals(); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM approvals`).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, request_id, tool, arguments,
		risk, reason, status, created_at, expires_at FROM approvals
		ORDER BY created_at DESC, rowid DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		if offset < 0 {
			offset = 0
		}
		rows, err = s.db.Query(q+` LIMIT ? OFFSET ?`, limit, offset)
	} else if offset > 0 {
		rows, err = s.db.Query(q+` LIMIT -1 OFFSET ?`, offset)
	} else {
		rows, err = s.db.Query(q)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// DecideApproval marks pending to approved or denied. Pending rows past
// their TTL flip to expired instead; already-decided rows are returned
// as-is. sql.ErrNoRows when the ID is unknown. Mirrors approval.Store.Decide.
func (s *Store) DecideApproval(id string, approve bool) (domain.Approval, error) {
	if err := s.expireDueApprovals(); err != nil {
		return domain.Approval{}, err
	}
	a, err := scanApproval(s.db.QueryRow(`SELECT id, request_id, tool, arguments,
		risk, reason, status, created_at, expires_at FROM approvals WHERE id = ?`, id))
	if err != nil {
		return domain.Approval{}, err
	}
	if a.Status != domain.ApprovalPending {
		return a, nil
	}
	newStatus := domain.ApprovalDenied
	if approve {
		newStatus = domain.ApprovalApproved
	}
	res, err := s.db.Exec(`UPDATE approvals SET status = ?
		WHERE id = ? AND status = 'pending'`, string(newStatus), id)
	if err != nil {
		return domain.Approval{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return domain.Approval{}, err
	} else if n == 0 {
		// Lost a race with expiry (or a concurrent decider): re-read.
		return scanApproval(s.db.QueryRow(`SELECT id, request_id, tool, arguments,
			risk, reason, status, created_at, expires_at FROM approvals WHERE id = ?`, id))
	}
	a.Status = newStatus
	return a, nil
}
