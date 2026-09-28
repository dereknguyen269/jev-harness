package menubar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testPage() Page {
	return Page{
		Pending: 1,
		Approvals: []Approval{
			{ID: "abcdef123456", Tool: "terminal", Risk: 0.9,
				Reason: "sudo reboot", Status: "pending",
				ExpiresAt: time.Now().Add(30 * time.Second)},
		},
	}
}

// servePage returns a mock gateway answering the approvals surface.
func servePage(t *testing.T, page Page, status int, seen *struct {
	auth string
	path string
}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			seen.auth = r.Header.Get("Authorization")
			seen.path = r.URL.Path
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
}

func TestFetch(t *testing.T) {
	var seen struct{ auth, path string }
	srv := servePage(t, testPage(), http.StatusOK, &seen)
	defer srv.Close()

	c := NewClient(srv.URL+"///", "tok123") // trailing slashes normalized
	p, err := c.Fetch()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if p.Pending != 1 || len(p.Approvals) != 1 {
		t.Fatalf("page=%+v", p)
	}
	a := p.Approvals[0]
	if !a.Pending() || a.ShortID() != "abcdef12" || a.Tool != "terminal" {
		t.Fatalf("approval=%+v", a)
	}
	if seen.auth != "Bearer tok123" {
		t.Fatalf("auth=%q", seen.auth)
	}
	if seen.path != "/v1/approvals/page" {
		t.Fatalf("path=%q", seen.path)
	}
}

func TestFetchAuth(t *testing.T) {
	srv := servePage(t, testPage(), http.StatusUnauthorized, nil)
	defer srv.Close()
	if _, err := NewClient(srv.URL, "").Fetch(); !IsAuthErr(err) {
		t.Fatalf("want auth err, got %v", err)
	}
	srv2 := servePage(t, testPage(), http.StatusForbidden, nil)
	defer srv2.Close()
	if _, err := NewClient(srv2.URL, "").Fetch(); !IsAuthErr(err) {
		t.Fatalf("want auth err, got %v", err)
	}
}

func TestDecide(t *testing.T) {
	var seen struct{ auth, path string }
	srv := servePage(t, testPage(), http.StatusOK, &seen)
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	if err := c.Decide("abcdef123456", true); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if seen.path != "/v1/approvals/abcdef123456/approve" {
		t.Fatalf("path=%q", seen.path)
	}
	if err := c.Decide("abcdef123456", false); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if seen.path != "/v1/approvals/abcdef123456/deny" {
		t.Fatalf("path=%q", seen.path)
	}
}

func TestWatchArrivalsOnce(t *testing.T) {
	srv := servePage(t, testPage(), http.StatusOK, nil)
	defer srv.Close()
	c := NewClient(srv.URL, "")
	w := &Watch{}

	arr, pending, err := w.PollOnce(c)
	if err != nil || len(arr) != 1 || len(pending) != 1 {
		t.Fatalf("first poll: arr=%d pending=%d err=%v", len(arr), len(pending), err)
	}
	arr, pending, err = w.PollOnce(c)
	if err != nil || len(arr) != 0 || len(pending) != 1 {
		t.Fatalf("second poll must not re-notify: arr=%d pending=%d err=%v", len(arr), len(pending), err)
	}
}

func TestWatchForgetsDecided(t *testing.T) {
	page := testPage()
	srv := servePage(t, page, http.StatusOK, nil)
	defer srv.Close()
	c := NewClient(srv.URL, "")
	w := &Watch{}
	if _, _, err := w.PollOnce(c); err != nil {
		t.Fatal(err)
	}
	// Approval decided server-side: page now empty.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"approvals":[],"pending":0}`))
	})
	arr, pending, err := w.PollOnce(c)
	if err != nil || len(arr) != 0 || len(pending) != 0 {
		t.Fatalf("decided: arr=%d pending=%d err=%v", len(arr), len(pending), err)
	}
	if len(w.seen) != 0 {
		t.Fatalf("seen not pruned: %v", w.seen)
	}
}

func TestWatchOffline(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "") // nothing listens here
	w := &Watch{}
	if _, _, err := w.PollOnce(c); err == nil || IsAuthErr(err) {
		t.Fatalf("want non-auth err, got %v", err)
	}
}

func TestApprovalHelpers(t *testing.T) {
	if !(Approval{Status: "pending"}.Pending()) || (Approval{Status: "approved"}.Pending()) {
		t.Fatal("Pending wrong")
	}
	if (Approval{ID: "abc"}.ShortID()) != "abc" {
		t.Fatal("ShortID wrong")
	}
	if !strings.HasPrefix((Approval{ID: "abcdef123456"}.ShortID()), "abcdef12") {
		t.Fatal("ShortID prefix wrong")
	}
}
