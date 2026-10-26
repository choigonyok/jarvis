package suggest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
)

func chatAction(kind string, in any) action.Action {
	raw, _ := json.Marshal(in)
	return action.Action{Kind: kind, Input: raw}
}

// mindCard is mind.card's data for a calendar intent.
func mindCard(id, kind string, cal mind.Calendar) map[string]any {
	return map[string]any{"intentId": id, "mode": "ask", "skill": "schedule", "kind": kind,
		"title": "캘린더에 추가: " + cal.Event.Title, "preview": "내 일정에 추가: " + cal.Event.Date + " " + cal.Event.Title,
		"reason": "대화에서 확정됐는데 캘린더에 없어요", "confidence": 0.5, "calendar": cal}
}

// 2026-10-26: asked in the conversation to put "Netflix 플랜 변경·해지" on
// the day before the renewal, the assistant raised its card for 20:00; mind,
// reading the same conversation, found the entry missing and its card came
// six minutes later. While the conversation's card waits, mind's is not
// opened, and mind is told which card asks it.
func TestMindCardWaitsOnTheConversationsCard(t *testing.T) {
	e, props := setup(t)
	fm := &fakeMind{}
	e.cfg.Mind = fm.server(t)
	chat, _ := props.Open(proposal.Proposal{Origin: proposal.OriginChat, Action: chatAction(calendar.KindCreate,
		calendar.CreateInput{Date: "2026-11-15", Title: "Netflix 플랜 변경·해지", Start: "20:00", End: "20:30"})})

	deliver(e, mind.CardRaised, mindCard("int_a", "calendar.create",
		mind.Calendar{Event: mind.Event{Title: "Netflix 플랜 변경·해지", Date: "2026-11-15"}}))
	if n := len(props.List()); n != 1 {
		t.Fatalf("대화의 카드 하나만: %+v", props.List())
	}
	if len(fm.calls) != 1 || fm.calls[0] != "POST /api/intents/int_a/covered Bearer tok" || fm.body[0]["by"] != chat.ID {
		t.Fatalf("mind 에 어느 카드가 묻는지 알려야 합니다: %v %v", fm.calls, fm.body)
	}

	// Another day is another question.
	deliver(e, mind.CardRaised, mindCard("int_b", "calendar.create",
		mind.Calendar{Event: mind.Event{Title: "Netflix 플랜 변경·해지", Date: "2026-12-15"}}))
	if p, ok := e.findCard("int_b"); !ok || p.State != proposal.Pending {
		t.Fatalf("다른 날의 일정은 물어야 합니다: %+v", props.List())
	}

	// mind cannot be told: the card opens, as before - two cards rather
	// than a question lost.
	fm.mu.Lock()
	fm.down = true
	fm.mu.Unlock()
	deliver(e, mind.CardRaised, mindCard("int_c", "calendar.create",
		mind.Calendar{Event: mind.Event{Title: "netflix 플랜 변경/해지", Date: "2026-11-15", Start: "20:00"}}))
	if _, ok := e.findCard("int_c"); !ok {
		t.Fatalf("mind 에 못 알리면 카드를 열어야 합니다: %+v", props.List())
	}
}

// The other way round: mind's card waits, and the operator asks the
// assistant for the same entry. The conversation's card takes its place.
func TestConversationsCardTakesMindsPlace(t *testing.T) {
	e, props := setup(t)
	fm := &fakeMind{}
	e.cfg.Mind = fm.server(t)
	deliver(e, mind.CardRaised, mindCard("int_k", "calendar.create",
		mind.Calendar{Event: mind.Event{Title: "지수와 합주", Date: "2026-10-17", Start: "15:00"}}))
	waiting, ok := e.findCard("int_k")
	if !ok {
		t.Fatal("mind 카드가 떠야 합니다")
	}

	other, _ := props.Open(proposal.Proposal{Origin: proposal.OriginChat, Action: chatAction(calendar.KindCreate,
		calendar.CreateInput{Date: "2026-10-17", Title: "치과", Start: "15:00"})})
	e.standAside(*other)
	if p, _ := props.Get(waiting.ID); p.State != proposal.Pending || len(fm.calls) != 0 {
		t.Fatalf("다른 일정의 카드로는 바꾸지 않습니다: %+v %v", p, fm.calls)
	}

	// Put away without a decision (closed, not turned down): mind hears
	// "covered", never a rejection to learn from.
	var closed []string
	props.OnClose(func(p proposal.Proposal) { closed = append(closed, p.ID) })
	chat, _ := props.Open(proposal.Proposal{Origin: proposal.OriginChat, Action: chatAction(calendar.KindCreate,
		calendar.CreateInput{Date: "2026-10-17", Title: "기타 합주 (지수)", Start: "15:00", End: "17:00"})})
	e.standAside(*chat)
	p, _ := props.Get(waiting.ID)
	if p.State != proposal.Rejected || !strings.Contains(p.Note, chat.ID) || len(closed) != 1 || closed[0] != waiting.ID {
		t.Fatalf("mind 카드는 대화의 카드로 바뀌어야 합니다: %+v %v", p, closed)
	}
	if len(fm.calls) != 1 || fm.calls[0] != "POST /api/intents/int_k/covered Bearer tok" || fm.body[0]["by"] != chat.ID {
		t.Fatalf("mind 에 알려야 합니다: %v %v", fm.calls, fm.body)
	}
}

func TestSameChange(t *testing.T) {
	cal := func(id, title, date, start string) *mind.Calendar {
		return &mind.Calendar{EventID: id, Event: mind.Event{Title: title, Date: date, Start: start}}
	}
	for _, c := range []struct {
		name string
		kind string
		cal  *mind.Calendar
		a    action.Action
		want bool
	}{
		{"시각 없는 mind 일정과 같은 제목", "calendar.create", cal("", "Netflix 플랜 변경·해지", "2026-11-15", ""),
			chatAction(calendar.KindCreate, calendar.CreateInput{Date: "2026-11-15", Title: "Netflix 플랜 변경·해지", Start: "20:00"}), true},
		{"같은 때 낱말이 같음", "calendar.create", cal("", "지수와 합주", "2026-10-17", "15:00"),
			chatAction(calendar.KindCreate, calendar.CreateInput{Date: "2026-10-17", Title: "기타 합주 (지수)", Start: "15:20"}), true},
		{"같은 날 다른 일", "calendar.create", cal("", "치과", "2026-10-17", ""),
			chatAction(calendar.KindCreate, calendar.CreateInput{Date: "2026-10-17", Title: "팀 회의", Start: "15:00"}), false},
		{"시각이 한 시간 넘게 다름", "calendar.create", cal("", "치과", "2026-10-17", "10:00"),
			chatAction(calendar.KindCreate, calendar.CreateInput{Date: "2026-10-17", Title: "치과", Start: "15:00"}), false},
		{"같은 일정을 같은 때로 옮김", "calendar.update", cal("c9", "합주", "2026-10-18", "16:00"),
			chatAction(calendar.KindUpdate, calendar.UpdateInput{ID: "c9", Start: "16:00"}), true},
		{"같은 일정을 다른 때로 옮김", "calendar.update", cal("c9", "합주", "2026-10-18", "16:00"),
			chatAction(calendar.KindUpdate, calendar.UpdateInput{ID: "c9", Start: "17:00"}), false},
		{"같은 일정의 제목만 고침", "calendar.update", cal("c9", "합주", "2026-10-18", "16:00"),
			chatAction(calendar.KindUpdate, calendar.UpdateInput{ID: "c9", Title: "밴드 합주"}), false},
		{"같은 일정 지우기", "calendar.delete", cal("c9", "합주", "2026-10-18", ""),
			chatAction(calendar.KindDelete, calendar.DeleteInput{ID: "c9"}), true},
		{"다른 일정 지우기", "calendar.delete", cal("c9", "합주", "2026-10-18", ""),
			chatAction(calendar.KindDelete, calendar.DeleteInput{ID: "c10"}), false},
		{"추가와 지우기는 다른 일", "calendar.create", cal("c9", "합주", "2026-10-18", ""),
			chatAction(calendar.KindDelete, calendar.DeleteInput{ID: "c9"}), false},
		{"캘린더 카드가 아님", "cart.add", nil,
			chatAction(calendar.KindCreate, calendar.CreateInput{Date: "2026-10-17", Title: "치과"}), false},
	} {
		if got := sameChange(c.kind, c.cal, c.a); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
