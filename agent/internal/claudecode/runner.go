// Package claudecode drives the Claude Code CLI as a subprocess. Billing
// rides the subscription token in the environment; every state-changing tool
// call is routed to the approval gate over MCP before it runs.
package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/adapter/permission"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

var ErrBusy = errors.New("이미 처리 중인 요청이 있습니다")

type Runner struct {
	cfg   config.Config
	store *thread.Store
	log   *slog.Logger

	mu        sync.Mutex
	sessionID string
	running   bool
}

func New(cfg config.Config, store *thread.Store, log *slog.Logger) *Runner {
	return &Runner{cfg: cfg, store: store, log: log}
}

// Send posts the operator's message and launches a turn. It returns once the
// process is started; output reaches the client over SSE.
func (r *Runner) Send(text string) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrBusy
	}
	r.running = true
	r.mu.Unlock()

	r.store.AppendUser(text)

	go func() {
		defer func() {
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
			r.store.SetThinking(false)
		}()
		r.store.SetThinking(true)
		if err := r.turn(text); err != nil {
			r.log.Error("turn failed", "err", err)
			r.store.Fail(userFacingError(err))
		}
	}()
	return nil
}

func (r *Runner) turn(prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.TurnTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.cfg.ClaudeBin, r.args(prompt)...)
	cmd.Dir = r.cfg.Workspace
	cmd.Env = r.env()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude 실행: %w", err)
	}

	scanErr := r.consume(stdout)
	waitErr := cmd.Wait()

	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		r.log.Error("claude stderr", "msg", clamp(msg, 2000))
	}
	// The stream says why it failed; the exit code only says that it did.
	// Preferring the exit code here would throw the diagnosis away.
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		return fmt.Errorf("claude 종료: %w", waitErr)
	}
	return nil
}

func (r *Runner) args(prompt string) []string {
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-prompt-tool", permission.QualifiedToolName(),
		// Routes prompts to the tool above rather than denying them outright.
		"--permission-prompts", "host",
		"--permission-mode", r.cfg.PermissionMode,
		"--mcp-config", r.mcpConfig(),
		// Ignore any MCP config on disk: the approval server is the only one.
		"--strict-mcp-config",
		// Rendered per turn, not at boot: the prompt carries today's date,
		// and this process is expected to stay up for weeks.
		"--append-system-prompt", r.systemPrompt(),
	}
	if tools := r.cfg.AllowedTools; len(tools) > 0 {
		// Pre-allowed tools never reach the card; keep this list read-only.
		args = append(args, "--allowedTools", strings.Join(tools, ","))
	}
	if r.cfg.Model != "" {
		args = append(args, "--model", r.cfg.Model)
	}
	r.mu.Lock()
	sessionID := r.sessionID
	r.mu.Unlock()
	if sessionID != "" {
		// Continuity across turns lives in the CLI's session, not in memory here.
		args = append(args, "--resume", sessionID)
	}
	return args
}

// systemPrompt appends what the model cannot know from its own context: the
// date it is reasoning about. Every module that takes a date takes an
// absolute one, so the conversion has to happen here, once, in the model.
func (r *Runner) systemPrompt() string {
	now := time.Now()
	today := fmt.Sprintf("%d-%02d-%02d (%s)",
		now.Year(), int(now.Month()), now.Day(), weekdays[int(now.Weekday())])

	return r.cfg.SystemPrompt + fmt.Sprintf(`

현재 시각
- 오늘은 %s, 지금은 %s입니다. 타임존은 %s입니다.
- '내일', '다음 주 화요일' 같은 상대 날짜는 직접 절대 날짜로 계산해서 도구에 넘기세요.`,
		today, now.Format("15:04"), now.Location())
}

var weekdays = [...]string{"일", "월", "화", "수", "목", "금", "토"}

func (r *Runner) mcpConfig() string {
	servers := map[string]any{
		permission.ServerName: map[string]any{
			"type": "http",
			"url":  r.cfg.PublicMCPURL,
		},
	}
	// One entry per module that talks to the model. Dropping a module from
	// the registry is what should remove it here, so this reads it back.
	for name, url := range r.cfg.ModuleMCPURLs {
		servers[name] = map[string]any{"type": "http", "url": url}
	}
	cfg := map[string]any{"mcpServers": servers}
	body, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func (r *Runner) env() []string {
	// Narrow on purpose - nothing here can redirect billing - but a shell
	// still needs enough of an environment to behave like one. Leaving SHELL
	// unset makes the CLI's Bash tool fail, and leaving LANG unset mangles
	// non-ASCII content on its way through the shell.
	// Both MCP limits are MILLISECONDS to the CLI (MCP_TIMEOUT defaults to
	// 30000, MCP_TOOL_TIMEOUT is documented as a hard wall-clock limit per
	// call that progress notifications do not extend). Passing seconds here
	// made the real limit 1.8s, and a tool call that waits on a person is
	// always slower than that: the card was approved and the write never ran.
	// The budget has to cover the human, so it is the approval wait plus slack.
	budget := (r.cfg.ApprovalWait + 5*time.Minute).Milliseconds()

	env := []string{
		"CLAUDE_CODE_OAUTH_TOKEN=" + r.cfg.OAuthToken,
		"MCP_TIMEOUT=" + strconv.FormatInt(budget, 10),
		"MCP_TOOL_TIMEOUT=" + strconv.FormatInt(budget, 10),
		"HOME=" + r.cfg.Home,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SHELL=" + r.cfg.Shell,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=dumb",
		"TMPDIR=/tmp",
		// The CLI must agree with the agent about what day it is.
		"TZ=" + os.Getenv("TZ"),
	}
	if u := os.Getenv("USER"); u != "" {
		env = append(env, "USER="+u, "LOGNAME="+u)
	}
	return env
}

// event is a deliberately loose view of one stream-json line: fields we do
// not recognise are ignored rather than failing the turn.
type event struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Message   json.RawMessage `json:"message"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	// The CLI reports its own failures as assistant text. Those are not
	// Jarvis speaking, so they must not land in the thread as a reply.
	IsAPIErrorMessage bool   `json:"is_api_error_message"`
	Error             string `json:"error"`
}

type assistantMessage struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (r *Runner) consume(stdout interface{ Read([]byte) (int, error) }) error {
	scanner := bufio.NewScanner(stdout)
	// Tool results can be large; the default 64KB line cap is not enough.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			if r.cfg.Debug {
				r.log.Info("unparsed stream line", "line", clamp(line, 500))
			}
			continue
		}
		if r.cfg.Debug {
			r.log.Info("stream event", "type", ev.Type, "subtype", ev.Subtype)
		}

		switch ev.Type {
		case "system":
			if ev.SessionID != "" {
				r.mu.Lock()
				r.sessionID = ev.SessionID
				r.mu.Unlock()
			}
		case "assistant":
			if ev.IsAPIErrorMessage {
				return fmt.Errorf("claude api error: %s: %s",
					ev.Error, clamp(strings.Join(paragraphsOf(ev.Message), " "), 300))
			}
			for _, p := range paragraphsOf(ev.Message) {
				r.store.AppendAgent([]string{p})
			}
		case "result":
			if ev.IsError {
				return fmt.Errorf("claude result error: %s", clamp(ev.Result, 500))
			}
		}
	}
	return scanner.Err()
}

func paragraphsOf(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var msg assistantMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}
	var out []string
	for _, block := range msg.Content {
		if block.Type != "text" {
			continue
		}
		for _, p := range strings.Split(block.Text, "\n\n") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func clamp(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func userFacingError(err error) string {
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "claude CLI를 찾을 수 없습니다. 이미지에 설치됐는지 확인하세요."
	case errors.Is(err, context.DeadlineExceeded):
		return "응답 대기 시간이 초과되었습니다."
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "authentication_failed"), strings.Contains(msg, "Invalid bearer token"):
		return "Claude Code 인증에 실패했습니다. CLAUDE_CODE_OAUTH_TOKEN을 확인하세요."
	case strings.Contains(msg, "rate"):
		return "구독 사용 한도에 걸렸습니다. 잠시 후 다시 보내주세요."
	default:
		return "요청을 처리하지 못했습니다. 에이전트 로그를 확인하세요."
	}
}
