package jev

import (
	"errors"
	"testing"
	"time"
)

type fakePersist struct {
	calls []Call
	err   error
}

func (f *fakePersist) InsertJevCall(c Call) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, c)
	return nil
}

func TestClientPersistWriteThrough(t *testing.T) {
	c := NewClient("k", "https://example.test/eval", "m", "")
	fp := &fakePersist{}
	c.SetPersistence(fp)
	c.logCall(time.Now(), 200, nil, 10, 5)
	if len(fp.calls) != 1 {
		t.Fatalf("persisted=%+v", fp.calls)
	}
	got := fp.calls[0]
	if got.Model != "m" || got.Endpoint != "https://example.test/eval" || got.Status != "ok" ||
		got.HTTPStatus != 200 || got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Fatalf("got %+v", got)
	}
	if len(c.Calls(0)) != 1 {
		t.Fatal("memory ring should also hold the call")
	}
}

func TestClientPersistOutageKeepsMemory(t *testing.T) {
	c := NewClient("k", "", "", "")
	fp := &fakePersist{err: errors.New("disk gone")}
	c.SetPersistence(fp)
	c.logCall(time.Now(), 0, errors.New("boom"), 0, 0)
	if len(fp.calls) != 0 {
		t.Fatalf("failed persist should not record: %+v", fp.calls)
	}
	mem := c.Calls(0)
	if len(mem) != 1 || mem[0].Status != "error" {
		t.Fatalf("memory fallback=%+v", mem)
	}
	c.SetPersistence(nil)
	c.logCall(time.Now(), 200, nil, 0, 0)
	if len(c.Calls(0)) != 2 {
		t.Fatal("detach should keep memory logging")
	}
}
