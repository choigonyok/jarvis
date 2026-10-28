package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/usage"
)

// ErrYielded is what a background task returns when it gave the browser
// up while it ran - the conversation went to use it, or the operator put
// another job first. It is not a failure of the task; it is asked again
// later.
var ErrYielded = errors.New("브라우저를 내주느라 백그라운드 작업을 멈췄습니다")

// ErrHold is a task whose Hold failed: it never started.
var ErrHold = errors.New("백그라운드 작업을 브라우저에 묶지 못했습니다")

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
	// without the browser runs beside everything, up to MaxPlain at once.
	Browser bool
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

// MaxPlain is how many background tasks without the browser may run at
// once, beside the one on the browser. Each is a CLI process of a few
// hundred MB; the Mac mini (16GB) runs the conversation, the services and
// the voice beside them. It was two for all of them, browser included,
// until 2026-10-28 - a job on the browser and one checking a price filled
// it, and a third waited for no reason.
const MaxPlain = 4

// bgTask is one background task in flight.
type bgTask struct {
	name    string
	browser bool
	cancel  func()
	yielded bool
}

// Step is one thing a background task did: a tool call (Tool and Input set)
// or a line of its own text (Text set).
type Step struct {
	Tool  string
	Input json.RawMessage
	Text  string
}

// InBackground is true while a background task holds the browser.
func (r *Runner) InBackground() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.browserHeldLocked()
}

func (r *Runner) browserHeldLocked() bool {
	for t := range r.bgs {
		if t.browser {
			return true
		}
	}
	return false
}

// plainLocked is how many tasks without the browser are in flight.
func (r *Runner) plainLocked() int {
	n := 0
	for t := range r.bgs {
		if !t.browser {
			n++
		}
	}
	return n
}

// CanStart is whether a background task may begin now. One that wants the
// browser waits for no other task to hold it and for the conversation not to
// be using it (TakeBrowser) - a turn that only talks leaves it be; one that
// does not want it needs only one of the MaxPlain places. It does not wait
// for the conversation to stay quiet a while first - a job the operator just
// asked for waited five minutes on "예약됨" for that (2026-10-18), and a run
// the conversation cuts short is picked up by the next one (yield backoff).
func (r *Runner) CanStart(browser bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.canStartLocked(browser)
}

func (r *Runner) canStartLocked(browser bool) bool {
	if browser {
		return !r.chatBrowser && !r.browserHeldLocked()
	}
	return r.plainLocked() < MaxPlain
}

// TakeBrowser gives the browser to the conversation, which is about to call
// one of its tools: a background task on it is stopped (it is asked again
// later), and no other starts on it until the conversation is out of its
// turns. It waits, up to wait, for the task to let go - its process ended
// and its site guard released (Task.Done) - so the call finds the browser
// the conversation's own; false when the task had not let go by then.
//
// Before 2026-10-28 every turn did this as it started, whatever it was
// about: "안녕" stopped a listing half typed on 중고나라.
func (r *Runner) TakeBrowser(wait time.Duration) bool {
	r.mu.Lock()
	// Out of a turn nothing of the conversation is calling: a call that
	// arrives then must not keep the browser from the jobs for good.
	if r.running {
		r.chatBrowser = true
	}
	r.stopBrowserLocked("대화가 브라우저를 씁니다")
	r.mu.Unlock()
	deadline := time.Now().Add(wait)
	for {
		r.mu.Lock()
		held := r.browserHeldLocked()
		r.mu.Unlock()
		if !held {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// YieldBrowser stops the background task on the browser, if one is there,
// for another task to go first - the operator put a waiting job ahead of
// the one running (the jobs module's 먼저 card). It says whether one was
// stopped; the place comes free when it has let go (OnFree).
func (r *Runner) YieldBrowser(why string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopBrowserLocked(why) > 0
}

// Idle is true when nothing runs here at all: no conversation turn, no
// background task. What the browser check and the usage probe want.
func (r *Runner) Idle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.running && len(r.bgs) == 0
}

// Background runs a task when it may start (CanStart), and - when the task
// drives the browser - gives way the moment the conversation goes to use it
// (TakeBrowser) or another task is put first (YieldBrowser). The browser is
// one shared window, and the person's own request always wins it. A task
// without the browser runs on through everything; it has nothing to fight
// over.
func (r *Runner) Background(parent context.Context, t Task) (string, error) {
	ctx, cancel := context.WithTimeout(parent, t.Timeout)
	defer cancel()

	r.mu.Lock()
	if !r.canStartLocked(t.Browser) {
		r.mu.Unlock()
		return "", ErrBusy
	}
	task := &bgTask{name: t.Name, browser: t.Browser}
	task.cancel = func() { task.yielded = true; cancel() }
	if r.bgs == nil {
		r.bgs = map[*bgTask]struct{}{}
	}
	r.bgs[task] = struct{}{}
	r.mu.Unlock()
	defer func() {
		// Released before the place is: whoever is waiting for the browser
		// (TakeBrowser) must find it out of the task's guard.
		if t.Done != nil {
			t.Done()
		}
		r.mu.Lock()
		delete(r.bgs, task)
		r.mu.Unlock()
		r.freed()
	}()

	if t.Hold != nil {
		herr := t.Hold(ctx)
		r.mu.Lock()
		taken := task.yielded
		r.mu.Unlock()
		switch {
		case taken:
			// The browser was taken while the task was being held to it.
			return "", ErrYielded
		case herr != nil:
			return "", fmt.Errorf("%w: %v", ErrHold, herr)
		}
	}

	args, err := r.backgroundArgs(t)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, r.cfg.ClaudeBin, args...)
	cmd.Dir = r.cfg.Workspace
	// Its requests come seconds apart and nothing resumes it: an hour-long
	// cache would only make every write cost more.
	cmd.Env = r.env("5m")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude 실행: %w", err)
	}
	var used tally
	r.saw(t.Prompt)
	result, scanErr := backgroundResult(stdout, t.OnStep, r.usage.Observe, &used, r.saw)
	waitErr := cmd.Wait()

	r.mu.Lock()
	gaveWay := task.yielded
	r.mu.Unlock()
	// Logged however it ended: a run cut short is tokens thrown away.
	r.log.Info("백그라운드 사용량", append([]any{"task", t.Name, "model", r.modelOf(t), "yielded", gaveWay}, used.attrs()...)...)
	if gaveWay {
		return "", ErrYielded
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		r.log.Warn("background stderr", "task", t.Name, "msg", clamp(msg, 1000))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("%s: 시간 초과", t.Name)
	}
	if scanErr != nil {
		return result, scanErr
	}
	if waitErr != nil {
		return result, fmt.Errorf("claude 종료: %w", waitErr)
	}
	return result, nil
}

// stopBrowserLocked stops the background task on the browser and says how
// many it stopped (one at most). Called with r.mu held.
func (r *Runner) stopBrowserLocked(why string) int {
	n := 0
	for t := range r.bgs {
		if t.browser && !t.yielded {
			r.log.Info("브라우저를 쓰는 백그라운드 작업을 멈춥니다", "task", t.name, "why", why)
			t.cancel()
			n++
		}
	}
	return n
}

func (r *Runner) backgroundArgs(t Task) ([]string, error) {
	servers := map[string]any{}
	for name, url := range t.Servers {
		servers[name] = r.internal(url)
	}
	for _, name := range t.Extra {
		url, ok := r.cfg.ExtraMCPURLs[name]
		if !ok {
			return nil, fmt.Errorf("%s: JARVIS_EXTRA_MCP 에 %s 가 없습니다", t.Name, name)
		}
		entry := map[string]any{"type": "http", "url": url}
		if r.cfg.ExtraMCPToken != "" {
			entry["headers"] = map[string]any{"Authorization": "Bearer " + r.cfg.ExtraMCPToken}
		}
		servers[name] = entry
	}
	mcp, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return nil, err
	}

	// The clock rides on the prompt, not the instructions: instructions that
	// change every minute would share no cache with the run before.
	now := time.Now()
	prompt := fmt.Sprintf("오늘은 %d-%02d-%02d (%s), 지금은 %s 입니다.\n\n%s",
		now.Year(), int(now.Month()), now.Day(), weekdays[int(now.Weekday())], now.Format("15:04"), t.Prompt)

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		// Anything not in --allowedTools is refused outright; there is no
		// one to ask.
		"--permission-mode", "dontAsk",
		// No built-in tools: no shell, no file access.
		"--tools", "",
		"--mcp-config", string(mcp),
		"--strict-mcp-config",
		"--allowedTools", strings.Join(t.Allowed, ","),
		// Its own throwaway session: the chat's --resume session is the
		// person's conversation, and this is not part of it.
		"--no-session-persistence",
		"--append-system-prompt", t.System,
	}
	if model := r.modelOf(t); model != "" {
		args = append(args, "--model", model)
	}
	return args, nil
}

// modelOf is the model a task runs on: its own, or the conversation's.
func (r *Runner) modelOf(t Task) string {
	if t.Model != "" {
		return t.Model
	}
	return r.cfg.Model
}

// backgroundResult reads the stream only for its outcome: the final result
// text (for the log), whether the CLI reported an error, and what it used -
// and hands what the tools gave back to onSeen.
func backgroundResult(stdout interface{ Read([]byte) (int, error) }, onStep func(Step), onRate func(json.RawMessage), used *tally, onSeen func(string)) (string, error) {
	var result string
	var failed error
	err := scan(stdout, func(ev event) {
		if used != nil {
			used.observe(ev)
		}
		if onSeen != nil && ev.Type == "user" {
			onSeen(toolResults(ev.Message))
		}
		if ev.Type == "rate_limit_event" && onRate != nil {
			onRate(ev.RateLimitInfo)
		}
		if onStep != nil && ev.Type == "assistant" && !ev.IsAPIErrorMessage {
			for _, s := range stepsOf(ev.Message) {
				onStep(s)
			}
		}
		switch {
		case ev.Type == "assistant" && ev.IsAPIErrorMessage:
			failed = fmt.Errorf("claude api error: %s", ev.Error)
		case ev.Type == "result":
			result = ev.Result
			if ev.IsError {
				failed = fmt.Errorf("claude result error: %s", clamp(ev.Result, 500))
			}
		}
	})
	if failed != nil {
		return result, failed
	}
	return result, err
}

func stepsOf(raw json.RawMessage) []Step {
	var msg struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &msg) != nil {
		return nil
	}
	var out []Step
	for _, b := range msg.Content {
		switch b.Type {
		case "tool_use":
			out = append(out, Step{Tool: b.Name, Input: b.Input})
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				out = append(out, Step{Text: t})
			}
		}
	}
	return out
}

// SetUsage wires the tracker every run reports to.
func (r *Runner) SetUsage(u *usage.Tracker) { r.usage = u }

// ProbeUsage reads the subscription's usage when no run has for a while: the
// smallest possible request (one word, the small model, no tools, no
// session), only for the rate_limit_event it brings back. It does nothing
// while a turn or a background task runs - those report usage themselves.
func (r *Runner) ProbeUsage(ctx context.Context) error {
	if !r.Idle() || r.usage == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.cfg.ClaudeBin,
		"-p", ".", "--output-format", "stream-json", "--verbose",
		"--model", "haiku", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--no-session-persistence", "--permission-mode", "dontAsk")
	cmd.Dir = os.TempDir()
	cmd.Env = r.env("5m")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, _ = backgroundResult(stdout, nil, r.usage.Observe, nil, nil)
	return cmd.Wait()
}

// WatchUsage probes whenever the last reading is older than every.
func (r *Runner) WatchUsage(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		if r.usage != nil && r.usage.Stale(time.Now(), every) {
			if err := r.ProbeUsage(ctx); err != nil {
				r.log.Warn("사용량을 읽지 못했습니다", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
