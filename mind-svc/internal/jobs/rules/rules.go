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
	OK = "ok"
	// Yielded: the run gave the browser to the conversation (or could not
	// get it). Not an attempt; it waits a while (YieldWait), since the
	// person may go on using the browser.
	Yielded = "yielded"
	// Deferred: the run gave the browser to a job the operator put first
	// (the agent's 먼저 card). Not an attempt either, and the job keeps its
	// place in line - it goes on right after that one, ahead of the jobs
	// asked for after it (2026-10-28: "브라우저 작업은 요청 순대로").
	Deferred = "deferred"
	Error    = "error"
)

// MaxAttempts is how many runs in a row may end badly before the job stops.
const MaxAttempts = 3

// RetryAfter is the wait before a run that ended badly is tried again.
const RetryAfter = 15 * time.Minute

// YieldAfter is the wait after a run gave way to the conversation, doubled
// for each run before it in a row that did, up to MaxYieldWait. A run cut
// short spent its tokens for nothing, and while the person keeps talking the
// next one would be cut short too.
const (
	YieldAfter   = 5 * time.Minute
	MaxYieldWait = time.Hour
)

// MinGap is the shortest interval a run may schedule itself at. A model that
// asks for "every 10 seconds" is polling a website, not checking on it - and
// every run starts its model over, instructions and tools and all.
const MinGap = 10 * time.Minute

// End is what to do with a job once its run has ended.
type End struct {
	// Retry: set next_at to now+After and keep the state.
	Retry bool
	After time.Duration
	// Keep: the job goes again in the place it had - next_at back to when
	// the run started - and the state as it is.
	Keep bool
	// Attempts is the new count of bad runs in a row.
	Attempts int
	// Fail: the job stops for good.
	Fail bool
	Note string
}

// AfterRun decides. decided is whether the run chose its own next step
// (schedule, wait, finish) - a run that did is left exactly as it set itself.
// yields is how many runs in a row just before this one gave way.
func AfterRun(outcome string, decided bool, attempts, yields int, note string) End {
	switch {
	case outcome == Yielded:
		// The person took the browser; not the job's fault, not an attempt.
		return End{Retry: true, After: YieldWait(yields), Attempts: attempts}
	case outcome == Deferred:
		// Another job was put first; not an attempt, and no wait.
		return End{Keep: true, Attempts: attempts}
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

// YieldWait is the wait after a run gave way, given how many runs in a row
// before it did.
func YieldWait(yields int) time.Duration {
	wait := YieldAfter
	for i := 0; i < yields && wait < MaxYieldWait; i++ {
		wait *= 2
	}
	return min(wait, MaxYieldWait)
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
