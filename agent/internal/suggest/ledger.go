package suggest

import (
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
)

// Ledger remembers what was suggested and how it went, so the assistant does
// not repeat itself or nag. A topic ("rebalance", "plan:<hash>") is quiet for
// a while after it was raised, longer after it was turned down; and only so
// many suggestions go out in a day.
type Ledger struct {
	mu      sync.Mutex
	path    string
	Entries []Entry `json:"entries"`
}

type Entry struct {
	Topic      string    `json:"topic"`
	ProposalID string    `json:"proposalId"`
	Title      string    `json:"title"`
	At         time.Time `json:"at"`
	// approved | rejected, or empty while undecided.
	Decision  string     `json:"decision,omitempty"`
	DecidedAt *time.Time `json:"decidedAt,omitempty"`
}

const (
	// DailyCap is how many suggestions may go out in a day.
	DailyCap           = 5
	quietAfterRaised   = 3 * 24 * time.Hour
	quietAfterApproved = 7 * 24 * time.Hour
	quietAfterRejected = 14 * 24 * time.Hour
)

func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path}
	if path == "" {
		return l, nil
	}
	if err := jsonfile.Load(path, l); err != nil {
		return nil, err
	}
	return l, nil
}

// Allowed says whether a topic may be raised now, and if not, why.
func (l *Ledger) Allowed(topic string, now time.Time) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	today := 0
	for _, e := range l.Entries {
		if sameDay(e.At, now) {
			today++
		}
	}
	if today >= DailyCap {
		return false, "오늘 제안 한도"
	}
	for i := len(l.Entries) - 1; i >= 0; i-- {
		e := l.Entries[i]
		if e.Topic != topic {
			continue
		}
		quiet, since := quietAfterRaised, e.At
		switch e.Decision {
		case "approved":
			quiet, since = quietAfterApproved, *e.DecidedAt
		case "rejected":
			quiet, since = quietAfterRejected, *e.DecidedAt
		}
		if now.Sub(since) < quiet {
			return false, "최근에 제안함(" + e.Decision + ")"
		}
		break
	}
	return true, ""
}

func (l *Ledger) Record(topic, proposalID, title string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Entries = append(l.Entries, Entry{Topic: topic, ProposalID: proposalID, Title: title, At: at})
	if len(l.Entries) > 500 {
		l.Entries = l.Entries[len(l.Entries)-500:]
	}
	l.save()
}

// Decide records the operator's call on a suggestion; false if it is not one.
func (l *Ledger) Decide(proposalID, decision string, at time.Time) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.Entries {
		if l.Entries[i].ProposalID == proposalID {
			l.Entries[i].Decision, l.Entries[i].DecidedAt = decision, &at
			l.save()
			return l.Entries[i], true
		}
	}
	return Entry{}, false
}

// Recent is the last few suggestions on topics like this one, with how they
// went - what the judging turn reads before suggesting again.
func (l *Ledger) Recent(prefix string, n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for i := len(l.Entries) - 1; i >= 0 && len(out) < n; i-- {
		if len(prefix) == 0 || len(l.Entries[i].Topic) >= len(prefix) && l.Entries[i].Topic[:len(prefix)] == prefix {
			out = append(out, l.Entries[i])
		}
	}
	return out
}

func (l *Ledger) save() {
	if l.path != "" {
		_ = jsonfile.Save(l.path, l)
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
