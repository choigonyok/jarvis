// Package health is the one shape every service's GET /health answers in, and
// the one rule for what a queue does with an item that keeps failing.
//
// The status tab (status-svc) reads it to tell "the process is up" from "the
// work it is there for is getting done": when each background loop last got
// through and what last went wrong, and what the service set aside. Before
// this, a loop that stopped - card alerts stuck behind one message, facts
// given up on after a day, a consumer the event log could not reach - looked
// healthy from outside, and nobody heard of it.
//
// The rule, the same in every service:
//
//  1. An item that fails goes again later, waiting longer each time (Backoff).
//  2. A failure that is not the item's - the other side is down, out of
//     budget, or refuses the token - is an outage. It costs the item no try:
//     the queue waits, in order, and the loop reports the error until it
//     clears, so the status tab turns red if it lasts.
//  3. An item that fails MaxTries times on its own is held (격리함): set
//     aside with the reason, and the queue moves on past it.
//  4. /health counts what is held, so the status tab shows it.
//  5. A held item goes again, once, when the other side is working again,
//     and whenever a person asks. Dropping one is a person's call.
//
// Background jobs are the one queue with a smaller budget: a try there is a
// whole model run, so a job is held (stopped as failed) after three.
package health

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MaxTries is how often an item may fail on its own before it is held.
const MaxTries = 5

// Backoff is the wait after the n-th failed try (n from 1): first, four times
// longer each time after, never more than most.
func Backoff(n int, first, most time.Duration) time.Duration {
	d := first
	for i := 1; i < n && d < most; i++ {
		d *= 4
	}
	return min(d, most)
}

// Report is the body of GET /health.
type Report struct {
	// Status is "ok" whenever the process can answer. A loop that stopped
	// does not change it - the loops say so themselves, and a container
	// whose healthcheck fails takes the services that wait on it down too.
	Status string `json:"status"`
	Loops  []Loop `json:"loops,omitempty"`
	Held   *Held  `json:"held,omitempty"`
}

// Loop is one background loop: what it is for, and how it is doing. A round
// that ends without an error is OK even when there was nothing to do, so a
// loop that is wedged, not only one that fails, goes stale.
type Loop struct {
	Name  string `json:"name"`
	Label string `json:"label"` // what a person calls it: "카드 알림 수집"
	// StaleSec is how long the loop may go without a round that got through
	// before it counts as stopped. Zero: it is reported, never judged by age.
	StaleSec  int        `json:"staleSec,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	LastOK    *time.Time `json:"lastOkAt,omitempty"`
	LastError string     `json:"lastError,omitempty"`
	ErrorAt   *time.Time `json:"lastErrorAt,omitempty"`
	// Failing is how many rounds in a row ended in an error.
	Failing int `json:"failing,omitempty"`
	// Behind is when the oldest item still waiting on the loop came in, for
	// a loop with a queue: nil when nothing waits.
	Behind *time.Time `json:"behindSince,omitempty"`
}

// Stalled is how long the loop has gone without a round that got through,
// counted from its start when none has, and whether that is past StaleSec.
func (l Loop) Stalled(now time.Time) (time.Duration, bool) {
	since := l.StartedAt
	if l.LastOK != nil {
		since = *l.LastOK
	}
	d := now.Sub(since)
	return d, l.StaleSec > 0 && d > time.Duration(l.StaleSec)*time.Second
}

// Held is what a service set aside under rule 3.
type Held struct {
	Count  int        `json:"count"`
	Oldest *time.Time `json:"oldest,omitempty"`
	Items  []Item     `json:"items,omitempty"` // the newest few
	// Where says where a person deals with them: a tab of the console, or
	// what to run.
	Where string `json:"where,omitempty"`
}

// Item is one held thing, as a person reads it.
type Item struct {
	What string    `json:"what"`
	Why  string    `json:"why,omitempty"`
	At   time.Time `json:"at"`
}

// ShownItems is how many held items /health lists.
const ShownItems = 5

// Loops keeps a service's loops for its /health. Safe for concurrent use; a
// nil *Loops does nothing.
type Loops struct {
	mu    sync.Mutex
	loops []*Loop
	now   func() time.Time
}

func NewLoops() *Loops { return &Loops{now: time.Now} }

// Add declares a loop. stale is how long without a success means stopped.
func (l *Loops) Add(name, label string, stale time.Duration) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loops = append(l.loops, &Loop{Name: name, Label: label, StaleSec: int(stale.Seconds()), StartedAt: l.now()})
}

func (l *Loops) find(name string) *Loop {
	for _, lp := range l.loops {
		if lp.Name == name {
			return lp
		}
	}
	lp := &Loop{Name: name, Label: name, StartedAt: l.now()}
	l.loops = append(l.loops, lp)
	return lp
}

// OK records a round that got through.
func (l *Loops) OK(name string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	lp, now := l.find(name), l.now()
	lp.LastOK, lp.Failing = &now, 0
}

// Fail records a round that ended in err. The error stays shown after the
// loop recovers, beside the time it happened.
func (l *Loops) Fail(name string, err error) {
	if l == nil || err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	lp, now := l.find(name), l.now()
	lp.LastError, lp.ErrorAt = clip(err.Error(), 300), &now
	lp.Failing++
}

// Waiting records when the oldest item waiting on the loop came in; nil
// when nothing waits.
func (l *Loops) Waiting(name string, since *time.Time) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	lp := l.find(name)
	if since == nil {
		lp.Behind = nil
		return
	}
	t := *since
	lp.Behind = &t
}

// List is a copy of the loops, in the order they were added.
func (l *Loops) List() []Loop {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Loop, len(l.loops))
	for i, lp := range l.loops {
		out[i] = *lp
	}
	return out
}

// Write answers GET /health: code (200, or 503 when the service cannot do
// its work at all) and the report.
func Write(w http.ResponseWriter, code int, r Report) {
	if r.Status == "" {
		r.Status = "ok"
		if code != http.StatusOK {
			r.Status = "down"
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(r)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
