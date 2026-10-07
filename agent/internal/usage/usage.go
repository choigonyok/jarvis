// Package usage keeps how much of the Claude subscription is used: the
// rolling 5-hour window and the weekly one.
//
// The CLI reports both on every run, as a rate_limit_event in its stream
// (unifiedWindows.five_hour / seven_day, utilization 0-1 and a reset time).
// Every chat turn and background run already reads that stream, so the
// numbers come for free; this only remembers the latest, keeps it across a
// restart, and says when it has gone stale.
package usage

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
)

type Window struct {
	// Utilization is the share used, 0-1.
	Utilization float64 `json:"utilization"`
	// ResetsAt is when the window starts over.
	ResetsAt time.Time `json:"resetsAt"`
}

type Usage struct {
	FiveHour *Window `json:"fiveHour,omitempty"`
	SevenDay *Window `json:"sevenDay,omitempty"`
	// Status is the CLI's word for the current limit: allowed,
	// allowed_warning, rejected.
	Status string `json:"status,omitempty"`
	// At is when these numbers were read.
	At time.Time `json:"at"`
}

type Tracker struct {
	mu   sync.Mutex
	u    Usage
	path string
}

// New reads what was saved at path, if anything.
func New(path string) *Tracker {
	t := &Tracker{path: path}
	if path != "" {
		_ = jsonfile.Load(path, &t.u)
	}
	return t
}

// The shape of rate_limit_info, as much as is used here.
type info struct {
	Status         string `json:"status"`
	UnifiedWindows struct {
		FiveHour *raw `json:"five_hour"`
		SevenDay *raw `json:"seven_day"`
	} `json:"unifiedWindows"`
}

type raw struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

func (r *raw) window() *Window {
	if r == nil {
		return nil
	}
	return &Window{Utilization: r.Utilization, ResetsAt: time.Unix(r.ResetsAt, 0)}
}

// Observe takes one rate_limit_info object from the stream.
func (t *Tracker) Observe(rawInfo json.RawMessage) {
	if t == nil || len(rawInfo) == 0 {
		return
	}
	var in info
	if json.Unmarshal(rawInfo, &in) != nil {
		return
	}
	five, seven := in.UnifiedWindows.FiveHour.window(), in.UnifiedWindows.SevenDay.window()
	if five == nil && seven == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if five != nil {
		t.u.FiveHour = five
	}
	if seven != nil {
		t.u.SevenDay = seven
	}
	t.u.Status = in.Status
	t.u.At = time.Now()
	if t.path != "" {
		_ = jsonfile.Save(t.path, t.u)
	}
}

// Snapshot is the latest reading. A window that has already reset reads as
// empty: the old number is no longer true and nothing new was seen yet.
func (t *Tracker) Snapshot(now time.Time) Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.u
	for _, w := range []**Window{&u.FiveHour, &u.SevenDay} {
		if *w != nil && !(*w).ResetsAt.IsZero() && now.After((*w).ResetsAt) {
			*w = &Window{Utilization: 0, ResetsAt: (*w).ResetsAt}
		}
	}
	return u
}

// Stale reports whether nothing has been read for longer than d.
func (t *Tracker) Stale(now time.Time, d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.u.At.IsZero() || now.Sub(t.u.At) > d
}
