// Package eval checks the loop against conversations written down with what
// should come of them: what the assistant should believe after each turn,
// what it should want to do, and what it must not do.
//
// The same scenarios run two ways. With each turn's "script" standing in for
// the model they test everything around it - evidence checks, the world
// model, skills, the gate - deterministically (go test). With the real model
// they measure how well it reads (go run . eval): a change to the prompt or
// the model is checked here before it reaches anyone's calendar.
package eval

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

//go:embed scenarios/*.json
var files embed.FS

type Scenario struct {
	Name     string `json:"name"`
	Room     string `json:"room"`
	RoomName string `json:"roomName"`
	Turns    []Turn `json:"turns"`
}

// Turn is one step: messages arrive and the room is read, or the person
// answers a card, or time passes.
type Turn struct {
	Messages []Msg  `json:"messages,omitempty"`
	Approve  string `json:"approve,omitempty"` // the latest pending intent of this kind
	Reject   string `json:"reject,omitempty"`
	Undo     string `json:"undo,omitempty"`    // the latest executed intent of this kind
	Advance  string `json:"advance,omitempty"` // move the clock, firing timers
	Tick     string `json:"tick,omitempty"`
	// Script is what the model should answer to this turn's reading, for the
	// deterministic run.
	Script json.RawMessage `json:"script,omitempty"`
	Expect *Expect         `json:"expect,omitempty"`
}

type Msg struct {
	At   string `json:"at"` // 2026-10-12T20:01, Asia/Seoul
	From string `json:"from,omitempty"`
	Me   bool   `json:"me,omitempty"`
	Text string `json:"text"`
}

type Expect struct {
	Plans         []Match  `json:"plans,omitempty"`   // each must match some plan
	NoPlans       []Match  `json:"noPlans,omitempty"` // none may match any plan
	PlanCount     *int     `json:"planCount,omitempty"`
	Loops         []Match  `json:"loops,omitempty"`
	NoLoops       bool     `json:"noLoops,omitempty"`
	Intents       []string `json:"intents,omitempty"`   // kinds raised this turn
	NoActions     bool     `json:"noActions,omitempty"` // no action intents raised this turn
	Calendar      []Match  `json:"calendar,omitempty"`
	CalendarCount *int     `json:"calendarCount,omitempty"`
}

// Match is a partial description; empty fields match anything.
type Match struct {
	Status   string   `json:"status,omitempty"`
	Place    string   `json:"place,omitempty"`
	Date     string   `json:"date,omitempty"`
	Start    string   `json:"start,omitempty"`
	TitleHas string   `json:"titleHas,omitempty"`
	People   []string `json:"people,omitempty"`
	Owner    string   `json:"owner,omitempty"`
	DueFrom  string   `json:"due,omitempty"` // due starts with this
	OnCal    *bool    `json:"onCalendar,omitempty"`
}

type Result struct {
	Name     string
	Failures []string
}

func (r Result) Pass() bool { return len(r.Failures) == 0 }

// Load is every scenario.
func Load() ([]Scenario, error) {
	entries, err := files.ReadDir("scenarios")
	if err != nil {
		return nil, err
	}
	var out []Scenario
	for _, e := range entries {
		raw, err := files.ReadFile(path.Join("scenarios", e.Name()))
		if err != nil {
			return nil, err
		}
		var sc Scenario
		if err := json.Unmarshal(raw, &sc); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if sc.Name == "" {
			sc.Name = e.Name()
		}
		out = append(out, sc)
	}
	return out, nil
}

// RunAll runs every scenario with the given model.
func RunAll(ctx context.Context, client llm.Client, loc *time.Location) ([]Result, error) {
	scs, err := Load()
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, sc := range scs {
		out = append(out, Run(ctx, sc, client, loc))
	}
	return out, nil
}

// Scripted is a model that answers each reading with the scenario's script,
// in turn order.
func Scripted(sc Scenario) llm.Client {
	var scripts []json.RawMessage
	for _, t := range sc.Turns {
		if len(t.Messages) > 0 {
			scripts = append(scripts, t.Script)
		}
	}
	n := 0
	return &llm.Script{Answer: func(_, _ string) (string, error) {
		if n >= len(scripts) {
			return "", errors.New("스크립트가 모자랍니다")
		}
		s := scripts[n]
		n++
		if len(s) == 0 {
			return `{"changes":[]}`, nil
		}
		return string(s), nil
	}}
}

// Run plays one scenario through a fresh loop in memory.
func Run(ctx context.Context, sc Scenario, client llm.Client, loc *time.Location) Result {
	res := Result{Name: sc.Name}
	fail := func(turn int, format string, args ...any) {
		res.Failures = append(res.Failures, fmt.Sprintf("%d번째 단계: ", turn+1)+fmt.Sprintf(format, args...))
	}

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.New()
	st := store.NewMemory()
	log := obs.NewMemoryLog(quiet)
	cal := act.NewMemoryCalendar()
	m := mind.New(mind.Options{
		Store: st, Log: log, LLM: client, Calendar: cal, Notifier: &act.MemoryNotifier{},
		Loc: loc, Now: clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"},
		Settle: 0, Policy: gate.DefaultPolicy(), Mode: "eval",
	})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go log.Run(ctx, m.Handle)
	quiesce := func() {
		for range 5 {
			log.Drain()
			m.Attn.Settle(ctx)
		}
	}
	room := "sim:" + sc.Room

	for i, t := range sc.Turns {
		before, _ := st.Intents(ctx, 10000)
		seen := map[string]bool{}
		for _, in := range before {
			seen[in.ID] = true
		}

		switch {
		case len(t.Messages) > 0:
			for _, msg := range t.Messages {
				at, err := time.ParseInLocation("2006-01-02T15:04", msg.At, loc)
				if err != nil {
					fail(i, "시각 %q 을 읽지 못함", msg.At)
					return res
				}
				clk.Pin(at)
				if _, err := m.Simulate(ctx, room, sc.RoomName, msg.From, msg.Me, msg.Text, at); err != nil {
					fail(i, "%v", err)
					return res
				}
			}
			log.Drain()
			if err := m.Conv.Read(ctx, room); err != nil {
				fail(i, "해석 실패: %v", err)
				return res
			}
		case t.Approve != "" || t.Reject != "":
			kind := t.Approve + t.Reject
			id := latest(before, kind, model.Pending)
			if id == "" {
				fail(i, "%s 대기 중인 제안이 없음", kind)
				continue
			}
			var err error
			if t.Approve != "" {
				_, err = m.Gate.Approve(ctx, id)
			} else {
				_, err = m.Gate.Reject(ctx, id)
			}
			if err != nil {
				fail(i, "%v", err)
			}
		case t.Undo != "":
			id := latest(before, t.Undo, model.Executed)
			if id == "" {
				fail(i, "%s 되돌릴 것이 없음", t.Undo)
				continue
			}
			if _, err := m.Gate.Undo(ctx, id); err != nil {
				fail(i, "%v", err)
			}
		case t.Advance != "":
			d, err := time.ParseDuration(t.Advance)
			if err != nil {
				fail(i, "advance %q", t.Advance)
				continue
			}
			clk.Advance(d)
		case t.Tick != "":
			m.Attn.Tick(ctx, t.Tick)
		}
		quiesce()

		if t.Expect != nil {
			for _, f := range check(ctx, st, cal, *t.Expect, seen) {
				fail(i, "%s", f)
			}
		}
	}
	return res
}

func latest(intents []model.Intent, kind, status string) string {
	for _, in := range intents { // newest first
		if in.Kind == kind && in.Status == status {
			return in.ID
		}
	}
	return ""
}

func check(ctx context.Context, st store.Store, cal *act.MemoryCalendar, ex Expect, seen map[string]bool) []string {
	var out []string
	plans, _ := st.Entities(ctx, model.KindPlan, "", false)
	loops, _ := st.Entities(ctx, model.KindLoop, "", false)
	intents, _ := st.Intents(ctx, 10000)
	var raised []string
	for _, in := range intents {
		if !seen[in.ID] && in.Status != model.Superseded {
			raised = append(raised, in.Kind)
		}
	}

	for _, want := range ex.Plans {
		if !anyMatch(plans, want) {
			out = append(out, "이런 일정이 없음: "+desc(want)+" · 지금: "+summary(plans))
		}
	}
	for _, not := range ex.NoPlans {
		if anyMatch(plans, not) {
			out = append(out, "있으면 안 되는 일정이 있음: "+desc(not)+" · 지금: "+summary(plans))
		}
	}
	if ex.PlanCount != nil && len(plans) != *ex.PlanCount {
		out = append(out, fmt.Sprintf("일정 %d개여야 하는데 %d개: %s", *ex.PlanCount, len(plans), summary(plans)))
	}
	for _, want := range ex.Loops {
		if !anyMatch(loops, want) {
			out = append(out, "이런 열린 고리가 없음: "+desc(want)+" · 지금: "+summary(loops))
		}
	}
	if ex.NoLoops && len(loops) > 0 {
		out = append(out, "열린 고리가 없어야 함: "+summary(loops))
	}
	for _, k := range ex.Intents {
		if !slices.Contains(raised, k) {
			out = append(out, fmt.Sprintf("의도 %s 가 나와야 함 · 나온 것: %v", k, raised))
		}
	}
	if ex.NoActions {
		for _, k := range raised {
			if k != "info" {
				out = append(out, "행동 의도가 없어야 하는데 "+k)
			}
		}
	}
	if ex.CalendarCount != nil && len(cal.All()) != *ex.CalendarCount {
		out = append(out, fmt.Sprintf("캘린더 일정 %d개여야 하는데 %d개", *ex.CalendarCount, len(cal.All())))
	}
	for _, want := range ex.Calendar {
		found := false
		for _, e := range cal.All() {
			if (want.Place == "" || e.Place == want.Place) && (want.Date == "" || e.Date == want.Date) &&
				(want.TitleHas == "" || strings.Contains(e.Title, want.TitleHas)) {
				found = true
			}
		}
		if !found {
			out = append(out, fmt.Sprintf("캘린더에 없음: %s · 캘린더: %v", desc(want), cal.All()))
		}
	}
	return out
}

func anyMatch(es []model.Entity, m Match) bool {
	for _, e := range es {
		if matches(e, m) {
			return true
		}
	}
	return false
}

func matches(e model.Entity, m Match) bool {
	s := e.State
	if m.Status != "" && s.Status != m.Status ||
		m.Place != "" && s.Place != m.Place ||
		m.Date != "" && s.Date != m.Date ||
		m.Start != "" && s.Start != m.Start ||
		m.TitleHas != "" && !strings.Contains(s.Title, m.TitleHas) ||
		m.Owner != "" && s.Owner != m.Owner ||
		m.DueFrom != "" && !strings.HasPrefix(s.Due, m.DueFrom) {
		return false
	}
	for _, p := range m.People {
		if !slices.Contains(s.People, p) {
			return false
		}
	}
	if m.OnCal != nil && (s.CalendarID != "") != *m.OnCal {
		return false
	}
	return true
}

func desc(m Match) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func summary(es []model.Entity) string {
	if len(es) == 0 {
		return "없음"
	}
	var parts []string
	for _, e := range es {
		s := e.State
		p := fmt.Sprintf("[%s %s %s", s.Title, s.Status, s.Date)
		if s.Start != "" {
			p += " " + s.Start
		}
		if s.Place != "" {
			p += " @" + s.Place
		}
		if s.Due != "" {
			p += " 기한 " + s.Due
		}
		if s.CalendarID != "" {
			p += " 캘린더"
		}
		parts = append(parts, p+"]")
	}
	return strings.Join(parts, " ")
}
