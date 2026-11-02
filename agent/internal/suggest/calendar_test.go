package suggest

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
)

func chatAction(kind string, in any) action.Action {
	raw, _ := json.Marshal(in)
	return action.Action{Kind: kind, Input: raw}
}

// mindCard is mind.card's data for a calendar intent.
func mindCard(id, kind, title string) map[string]any {
	return map[string]any{"intentId": id, "mode": "ask", "skill": "schedule", "kind": kind,
		"title": "캘린더에 추가: " + title, "preview": "내 일정에 추가: " + title,
		"reason": "대화에서 확정됐는데 캘린더에 없어요", "confidence": 0.5}
}

// A calendar card raised in the conversation is told to mind, which alone says
// whether its own card is about the same entry: nothing here compares them.
func TestACardInTheConversationIsToldToMind(t *testing.T) {
	e, props := setup(t)
	fm := &fakeMind{}
	e.cfg.Mind = fm.server(t)
	created, _ := props.Open(proposal.Proposal{Origin: proposal.OriginChat, Card: action.Card{Title: "Netflix 해지"}, Action: chatAction(calendar.KindCreate,
		calendar.CreateInput{Date: "2026-11-15", EndDate: "2026-11-16", Title: "Netflix 해지", Start: "20:00", End: "20:30"})})
	e.tellAsked(*created)
	if len(fm.calls) != 1 || fm.calls[0] != "POST /api/intents/"+created.ID+"/asked Bearer tok" {
		t.Fatalf("%v", fm.calls)
	}
	body := fm.body[0]
	cal, _ := body["calendar"].(map[string]any)
	ev, _ := cal["event"].(map[string]any)
	if body["kind"] != calendar.KindCreate || body["title"] != "Netflix 해지" || ev["date"] != "2026-11-15" || ev["endDate"] != "2026-11-16" || ev["start"] != "20:00" || ev["end"] != "20:30" {
		t.Fatalf("%+v", body)
	}

	moved, _ := props.Open(proposal.Proposal{Origin: proposal.OriginChat, Action: chatAction(calendar.KindUpdate,
		calendar.UpdateInput{ID: "ev1", Start: "21:00"})})
	e.tellAsked(*moved)
	cal, _ = fm.body[1]["calendar"].(map[string]any)
	ev, _ = cal["event"].(map[string]any)
	if cal["eventId"] != "ev1" || ev["start"] != "21:00" {
		t.Fatalf("%+v", fm.body[1])
	}

	gone, _ := props.Open(proposal.Proposal{Origin: proposal.OriginChat, Action: chatAction(calendar.KindDelete, calendar.DeleteInput{ID: "ev2"})})
	e.tellAsked(*gone)
	cal, _ = fm.body[2]["calendar"].(map[string]any)
	if cal["eventId"] != "ev2" {
		t.Fatalf("%+v", fm.body[2])
	}
}

// Only the conversation's calendar cards: what is not one is nothing to mind.
func TestOtherCardsAreNotToldToMind(t *testing.T) {
	if _, ok := calendarChange(action.Action{Kind: mind.Approve, Input: []byte(`{}`)}); ok {
		t.Fatal("mind 의 카드는 알릴 것이 아닙니다")
	}
	if _, ok := calendarChange(action.Action{Kind: "memo.create", Input: []byte(`{}`)}); ok {
		t.Fatal("일정 카드가 아닙니다")
	}
	e, _ := setup(t)
	fm := &fakeMind{}
	e.cfg.Mind = fm.server(t)
	e.OnOpen(proposal.Proposal{ID: "p-1", Origin: proposal.OriginSuggest, Action: chatAction(calendar.KindCreate, calendar.CreateInput{Date: "2026-11-15", Title: "x"})})
	if len(fm.calls) != 0 {
		t.Fatalf("%v", fm.calls)
	}
	e.cfg.Mind = nil
	e.OnOpen(proposal.Proposal{ID: "p-2", Origin: proposal.OriginChat, Action: chatAction(calendar.KindCreate, calendar.CreateInput{Date: "2026-11-15", Title: "x"})})
}

// A question that changed stays on its card: the same card, where it was, saying
// what it asks now, and nothing rings. Approving it does what it says now.
func TestACardIsToldWhatChanged(t *testing.T) {
	b := bus.New()
	props := proposal.NewStore(b)
	said := 0
	e := New(props, Config{Say: func(string, string) { said++ }}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	deliver(e, mind.CardRaised, mindCard("int_a", "calendar.create", "지수와 합주 15:00"))
	first, ok := e.findCard("int_a")
	if !ok {
		t.Fatal("카드가 떠야 합니다")
	}
	events, stop := b.Subscribe()
	defer stop()

	deliver(e, mind.CardUpdated, mindCard("int_a", "calendar.create", "지수와 합주 16:00"))
	p, ok := props.Get(first.ID)
	if !ok || len(props.List()) != 1 || p.State != proposal.Pending || !strings.Contains(p.Card.Title, "16:00") || p.At != first.At {
		t.Fatalf("같은 카드가 고쳐져야 합니다: %+v", props.List())
	}
	select {
	case ev := <-events:
		if p, ok := ev.Proposal.(proposal.Proposal); ev.Type != "proposal" || !ok || p.ID != first.ID || !strings.Contains(p.Card.Title, "16:00") {
			t.Fatalf("%+v", ev)
		}
	default:
		t.Fatal("화면이 다시 그려져야 합니다")
	}
	if said != 1 {
		t.Fatalf("대화에 새로 놓지 않습니다: %d", said)
	}
	var in mind.Input
	if json.Unmarshal(p.Action.Input, &in) != nil || in.IntentID != "int_a" {
		t.Fatalf("%s", p.Action.Input)
	}

	// Answered meanwhile, it stays as answered.
	if err := props.Decide(first.ID, proposal.Reject); err != nil {
		t.Fatal(err)
	}
	deliver(e, mind.CardUpdated, mindCard("int_a", "calendar.create", "지수와 합주 17:00"))
	if p, _ = props.Get(first.ID); strings.Contains(p.Card.Title, "17:00") {
		t.Fatalf("%+v", p)
	}

	// One the agent never saw (the raise lost, a restart) is raised.
	deliver(e, mind.CardUpdated, mindCard("int_b", "calendar.create", "민수와 저녁"))
	if p, ok := e.findCard("int_b"); !ok || p.State != proposal.Pending {
		t.Fatalf("%+v", props.List())
	}
}
