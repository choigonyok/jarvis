package gate

import (
	"context"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// askCar has the conversation ask a car rental of the calendar, as the agent
// tells mind of its card (Asked).
func askCar(t *testing.T, f *fixture, card, date string) {
	t.Helper()
	if _, err := f.g.Asked(context.Background(), card, model.HeldCalendarCreate, "일정을 추가합니다", model.CalendarAction{
		Event: model.CalendarEvent{Title: "쏘카 대여", Date: date, Start: "10:00"}}); err != nil {
		t.Fatal(err)
	}
}

// A calendar card of the conversation's turned down is turned down for mind: the
// operator's words stay with it, mind's own question that stood aside for it is
// turned down with them and heard, and the same entry read later is answered by
// it too. Nothing counts against the schedule's record: nobody answered mind.
func TestANoInTheConversationIsMindsToo(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	var heard []model.Intent
	f.g.OnDecide = func(_ context.Context, in model.Intent) { heard = append(heard, in) }
	open, _ := f.g.Submit(ctx, addAt(t, f, "plan_a", "kakao:1", "쏘카 대여", "2026-11-17", "10:00"))
	askCar(t, f, "p-1", "2026-11-17")
	held, err := f.g.Answered(ctx, HeldAnswer{Card: "p-1", Kind: model.HeldCalendarCreate, Reason: " 차 예약은 일정 카드 필요없어 ", At: f.clock})
	if err != nil || held.Status != model.Rejected || held.Error != "차 예약은 일정 카드 필요없어" || held.DecidedAt == nil {
		t.Fatalf("%+v %v", held, err)
	}
	if in, _ := f.st.Intent(ctx, open.ID); in.Status != model.Rejected || in.Error != "차 예약은 일정 카드 필요없어" {
		t.Fatalf("비켜선 mind 의 질문도 그 답을 받습니다: %+v", in)
	}
	if len(heard) != 1 || heard[0].ID != open.ID {
		t.Fatalf("배우는 곳에 알려야 합니다: %+v", heard)
	}
	later, _ := f.g.Submit(ctx, addAt(t, f, "plan_b", "kakao:2", "쏘카 대여", "2026-11-17", "10:00"))
	if later.Status != model.Rejected || later.Error != "차 예약은 일정 카드 필요없어" || later.Card {
		t.Fatalf("나중에 읽은 같은 일정도 반려된 것입니다: %+v", later)
	}
	if len(heard) != 2 || heard[1].ID != later.ID {
		t.Fatalf("%+v", heard)
	}
	if stat, _ := f.st.Policy(ctx, ScheduleSkill, "calendar.create"); stat.Rejected != 0 {
		t.Fatalf("mind 에게 답한 사람은 없습니다: %+v", stat)
	}

	// Approved, it is done and nothing of mind's is turned down.
	other, _ := f.g.Submit(ctx, addAt(t, f, "plan_c", "kakao:1", "쏘카 대여", "2026-11-20", "10:00"))
	askCar(t, f, "p-2", "2026-11-20")
	if done, _ := f.g.Answered(ctx, HeldAnswer{Card: "p-2", Kind: model.HeldCalendarCreate, Approved: true, Reason: "무시", At: f.clock}); done.Status != model.Executed || done.Error != "" {
		t.Fatalf("%+v", done)
	}
	if in, _ := f.st.Intent(ctx, other.ID); !in.Covered() {
		t.Fatalf("승인한 카드에 비켜선 질문은 그대로: %+v", in)
	}

	// A card mind was never told of - the guest's agent tells it nothing - is
	// closed, and its words are not the operator's.
	guest, err := f.g.Answered(ctx, HeldAnswer{Card: "p-9", Kind: model.HeldCalendarCreate, Title: "일정을 추가합니다", Reason: "손님의 말", At: f.clock})
	if err != nil || guest.Status != model.Rejected || guest.Error != "" || guest.Holder != "chat" {
		t.Fatalf("%+v %v", guest, err)
	}
}

// Before a calendar card goes up in the conversation, the noes given lately in
// the operator's own words - mind's and the conversation's - are put to the
// judgement with what was said just now, and the one it names is the answer.
func TestTheConversationAsksWhatWasTurnedDown(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	ev := model.CalendarEvent{Title: "그린카 대여", Date: "2026-11-24", Start: "10:00"}
	if _, ok, err := f.g.TurnedDown(ctx, ev, ""); ok || err != nil {
		t.Fatalf("판단할 곳이 없으면 막지 않습니다: %v", err)
	}
	var past []model.Turned
	var said string
	f.g.Turned = func(_ context.Context, p []model.Turned, _ model.CalendarEvent, s string) int {
		past, said = p, s
		for i, t := range p {
			if strings.Contains(t.Reason, "차 예약") {
				return i + 1
			}
		}
		return 0
	}
	if _, ok, _ := f.g.TurnedDown(ctx, ev, ""); ok || past != nil {
		t.Fatal("반려한 것이 없으면 묻지도 않습니다")
	}
	quick, _ := f.g.Submit(ctx, addAt(t, f, "plan_q", "kakao:1", "치과", "2026-11-18", "15:00"))
	if _, err := f.g.RejectWhy(ctx, quick.ID, "틀렸어"); err != nil {
		t.Fatal(err)
	}
	askCar(t, f, "p-1", "2026-11-17")
	if _, err := f.g.Answered(ctx, HeldAnswer{Card: "p-1", Kind: model.HeldCalendarCreate, Reason: "차 예약은 일정 카드 필요없어", At: f.clock}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := f.g.TurnedDown(ctx, ev, "그린카 예약했어")
	if err != nil || !ok || got.Title != "쏘카 대여" || got.Reason != "차 예약은 일정 카드 필요없어" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if len(past) != 1 || said != "그린카 예약했어" {
		t.Fatalf("한 번 누른 답은 묻지 않고, 방금 한 말은 같이 봅니다: %+v %q", past, said)
	}
	for _, n := range []int{0, 2, -1} {
		f.g.Turned = func(context.Context, []model.Turned, model.CalendarEvent, string) int { return n }
		if _, ok, _ := f.g.TurnedDown(ctx, ev, ""); ok {
			t.Fatalf("%d 번은 막지 않습니다", n)
		}
	}
}

// Driving, in a meeting, in a focus: a buzz costs twice, and what is not worth
// it waits for the evening; what is, rings as before.
func TestBusyCostsTwice(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	busy := "운전 중"
	f.g.Busy = func() string { return busy }
	in, _ := f.g.Submit(ctx, create("p1"))
	if in.Tier != "digest" || !strings.Contains(in.Why, "운전 중") || !in.Card {
		t.Fatalf("바쁠 때 그만한 값이 아닌 카드: %+v", in)
	}
	busy = ""
	if in, _ := f.g.Submit(ctx, create("p2")); in.Tier != "now" {
		t.Fatalf("바쁘지 않으면 그대로: %+v", in)
	}
	busy = "회의 중"
	strong := create("p3")
	strong.Value = 3
	if in, _ := f.g.Submit(ctx, strong); in.Tier != "now" {
		t.Fatalf("그만한 값이면 울립니다: %+v", in)
	}
}
