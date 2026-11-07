// Package run is what a run of the model is to everything that is not the
// runner: work handed over outside the conversation (Task) and what it did
// (Step), the place a task holds while it runs (Place), the messages waiting
// behind a turn (Queued), and the ways a run is refused or cut short.
//
// The runner (internal/claudecode) implements it. Modules and the HTTP layer
// depend on this package alone, so a runner can be faked in a test or swapped
// for another without touching them.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/thread"
)

var (
	// ErrBusy is a task or a message refused because what it needs is taken:
	// the browser by the conversation or another task, or every place.
	ErrBusy = errors.New("이미 처리 중인 요청이 있습니다")
	// ErrYielded is what a background task returns when it gave the browser
	// up while it ran - the conversation went to use it, or the operator put
	// another job first. It is not a failure of the task; it is asked again
	// later.
	ErrYielded = errors.New("브라우저를 내주느라 백그라운드 작업을 멈췄습니다")
	// ErrHold is a task whose Hold failed: it never started.
	ErrHold = errors.New("백그라운드 작업을 브라우저에 묶지 못했습니다")
	// ErrNotQueued is a steer or drop of a message no longer waiting.
	ErrNotQueued = errors.New("대기 중인 메시지가 아닙니다")
	// ErrNotRunning is a stop with no answer being written.
	ErrNotRunning = errors.New("멈출 답이 없습니다")
)

// Background runs a task outside the conversation and returns what the task
// said last. A task that gave way returns ErrYielded, one that could not
// start ErrBusy, one that could not be held to its sites ErrHold.
type Background interface {
	Background(ctx context.Context, t Task) (string, error)
}

// Task is work the assistant does on its own, outside the conversation.
//
// It shares nothing with the thread: no session to resume, nothing written
// to the transcript, and none of the chat's tools. What it may touch is
// exactly Servers plus the named Extra servers, and of their tools exactly
// Allowed - every other call is refused without asking anyone
// (--permission-mode dontAsk), and the CLI's own built-in tools (shell,
// files) are not loaded at all. Nobody is watching a background task, so
// nothing it can do may need watching.
type Task struct {
	Name string
	// Prompt is the task's message; today's date and the time go in front of
	// it. System is appended to the CLI's system prompt and should not change
	// from run to run: requests whose instructions match share their cache.
	Prompt string
	System string
	// Model is the model the task runs on; empty uses the conversation's.
	Model string
	// Servers are this agent's own MCP endpoints (loopback, no credential).
	Servers map[string]string
	// Extra names entries of JARVIS_EXTRA_MCP to include, with their token.
	Extra   []string
	Allowed []string
	Timeout time.Duration
	// Browser says the task drives the shared browser window. One such task
	// at a time, and none while the conversation is using the browser: a
	// turn that calls a browser tool takes it (TakeBrowser) and stops the
	// task. A turn that does not use it leaves the task running. A task
	// without the browser runs beside everything, up to a few at once.
	Browser bool
	// Place, when set, is the place Reserve took for this task: it is used
	// instead of looking for a free one now.
	Place Place
	// Hold, when set, runs once the task has its place - the browser its
	// own - and before its process starts: where a site guard goes on, so it
	// is never on while someone else has the browser. An error ends the task
	// there (ErrHold).
	Hold func(ctx context.Context) error
	// Done, when set, runs once the task's process has ended (or Hold
	// failed) and before the browser counts as free again - where the site
	// guard is released, so whoever takes the browser next is not held to
	// the task's sites.
	Done func()
	// OnStep, when set, hears each tool call and each bit of text the model
	// writes while the task runs - how a person watching sees progress.
	OnStep func(Step)
}

// Place is a place for a background task, taken before the task starts
// (Reserve) and used by the Background call that carries it (Task.Place).
// Release gives it back if that call never comes; after it, Release does
// nothing.
type Place interface{ Release() }

// Step is one thing a background task did: a tool call (Tool and Input set)
// or a line of its own text (Text set).
type Step struct {
	Tool  string
	Input json.RawMessage
	Text  string
}

// Queued is one message the operator sent while a turn ran, waiting behind
// it - as the screens show it.
type Queued struct {
	ID     string      `json:"id"`
	Text   string      `json:"text"`
	Images []string    `json:"images,omitempty"`
	Ref    *thread.Ref `json:"ref,omitempty"`
	At     time.Time   `json:"at"`
}
