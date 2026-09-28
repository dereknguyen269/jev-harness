//go:build !darwin

package menubar

import (
	"errors"
	"time"
)

// Run is a stub off macOS: the status item needs Cocoa (via systray/cgo),
// which Linux CI can't compile (no GTK headers). The client/poller above
// stay portable; only this entrypoint is gated.
func Run(c *Client, poll time.Duration) {
	_ = c
	_ = poll
	panic(errors.New("menubar: macOS only"))
}
