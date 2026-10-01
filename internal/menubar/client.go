package menubar

// HTTP client for the gateway surface the agent plugins already use:
// GET /v1/approvals/page (poll) and POST /v1/approvals/{id}/approve|deny.
// Auth mirrors the plugins: Authorization: Bearer from --auth-token /
// JEV_AUTH_TOKEN; the gateway answers 401/403 on mismatch.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnauthorized signals a 401/403 from the gateway. Callers surface it
// as an "auth mismatch — set JEV_AUTH_TOKEN" state instead of failing
// silently: a notifier that can't read approvals must say so.
type ErrUnauthorized struct{ Status int }

func (e *ErrUnauthorized) Error() string {
	return fmt.Sprintf("gateway returned HTTP %d (auth mismatch?)", e.Status)
}

// Approval is the JSON subset the tray needs from /v1/approvals/page.
type Approval struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Risk      float64        `json:"risk"`
	Reason    string         `json:"reason"`
	Status    string         `json:"status"`
	ExpiresAt time.Time      `json:"expires_at"`
}

// Pending reports whether the approval still needs a human.
func (a Approval) Pending() bool { return a.Status == "pending" }

// ShortID is the 8-char prefix the approve|deny CLI also accepts.
func (a Approval) ShortID() string {
	if len(a.ID) > 8 {
		return a.ID[:8]
	}
	return a.ID
}

// RiskLevel classifies the score for the approve/deny prompt (mirrors
// domain.RiskFromScore without importing domain from the tray).
func RiskLevel(risk float64) string {
	switch {
	case risk >= 0.95:
		return "CRITICAL"
	case risk >= 0.70:
		return "PRIVILEGED"
	case risk >= 0.40:
		return "MUTATION"
	case risk >= 0.15:
		return "LOW"
	default:
		return "READ"
	}
}

// FormatArgs renders the attempted action for the decision prompt:
// command > path/file > raw JSON. Always non-empty for display.
func FormatArgs(a Approval) string {
	if len(a.Arguments) == 0 {
		return "—"
	}
	for _, k := range []string{"command", "cmd", "commandLine", "path", "file", "file_path", "url"} {
		if v, ok := a.Arguments[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	b, err := json.Marshal(a.Arguments)
	if err != nil || len(b) == 0 {
		return "—"
	}
	return string(b)
}

// Page mirrors server.ApprovalsPage (approvals newest-first + live count).
type Page struct {
	Approvals []Approval `json:"approvals"`
	Pending   int        `json:"pending"`
}

// Client talks to one gateway over localhost HTTP.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// NewClient normalizes the base URL (trailing slashes break path joins).
func NewClient(baseURL, token string) *Client {
	return &Client{
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) req(method, path string, body io.Reader) (*http.Request, error) {
	r, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	return r, nil
}

func checkAuth(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &ErrUnauthorized{Status: resp.StatusCode}
	}
	return nil
}

// Fetch returns the newest approvals page (per_page=20, enough for a tray).
func (c *Client) Fetch() (Page, error) {
	var p Page
	r, err := c.req(http.MethodGet, "/v1/approvals/page?per_page=20", nil)
	if err != nil {
		return p, err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return p, err
	}
	defer resp.Body.Close()
	if err := checkAuth(resp); err != nil {
		return p, err
	}
	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("approvals page: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return p, err
	}
	if p.Approvals == nil {
		p.Approvals = []Approval{}
	}
	return p, nil
}

// Decide approves (true) or denies one approval by full ID.
func (c *Client) Decide(id string, approve bool) error {
	verb := "deny"
	if approve {
		verb = "approve"
	}
	r, err := c.req(http.MethodPost, "/v1/approvals/"+id+"/"+verb, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkAuth(resp); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: HTTP %d", verb, id, resp.StatusCode)
	}
	return nil
}
