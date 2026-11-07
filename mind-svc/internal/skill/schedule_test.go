package skill

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// An entry like one turned down lately is not asked again: the same title
// on another day, or one that carries the word the reason was about. An
// entry that shares neither is asked as before.
func TestEntryLikeOneTurnedDownIsNotAskedAgain(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, seoul)
	at := now.Add(-24 * time.Hour)
	payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "은주와 롤체", Date: "2026-10-25", Start: "23:20", Owner: "me"}})
	v := view{now: now, intents: []model.Intent{{
		ID: "int_old", Kind: "calendar.create", EntityID: "plan_old", Payload: payload, Status: model.Rejected,
		Error: "롤체 약속은 캘린더에 추가하지마", CreatedAt: at, DecidedAt: &at,
	}}}
	plan := func(id, title string) model.Entity {
		return model.Entity{ID: id, Kind: model.KindPlan, State: model.State{Title: title, Status: model.Confirmed, Date: "2026-10-27", Start: "18:00"}}
	}
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_same", "은주와 롤체"), nil); ok {
		t.Fatal("거절한 것과 같은 제목은 다시 묻지 않아야 합니다")
	}
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_kind", "롤체 대회 같이 보기"), nil); ok {
		t.Fatal("거절 사유가 말한 것(롤체)이 든 일정도 묻지 않아야 합니다")
	}
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_other", "은주와 저녁"), nil); !ok {
		t.Fatal("다른 일정은 그대로 물어야 합니다")
	}

	// A no without a word in common only covers that title.
	v.intents[0].Error = "틀렸어"
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_kind2", "롤체 대회 같이 보기"), nil); !ok {
		t.Fatal("이유가 제목의 말을 짚지 않으면 같은 제목만 거릅니다")
	}

	// Long ago, it is forgotten.
	old := now.Add(-40 * 24 * time.Hour)
	v.intents[0].Error, v.intents[0].CreatedAt, v.intents[0].DecidedAt = "롤체 약속은 캘린더에 추가하지마", old, &old
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_later", "은주와 롤체"), nil); !ok {
		t.Fatal("한 달이 지난 거절은 더 보지 않습니다")
	}
}

func TestAlikeWords(t *testing.T) {
	if got := words("롤체 약속은 캘린더에 추가하지마."); len(got) != 4 || got[0] != "롤체" || got[1] != "약속" || got[2] != "캘린더" || got[3] != "추가하지마" {
		t.Fatalf("words: %v", got)
	}
	if alike("승전과 톤 세팅 만남", "은주 일정은 넣지 마", "은주와 저녁") {
		t.Fatal("거절한 제목에 없는 말로는 거르지 않습니다")
	}
	if !alike("은주와 롤체", "은주 일정은 넣지 마", "은주와 저녁") {
		t.Fatal("거절한 제목과 이유가 함께 짚은 말(은주)은 거릅니다")
	}
}

// Today and the days gone by are not asked about: 25 cards to add today's plan,
// and not one approved. Tomorrow is, and a card for a plan that came to today
// goes.
func TestOnlyWhatIsComingIsAskedAbout(t *testing.T) {
	ctx := context.Background()
	v := view{now: time.Date(2026, 10, 26, 15, 0, 0, 0, seoul)}
	plan := model.Entity{ID: "plan_a", Kind: model.KindPlan, State: model.State{Title: "저녁", Status: model.Confirmed}}
	for date, want := range map[string]int{"2026-10-25": 0, "2026-10-26": 0, "2026-10-27": 1} {
		plan.State.Date = date
		r := Schedule{}.OnChange(ctx, v, plan, []model.Change{{After: plan.State}})
		if len(r.Intents) != want || r.Withdraw != (want == 0) {
			t.Fatalf("%s: %+v", date, r)
		}
	}
	// A plan told late at night for the next morning is still tomorrow's.
	late := view{now: time.Date(2026, 10, 26, 23, 50, 0, 0, seoul)}
	plan.State.Date, plan.State.Start = "2026-10-27", "09:00"
	if r := (Schedule{}).OnChange(ctx, late, plan, []model.Change{{After: plan.State}}); len(r.Intents) != 1 {
		t.Fatalf("내일 아침 일정은 묻습니다: %+v", r)
	}
}

// A card still open stands while its plan gathers notes: asked again each time,
// one question would leave and come back again and again in a day.
func TestAnOpenCardIsNotAskedAgain(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 11, 13, 0, 0, seoul)
	plan := model.Entity{ID: "plan_a", Kind: model.KindPlan, State: model.State{Title: "지수와 톤 세팅", Status: model.Confirmed,
		Date: "2026-11-28", Start: "10:00", Place: "연습실 역삼점"}}
	first, ok := (Schedule{}).add(ctx, view{now: now}, plan, nil)
	if !ok {
		t.Fatal("처음에는 물어야 합니다")
	}
	first.ID, first.Status, first.CreatedAt = "int_open", model.Pending, now
	open := view{now: now, intents: []model.Intent{first}}
	noted := plan
	noted.State.Note = "끝나고 점심"
	evidence := []model.Change{{Evidence: []model.Evidence{{Ref: "m1", Quote: "끝나고 점심 먹자"}}}}
	if _, ok := (Schedule{}).add(ctx, open, noted, evidence); ok {
		t.Fatal("메모만 늘었으면 열린 카드가 그대로입니다")
	}
	moved := plan
	moved.State.Start = "11:00"
	if _, ok := (Schedule{}).add(ctx, open, moved, nil); !ok {
		t.Fatal("시각이 바뀌면 새로 물어야 합니다")
	}
}

// A move still waiting has not happened. Told 15:00, then 16:00, then 15:00 again
// with nobody answering, the calendar never left 15:00: the card said 16:00 → 15:00,
// and turned down it would have left the plan at an hour nobody holds.
func TestAMoveMovesFromWhatTheCalendarHolds(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 15, 8, 0, 0, seoul)
	at15 := model.State{Title: "지수와 합주", Status: model.Confirmed, Date: "2026-11-28", Start: "15:00", CalendarID: "cal1"}
	at16 := at15
	at16.Start = "16:00"
	change := func(before, after model.State) []model.Change {
		return []model.Change{{Before: &before, After: after, Actor: "interpreter", Confidence: 0.9}}
	}
	moved := Schedule{}.OnChange(ctx, view{now: now}, model.Entity{ID: "plan_a", Kind: model.KindPlan, State: at16}, change(at15, at16))
	if len(moved.Intents) != 1 {
		t.Fatalf("%+v", moved)
	}
	card := moved.Intents[0]
	card.ID, card.Status, card.CreatedAt = "int_move", model.Pending, now
	open := view{now: now, intents: []model.Intent{card}}

	back := Schedule{}.OnChange(ctx, open, model.Entity{ID: "plan_a", Kind: model.KindPlan, State: at15}, change(at16, at15))
	if len(back.Intents) != 0 || !back.Withdraw {
		t.Fatalf("캘린더 그대로로 돌아왔으면 물을 것이 없습니다: %+v", back)
	}

	at15to17 := at15
	at15to17.End = "17:00"
	end := Schedule{}.OnChange(ctx, open, model.Entity{ID: "plan_a", Kind: model.KindPlan, State: at15to17}, change(at16, at15to17))
	if len(end.Intents) != 1 {
		t.Fatalf("%+v", end)
	}
	var a model.CalendarAction
	if err := json.Unmarshal(end.Intents[0].Payload, &a); err != nil || a.Was == nil || a.Was.Start != "15:00" {
		t.Fatalf("캘린더에 있는 15:00 에서 옮깁니다: %+v %v", a, err)
	}
	if title := end.Intents[0].Title; strings.Contains(title, "16:00") || !strings.Contains(title, "끝 없음 → 17:00") {
		t.Fatalf("바뀐 것을 말하고 없던 시각은 말하지 않습니다: %s", title)
	}
}
