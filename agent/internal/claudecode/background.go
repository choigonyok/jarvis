package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
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
	Name   string
	Prompt string
	// System is appended to the CLI's system prompt, after today's date.
	System string
	// Servers are this agent's own MCP endpoints (loopback, no credential).
	Servers map[string]string
	// Extra names entries of JARVIS_EXTRA_MCP to include, with their token.
	Extra   []string
	Allowed []string
	Timeout time.Duration
	// OnStep, when set, hears each tool call and each bit of text the model
	// writes while the task runs - how a person watching sees progress.
	OnStep func(Step)
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
	return r.bgCancel != nil
}

// Idle is true when no conversation turn is running or queued.
func (r *Runner) Idle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.running && r.bgCancel == nil
}

// Background runs a task if the assistant is idle, and gives way the moment
// the person speaks: Send and Enqueue cancel it. The browser is one shared
// window, and the person's own request always wins it.
func (r *Runner) Background(parent context.Context, t Task) (string, error) {
	ctx, cancel := context.WithTimeout(parent, t.Timeout)
	defer cancel()

	r.mu.Lock()
	if r.running || r.bgCancel != nil {
		r.mu.Unlock()
		return "", ErrBusy
	}
	yielded := false
	r.bgCancel = func() { yielded = true; cancel() }
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.bgCancel = nil
		r.mu.Unlock()
	}()

	args, err := r.backgroundArgs(t)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, r.cfg.ClaudeBin, args...)
	cmd.Dir = r.cfg.Workspace
	cmd.Env = r.env()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude 실행: %w", err)
	}
	result, scanErr := backgroundResult(stdout, t.OnStep)
	waitErr := cmd.Wait()

	r.mu.Lock()
	gaveWay := yielded
	r.mu.Unlock()
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
	if r.bgCancel != nil {
		r.log.Info("대화가 시작되어 백그라운드 작업을 멈춥니다")
		r.bgCancel()
	}
}

func (r *Runner) backgroundArgs(t Task) ([]string, error) {
	servers := map[string]any{}
	for name, url := range t.Servers {
		servers[name] = map[string]any{"type": "http", "url": url}
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

	now := time.Now()
	system := fmt.Sprintf("오늘은 %d-%02d-%02d (%s), 지금은 %s 입니다.\n\n%s",
		now.Year(), int(now.Month()), now.Day(), weekdays[int(now.Weekday())], now.Format("15:04"), t.System)

	args := []string{
		"-p", t.Prompt,
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
		"--append-system-prompt", system,
	}
	if r.cfg.Model != "" {
		args = append(args, "--model", r.cfg.Model)
	}
	return args, nil
}

// backgroundResult reads the stream only for its outcome: the final result
// text (for the log) and whether the CLI reported an error.
func backgroundResult(stdout interface{ Read([]byte) (int, error) }, onStep func(Step)) (string, error) {
	var result string
	var failed error
	err := scan(stdout, func(ev event) {
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
