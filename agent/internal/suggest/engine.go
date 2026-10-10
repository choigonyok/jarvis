// Package suggest lets the assistant speak first.
//
// events-svc delivers the event types the assistant subscribed to. Each one
// passes two cheap gates before any model is asked anything: a rule that
// says what the event could be a suggestion about (its topic), and the
// ledger, which says whether that topic was raised or turned down recently
// and whether today's suggestions are used up. What passes is judged in a
// background turn - read-only tools plus one tool, propose - and most of the
// time the right answer is to say nothing. A suggestion is an approval card
// (origin suggest): approving runs the action it carries, if it carries one.
package suggest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/notify"
)

// ServerName is the MCP server the judging turn proposes through.
const ServerName = "suggest"

// Types is what the assistant subscribes to - the events a rule below knows
// how to turn into a topic.
var Types = []string{"assets.band", "memory.plan"}

// Event is one event as events-svc delivers it.
type Event struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject"`
	Data       json.RawMessage `json:"data"`
	Key        string          `json:"key"`
	OccurredAt time.Time       `json:"occurredAt"`
	Notify     *struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	} `json:"notify"`
}

type Background interface {
	Background(ctx context.Context, t claudecode.Task) (string, error)
}

type Config struct {
	// MCPBase is where this agent's MCP servers listen, for the judging turn.
	MCPBase   string
	EventsURL string
	Token     string
	// Out is where suggestions are reported (the events outbox). Nil posts
	// straight to EventsURL.
	Out *notify.Client
}

type Engine struct {
	ledger    *Ledger
	proposals *proposal.Store
	modules   *module.Registry
	bg        Background
	cfg       Config
	log       *slog.Logger
	http      *http.Client
	now       func() time.Time

	queue chan job
	mu    sync.Mutex
	cur   *job
}

type job struct {
	ev       Event
	topic    string
	brief    string
	tries    int
	proposed bool
}

func New(l *Ledger, p *proposal.Store, m *module.Registry, bg Background, cfg Config, log *slog.Logger) *Engine {
	e := &Engine{
		ledger: l, proposals: p, modules: m, bg: bg, cfg: cfg, log: log,
		http: &http.Client{Timeout: 10 * time.Second}, now: time.Now,
		queue: make(chan job, 32),
	}
	if e.cfg.Out == nil {
		e.cfg.Out = notify.New(cfg.EventsURL, "", cfg.Token, log)
	}
	return e
}

// --- gate 1: what could this be a suggestion about? -------------------------

// triage turns an event into a topic and a one-line brief for the judging
// turn, or rejects it. Rules only - nothing here costs a model call.
func triage(ev Event) (topic, brief string, ok bool) {
	switch ev.Type {
	case "assets.band":
		brief = "자산 비중이 목표 허용 범위를 벗어났다."
		if ev.Notify != nil {
			brief += " " + ev.Notify.Title + " - " + ev.Notify.Body
		}
		// One topic for the whole allocation: a rebalance is one plan, not one per bucket.
		return "rebalance", brief, true
	case "memory.plan":
		var d struct {
			Fact    string `json:"fact"`
			ValidAt string `json:"validAt"`
			Source  string `json:"source"`
		}
		if json.Unmarshal(ev.Data, &d) != nil || strings.TrimSpace(d.Fact) == "" {
			return "", "", false
		}
		sum := sha256.Sum256([]byte(d.Fact))
		brief = fmt.Sprintf("%s에서 약속·계획이 나왔다: %q", d.Source, d.Fact)
		if d.ValidAt != "" {
			brief += " (기억이 붙인 때: " + d.ValidAt + ")"
		}
		return "plan:" + hex.EncodeToString(sum[:6]), brief, true
	}
	return "", "", false
}

// Consume is POST /consume: events-svc delivering. It answers at once - the
// judging happens later, in order - and a full queue drops the event rather
// than holding events-svc back (it is a suggestion, not a duty).
func (e *Engine) Consume(w http.ResponseWriter, r *http.Request) {
	var ev Event
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&ev); err != nil {
		http.Error(w, "JSON 을 읽지 못했습니다", http.StatusBadRequest)
		return
	}
	topic, brief, ok := triage(ev)
	if ok {
		// gate 2: has this been raised or turned down lately?
		if allowed, why := e.ledger.Allowed(topic, e.now()); !allowed {
			e.log.Info("제안하지 않습니다", "topic", topic, "why", why)
			ok = false
		}
	}
	if ok {
		select {
		case e.queue <- job{ev: ev, topic: topic, brief: brief}:
		default:
			e.log.Warn("제안 대기열이 가득 차 이벤트를 건너뜁니다", "type", ev.Type)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Run judges queued events one at a time. A turn that had to give way to
// the operator speaking is tried again a little later.
func (e *Engine) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-e.queue:
			// Raised by an earlier job in the meantime?
			if allowed, _ := e.ledger.Allowed(j.topic, e.now()); !allowed {
				continue
			}
			err := e.judge(ctx, &j)
			switch {
			case errors.Is(err, claudecode.ErrBusy), errors.Is(err, claudecode.ErrYielded):
				if j.tries++; j.tries < 6 {
					go func(j job) {
						select {
						case <-ctx.Done():
						case <-time.After(3 * time.Minute):
							select {
							case e.queue <- j:
							default:
							}
						}
					}(j)
				}
			case err != nil:
				e.log.Warn("제안을 판단하지 못했습니다", "topic", j.topic, "err", err)
			}
		}
	}
}

const system = `너는 jarvis 비서다. 지금은 대화가 아니라, 방금 일어난 일 하나를 보고 운영자에게 먼저 제안할 가치가 있는지 판단하는 시간이다.

- 대부분은 제안하지 않는 게 맞다. 운영자가 이미 알고 있거나, 이미 처리됐거나, 손댈 게 없거나, 최근에 반려한 것과 비슷하면 제안하지 않는다.
- 판단 전에 도구로 사실을 확인한다: 일정은 mcp__calendar__list_events, 자산 비중과 보유는 mcp__assets__get_allocation·get_portfolio, 지난 맥락은 mcp__memory__search_memory.
- 제안한다면 mcp__suggest__propose 를 정확히 한 번 호출한다. 제안하지 않는다면 아무 도구도 부르지 말고 이유를 한 줄 쓰고 끝낸다.
- 실행까지 할 수 있는 건 캘린더 일정 추가(action "calendar.create_event")뿐이다. 매매·이체는 실행하지 않는다 - action "none" 으로 무엇을 얼마나 할지 계획만 제안한다.
- 카드 제목은 한 줄, 본문은 무엇을 왜, 숫자와 근거로 짧게. 추측은 추측이라고 쓴다.
- confidence 는 이 제안이 운영자에게 실제로 도움이 될 가능성(0~1)이다. 0.6 미만이면 카드 대신 저녁 요약에 한 줄로 남는다.`

func (e *Engine) judge(ctx context.Context, j *job) error {
	e.mu.Lock()
	e.cur = j
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.cur = nil
		e.mu.Unlock()
	}()

	var past strings.Builder
	prefix, _, _ := strings.Cut(j.topic, ":")
	for _, en := range e.ledger.Recent(prefix, 5) {
		decision := en.Decision
		if decision == "" {
			decision = "대기 중"
		}
		fmt.Fprintf(&past, "- %s %s (%s)\n", en.At.Format("01/02"), en.Title, decision)
	}
	if past.Len() == 0 {
		past.WriteString("- 없음\n")
	}
	prompt := fmt.Sprintf("지금: %s\n\n일어난 일: %s\n이벤트 종류: %s\n이벤트 데이터: %s\n\n같은 종류로 최근에 한 제안:\n%s\n"+
		"캘린더 일정 추가를 제안할 때 action_input 은 {\"date\":\"YYYY-MM-DD\",\"title\":\"...\",\"start\":\"HH:MM\",\"place\":\"...\",\"shared\":true/false} 모양이다.",
		e.now().Format("2006-01-02 15:04 (Mon)"), j.brief, j.ev.Type, string(j.ev.Data), past.String())

	base := strings.TrimRight(e.cfg.MCPBase, "/")
	_, err := e.bg.Background(ctx, claudecode.Task{
		Name:   "제안 판단 " + j.topic,
		Prompt: prompt,
		System: system,
		Servers: map[string]string{
			ServerName: base + "/" + ServerName,
			"calendar": base + "/calendar",
			"assets":   base + "/assets",
			"memory":   base + "/memory",
		},
		Allowed: []string{
			"mcp__suggest__propose",
			"mcp__calendar__list_events",
			"mcp__assets__get_allocation",
			"mcp__assets__get_portfolio",
			"mcp__memory__search_memory",
		},
		Timeout: 4 * time.Minute,
	})
	return err
}

// --- the one tool the judging turn can change anything with -----------------

type ProposeInput struct {
	Title       string          `json:"title" jsonschema:"카드 제목. 한 줄."`
	Body        string          `json:"body" jsonschema:"무엇을 하자는지, 숫자와 근거로 짧게."`
	Why         string          `json:"why" jsonschema:"왜 지금 이걸 제안하는지 한두 문장."`
	Action      string          `json:"action" jsonschema:"승인 시 실행할 것: calendar.create_event, 또는 실행 없이 계획만이면 none."`
	ActionInput json.RawMessage `json:"action_input,omitempty" jsonschema:"action 의 입력. calendar.create_event 면 {date,title,start,place,shared}. none 이면 비운다."`
	Confidence  float64         `json:"confidence" jsonschema:"운영자에게 실제로 도움이 될 가능성, 0~1."`
}

// Executable is what a suggestion may carry to run on approval.
var Executable = map[string]bool{"calendar.create_event": true}

func (e *Engine) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "propose",
		Description: "운영자에게 제안 카드를 올린다. 한 번의 판단에서 한 번만 부른다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProposeInput) (*mcp.CallToolResult, any, error) {
		msg, err := e.propose(ctx, in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

func (e *Engine) propose(ctx context.Context, in ProposeInput) (string, error) {
	e.mu.Lock()
	j := e.cur
	if j != nil && j.proposed {
		e.mu.Unlock()
		return "", errors.New("이번 판단에서는 이미 제안했습니다")
	}
	if j != nil {
		j.proposed = true
	}
	e.mu.Unlock()
	if j == nil {
		return "", errors.New("판단 중인 일이 없습니다")
	}
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	if in.Title == "" || in.Body == "" {
		return "", errors.New("title 과 body 가 필요합니다")
	}
	kind := "suggest.note"
	// What the action itself will do, as its module shows it on any card -
	// rendering it is also how a malformed input is caught before a card exists.
	var preview action.Card
	if in.Action != "" && in.Action != "none" {
		if !Executable[in.Action] {
			return "", fmt.Errorf("%s 는 제안으로 실행할 수 없습니다. 계획만이면 action 을 none 으로", in.Action)
		}
		actuator, _, ok := e.modules.Lookup(in.Action)
		if !ok {
			return "", fmt.Errorf("%s 를 실행할 모듈이 없습니다", in.Action)
		}
		if !json.Valid(in.ActionInput) {
			return "", errors.New("action_input 이 JSON 이 아닙니다")
		}
		var err error
		if preview, err = actuator.Preview(ctx, action.Action{Kind: in.Action, Input: in.ActionInput}); err != nil {
			return "", fmt.Errorf("action_input 을 확인하세요: %w", err)
		}
		kind = in.Action
	}
	now := e.now()

	// Not sure enough for a card: a line in tonight's digest instead.
	if in.Confidence < 0.6 {
		e.ledger.Record(j.topic, "", in.Title, now)
		e.emit(map[string]any{
			"source": "agent", "type": "suggestion.note", "subject": j.topic,
			"data":   map[string]any{"topic": j.topic, "confidence": in.Confidence},
			"notify": map[string]any{"tier": "digest", "title": "제안: " + in.Title, "body": in.Body, "url": "/"},
			"key":    fmt.Sprintf("suggestion:%s:%s", j.topic, now.Format("20060102")),
		})
		return "확신이 낮아 카드 대신 저녁 요약에 한 줄로 남겼습니다.", nil
	}

	consequence := "승인하면 운영자가 확인한 것으로 기록만 합니다. 실행되는 것은 없습니다."
	if kind != "suggest.note" {
		consequence = "승인하면 바로 실행합니다."
	}
	p, _ := e.proposals.Open(proposal.Proposal{
		Origin:     proposal.OriginSuggest,
		Action:     action.Action{Kind: kind, Input: in.ActionInput},
		Card:       action.Card{Title: in.Title, Body: cardBody(in, preview), Consequence: consequence},
		Confidence: in.Confidence,
	})
	e.ledger.Record(j.topic, p.ID, in.Title, now)
	supersedes := []string{}
	if j.ev.Key != "" {
		supersedes = append(supersedes, j.ev.Key)
	}
	e.emit(map[string]any{
		"source": "agent", "type": "suggestion.raised", "subject": p.ID,
		"data": map[string]any{"topic": j.topic, "proposalId": p.ID, "title": in.Title, "supersedes": supersedes},
		"key":  "suggestion:raised:" + p.ID,
	})
	return "카드를 올렸습니다.", nil
}

func cardBody(in ProposeInput, preview action.Card) string {
	body := in.Body
	if preview.Body != "" {
		body += "\n\n" + preview.Body
	}
	if why := strings.TrimSpace(in.Why); why != "" {
		body += "\n\n왜: " + why
	}
	return body
}

// OnDecide hears every decision; a suggestion's goes to the ledger and back
// out as an event (memory-svc will learn from it).
func (e *Engine) OnDecide(p proposal.Proposal, d proposal.Decision) {
	if p.Origin != proposal.OriginSuggest {
		return
	}
	en, ok := e.ledger.Decide(p.ID, string(d), e.now())
	if !ok {
		return
	}
	e.emit(map[string]any{
		"source": "agent", "type": "suggestion.decided", "subject": p.ID,
		"data": map[string]any{"topic": en.Topic, "proposalId": p.ID, "title": en.Title, "decision": string(d)},
		"key":  "suggestion:decided:" + p.ID,
	})
}

func (e *Engine) emit(ev map[string]any) { e.cfg.Out.Emit(ev) }

// Subscribe registers this agent with events-svc for Types, retrying until
// events-svc answers.
func (e *Engine) Subscribe(ctx context.Context, self string) {
	if e.cfg.EventsURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"url": self, "types": Types})
	for wait := time.Second; ; wait = min(wait*2, time.Minute) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(e.cfg.EventsURL, "/")+"/subscriptions/agent", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if e.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+e.cfg.Token)
		}
		if resp, err := e.http.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				e.log.Info("events-svc 에 제안용으로 구독했습니다", "types", Types)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
