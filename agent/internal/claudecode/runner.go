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
	"sync/atomic"
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
	// prefs is the operator's taste as mind last gave it. It is part of the
	// instructions, so it stays as it was while mind cannot be asked: a turn
	// without it would be other instructions, and a new session.
	prefs string
	// What the session in use has shown of itself (see session): when its
	// last turn ended, what its requests carry besides the conversation
	// (instructions and tools, estimated from its first request), and how
	// big its last request was.
	lastAt  time.Time
	base    int
	context int
	// shown is the situation last put in front of a message in this session;
	// showing is this turn's, kept once the turn went through.
	shown, showing string
	// hold, when set, says a card this conversation raised is still open:
	// its decision continues the work, so the session must not change under it.
	hold    func(now time.Time) bool
	running bool
	// queue is what waits behind the running turn, in order: messages the
	// operator sent meanwhile (shown, and each can be steered into the
	// running turn or dropped) and turns nobody typed - a card's decision.
	queue []*Queued
	// input is the running turn's stdin while it still takes messages (until
	// its result): a steered message is written here.
	input io.WriteCloser
	// bgs is every background task in flight (see Background); the one on
	// the browser is what TakeBrowser stops when the conversation goes to
	// use it.
	bgs map[*bgTask]struct{}
	// places are the places reserved for background tasks about to start
	// (Reserve), counted as taken until those tasks start or give them back.
	places map[*place]struct{}
	// chatBrowser: a turn of this conversation called a browser tool
	// (TakeBrowser). No background task gets the browser until the turns
	// run out (loop) - the next message is likely about the same page.
	chatBrowser bool
	// usage hears every rate_limit_event, chat and background alike.
	usage *usage.Tracker
	// recall, when set, returns what long-term memory holds about a message,
	// put in front of it for the model (not in the transcript).
	recall func(ctx context.Context, text string) string
	// newsSeen is the time (atIso) of the newest job news the model has been
	// handed (jobNews). Jobs speak into the transcript without a model turn,
	// so the session never saw them; what came since is put in front of
	// the next message. In memory only: after a restart a few are told again.
	newsSeen string
	// onFree hears a place come free (OnFree).
	onFree []func()
	// seen hears all text the model is shown: what it is sent and what
	// every tool gives back (OnSeen).
	seen func(string)
	// live is the draft of the text block the model is writing, and that
	// block's index in its message; "" between blocks. Only the turn's
	// reading of its stream touches it.
	live      string
	liveBlock int
}

// OnSeen hands fn all text the model is shown - each message sent to it,
// each tool's result, the CLI's own WebSearch included, in the conversation
// and in background tasks. The web module keeps the addresses in it: the
// only ones its read will open.
func (r *Runner) OnSeen(fn func(string)) { r.seen = fn }

func (r *Runner) saw(text string) {
	if r.seen != nil && text != "" {
		r.seen(text)
	}
}

// OnFree registers fn to hear that a place for a background task came free:
// the conversation finished its turn (and what queued behind it), or a
// background task ended. A job waiting on the browser starts then, not on a
// clock. fn runs on its own goroutine and must be quick.
func (r *Runner) OnFree(fn func()) {
	r.mu.Lock()
	r.onFree = append(r.onFree, fn)
	r.mu.Unlock()
}

func (r *Runner) freed() {
	r.mu.Lock()
	fns := slices.Clone(r.onFree)
	r.mu.Unlock()
	for _, fn := range fns {
		go fn()
	}
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
	// Prefs is the taste list those instructions carry, kept so a restart
	// while mind is down resumes the session instead of dropping the list.
	Prefs string `json:"prefs,omitempty"`
	// LastAt, Base and Context are what deciding on a new session reads
	// (Runner.rotateLocked); kept so a restart does not forget them.
	LastAt  time.Time `json:"lastAt,omitzero"`
	Base    int       `json:"base,omitempty"`
	Context int       `json:"context,omitempty"`
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
	r.prefs = saved.Prefs
	r.lastAt, r.base, r.context = saved.LastAt, saved.Base, saved.Context
	return r
}

// persist stores the session id the CLI reported, the prompt it was opened
// with and what it has shown of itself, so the next process resumes the same
// conversation and judges it the same way.
func (r *Runner) persist() {
	r.mu.Lock()
	s := session{ID: r.sessionID, Prompt: r.sessionPrompt, Prefs: r.prefs, LastAt: r.lastAt, Base: r.base, Context: r.context}
	r.mu.Unlock()
	if r.cfg.SessionPath == "" {
		return
	}
	if err := jsonfile.Save(r.cfg.SessionPath, s); err != nil {
		r.log.Error("세션 id를 저장하지 못했습니다", "err", err)
	}
}

// SetHold names the cards the session must not change under (rotateLocked).
func (r *Runner) SetHold(fn func(now time.Time) bool) { r.hold = fn }

// rotateLocked drops the session before a typed message when it went quiet
// (SessionIdle since its last turn) or grew large (SessionMaxTokens), so the
// message opens a new one handed only the recent conversation (recap). A
// large session costs on every request - each one reads it all, and a pause
// past the cache's lifetime writes it all again - while what carries over
// outside it (mind's facts recalled per message, the situation, the
// services' tools) does not depend on it. Kept: a session with little
// history, where a recap saves nothing; and one with a card this
// conversation raised still open (held), whose decision continues the work
// in it. Called with r.mu held, before the turn starts and with nothing
// queued - only typed messages get here, never a card's decision.
func (r *Runner) rotateLocked(now time.Time, held bool) {
	if r.sessionID == "" {
		return
	}
	history := r.historyLocked()
	idle := !r.lastAt.IsZero() && now.Sub(r.lastAt) >= r.cfg.SessionIdle
	large := r.cfg.SessionMaxTokens > 0 && history >= r.cfg.SessionMaxTokens
	if !idle && !large || history < r.cfg.SessionMinTokens {
		return
	}
	if held {
		r.log.Info("이 대화의 카드가 남아 있어 세션을 그대로 이어 갑니다", "history", history)
		return
	}
	reason := "large"
	if idle {
		reason = "idle"
	}
	r.log.Info("새 세션으로 이어 갑니다", "old", r.sessionID, "reason", reason, "history", history,
		"idle", now.Sub(r.lastAt).Round(time.Second).String())
	r.sessionID = ""
}

// historyLocked is how much conversation the session carries: its last
// request less what every request sends anyway (instructions and tools).
func (r *Runner) historyLocked() int {
	return max(r.context-r.base, 0)
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
	now := time.Now()
	q := &Queued{ID: queueID("q"), Text: text, Images: images, Ref: ref, At: now}
	// Asked before taking the lock: the cards have their own.
	held := r.hold != nil && r.hold(now)
	r.mu.Lock()
	if r.running {
		r.queue = append(r.queue, q)
		r.mu.Unlock()
		r.showQueue()
		return true, nil
	}
	r.running = true
	r.rotateLocked(now, held)
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
	prompt := withRef(withImages(q.Text, q.Images, r.cfg.UploadsDir), q.Ref, r.person())
	if r.recall != nil {
		if known := r.recall(context.Background(), q.Text); known != "" {
			prompt = known + "\n" + prompt
		}
	}
	return prompt
}

// queued numbers the queue's entries. The clock alone gave two messages
// queued in the same microsecond one id, and dropping one dropped either.
var queued atomic.Int64

func queueID(kind string) string {
	return kind + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatInt(queued.Add(1), 36)
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
		r.saw(q.Text)
		if err := writeUser(r.input, withRef(withImages(q.Text, q.Images, r.cfg.UploadsDir), q.Ref, r.person())); err == nil {
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
// person is who is on the other side of this conversation (Runner.person).
func withRef(prompt string, ref *thread.Ref, person string) string {
	if ref == nil {
		return prompt
	}
	who := person
	if ref.Role == thread.RoleAgent {
		who = "Jarvis(당신)"
	}
	return fmt.Sprintf("[%s가 참조한 이전 메시지 · %s · %s]\n%s\n[참조 끝]\n\n%s", person, who, ref.At, ref.Text, prompt)
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
		r.queue = append(r.queue, &Queued{ID: queueID("c"), At: time.Now(), prompt: prompt})
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	go r.loop(prompt)
}

// loop runs one turn, then whatever queued up behind it, and only then lets
// the runner go idle. Holding running across the queue is what stops an
// operator's message from slipping in between a decision and its follow-up.
func (r *Runner) loop(prompt string) {
	r.store.SetThinking(true)
	for {
		err := r.turn(prompt)
		limited, until := r.limitedUntil(err, time.Now())
		if err != nil {
			r.log.Error("turn failed", "err", err)
			msg := userFacingError(err)
			if limited {
				msg = fmt.Sprintf("구독 사용 한도에 걸려 이 메시지에는 답하지 못했습니다. %s 에 풀리면 그사이 보낸 메시지에 차례로 답합니다.",
					until.Format("15:04"))
			}
			r.store.Fail(msg)
		}
		if limited {
			// Out of the allowance, any turn would only fail the same way. The
			// runner stays busy until the window starts over - what is sent
			// meanwhile queues behind it - and then goes on with that.
			r.log.Info("구독 한도가 풀릴 때까지 기다립니다", "until", until)
			r.store.SetThinking(false)
			time.Sleep(time.Until(until.Add(limitSlack)))
			r.store.SetThinking(true)
		}
		r.mu.Lock()
		if len(r.queue) == 0 {
			r.running = false
			// Out of its turns, the conversation gives the browser back.
			r.chatBrowser = false
			r.mu.Unlock()
			r.store.SetThinking(false)
			r.freed()
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

// limitSlack is how long past a usage window's reset the queue waits before
// it goes on: the reset is the server's clock, and a turn started a moment
// early would fail again.
var limitSlack = 30 * time.Second

// maxLimitWait is the longest a usage limit is waited out: the five hours'
// window. A week's limit is not - nothing would be answered for days.
const maxLimitWait = 5*time.Hour + 10*time.Minute

// limitedUntil is whether err is a turn running out of the subscription's
// allowance - the stream said the limit rejected it - and when the allowance
// comes back, if that is soon enough to wait for.
func (r *Runner) limitedUntil(err error, now time.Time) (bool, time.Time) {
	if err == nil || r.usage == nil {
		return false, time.Time{}
	}
	u := r.usage.Snapshot(now)
	if u.Status != "rejected" {
		return false, time.Time{}
	}
	var until time.Time
	for _, w := range []*usage.Window{u.FiveHour, u.SevenDay} {
		if w != nil && !w.Reset && w.Utilization >= 1 && w.ResetsAt.After(until) {
			until = w.ResetsAt
		}
	}
	if until.IsZero() && u.FiveHour != nil && !u.FiveHour.Reset {
		until = u.FiveHour.ResetsAt // rejected, no window full: the five hours'
	}
	if !until.After(now) || until.Sub(now) > maxLimitWait {
		return false, time.Time{}
	}
	return true, until
}

func (r *Runner) turn(prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.TurnTimeout)
	defer cancel()

	// Read first: the taste list mind gives with the situation is part of
	// the instructions.
	situation := r.situation()
	system := r.systemPrompt()
	// A session opened under other instructions would keep them: open a new
	// one instead, handed the conversation so far.
	want := fingerprint(system)
	r.mu.Lock()
	fresh := r.sessionID == "" || r.sessionPrompt != want
	if fresh && r.sessionID != "" {
		r.log.Info("지시가 바뀌어 새 세션으로 이어 갑니다", "old", r.sessionID)
		r.sessionID = ""
	}
	if fresh {
		r.shown, r.showing = "", ""
	}
	r.opening = want
	var gap time.Duration
	if !r.lastAt.IsZero() {
		gap = time.Since(r.lastAt)
	}
	r.mu.Unlock()
	// What changes from turn to turn - the date, mind's situation - rides on
	// the message: a resumed session never sees a new system prompt.
	head := r.now(situation)
	if fresh {
		head = r.recap() + head
	}
	// What the jobs said meanwhile, which the session has not seen.
	head += r.jobNews()

	cmd := exec.CommandContext(ctx, r.cfg.ClaudeBin, r.args(system)...)
	cmd.Dir = r.cfg.Workspace
	cmd.Env = r.env(r.cfg.CacheTTL)

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
	r.saw(head + prompt)
	if err := writeUser(stdin, head+prompt); err != nil {
		stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("claude 입력: %w", err)
	}
	r.mu.Lock()
	r.input = stdin
	r.mu.Unlock()

	var t tally
	scanErr := r.consume(stdout, &t)
	// A message cut off mid-sentence (the CLI died, the turn timed out) is
	// not left on screen as if it were said.
	r.live = ""
	r.store.DropDraft()
	r.closeInput()
	waitErr := cmd.Wait()

	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		r.log.Error("claude stderr", "msg", clamp(msg, 2000))
	}
	// The stream says why it failed; the exit code only says that it did.
	// Preferring the exit code here would throw the diagnosis away.
	err = scanErr
	if err == nil && waitErr != nil {
		err = fmt.Errorf("claude 종료: %w", waitErr)
	}
	r.account(&t, fresh, head+prompt, gap, err == nil)
	return err
}

// account notes what the turn's requests showed of the session and logs
// what the turn used - the numbers the session and cache settings are
// judged by.
func (r *Runner) account(t *tally, fresh bool, opening string, gap time.Duration, ok bool) {
	first, last := t.first(), t.last()
	r.mu.Lock()
	if t.requests() > 0 {
		if fresh {
			// The first request of a new session is the instructions and the
			// tools plus its one message.
			r.base = max(first.context()-estimateTokens(opening), 0)
		}
		r.context = last.context()
	}
	r.lastAt = time.Now()
	if ok && r.showing != "" {
		// What the session saw last; a turn that showed none leaves it.
		r.shown = r.showing
	}
	base, history := r.base, r.historyLocked()
	r.mu.Unlock()
	r.persist()

	attrs := append(t.attrs(), "fresh", fresh, "fixed", base, "history", history, "ttl", r.cfg.CacheTTL)
	if gap > 0 {
		attrs = append(attrs, "gap", gap.Round(time.Second).String())
	}
	r.log.Info("턴 사용량", attrs...)
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

func (r *Runner) args(system string) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		// The answer as it is written, not only once each message is done:
		// the console shows it growing (handle, thread.Draft).
		"--include-partial-messages",
		"--permission-prompt-tool", permission.QualifiedToolName(),
		// Routes prompts to the tool above rather than denying them outright.
		"--permission-prompts", "host",
		"--permission-mode", r.cfg.PermissionMode,
		"--mcp-config", r.mcpConfig(),
		// Ignore any MCP config on disk: the approval server is the only one.
		"--strict-mcp-config",
		// The instructions only: a resumed session keeps the ones it was
		// opened with, so what changes per turn goes in the message (now).
		"--append-system-prompt", system,
		// Reads stop at the working directories - the workspace, the
		// uploads and the CLI's own memory (--add-dir). Nothing else in the
		// container is the model's to read, with a card or without: not the
		// CLI's credentials, not /etc, not another process's environment.
		"--settings", r.settings(map[string]any{"permissions": map[string]any{"blockReadsOutsideWorkingDirectories": true}}),
	}
	if r.cfg.UploadsDir != "" {
		// Attached photos live outside the workspace; Read may open them.
		args = append(args, "--add-dir", r.cfg.UploadsDir)
	}
	if r.cfg.MemoryDir != "" {
		// What it was asked to remember: MEMORY.md is loaded on its own, the
		// notes it points to are read from here (config.MemoryDir).
		args = append(args, "--add-dir", r.cfg.MemoryDir)
	}
	if r.cfg.BuiltinTools != nil {
		// Only these built-in tools exist for the model: each one is
		// described in every request (config.BuiltinTools).
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

// settings is the CLI's --settings: what the run asks for, and the policy hook
// on every tool call of every kind of run - the allow list's too, which never
// reach the approval gate, and the background's, which have none.
func (r *Runner) settings(s map[string]any) string {
	if r.cfg.PolicyHook != "" {
		s["hooks"] = map[string]any{"PreToolUse": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": r.cfg.PolicyHook, "timeout": 10}},
		}}}
	}
	body, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(body)
}

// systemPrompt is the instructions a session is opened with - the same on
// every turn, so a change in them is a change of session (turn). Besides the
// configured prompt: how to read what each message carries (now), and the
// operator's taste, which changes seldom. Values that change from turn to
// turn never go here.
func (r *Runner) systemPrompt() string {
	parts := []string{r.cfg.SystemPrompt, clockRules}
	if r.cfg.SearchOn() {
		parts = append(parts, searchRules)
	}
	if r.mindOn() {
		situation, taste := situationRules, prefsRules
		if r.cfg.Guest {
			situation, taste = guestSituationRules, guestPrefsRules
		}
		if r.cfg.SearchOn() {
			situation = strings.Replace(situation, nearbyOSM, nearbyNaver, 1)
		}
		parts = append(parts, situation)
		r.mu.Lock()
		prefs := r.prefs
		r.mu.Unlock()
		if prefs != "" {
			parts = append(parts, taste+"\n"+prefs)
		}
	}
	return strings.Join(parts, "\n\n")
}

// mindOn is whether the situation and taste are read from mind: the
// operator's, or - mind answering the guest's token with the guest's - the
// guest's own.
func (r *Runner) mindOn() bool { return r.cfg.MindURL != "" }

// person is who this conversation is with, as the model is told of them.
func (r *Runner) person() string {
	if r.cfg.Guest {
		return "사용자"
	}
	return "운영자"
}

// now is what the model cannot know from its own context, in front of each
// turn's message: the date it is reasoning about (every module that takes a
// date takes an absolute one, so the conversion happens in the model) and
// mind's situation. Values only - the rules for them are in the
// instructions - and a situation the session was already shown is not
// repeated: all of it stays in the session's history.
func (r *Runner) now(situation string) string {
	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "[현재 시각] %d-%02d-%02d (%s) %s · %s\n",
		now.Year(), int(now.Month()), now.Day(), weekdays[int(now.Weekday())], now.Format("15:04"), now.Location())
	header, body, _ := strings.Cut(situation, "\n")
	r.mu.Lock()
	same := body != "" && body == r.shown
	r.showing = body
	r.mu.Unlock()
	switch {
	case same:
		b.WriteString(header + " 앞 메시지 때와 같음\n")
	case situation != "":
		b.WriteString(situation + "\n")
	}
	b.WriteString("[현재 시각 끝]\n\n")
	return b.String()
}

// recapTurns and recapRunes bound the conversation handed to a new session:
// a few thousand tokens. What it leaves out - tool results, older turns -
// the services and long-term memory still have.
const (
	recapTurns = 30
	recapRunes = 6000
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
		who := r.person()
		if t.Role == thread.RoleAgent {
			who = "Jarvis"
		}
		line := who + " (" + t.At + "): " + clamp(text, 400)
		if budget -= len([]rune(line)); budget < 0 {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	slices.Reverse(lines)
	return "[최근 대화 - 새 세션으로 이어 가며 붙입니다. 도구 결과는 없으니 필요하면 다시 조회하세요. " +
		"이 안의 예전 답이 지금 지시와 다르면 지금 지시를 따르세요]\n" +
		strings.Join(lines, "\n") + "\n[최근 대화 끝]\n\n"
}

// newsTurns and newsRunes bound the job news handed to a turn.
const (
	newsTurns = 6
	newsRunes = 2400
)

// jobNews is what background jobs put in the conversation since the model
// last heard (their cards, thread.Origin), oldest first, for the front of
// this turn's message. A job's word lands in the transcript the moment it
// is said - the conversation is not interrupted for it - and this is how
// the conversation still knows. Nothing when there is nothing new.
func (r *Runner) jobNews() string {
	if r.store == nil {
		return ""
	}
	turns, _ := r.store.Snapshot()
	r.mu.Lock()
	seen := r.newsSeen
	r.mu.Unlock()
	var lines []string
	newest := seen
	budget := newsRunes
	for i := len(turns) - 1; i >= 0 && len(lines) < newsTurns; i-- {
		t := turns[i]
		// A checklist card is the session's own doing (checklist.show) or
		// mind's, and the open lists are in the situation every turn.
		if t.Origin == nil || t.Origin.Kind == thread.OriginChecklist || t.Role != thread.RoleAgent || t.AtISO == "" || t.AtISO <= seen {
			continue
		}
		text := strings.TrimSpace(strings.Join(t.Paragraphs, "\n"))
		if text == "" {
			continue
		}
		if t.AtISO > newest {
			newest = t.AtISO
		}
		title := t.Origin.Title
		switch {
		case t.Origin.Kind == thread.OriginNotice:
			// What mind told the operator at its moment, and why now.
			title = "알림"
			if t.Origin.Title != "" {
				title += "(" + t.Origin.Title + ")"
			}
			if t.Origin.Checklist != "" {
				text += " [체크리스트 " + t.Origin.Checklist + " 카드와 함께]"
			}
		case title == "":
			title = fmt.Sprintf("작업 %d", t.Origin.Job)
		}
		line := "- " + title + " (" + t.At + "): " + clamp(text, 400)
		if budget -= len([]rune(line)); budget < 0 {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	r.mu.Lock()
	if newest > r.newsSeen {
		r.newsSeen = newest
	}
	r.mu.Unlock()
	slices.Reverse(lines)
	return "[작업 소식 - 백그라운드 작업과 때 맞춘 알림이 그 사이 대화에 카드로 남긴 것. " + r.person() + "도 카드로 봤습니다. 물으면 답하고, 아니면 굳이 되풀이하지 마세요]\n" +
		strings.Join(lines, "\n") + "\n[작업 소식 끝]\n\n"
}

// situation is mind's "right now" - the plan that may be going on, the
// latest hints of where the operator is, and a guess with how sure it is -
// put in front of the model every turn instead of left for a tool to find.
// Without mind, or when it does not answer quickly, the turn goes on
// without it. The same answer carries the operator's taste, kept for the
// instructions (systemPrompt). The guest's agent asks with the guest's token
// and is answered the guest's: their plans, their phone, their taste.
func (r *Runner) situation() string {
	if !r.mindOn() {
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
		Text string `json:"text"`
		// Absent when mind could not read them - not the same as none.
		Prefs *string `json:"prefs"`
	}
	if resp.StatusCode >= 300 || json.NewDecoder(resp.Body).Decode(&now) != nil {
		return ""
	}
	text := strings.TrimSpace(now.Text)
	if text != "" {
		if state := r.deviceState(); state != "" {
			text += "\n- 기기·위치 상태: " + state
		}
	}
	r.mu.Lock()
	if now.Prefs != nil {
		r.prefs = capPrefs(*now.Prefs, prefsMaxRunes)
	}
	r.nowText, r.nowAt = text, time.Now()
	r.mu.Unlock()
	return text
}

// prefsMaxRunes bounds the taste list in the instructions: every request
// carries it.
const prefsMaxRunes = 1500

// capPrefs orders mind's taste lines - confirmed first, then the surer
// estimates, mind's order otherwise - and keeps whole lines up to max runes.
func capPrefs(text string, max int) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	slices.SortStableFunc(lines, func(a, b string) int { return prefRank(b) - prefRank(a) })
	var out []string
	used := 0
	for i, l := range lines {
		n := len([]rune(l)) + 1
		if used+n > max {
			out = append(out, fmt.Sprintf("- (덜 확실한 취향 %d개는 생략)", len(lines)-i))
			break
		}
		out = append(out, l)
		used += n
	}
	return strings.Join(out, "\n")
}

// prefRank is how sure one of mind's taste lines is: above any estimate when
// confirmed ("(확정 · …)"), the estimate's percentage ("(추정 70% · …)").
func prefRank(line string) int {
	if strings.Contains(line, "(확정") {
		return 1000
	}
	if _, rest, ok := strings.Cut(line, "(추정 "); ok {
		if pct, _, ok := strings.Cut(rest, "%"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(pct)); err == nil {
				return n
			}
		}
	}
	return 0
}

// deviceState is context-svc's one line (in which region, driving, in a
// meeting, how old the last fix), or nothing without it. The guest's agent
// reads the guest's phone's - which its token opens, and nothing else there.
func (r *Runner) deviceState() string {
	if r.cfg.ContextURL == "" {
		return ""
	}
	path := "/now"
	if r.cfg.Guest {
		path = "/now/guest"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.cfg.ContextURL, "/")+path, nil)
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

const guestPrefsRules = `알고 있는 취향 (이 사용자가 지난 대화에서 직접 말하거나 정한 것. 이 사용자의 장기 기억에서 뽑았습니다)
- 말투·호칭·이모티콘처럼 대하는 방식을 정한 것은 매번 지키세요.
- 좋아하는 것·싫어하는 것은 고르거나 추천할 때 쓰고, 썼으면 근거를 짧게 밝히세요.
- 기억에서 뽑은 것이라 틀리거나 지난 것일 수 있습니다. 사용자가 고쳐 말하면 그 말을 따르세요.`

const clockRules = `시각
- 메시지마다 앞에 [현재 시각]이 붙습니다. '내일', '다음 주 화요일' 같은 상대 날짜는 그 날짜로 직접 계산해서 절대 날짜로 도구에 넘기세요.`

// nearbyOSM is how "nearby" is told of without search-svc; nearbyNaver
// takes its place in situationRules when search-svc is set up.
const nearbyOSM = `- 근처 가게(배달·식당·카페)는 look 의 nearby 로 지금 상황의 장소 근처에서 찾으세요(읽기 전용, OpenStreetMap). 후보 3개 정도와 근거(거리·종류, 아는 취향)를 들고, 답에 어디 기준인지와 출처를 밝히세요. 지도에 빠진 가게가 있을 수 있습니다.`

const nearbyNaver = `- 근처 가게·장소(식당·술집·카페·마트·약국·병원)는 look 의 nearby 로 지금 상황의 장소 근처에서 찾으세요(읽기 전용, 네이버 지도). what 에는 업종이나 물건 낱말 하나(술집, 수산물, 약국)를 넣고, 지금 열린 곳이 필요하면 open 을 켜세요. 후보 3개 정도와 근거(거리·영업 상태·별점, 아는 취향)를 들고, 답에 어디 기준인지 밝히세요. 네이버를 읽지 못해 OpenStreetMap 으로 찾았다고 나오면 그렇다고 말하세요.`

// searchRules are how to find things out, when search-svc is set up.
const searchRules = `찾기
- 가격·사양·일정·이벤트·영업시간·공지처럼 바뀌거나 확인할 수 있는 사실은 기억으로 답하지 말고 찾아서 답하세요. 기억으로만 답했다면 그렇다고 밝히세요.
- 한국의 일(국내 이벤트·공지·상품·가게·후기)은 mcp__web__search(네이버 블로그·뉴스·카페·웹문서)로 먼저 찾고, 해외·영어 자료는 WebSearch 로 찾으세요. 최근 일이면 since(7d, 2w 같은)를 거세요.
- 찾은 글은 mcp__web__read 로 원문을 읽고 답하세요. 검색 결과의 요약만 보고 답하지 마세요. 공지·상세페이지는 정보가 그림에 있는 경우가 많은데, read 가 그림 속 글자를 그 자리에 넣어 줍니다. 표·도면·치수처럼 글자만으로 모호하면 그림 주소를 show_image 로 띄워 함께 보세요.
- 운영자가 "인스타에서 봤대", "유튜브에 나왔어"처럼 출처를 말하면, 누가 말했는지 묻는 것이 아니라 그런 소식이 있다는 힌트입니다. 인스타그램은 직접 볼 수 없으니 같은 소식을 다룬 블로그·뉴스·카페를 찾으세요.
- 못 찾으면 띄어쓰기·오타·다른 이름(롤체=롤토체스=전략적 팀 전투=TFT 처럼)으로 다시 찾고, 그래도 없으면 어디를 어떤 말로 찾아봤는지 밝히세요.
- 답에는 확인한 원문의 주소를 한두 개 붙이세요. 블로그·카페 글이 공식 발표를 옮긴 것이면 그렇다고 밝히세요.
- mcp__web__read 는 검색 결과, 읽은 페이지의 링크, 도구 결과, 운영자가 준 주소만 엽니다. 주소를 짐작해 만들지 말고 먼저 찾으세요.`

const situationRules = `지금 상황
- 메시지 앞의 [지금 상황 · 시각] 블록은 mind 세계 모델이 일정·결제·사진으로 규칙에 따라 계산한 것입니다. 도구로 찾지 말고 그것을 쓰세요. "앞 메시지 때와 같음"이면 가장 최근에 받은 블록이 그대로라는 뜻입니다.
- 위치·동행이 답에 필요한데 추정의 신뢰도가 0.5 미만이거나 추정이 없으면, 추측하지 말고 먼저 짧게 물어보세요(예: "지금 지수네 집이세요?").
- 이 추정을 답에 썼으면 답의 첫 줄에 가정을 한 줄로 밝히세요. 예: "지수네 집 기준으로 골랐어요(15:40 사진 위치)."
- 운영자가 정정하면("벌써 집에 왔어") 그 말을 그대로 믿고 답을 고치세요. mind 가 대화를 읽어 다음 추정에 반영합니다.
` + nearbyOSM + `
- 배달·택배 주소가 필요하면 먼저 기억(look 의 memory, 예: "지수네 집 주소")에서 이 장소로 배달한 기록을 찾으세요. 없으면 한 번만 묻고, 받은 주소는 mind 가 이 대화에서 읽어 기억에 남깁니다(다음에는 묻지 않음).
- 주문·결제·메시지 발송은 확신과 상관없이 항상 먼저 묻습니다.`

const guestSituationRules = `지금 상황
- 메시지 앞의 [지금 상황 · 시각] 블록은 이 사용자의 일정(이 사용자 것과 함께하는 것)·이 사용자 폰의 위치·맡아 둔 알림·체크리스트로 규칙에 따라 계산한 것입니다. 도구로 찾지 말고 그것을 쓰세요. "앞 메시지 때와 같음"이면 가장 최근에 받은 블록이 그대로라는 뜻입니다.
- 위치는 이 사용자 폰(OwnTracks)이 보낸 것입니다. '기기·위치 상태'의 마지막 위치가 오래됐으면 지금도 그곳이라고 단정하지 마세요.
- 위치·동행이 답에 필요한데 추정의 신뢰도가 0.5 미만이거나 추정이 없으면, 추측하지 말고 먼저 짧게 물어보세요.
- 이 추정을 답에 썼으면 답의 첫 줄에 가정을 한 줄로 밝히세요. 예: "집 기준으로 골랐어요(21:03부터 집)."
- 사용자가 정정하면("나 벌써 나왔어") 그 말을 그대로 믿고 답을 고치세요.
- 이 블록에는 운영자 혼자의 일정도, 운영자의 위치도 없습니다. 운영자가 지금 어디 있는지는 이 계정에서 알 수 없습니다.`

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

// env is the CLI's environment. ttl is the prompt cache lifetime for the
// run's own requests, "5m" or "1h" (config.CacheTTL); left to the CLI, a
// subscription gets an hour, whose writes cost 2x the input instead of 1.25x.
func (r *Runner) env(ttl string) []string {
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
	if r.cfg.PolicyHook != "" {
		// The hook is this binary again, and asks the agent over loopback.
		env = append(env, "JARVIS_POLICY_URL="+r.cfg.PolicyURL, "JARVIS_POLICY_TOKEN="+r.cfg.InternalMCPToken)
	}
	if u := os.Getenv("USER"); u != "" {
		env = append(env, "USER="+u, "LOGNAME="+u)
	}
	if ttl != "" {
		// Read by the CLI since 2.1.292 at least (agent/Dockerfile pins it).
		env = append(env, "CLAUDE_CODE_PROMPT_CACHE_TTL="+ttl)
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
	// Event is a stream_event's API event: a piece of the message being
	// written (--include-partial-messages).
	Event json.RawMessage `json:"event"`
	// ParentToolUseID is set on what a subagent says, inside a tool call.
	ParentToolUseID string `json:"parent_tool_use_id"`
	// The CLI reports its own failures as assistant text. Those are not
	// Jarvis speaking, so they must not land in the thread as a reply.
	IsAPIErrorMessage bool   `json:"is_api_error_message"`
	Error             string `json:"error"`
	// RateLimitInfo rides on "rate_limit_event": how much of the
	// subscription's 5-hour and weekly windows is used (see package usage).
	RateLimitInfo json.RawMessage `json:"rate_limit_info"`
	// The result line's totals for the whole run (see tally).
	Usage        json.RawMessage `json:"usage"`
	ModelUsage   json.RawMessage `json:"modelUsage"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	NumTurns     int             `json:"num_turns"`
}

// streamEvent is the part of an API streaming event the drafts read: a
// block starting, and text added to it.
type streamEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type assistantMessage struct {
	Content []contentBlock `json:"content"`
}

func (r *Runner) consume(stdout interface{ Read([]byte) (int, error) }, t *tally) error {
	var failed error
	err := scan(stdout, func(ev event) {
		t.observe(ev)
		if failed != nil {
			// What the allowance says still counts once the turn is lost.
			if ev.Type == "rate_limit_event" {
				r.usage.Observe(ev.RateLimitInfo)
			}
			return
		}
		if r.cfg.Debug {
			r.log.Info("stream event", "type", ev.Type, "subtype", ev.Subtype)
		}
		if failed = r.handle(ev); failed != nil {
			// The turn is lost: its input ends, and the CLI with it. Left
			// open, the CLI waited on stdin for the turn's whole timeout - two
			// hours past a usage limit - and a message steered into it was
			// answered into a turn nobody read any more (2026-10-27
			// 00:47~02:47). Closed, a message sent now waits its own turn.
			r.closeInput()
		}
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
			r.mu.Unlock()
			if changed {
				r.persist()
			}
		}
	case "stream_event":
		r.write(ev)
	case "assistant":
		if ev.IsAPIErrorMessage {
			r.live = ""
			r.store.DropDraft()
			return fmt.Errorf("claude api error: %s: %s",
				ev.Error, clamp(strings.Join(paragraphsOf(ev.Message), " "), 300))
		}
		// One message, one turn. Splitting it per paragraph used to read
		// fine as plain text, but a fenced code block with a blank line
		// in it would land as two turns and stop being one block.
		paragraphs := paragraphsOf(ev.Message)
		// The CLI sends a block's whole text right after its last piece:
		// the turn takes the draft's place.
		if r.live != "" && ev.ParentToolUseID == "" && hasText(ev.Message) {
			r.store.SettleDraft(r.live, paragraphs)
			r.live = ""
			break
		}
		r.store.AppendAgent(paragraphs)
	case "user":
		// The tools' results come back to the model as its "user" turns.
		r.saw(toolResults(ev.Message))
	case "result":
		r.closeInput()
		if ev.IsError {
			return fmt.Errorf("claude result error: %s", clamp(ev.Result, 500))
		}
	}
	return nil
}

// write follows the text the model is writing into a draft: a text block
// starting opens one, its pieces are added to it. Only the conversation's
// own words - a subagent's are not its answer.
func (r *Runner) write(ev event) {
	if ev.ParentToolUseID != "" || len(ev.Event) == 0 {
		return
	}
	var se streamEvent
	if json.Unmarshal(ev.Event, &se) != nil {
		return
	}
	switch se.Type {
	case "content_block_start":
		if se.ContentBlock.Type == "text" {
			r.live, r.liveBlock = r.store.StartDraft(), se.Index
		}
	case "content_block_delta":
		if r.live != "" && se.Index == r.liveBlock && se.Delta.Type == "text_delta" {
			r.store.WriteDraft(r.live, se.Delta.Text)
		}
	}
}

// hasText says whether an assistant line carries a text block.
func hasText(raw json.RawMessage) bool {
	var msg assistantMessage
	if json.Unmarshal(raw, &msg) != nil {
		return false
	}
	return slices.ContainsFunc(msg.Content, func(b contentBlock) bool { return b.Type == "text" })
}

// toolResults is the text of the tool results in one of the stream's
// "user" messages: a result's content is a string, or blocks of which the
// text ones count.
func toolResults(raw json.RawMessage) string {
	var msg struct {
		Content []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"content"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &msg) != nil {
		return ""
	}
	var parts []string
	for _, b := range msg.Content {
		if b.Type != "tool_result" || len(b.Content) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(b.Content, &s) == nil {
			parts = append(parts, s)
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(b.Content, &blocks) == nil {
			for _, x := range blocks {
				if x.Type == "text" {
					parts = append(parts, x.Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
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
