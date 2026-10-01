package menubar

// Watch turns approval pages into arrival events: it remembers every
// pending ID it has notified for, emits newcomers once, and forgets IDs
// that stop being pending (approved/denied/expired/disappeared) so a
// future approval with the same ID would notify again.

import (
	"errors"
)

// Watch tracks notified approval IDs across polls.
type Watch struct {
	seen map[string]bool
}

// PollOnce fetches one page and returns the newly-pending arrivals plus
// the current pending list (newest-first, as served). Auth/offline errors
// propagate unwrapped so callers can type-assert ErrUnauthorized.
func (w *Watch) PollOnce(c *Client) (arrivals, pending []Approval, err error) {
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	p, err := c.Fetch()
	if err != nil {
		return nil, nil, err
	}
	live := map[string]bool{}
	for _, a := range p.Approvals {
		if !a.Pending() {
			continue
		}
		live[a.ID] = true
		pending = append(pending, a)
		if !w.seen[a.ID] {
			w.seen[a.ID] = true
			arrivals = append(arrivals, a)
		}
	}
	// Forget decided/expired IDs (bounded memory, correct re-notify).
	for id := range w.seen {
		if !live[id] {
			delete(w.seen, id)
		}
	}
	return arrivals, pending, nil
}

// IsAuthErr reports whether err is (or wraps) an auth mismatch.
func IsAuthErr(err error) bool {
	var unauth *ErrUnauthorized
	return errors.As(err, &unauth)
}
