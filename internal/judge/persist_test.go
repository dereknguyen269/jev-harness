package judge

import (
	"context"
	"testing"

	"github.com/dereknguyen269/jev-harness/internal/jev"
	"github.com/dereknguyen269/jev-harness/internal/policy"
)

type persistStub struct {
	got jev.CallPersistence
}

func (s *persistStub) Evaluate(_ context.Context, _ map[string]any, _ map[string]policy.Question) (map[string]policy.Answer, error) {
	return map[string]policy.Answer{}, nil
}

func (s *persistStub) SetPersistence(p jev.CallPersistence) { s.got = p }

type fakeCallPersist struct{}

func (fakeCallPersist) InsertJevCall(jev.Call) error { return nil }

func TestSetCallPersistenceForwards(t *testing.T) {
	stub := &persistStub{}
	j := NewJev(stub)
	fp := fakeCallPersist{}
	j.SetCallPersistence(fp)
	if stub.got == nil {
		t.Fatal("persistence not forwarded to wrapped client")
	}
}

type plainStub struct{}

func (plainStub) Evaluate(_ context.Context, _ map[string]any, _ map[string]policy.Question) (map[string]policy.Answer, error) {
	return map[string]policy.Answer{}, nil
}

func TestSetCallPersistenceUnsupportedNoop(t *testing.T) {
	// A wrapped client without SetPersistence: must not panic, must not fail.
	j := NewJev(plainStub{})
	j.SetCallPersistence(fakeCallPersist{})
}
