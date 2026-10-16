// Package claudecode drives the Claude Code CLI as a subprocess. Billing
// rides the subscription token in the environment; every state-changing tool
// call is routed to the approval gate over MCP before it runs.
package claudecode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/adapter/permission"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/usage"
)

var ErrBusy = errors.New("이미 처리 중인 요청이 있습니다")

// ErrNotQueued is a steer or drop of a message no longer waiting.
var ErrNotQueued = errors.New("대기 중인 메시지가 아닙니다")

// Queued is one message waiting behind the running turn.
type Queued struct {
	ID     string      `json:"id"`
	Text   string      `json:"text"`
	Images []string    `json:"images,omitempty"`
	Ref    *thread.Ref `json:"ref,omitempty"`
	At     time.Time   `json:"at"`
	// prompt is a turn nobody typed (a card's decision): not shown, and
	// its words are the prompt as given.
	prompt string
}

func (q *Queued) typed() bool { return q.prompt == "" }

type Runner struct {
	cfg   config.Config
	store *thread.Store
	log   *slog.Logger

	mu        sync.Mutex
	sessionID string
	// sessionPrompt fingerprints the system prompt the session was opened
	// with; opening, the one a session the running turn starts is opened with.
	sessionPrompt string
	opening       string
	// The last reading of mind's situation, reused for a few seconds.
	nowText string
	nowAt   time.Time
	running bool
	// queue is what waits behind the running turn, in order: messages the
	// operator sent meanwhile (shown, and each can be steered into the
	// running turn or dropped) and turns nobody typed - a card's decision.
	queue []*Queued
	// input is the running turn's stdin while it still takes messages (until
	// its result): a steered message is written here.
	input io.WriteCloser
	// bgCancel stops the background task in flight, if any (see Background).
	bgCancel func()
	// usage hears every rate_limit_event, chat and background alike.
	usage *usage.Tracker
	// recall, when set, returns what long-term memory holds about a message,
	// put in front of it for the model (not in the transcript).
	recall func(ctx context.Context, text string) string
}

// SetRecall hands every typed message to fn before the model sees it.
func (r *Runner) SetRecall(fn func(ctx context.Context, text string) string) { r.recall = fn }

// session is the one field of the CLI's state this agent owns. The transcript
// itself lives in the CLI's own home directory.
type session struct {
	ID string `json:"id"`
	// Prompt fingerprints the system prompt the session was opened with. The
	// CLI keeps a session's first system prompt for good: --append-system-prompt
	// on a --resume is ignored (Claude Code 2.1.292, checked 2026-10-17 - a
	// changed payment rule never reached the model for days). So a session
	// opened under other instructions is not resumed; a new one is opened
	// with the conversation so far put in front of it.
	Prompt string `json:"prompt,omitempty"`
}

// fingerprint names a system prompt without keeping it.
func fingerprint(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:8])
}

func New(cfg config.Config, store *thread.Store, log *slog.Logger) *Runner {
	r := &Runner{cfg: cfg, store: store, log: log}
	// A restart that forgets the session id leaves the operator looking at a
	// transcript the model cannot remember. Failing to read it is not fatal:
	// the next turn opens a new session, which is exactly what happened before
	// this file existed.
	var saved session
	if err := jsonfile.Load(cfg.SessionPath, &saved); err != nil {
		log.Error("세션 id를 읽지 못했습니다", "err", err)
	}
	r.sessionID = saved.ID
	r.sessionPrompt = saved.Prompt
	return r
}

// remember stores the session id the CLI reported, and the prompt it was
// opened with, so the next process resumes the same conversation.
func (r *Runner) remember(id, prompt string) {
	if err := jsonfile.Save(r.cfg.SessionPath, session{ID: id, Prompt: prompt}); err != nil {
		r.log.Error("세션 id를 저장하지 못했습니다", "err", err)
	}
}

// Send posts the operator's message and launches a turn. It returns once the
// process is started; output reaches the client over SSE.
//
// Attached photos are named in the prompt by path. The model opens them with
// Read, which shows it the image; the transcript keeps only the names.
func (r *Runner) Send(text string, images []string) error {
	return r.SendRef(text, images, nil)
}

// SendRef starts a turn for a message that quotes an earlier one: the model
// is handed the quoted message ahead of what the operator wrote, so "이 제품
// 구매해줘" knows which product.
func (r *Runner) SendRef(text string, images []string, ref *thread.Ref) error {
	_, err := r.Submit(text, images, ref)
	return err
}

// Submit starts a turn for the operator's message, or - while one is
// running - puts it in the queue, where it waits its turn unless it is
// steered into the running one. queued says which.
func (r *Runner) Submit(text string, images []string, ref *thread.Ref) (queued bool, err error) {
	q := &Queued{ID: "q-" + strconv.FormatInt(time.Now().UnixNano(), 36), Text: text, Images: images, Ref: ref, At: time.Now()}
	r.mu.Lock()
	if r.running {
		r.queue = append(r.queue, q)
		r.mu.Unlock()
		r.showQueue()
		return true, nil
	}
	r.running = true
	r.yieldLocked()
	r.mu.Unlock()
	go func() { r.loop(r.start(q)) }()
	return false, nil
}

// start writes a typed message into the transcript, now that it is being
// read, and returns its prompt; a turn nobody typed is its prompt.
func (r *Runner) start(q *Queued) string {
	if !q.typed() {
		return q.prompt
	}
	if q.Ref != nil {
		r.store.AppendUserRef(q.Text, q.Ref, q.Images...)
	} else {
		r.store.AppendUser(q.Text, q.Images...)
	}
	prompt := withRef(withImages(q.Text, q.Images, r.cfg.UploadsDir), q.Ref)
	if r.recall != nil {
		if known := r.recall(context.Background(), q.Text); known != "" {
			prompt = known + "\n" + prompt
		}
	}
	return prompt
}

// Queue is the operator's messages waiting behind the running turn.
func (r *Runner) Queue() []Queued {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Queued{}
	for _, q := range r.queue {
		if q.typed() {
			out = append(out, *q)
		}
	}
	return out
}

func (r *Runner) showQueue() { r.store.ShowQueue(r.Queue()) }

// Steer takes a waiting message out of the queue and hands it to the turn
// that is running, which reads it at its next step - the way a person
// interrupts with "아 그리고 ...". If that turn no longer takes messages (it
// is giving its last answer), the message goes first in line instead.
// steered says which.
func (r *Runner) Steer(id string) (steered bool, err error) {
	r.mu.Lock()
	at := slices.IndexFunc(r.queue, func(q *Queued) bool { return q.ID == id && q.typed() })
	if at < 0 {
		r.mu.Unlock()
		return false, ErrNotQueued
	}
	q := r.queue[at]
	r.queue = slices.Delete(r.queue, at, at+1)
	if r.input != nil {
		if err := writeUser(r.input, withRef(withImages(q.Text, q.Images, r.cfg.UploadsDir), q.Ref)); err == nil {
			r.mu.Unlock()
			if q.Ref != nil {
				r.store.AppendUserRef(q.Text, q.Ref, q.Images...)
			} else {
				r.store.AppendUser(q.Text, q.Images...)
			}
			r.showQueue()
			return true, nil
		}
	}
	r.queue = slices.Insert(r.queue, 0, q)
	r.mu.Unlock()
	r.showQueue()
	return false, nil
}

// Drop takes a waiting message out of the queue unsent.
func (r *Runner) Drop(id string) error {
	r.mu.Lock()
	at := slices.IndexFunc(r.queue, func(q *Queued) bool { return q.ID == id && q.typed() })
	if at < 0 {
		r.mu.Unlock()
		return ErrNotQueued
	}
	r.queue = slices.Delete(r.queue, at, at+1)
	r.mu.Unlock()
	r.showQueue()
	return nil
}

// writeUser is one user message on a stream-json stdin.
func writeUser(w io.Writer, text string) error {
	line, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	if err != nil {
		return err
	}
	_, err = w.Write(append(line, '\n'))
	return err
}

// withRef puts the quoted message in front of the prompt, marked as a quote.
func withRef(prompt string, ref *thread.Ref) string {
	if ref == nil {
		return prompt
	}
	who := "운영자"
	if ref.Role == thread.RoleAgent {
		who = "Jarvis(당신)"
	}
	return fmt.Sprintf("[운영자가 참조한 이전 메시지 · %s · %s]\n%s\n[참조 끝]\n\n%s", who, ref.At, ref.Text, prompt)
}

func withImages(text string, images []string, dir string) string {
	if len(images) == 0 {
		return text
	}
	var b strings.Builder
	if strings.TrimSpace(text) == "" {
		b.WriteString("(글 없이 사진만 보냈습니다.)")
	} else {
		b.WriteString(text)
	}
	fmt.Fprintf(&b, "\n\n[첨부 사진 %d장 - Read 도구로 하나씩 열어 보세요]\n", len(images))
	for _, name := range images {
		b.WriteString(filepath.Join(dir, name) + "\n")
	}
	return b.String()
}

// Enqueue starts a turn the operator did not type - how a card's decision
// reaches the model. It is not written to the transcript as the operator's
// words; the card's own change of state is what the operator sees, and the
// reply is what follows it. Queued behind a running turn rather than refused:
// a decision must never be dropped because the assistant was busy.
func (r *Runner) Enqueue(prompt string) {
	r.mu.Lock()
	if r.running {
		r.queue = append(r.queue, &Queued{ID: "c-" + strconv.FormatInt(time.Now().UnixNano(), 36), At: time.Now(), prompt: prompt})
		r.mu.Unlock()
		return
	}
	r.running = true
	r.yieldLocked()
	r.mu.Unlock()
	go r.loop(prompt)
}

// loop runs one turn, then whatever queued up behind it, and only then lets
// the runner go idle. Holding running across the queue is what stops an
// operator's message from slipping in between a decision and its follow-up.
func (r *Runner) loop(prompt string) {
	r.store.SetThinking(true)
	for {
		if err := r.turn(prompt); err != nil {
			r.log.Error("turn failed", "err", err)
			r.store.Fail(userFacingError(err))
		}
		r.mu.Lock()
		if len(r.queue) == 0 {
			r.running = false
			r.mu.Unlock()
			r.store.SetThinking(false)
			return
		}
		next := r.queue[0]
		r.queue = r.queue[1:]
		r.mu.Unlock()
		if next.typed() {
			r.showQueue()
		}
		prompt = r.start(next)
	}
}

func (r *Runner) turn(prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.TurnTimeout)
	defer cancel()

	// A session opened under other instructions would keep them: open a new
	// one instead, handed the conversation so far.
	want := fingerprint(r.systemPrompt())
	r.mu.Lock()
	fresh := r.sessionID == "" || r.sessionPrompt != want
	if fresh && r.sessionID != "" {
		r.log.Info("지시가 바뀌어 새 세션으로 이어 갑니다", "old", r.sessionID)
		r.sessionID = ""
	}
	r.opening = want
	r.mu.Unlock()
	// What changes from turn to turn - the date, mind's situation - rides on
	// the message: a resumed session never sees a new system prompt.
	head := r.now()
	if fresh {
		head = r.recap() + head
	}

	cmd := exec.CommandContext(ctx, r.cfg.ClaudeBin, r.args()...)
	cmd.Dir = r.cfg.Workspace
	cmd.Env = r.env()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// The prompt goes in on stdin, and stdin stays open until the result:
	// a message steered in meanwhile is read at the turn's next step.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude 실행: %w", err)
	}
	if err := writeUser(stdin, head+prompt); err != nil {
		stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("claude 입력: %w", err)
	}
	r.mu.Lock()
	r.input = stdin
	r.mu.Unlock()

	scanErr := r.consume(stdout)
	r.closeInput()
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

// closeInput ends the running turn's input: after its result nothing more
// is read, and the CLI exits once stdin is closed.
func (r *Runner) closeInput() {
	r.mu.Lock()
	in := r.input
	r.input = nil
	r.mu.Unlock()
	if in != nil {
		in.Close()
	}
}

func (r *Runner) args() []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-prompt-tool", permission.QualifiedToolName(),
		// Routes prompts to the tool above rather than denying them outright.
		"--permission-prompts", "host",
		"--permission-mode", r.cfg.PermissionMode,
		"--mcp-config", r.mcpConfig(),
		// Ignore any MCP config on disk: the approval server is the only one.
		"--strict-mcp-config",
		// The instructions only: a resumed session keeps the ones it was
		// opened with, so what changes per turn goes in the message (now).
		"--append-system-prompt", r.systemPrompt(),
	}
	if r.cfg.UploadsDir != "" {
		// Attached photos live outside the workspace; Read may open them.
		args = append(args, "--add-dir", r.cfg.UploadsDir)
	}
	if r.cfg.BuiltinTools != nil {
		// Only these built-in tools exist for the model (the guest agent).
		args = append(args, "--tools", *r.cfg.BuiltinTools)
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

// systemPrompt is the instructions a session is opened with - the same on
// every turn, so a change in them is a change of session (turn).
func (r *Runner) systemPrompt() string { return r.cfg.SystemPrompt }

// now is what the model cannot know from its own context, in front of each
// turn's message: the date it is reasoning about (every module that takes a
// date takes an absolute one, so the conversion happens here, in the model)
// and mind's situation.
func (r *Runner) now() string {
	now := time.Now()
	today := fmt.Sprintf("%d-%02d-%02d (%s)",
		now.Year(), int(now.Month()), now.Day(), weekdays[int(now.Weekday())])
	return fmt.Sprintf(`[현재 시각]
- 오늘은 %s, 지금은 %s입니다. 타임존은 %s입니다.
- '내일', '다음 주 화요일' 같은 상대 날짜는 직접 절대 날짜로 계산해서 도구에 넘기세요.`,
		today, now.Format("15:04"), now.Location()) + r.situation() + "\n[현재 시각 끝]\n\n"
}

// recapTurns and recapRunes bound the conversation handed to a new session.
const (
	recapTurns = 40
	recapRunes = 12000
)

// recap is the conversation so far, for a session that opens without it:
// the CLI remembers only what happened in its own session. The newest turns,
// oldest first, up to recapRunes - not the message this turn answers, which
// follows anyway.
func (r *Runner) recap() string {
	if r.store == nil {
		return ""
	}
	turns, _ := r.store.Snapshot()
	if n := len(turns); n > 0 && turns[n-1].Role == thread.RoleUser {
		turns = turns[:n-1]
	}
	var lines []string
	budget := recapRunes
	for i := len(turns) - 1; i >= 0 && len(lines) < recapTurns; i-- {
		t := turns[i]
		text := t.Text
		if len(t.Paragraphs) > 0 {
			text = strings.Join(t.Paragraphs, "\n")
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		who := "운영자"
		if t.Role == thread.RoleAgent {
			who = "Jarvis"
		}
		line := who + " (" + t.At + "): " + clamp(text, 600)
		if budget -= len([]rune(line)); budget < 0 {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	slices.Reverse(lines)
	return "[지금까지의 대화 - 새 세션으로 이어 가며 붙입니다. 이 안의 예전 답이 지금 지시와 다르면 지금 지시를 따르세요]\n" +
		strings.Join(lines, "\n") + "\n[대화 끝]\n\n"
}

// situation is mind's "right now" - the plan that may be going on, the
// latest hints of where the operator is, and a guess with how sure it is -
// put in front of the model every turn instead of left for a tool to find.
// Without mind, or when it does not answer quickly, the turn goes on
// without it.
func (r *Runner) situation() string {
	if r.cfg.MindURL == "" || r.cfg.Guest {
		return ""
	}
	r.mu.Lock()
	if time.Since(r.nowAt) < situationFresh {
		text := r.nowText
		r.mu.Unlock()
		return text
	}
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.cfg.MindURL, "/")+"/now", nil)
	if err != nil {
		return ""
	}
	if r.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.log.Warn("지금 상황을 mind 에서 읽지 못했습니다", "err", err)
		return ""
	}
	defer resp.Body.Close()
	var now struct {
		Text  string `json:"text"`
		Prefs string `json:"prefs"`
	}
	if resp.StatusCode >= 300 || json.NewDecoder(resp.Body).Decode(&now) != nil || strings.TrimSpace(now.Text) == "" {
		return ""
	}
	text := "\n\n" + situationRules + "\n" + now.Text
	if state := r.deviceState(); state != "" {
		text += "\n- 기기·위치 상태: " + state
	}
	if strings.TrimSpace(now.Prefs) != "" {
		text += "\n\n" + prefsRules + "\n" + now.Prefs
	}
	r.mu.Lock()
	r.nowText, r.nowAt = text, time.Now()
	r.mu.Unlock()
	return text
}

// deviceState is context-svc's one line (in which region, driving, in a
// meeting, how old the last fix), or nothing without it.
func (r *Runner) deviceState() string {
	if r.cfg.ContextURL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.cfg.ContextURL, "/")+"/now", nil)
	if err != nil {
		return ""
	}
	if r.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var now struct {
		Summary string `json:"summary"`
	}
	if resp.StatusCode >= 300 || json.NewDecoder(resp.Body).Decode(&now) != nil {
		return ""
	}
	return now.Summary
}

const prefsRules = `알고 있는 취향 (회고 카드와 운영자의 말에서. 장소·가게·숙소를 고를 때 쓰세요)
- 썼으면 근거를 한 줄로 밝히세요. 예: "조용한 곳 위주로 골랐어요 (지난 캠핑장 2곳에서 소음 지적)".
- 추정은 확정처럼 말하지 말고, 범위("가족과 갈 때")가 있으면 그때만 쓰세요.`

// situationFresh is how long one reading of the situation is reused.
const situationFresh = 30 * time.Second

const situationRules = `지금 상황 (mind 세계 모델이 일정·결제·사진으로 규칙에 따라 계산한 것. 도구로 찾지 말고 이것을 쓰세요)
- 위치·동행이 답에 필요한데 추정의 신뢰도가 0.5 미만이거나 추정이 없으면, 추측하지 말고 먼저 짧게 물어보세요(예: "지금 지수네 집이세요?").
- 이 추정을 답에 썼으면 답의 첫 줄에 가정을 한 줄로 밝히세요. 예: "지수네 집 기준으로 골랐어요(15:40 사진 위치)."
- 운영자가 정정하면("벌써 집에 왔어") 그 말을 그대로 믿고 답을 고치세요. mind 가 대화를 읽어 다음 추정에 반영합니다.
- 근처 가게(배달·식당·카페)는 mcp__context__find_nearby 로 지금 상황의 장소 근처에서 찾으세요(읽기 전용, OpenStreetMap). 후보 3개 정도와 근거(거리·종류, 아는 취향)를 들고, 답에 어디 기준인지와 출처를 밝히세요. 지도에 빠진 가게가 있을 수 있습니다.
- 배달·택배 주소가 필요하면 먼저 기억(mcp__memory__search_memory, 예: "지수네 집 주소")에서 이 장소로 배달한 기록을 찾으세요. 없으면 한 번만 묻고, 받은 주소는 mind 가 이 대화에서 읽어 기억에 남깁니다(다음에는 묻지 않음).
- 주문·결제·메시지 발송은 확신과 상관없이 항상 먼저 묻습니다.`

var weekdays = [...]string{"일", "월", "화", "수", "목", "금", "토"}

func (r *Runner) mcpConfig() string {
	servers := map[string]any{
		permission.ServerName: r.internal(r.cfg.PublicMCPURL),
	}
	// One entry per module that talks to the model. Dropping a module from
	// the registry is what should remove it here, so this reads it back.
	for name, url := range r.cfg.ModuleMCPURLs {
		servers[name] = r.internal(url)
	}
	// Servers outside the container. These listen where others can reach
	// them, so the bearer token goes only here - a module server is on
	// loopback and has no business seeing it.
	for name, url := range r.cfg.ExtraMCPURLs {
		entry := map[string]any{"type": "http", "url": url}
		if r.cfg.ExtraMCPToken != "" {
			entry["headers"] = map[string]any{
				"Authorization": "Bearer " + r.cfg.ExtraMCPToken,
			}
		}
		servers[name] = entry
	}
	cfg := map[string]any{"mcpServers": servers}
	body, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(body)
}

// internal is an MCP entry for one of this agent's own servers, carrying the
// token its /mcp routes ask of every caller.
func (r *Runner) internal(url string) map[string]any {
	entry := map[string]any{"type": "http", "url": url}
	if r.cfg.InternalMCPToken != "" {
		entry["headers"] = map[string]any{"Authorization": "Bearer " + r.cfg.InternalMCPToken}
	}
	return entry
}

func (r *Runner) env() []string {
	// Narrow on purpose - nothing here can redirect billing - but a shell
	// still needs enough of an environment to behave like one. Leaving SHELL
	// unset makes the CLI's Bash tool fail, and leaving LANG unset mangles
	// non-ASCII content on its way through the shell.
	// Both MCP limits are MILLISECONDS to the CLI (MCP_TIMEOUT defaults to
	// 30000, MCP_TOOL_TIMEOUT is documented as a hard wall-clock limit per
	// call that progress notifications do not extend). The approval tool no
	// longer waits on a person, but browser tools that hit a payment card
	// still do, so the budget still covers the approval wait.
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
	// RateLimitInfo rides on "rate_limit_event": how much of the
	// subscription's 5-hour and weekly windows is used (see package usage).
	RateLimitInfo json.RawMessage `json:"rate_limit_info"`
}

type assistantMessage struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (r *Runner) consume(stdout interface{ Read([]byte) (int, error) }) error {
	var failed error
	err := scan(stdout, func(ev event) {
		if failed != nil {
			return
		}
		if r.cfg.Debug {
			r.log.Info("stream event", "type", ev.Type, "subtype", ev.Subtype)
		}
		failed = r.handle(ev)
	})
	if failed != nil {
		return failed
	}
	return err
}

// scan reads stream-json lines. Lines it cannot parse are skipped rather
// than failing the turn.
func scan(stdout interface{ Read([]byte) (int, error) }, each func(event)) error {
	scanner := bufio.NewScanner(stdout)
	// Tool results can be large; the default 64KB line cap is not enough.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev event
		if json.Unmarshal([]byte(line), &ev) == nil {
			each(ev)
		}
	}
	return scanner.Err()
}

func (r *Runner) handle(ev event) error {
	switch ev.Type {
	case "rate_limit_event":
		r.usage.Observe(ev.RateLimitInfo)
	case "system":
		if ev.SessionID != "" {
			r.mu.Lock()
			changed := r.sessionID != ev.SessionID || r.sessionPrompt != r.opening
			r.sessionID = ev.SessionID
			r.sessionPrompt = r.opening
			prompt := r.opening
			r.mu.Unlock()
			if changed {
				r.remember(ev.SessionID, prompt)
			}
		}
	case "assistant":
		if ev.IsAPIErrorMessage {
			return fmt.Errorf("claude api error: %s: %s",
				ev.Error, clamp(strings.Join(paragraphsOf(ev.Message), " "), 300))
		}
		// One message, one turn. Splitting it per paragraph used to read
		// fine as plain text, but a fenced code block with a blank line
		// in it would land as two turns and stop being one block.
		r.store.AppendAgent(paragraphsOf(ev.Message))
	case "result":
		r.closeInput()
		if ev.IsError {
			return fmt.Errorf("claude result error: %s", clamp(ev.Result, 500))
		}
	}
	return nil
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
