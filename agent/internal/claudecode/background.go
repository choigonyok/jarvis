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

// ErrYielded is what a background task returns when a person started a turn
// while it ran. It is not a failure of the task; it is asked again later.
var ErrYielded = errors.New("대화가 시작되어 백그라운드 작업을 멈췄습니다")

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
	// at a time, never while a conversation turn runs (the conversation has
	// the browser too), and the person speaking stops it. A task without it
	// runs beside the conversation, up to MaxBackground tasks at once.
	Browser bool
	// OnStep, when set, hears each tool call and each bit of text the model
	// writes while the task runs - how a person watching sees progress.
	OnStep func(Step)
}

// MaxBackground is how many background tasks may run at once. Two: the Mac
// mini runs the conversation, the services and the voice beside them.
const MaxBackground = 2

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

// CanStart is whether a background task may begin now. One that wants the
// browser waits for the conversation to be out of a turn and for no other
// task to hold the browser; one that does not needs only a free slot. It
// does not wait for the conversation to stay quiet a while first - a job the
// operator just asked for waited five minutes on "예약됨" for that
// (2026-10-18), and a run the next message cuts short is picked up by the
// next one (yield backoff).
func (r *Runner) CanStart(browser bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.canStartLocked(browser)
}

func (r *Runner) canStartLocked(browser bool) bool {
	if len(r.bgs) >= MaxBackground {
		return false
	}
	if !browser {
		return true
	}
	return !r.running && !r.browserHeldLocked()
}

// Idle is true when nothing runs here at all: no conversation turn, no
// background task. What the browser check and the usage probe want.
func (r *Runner) Idle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.running && len(r.bgs) == 0
}

// Background runs a task when it may start (CanStart), and - when the task
// drives the browser - gives way the moment the person speaks: Send and
// Enqueue cancel it. The browser is one shared window, and the person's own
// request always wins it. A task without the browser runs on through the
// conversation; the two have nothing to fight over.
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
		r.mu.Lock()
		delete(r.bgs, task)
		r.mu.Unlock()
		r.freed()
	}()

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
	result, scanErr := backgroundResult(stdout, t.OnStep, r.usage.Observe, &used)
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

// yield stops a running background task. Called with r.mu held.
func (r *Runner) yieldLocked() {
	for t := range r.bgs {
		if t.browser {
			r.log.Info("대화가 시작되어 브라우저를 쓰는 백그라운드 작업을 멈춥니다", "task", t.name)
			t.cancel()
		}
	}
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
// text (for the log), whether the CLI reported an error, and what it used.
func backgroundResult(stdout interface{ Read([]byte) (int, error) }, onStep func(Step), onRate func(json.RawMessage), used *tally) (string, error) {
	var result string
	var failed error
	err := scan(stdout, func(ev event) {
		if used != nil {
			used.observe(ev)
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
	_, _ = backgroundResult(stdout, nil, r.usage.Observe, nil)
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
