package suggest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// fakeBG stands in for the judging turn: it proposes what it is told.
type fakeBG struct {
	e     *Engine
	input *ProposeInput
	tasks []claudecode.Task
	reply string
	err   error
}

func (f *fakeBG) Background(_ context.Context, t claudecode.Task) (string, error) {
	f.tasks = append(f.tasks, t)
	if f.input != nil {
		f.reply, f.err = f.e.propose(context.Background(), *f.input)
	}
	return "", nil
}

type calendarActuator struct{}

func (calendarActuator) Name() string                { return "calendar" }
func (calendarActuator) Start(context.Context) error { return nil }
func (calendarActuator) Stop(context.Context) error  { return nil }
func (calendarActuator) Specs() []action.Spec {
	return []action.Spec{{Kind: "calendar.create_event", Reversible: true}}
}
func (calendarActuator) Preview(_ context.Context, a action.Action) (action.Card, error) {
	var in struct{ Date, Title string }
	if err := json.Unmarshal(a.Input, &in); err != nil || in.Date == "" {
		return action.Card{}, errors.New("date 가 필요합니다")
	}
	return action.Card{Title: "일정 추가", Body: in.Date + " " + in.Title}, nil
}
func (calendarActuator) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{Note: "추가함"}, nil
}

type sink struct {
	mu     sync.Mutex
	events []map[string]any
}

func setup(t *testing.T) (*Engine, *fakeBG, *proposal.Store, *sink) {
	t.Helper()
	sk := &sink{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		sk.mu.Lock()
		sk.events = append(sk.events, m)
		sk.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	reg := module.NewRegistry()
	reg.Add(calendarActuator{})
	props := proposal.NewStore(bus.New())
	ledger, _ := OpenLedger("")
	bg := &fakeBG{}
	e := New(ledger, props, reg, bg, Config{MCPBase: "http://127.0.0.1:8080/mcp", EventsURL: srv.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bg.e = e
	return e, bg, props, sk
}

func (s *sink) wait(t *testing.T, n int) []map[string]any {
	t.Helper()
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		if len(s.events) >= n {
			out := append([]map[string]any(nil), s.events...)
			s.mu.Unlock()
			return out
		}
		s.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("이벤트 %d개를 기다렸지만 오지 않았습니다", n)
	return nil
}

func TestTriage(t *testing.T) {
	if topic, _, ok := triage(Event{Type: "assets.band"}); !ok || topic != "rebalance" {
		t.Fatalf("assets.band: %q %v", topic, ok)
	}
	if _, _, ok := triage(Event{Type: "memory.plan", Data: json.RawMessage(`{"fact":""}`)}); ok {
		t.Fatal("빈 약속이 통과했습니다")
	}
	a, _, _ := triage(Event{Type: "memory.plan", Data: json.RawMessage(`{"fact":"은주와 토요일 저녁 약속"}`)})
	b, _, _ := triage(Event{Type: "memory.plan", Data: json.RawMessage(`{"fact":"은주와 토요일 저녁 약속"}`)})
	if !strings.HasPrefix(a, "plan:") || a != b {
		t.Fatalf("같은 약속은 같은 주제여야 합니다: %q %q", a, b)
	}
	if _, _, ok := triage(Event{Type: "spending.big"}); ok {
		t.Fatal("구독하지 않은 종류가 통과했습니다")
	}
}

func TestProposeCardThenQuietAndDecision(t *testing.T) {
	e, bg, props, sk := setup(t)
	bg.input = &ProposeInput{
		Title: "토요일 저녁 약속을 캘린더에", Body: "10/18 19:00 은주와 저녁", Why: "카톡에서 약속했는데 캘린더에 없음",
		Action: "calendar.create_event", ActionInput: json.RawMessage(`{"date":"2026-10-18","title":"은주와 저녁","start":"19:00"}`),
		Confidence: 0.8,
	}
	j := &job{ev: Event{Type: "memory.plan", Key: "memory:plan:1"}, topic: "plan:abc", brief: "약속"}
	if err := e.judge(context.Background(), j); err != nil || bg.err != nil {
		t.Fatalf("judge: %v / %v", err, bg.err)
	}
	if len(bg.tasks) != 1 || !contains(bg.tasks[0].Allowed, "mcp__suggest__propose") || contains(bg.tasks[0].Allowed, "mcp__calendar__create_event") {
		t.Fatalf("판단 턴의 도구가 틀렸습니다: %v", bg.tasks[0].Allowed)
	}
	list := props.List()
	if len(list) != 1 || list[0].Origin != proposal.OriginSuggest || list[0].Action.Kind != "calendar.create_event" {
		t.Fatalf("카드: %+v", list)
	}
	raised := sk.wait(t, 1)[0]
	if raised["type"] != "suggestion.raised" || !strings.Contains(asJSON(raised["data"]), "memory:plan:1") {
		t.Fatalf("raised 이벤트에 원래 키가 없습니다: %v", raised)
	}
	if ok, _ := e.ledger.Allowed("plan:abc", time.Now()); ok {
		t.Fatal("방금 올린 주제가 다시 허용됐습니다")
	}

	e.OnDecide(list[0], proposal.Reject)
	decided := sk.wait(t, 2)[1]
	if decided["type"] != "suggestion.decided" || !strings.Contains(asJSON(decided["data"]), "rejected") {
		t.Fatalf("decided 이벤트: %v", decided)
	}
	e.now = func() time.Time { return time.Now().Add(10 * 24 * time.Hour) }
	if ok, _ := e.ledger.Allowed("plan:abc", e.now()); ok {
		t.Fatal("반려하고 열흘 만에 다시 허용됐습니다(14일)")
	}
}

func TestLowConfidenceGoesToDigestAndBadActionRefused(t *testing.T) {
	e, bg, props, sk := setup(t)
	bg.input = &ProposeInput{Title: "현금 비중 조정", Body: "현금이 2%p 많음", Action: "none", Confidence: 0.4}
	e.judge(context.Background(), &job{ev: Event{Type: "assets.band"}, topic: "rebalance"})
	if len(props.List()) != 0 {
		t.Fatal("확신이 낮은데 카드가 올라갔습니다")
	}
	ev := sk.wait(t, 1)[0]
	if ev["type"] != "suggestion.note" || ev["notify"] == nil {
		t.Fatalf("요약 한 줄이 아닙니다: %v", ev)
	}

	bg.input = &ProposeInput{Title: "스페이스X 매도", Body: "2주", Action: "assets.order", Confidence: 0.9}
	e.judge(context.Background(), &job{ev: Event{Type: "assets.band"}, topic: "rebalance2"})
	if bg.err == nil || len(props.List()) != 0 {
		t.Fatalf("실행할 수 없는 action 이 통과했습니다: %v", bg.err)
	}
}

func TestDailyCap(t *testing.T) {
	l, _ := OpenLedger("")
	now := time.Now()
	for i := 0; i < DailyCap; i++ {
		l.Record("t"+string(rune('a'+i)), "", "x", now)
	}
	if ok, why := l.Allowed("new", now); ok || why == "" {
		t.Fatal("하루 한도를 넘었습니다")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
