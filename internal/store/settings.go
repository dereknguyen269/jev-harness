package store

import (
	"strconv"
	"strings"
)

// Dashboard settings keys (settings table).
const (
	// SettingApprovalTTLSeconds is the UI-configured default approval TTL.
	SettingApprovalTTLSeconds = "approval_ttl_seconds"
)

// GetSetting reads one settings row. sql.ErrNoRows when the key was never saved.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetSetting inserts or replaces one settings row.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetApprovalTTLSeconds returns the UI-configured default TTL in seconds.
// ok=false when unset or unparsable (caller falls back to flag/env default).
func (s *Store) GetApprovalTTLSeconds() (secs int, ok bool) {
	raw, err := s.GetSetting(SettingApprovalTTLSeconds)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
