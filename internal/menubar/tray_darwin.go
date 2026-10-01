//go:build darwin

package menubar

// Status-item wiring (macOS only). Linux CI runs go test ./... without
// GTK headers, so anything importing systray lives behind this build tag;
// the client/poller stay portable and tested everywhere.

import (
	_ "embed"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gen2brain/beeep"
	"github.com/getlantern/systray"
)

//go:embed tray.png
var trayIcon []byte

// maxSlots bounds the menu: systray can't remove items, so pending
// approvals beyond this spill into an "open dashboard" overflow note.
const maxSlots = 5

type slot struct {
	parent  *systray.MenuItem
	approve *systray.MenuItem
	deny    *systray.MenuItem
	id      string
}

type app struct {
	client *Client
	poll   time.Duration
	watch  *Watch
	status *systray.MenuItem
	slots  []slot
	extra  *systray.MenuItem
	// mu guards slot IDs: refresh writes them on the poll goroutine while
	// click handlers read them. (systray menu calls themselves are
	// thread-safe; the IDs are ours.)
	mu sync.Mutex
}

// Run blocks on the tray event loop until Quit.
func Run(c *Client, poll time.Duration) {
	a := &app{client: c, poll: poll, watch: &Watch{}}
	systray.Run(a.onReady, func() {})
}

func (a *app) onReady() {
	systray.SetIcon(trayIcon)
	systray.SetTooltip("Jev Guard")
	a.status = systray.AddMenuItem("Connecting…", "")
	a.status.Disable()
	systray.AddSeparator()
	for i := 0; i < maxSlots; i++ {
		parent := systray.AddMenuItem("", "")
		parent.Disable()
		a.slots = append(a.slots, slot{
			parent:  parent,
			approve: parent.AddSubMenuItem("Approve", ""),
			deny:    parent.AddSubMenuItem("Deny", ""),
		})
	}
	a.extra = systray.AddMenuItem("", "")
	a.extra.Disable()
	systray.AddSeparator()
	dash := systray.AddMenuItem("Open dashboard", "")
	quit := systray.AddMenuItem("Quit", "")

	go a.loop()
	go func() {
		for range dash.ClickedCh {
			_ = exec.Command("open", a.client.base+"/").Start()
		}
	}()
	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()
	for i := range a.slots {
		s := &a.slots[i]
		go func() {
			for range s.approve.ClickedCh {
				a.decide(a.slotID(s), true)
			}
		}()
		go func() {
			for range s.deny.ClickedCh {
				a.decide(a.slotID(s), false)
			}
		}()
	}
}

// slotID reads a slot's approval ID under lock (refresh writes it on the
// poll goroutine).
func (a *app) slotID(s *slot) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return s.id
}
func (a *app) decide(id string, approve bool) {
	if id == "" {
		return
	}
	if err := a.client.Decide(id, approve); err != nil {
		// Status stays disabled (no handler); the text alone reports it.
		a.status.SetTitle("Decision failed: " + shortErr(err))
		return
	}
	a.refresh()
}

func (a *app) loop() {
	t := time.NewTicker(a.poll)
	defer t.Stop()
	a.refresh()
	for range t.C {
		a.refresh()
	}
}

func (a *app) refresh() {
	arrivals, pending, err := a.watch.PollOnce(a.client)
	switch {
	case err != nil && IsAuthErr(err):
		a.setIdle("Auth mismatch — set JEV_AUTH_TOKEN", nil, 0)
		return
	case err != nil:
		a.setIdle("Guard offline", nil, 0)
		return
	}
	for _, ap := range arrivals {
		_ = beeep.Notify("Jev Guard — approval required",
			fmt.Sprintf("%s [%.2f %s]: %s — %s", ap.Tool, ap.Risk, RiskLevel(ap.Risk), oneLine(ap.Reason, 120), oneLine(FormatArgs(ap), 60)), "")
	}
	a.setIdle("", pending, len(pending))
}

func (a *app) setIdle(status string, pending []Approval, n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if status != "" {
		a.status.SetTitle(status)
	}
	if n == 0 && status == "" {
		a.status.SetTitle("No pending approvals")
	}
	if n > 0 {
		a.status.SetTitle(fmt.Sprintf("%d pending approval(s)", n))
		systray.SetTitle(fmt.Sprintf("%d", n))
	} else {
		systray.SetTitle("")
	}
	systray.SetTooltip(fmt.Sprintf("Jev Guard — %d pending", n))

	shown := pending
	overflow := 0
	if len(shown) > maxSlots {
		overflow = len(shown) - maxSlots
		shown = shown[:maxSlots]
	}
	for i := range a.slots {
		s := &a.slots[i]
		if i < len(shown) {
			ap := shown[i]
			s.id = ap.ID
			s.parent.SetTitle(fmt.Sprintf("%s [%.2f %s] — %s", ap.Tool, ap.Risk, RiskLevel(ap.Risk), oneLine(ap.Reason, 60)))
			// Tooltip carries the full why + what for the approve/deny decision.
			s.parent.SetTooltip(fmt.Sprintf("%s\n%s\n%s", ap.Tool, ap.Reason, FormatArgs(ap)))
			s.parent.Enable()
			s.approve.Enable()
			s.deny.Enable()
		} else {
			s.id = ""
			s.parent.SetTitle("")
			s.parent.Disable()
			s.approve.Disable()
			s.deny.Disable()
		}
	}
	if overflow > 0 {
		a.extra.SetTitle(fmt.Sprintf("+%d more — open dashboard", overflow))
	} else {
		a.extra.SetTitle("")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(firstLine(s), "\t", " ")
	if len(s) > max {
		return s[:max-1] + "…"
	}
	if s == "" {
		return "(no reason)"
	}
	return s
}

func shortErr(err error) string {
	s := firstLine(err.Error())
	if len(s) > 80 {
		return s[:79] + "…"
	}
	return s
}
