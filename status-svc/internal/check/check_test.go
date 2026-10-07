package check

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func probe(id string, s State, deps ...string) Probe {
	return Probe{ID: id, Deps: deps, Every: time.Hour, Run: func(context.Context) Verdict { return Verdict{State: s} }}
}

func TestRootCausePointsAtDeepestFailure(t *testing.T) {
	r := NewRunner(nil, []Probe{
		probe("db", Fail),
		probe("svc", Fail, "db"),
		probe("feature", Warn, "svc"),
		probe("healthy", OK, "db"),
		probe("alone", Fail),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Refresh(context.Background())

	got := map[string]string{}
	for _, res := range r.Snapshot().Results {
		got[res.ID] = res.Cause
	}
	want := map[string]string{"db": "", "svc": "db", "feature": "db", "healthy": "", "alone": ""}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: cause %q, want %q", id, got[id], w)
		}
	}
}

func TestSinceKeepsFirstTimeOfState(t *testing.T) {
	r := NewRunner(nil, []Probe{probe("a", Fail)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Refresh(context.Background())
	first := r.Snapshot().Results[0].Since
	time.Sleep(5 * time.Millisecond)
	r.Refresh(context.Background())
	if again := r.Snapshot().Results[0].Since; !again.Equal(first) {
		t.Errorf("since moved from %v to %v while state stayed the same", first, again)
	}
}
