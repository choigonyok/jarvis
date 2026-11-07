package convo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/assemble"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// Options is how scenarios are run.
type Options struct {
	// Skills is the workspace's skills folder (workspace/.claude/skills).
	Skills string
	// Claude is the CLI to run; "" is the configured one (claude).
	Claude string
	// Timeout bounds one scenario's turn.
	Timeout time.Duration
	// Log hears the runner; nil is quiet.
	Log *slog.Logger
}

// Outcome is one run of a scenario.
type Outcome struct {
	Scenario string     `json:"scenario"`
	File     string     `json:"file"`
	Pass     bool       `json:"pass"`
	Failures []string   `json:"failures,omitempty"`
	Calls    []Recorded `json:"calls"`
	Answer   string     `json:"answer"`
	Error    string     `json:"error,omitempty"`
	// What the operator waits through, from the turn's own log line.
	StartupMS   int64 `json:"startupMs"`
	FirstTextMS int64 `json:"firstTextMs"`
	TotalMS     int64 `json:"totalMs"`
}

// env is what the run's configuration is read from: config.Load reads the
// environment, so the run sets it as the agent's compose file would and puts
// it back after.
type env map[string]string

func (e env) apply() (restore func()) {
	old := map[string]*string{}
	for k, v := range e {
		if prev, ok := os.LookupEnv(k); ok {
			old[k] = &prev
		} else {
			old[k] = nil
		}
		os.Setenv(k, v)
	}
	return func() {
		for k, v := range old {
			if v == nil {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, *v)
			}
		}
	}
}

// running keeps one scenario at a time: a run sets the process's environment.
var running sync.Mutex

// Run sends the scenario's message to the conversation and grades what came
// of it.
func Run(ctx context.Context, opt Options, sc Scenario) Outcome {
	running.Lock()
	defer running.Unlock()
	out := Outcome{Scenario: sc.Name, File: sc.File}
	calls, answer, timing, err := run(ctx, opt, sc)
	out.Calls, out.Answer = calls, answer
	out.StartupMS, out.FirstTextMS, out.TotalMS = timing.startup, timing.first, timing.total
	if err != nil {
		out.Error = err.Error()
		out.Failures = append(out.Failures, "실행 실패: "+err.Error())
		return out
	}
	out.Failures = append(out.Failures, Grade(sc, calls, answer, time.Now())...)
	out.Pass = len(out.Failures) == 0
	return out
}

type timing struct{ startup, first, total int64 }

func run(ctx context.Context, opt Options, sc Scenario) ([]Recorded, string, timing, error) {
	var t timing
	tmp, err := os.MkdirTemp("", "convo-")
	if err != nil {
		return nil, "", t, err
	}
	defer os.RemoveAll(tmp)
	dirs := map[string]string{}
	for _, d := range []string{"home", "workspace", "uploads", "data"} {
		dirs[d] = filepath.Join(tmp, d)
		if err := os.MkdirAll(dirs[d], 0o755); err != nil {
			return nil, "", t, err
		}
	}
	if opt.Skills != "" {
		if err := copyDir(opt.Skills, filepath.Join(dirs["workspace"], ".claude", "skills")); err != nil {
			return nil, "", t, fmt.Errorf("스킬을 옮기지 못했습니다: %w", err)
		}
	}

	transcript := &turns{}
	now := time.Now()
	for _, l := range sc.Before {
		ago, err := time.ParseDuration(l.Ago)
		if err != nil || ago <= 0 {
			ago = 10 * time.Minute
		}
		tr := thread.Turn{Role: thread.RoleUser, Text: l.Text}
		if l.Who == "Jarvis" {
			tr = thread.Turn{Role: thread.RoleAgent, Paragraphs: strings.Split(l.Text, "\n\n")}
		}
		transcript.add(tr, now.Add(-ago))
	}
	input := httptest.NewServer(transcript.handler())
	defer input.Close()
	mind := httptest.NewServer(mindFake(sc))
	defer mind.Close()

	// One server for every MCP endpoint, at the paths the agent mounts them.
	mux := http.NewServeMux()
	mcpSrv := httptest.NewServer(mux)
	defer mcpSrv.Close()
	const dead = "http://127.0.0.1:1"
	restore := env{
		"JARVIS_WORKSPACE":      dirs["workspace"],
		"JARVIS_HOME":           dirs["home"],
		"JARVIS_UPLOADS_DIR":    dirs["uploads"],
		"JARVIS_PROPOSALS_PATH": filepath.Join(dirs["data"], "proposals.json"),
		"JARVIS_SESSION_PATH":   filepath.Join(dirs["data"], "session.json"),
		"JARVIS_CHAT_URL":       input.URL,
		"JARVIS_MIND_URL":       mind.URL,
		"JARVIS_CONTEXT_URL":    mind.URL,
		"JARVIS_MEMORY_URL":     mind.URL,
		"JARVIS_MCP_URL":        mcpSrv.URL + "/mcp",
		"JARVIS_SEARCH_URL":     dead,
		"JARVIS_SEARCH_TOKEN":   "convo",
		"JARVIS_VAULT_URL":      dead,
		"JARVIS_PHOTOS_URL":     dead,
		"JARVIS_CALENDAR_URL":   dead,
		"JARVIS_ASSETS_URL":     dead,
		"JARVIS_EXTRA_MCP":      jobs.BrowserServer + "=" + mcpSrv.URL + "/browser",
		"JARVIS_MCP_TOKEN":      "convo",
		"JARVIS_API_TOKEN":      "convo",
		"JARVIS_ALLOWED_ORIGIN": "",
		// Nothing to ask: the policy hook calls a running agent, and this one
		// only answers from the scenario.
		"JARVIS_POLICY_HOOK": "off",
		"JARVIS_ROLE":        "",
		"JARVIS_JOBS":        "true",
		"JARVIS_DEBUG":       "false",
		// Nothing leaves the run: no pushes, no records, no stream read.
		"JARVIS_NOTIFY_URL":         "",
		"JARVIS_DATABASE_URL":       "",
		"JARVIS_QUEUE_DATABASE_URL": "",
	}.apply()
	defer restore()
	cfg, err := config.Load()
	if err != nil {
		return nil, "", t, err
	}
	cfg.DatabaseURL, cfg.QueueURL, cfg.NotifyURL = "", "", ""
	if opt.Claude != "" {
		cfg.ClaudeBin = opt.Claude
	}
	if opt.Timeout > 0 {
		cfg.TurnTimeout = opt.Timeout
	}

	log, clock := capture(opt.Log)
	answer := func(key string) string {
		if a, ok := sc.Answers[key]; ok {
			return a
		}
		return standIn(key)
	}
	var mu sync.Mutex
	var calls []Recorded
	waiting := sc.Waiting
	// The agent itself, put together as cmd/agent puts it together; what the
	// run fakes is handed in, and nothing of it is started.
	agent, err := wire(ctx, &cfg, assemble.Deps{
		Log: log,
		Tools: func(name string, input json.RawMessage) {
			mu.Lock()
			calls = append(calls, Recorded{Name: name, Input: append(json.RawMessage(nil), input...)})
			mu.Unlock()
		},
		Waiting: func() []string { return waiting },
	}, mux, answer)
	if err != nil {
		return nil, "", t, err
	}
	runner := agent.Runner

	before := transcript.count()
	if sc.Prompt != "" {
		runner.Enqueue(sc.Prompt)
	} else if _, err := runner.Submit(sc.Say, nil, nil); err != nil {
		return nil, "", t, err
	}
	deadline := time.Now().Add(cfg.TurnTimeout + 30*time.Second)
	for !runner.Idle() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return calls, transcript.answerAfter(before), t, errors.New("턴이 끝나지 않았습니다")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t = clock()
	mu.Lock()
	defer mu.Unlock()
	return calls, transcript.answerAfter(before), t, nil
}

// standIn is what a tool gives back when the scenario does not say: looking
// finds nothing, keeping something keeps it.
func standIn(key string) string {
	switch {
	case strings.HasPrefix(key, "look:"):
		return "찾은 것이 없습니다."
	case strings.HasPrefix(key, "change:"):
		return "남겼습니다."
	case key == "create_job":
		return "작업 1 을 만들었습니다."
	case key == "instruct_job":
		return "작업에 전했습니다."
	case key == "search":
		return "검색 결과가 없습니다."
	case key == "read":
		return "페이지를 읽지 못했습니다."
	case key == "show_image":
		return "대화에 띄웠습니다."
	case strings.HasPrefix(key, "browser_"):
		return "브라우저가 응답하지 않습니다."
	}
	return "됐습니다."
}

// capture is a logger that also keeps the turn's timing off its "턴 사용량"
// line, and what it kept.
func capture(next *slog.Logger) (*slog.Logger, func() timing) {
	if next == nil {
		next = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &timingHandler{next: next.Handler(), t: &timingBox{}}
	return slog.New(h), func() timing {
		h.t.mu.Lock()
		defer h.t.mu.Unlock()
		return h.t.v
	}
}

type timingBox struct {
	mu sync.Mutex
	v  timing
}

type timingHandler struct {
	next slog.Handler
	t    *timingBox
}

func (h *timingHandler) Enabled(ctx context.Context, l slog.Level) bool { return true }
func (h *timingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "턴 사용량" {
		h.t.mu.Lock()
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "startup_ms":
				h.t.v.startup = a.Value.Int64()
			case "first_text_ms":
				h.t.v.first = a.Value.Int64()
			case "total_ms":
				h.t.v.total = a.Value.Int64()
			}
			return true
		})
		h.t.mu.Unlock()
	}
	if h.next.Enabled(ctx, r.Level) {
		return h.next.Handle(ctx, r)
	}
	return nil
}
func (h *timingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &timingHandler{next: h.next.WithAttrs(as), t: h.t}
}
func (h *timingHandler) WithGroup(name string) slog.Handler {
	return &timingHandler{next: h.next.WithGroup(name), t: h.t}
}

// copyDir copies a folder of plain files.
func copyDir(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}
