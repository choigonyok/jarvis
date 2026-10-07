// Package rules decides what happens to a job after one background run,
// without a database: retry, give up, or leave it as the run left it.
package rules

import (
	"strings"
	"time"
)

// Job states.
const (
	Active    = "active"
	Waiting   = "waiting"
	Paused    = "paused"
	Done      = "done"
	Cancelled = "cancelled"
	Failed    = "failed"
)

// Run outcomes, as the agent reports them.
const (
	OK      = "ok"
	Yielded = "yielded"
	Error   = "error"
)

// MaxAttempts is how many runs in a row may end badly before the job stops.
const MaxAttempts = 3

// RetryAfter is the wait before a run that ended badly is tried again.
const RetryAfter = 15 * time.Minute

// YieldAfter is the wait after a run gave way to the conversation.
const YieldAfter = time.Minute

// MinGap is the shortest interval a run may schedule itself at. A model that
// asks for "every 10 seconds" is polling a website, not checking on it.
const MinGap = 5 * time.Minute

// End is what to do with a job once its run has ended.
type End struct {
	// Retry: set next_at to now+After and keep the state.
	Retry bool
	After time.Duration
	// Attempts is the new count of bad runs in a row.
	Attempts int
	// Fail: the job stops for good.
	Fail bool
	Note string
}

// AfterRun decides. decided is whether the run chose its own next step
// (schedule, wait, finish) - a run that did is left exactly as it set itself.
func AfterRun(outcome string, decided bool, attempts int, note string) End {
	switch {
	case outcome == Yielded:
		// The person spoke; not the job's fault, not an attempt.
		return End{Retry: true, After: YieldAfter, Attempts: attempts}
	case outcome == OK && decided:
		return End{Attempts: 0}
	}
	if strings.TrimSpace(note) == "" {
		note = "실행이 다음 할 일을 정하지 않고 끝났습니다"
	}
	n := attempts + 1
	if n >= MaxAttempts {
		return End{Fail: true, Attempts: n, Note: note}
	}
	return End{Retry: true, After: RetryAfter, Attempts: n, Note: note}
}

// ClampNext keeps a requested next run inside sane bounds.
func ClampNext(now, at time.Time) time.Time {
	if at.Before(now.Add(MinGap)) {
		return now.Add(MinGap)
	}
	if max := now.Add(30 * 24 * time.Hour); at.After(max) {
		return max
	}
	return at
}

// Host normalises a site the model names: "https://web.joongna.com/x" and
// "WEB.JOONGNA.COM" are the same site.
func Host(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}
