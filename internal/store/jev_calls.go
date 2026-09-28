package store

import (
	"database/sql"
	"time"

	"github.com/dereknguyen269/jev-harness/internal/jev"
)

// MaxJevCalls bounds the jev_calls table: every insert prunes older rows
// beyond this. AI judgments are high-volume; 1000 rows is plenty for the
// dashboard while keeping the DB small.
const MaxJevCalls = 1000

// InsertJevCall appends one outbound AI API call record, then prunes rows
// beyond MaxJevCalls (newest kept).
func (s *Store) InsertJevCall(c jev.Call) error {
	ts := c.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO jev_calls
		(timestamp, model, endpoint, status, http_status, latency_ms, input_tokens, output_tokens, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.UTC().Format(approvalTimeFormat), c.Model, c.Endpoint, c.Status,
		c.HTTPStatus, c.LatencyMS, c.InputTokens, c.OutputTokens, c.Error)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM jev_calls WHERE id NOT IN
		(SELECT id FROM jev_calls ORDER BY id DESC LIMIT ?)`, MaxJevCalls)
	return err
}

// ListJevCalls returns call records newest-first, up to limit (<=0 means all).
func (s *Store) ListJevCalls(limit int) ([]jev.Call, error) {
	q := `SELECT timestamp, model, endpoint, status, http_status,
		latency_ms, input_tokens, output_tokens, error FROM jev_calls ORDER BY id DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(q+` LIMIT ?`, limit)
	} else {
		rows, err = s.db.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jev.Call{}
	for rows.Next() {
		var c jev.Call
		var ts string
		if err := rows.Scan(&ts, &c.Model, &c.Endpoint, &c.Status,
			&c.HTTPStatus, &c.LatencyMS, &c.InputTokens, &c.OutputTokens, &c.Error); err != nil {
			return nil, err
		}
		if parsed, perr := time.Parse(time.RFC3339Nano, ts); perr == nil {
			c.Timestamp = parsed
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
