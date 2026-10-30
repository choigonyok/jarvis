// Package checks is how a service says how something stands that only it can
// see: a login, a disk, an account. Each result is a record, and status-svc
// shows the latest of each with no code of its own for any of them.
package checks

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/record"
)

// Kind is a result on the stream.
const Kind = "check.result"

// How a thing stands.
const (
	OK      = "ok"
	Warn    = "warn"
	Fail    = "fail"
	Unknown = "unknown"
)

// Beat is how often a result is said again when nothing changed, so that
// silence means the one saying it stopped.
const Beat = time.Hour

// Result is one row of the status tab.
type Result struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Name  string `json:"name"`
	State string `json:"state"`
	// Summary is the one sentence on the row: what is true right now.
	Summary string `json:"summary"`
	// ObservedAt is the newest signal it rests on, shown as "<ObservedLabel>
	// 3일 전".
	ObservedAt    *time.Time `json:"observedAt,omitempty"`
	ObservedLabel string     `json:"observedLabel,omitempty"`
	// Facts are extra lines for the opened row.
	Facts []string `json:"facts,omitempty"`
	// Figure is what the row shows at its right when it is not an age, and
	// Meter, 0-100, the thin bar under its summary.
	Figure string `json:"figure,omitempty"`
	Meter  *int   `json:"meter,omitempty"`
	// Rule says how the state is decided, Fix what to do when it is not ok.
	Rule string `json:"rule,omitempty"`
	Fix  string `json:"fix,omitempty"`
	// Deps are rows whose failure would explain this one's.
	Deps []string `json:"deps,omitempty"`
}

func (r Result) says() string {
	meter := ""
	if r.Meter != nil {
		meter = strconv.Itoa(*r.Meter)
	}
	return strings.Join(append([]string{r.State, r.Summary, r.Figure, meter}, r.Facts...), "\x00")
}

// Reporter leaves results as records: when one says something new, and once a
// Beat otherwise. A nil Reporter does nothing.
type Reporter struct {
	// Source is the service saying it.
	Source  string
	Records *record.Writer
	// Now is the clock; a test sets it.
	Now func() time.Time

	mu   sync.Mutex
	last map[string]said
}

type said struct {
	what string
	at   time.Time
}

// Report says a result, if it is worth saying.
func (r *Reporter) Report(res Result) {
	if r == nil {
		return
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	what := res.says()
	r.mu.Lock()
	if r.last == nil {
		r.last = map[string]said{}
	}
	prev, seen := r.last[res.ID]
	if seen && prev.what == what && now.Sub(prev.at) < Beat {
		r.mu.Unlock()
		return
	}
	r.last[res.ID] = said{what, now}
	r.mu.Unlock()
	r.Records.Write(record.Record{
		Kind: Kind, Source: r.Source, SourceKey: res.ID + "@" + strconv.FormatInt(now.UnixNano(), 36), Actor: r.Source,
		Subject: res.ID, OccurredAt: now, Tier: record.Raw, Payload: res,
	})
}
