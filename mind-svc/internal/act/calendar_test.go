package act

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// A trip of several days moved by its form's date moves whole: the last
// day goes as far as the first.
func TestMovedTripKeepsItsLength(t *testing.T) {
	ctx := context.Background()
	cal := NewMemoryCalendar()
	ev, _ := cal.Put(ctx, model.CalendarEvent{Title: "캠핑", Date: "2026-10-24", EndDate: "2026-10-25"})
	payload, _ := json.Marshal(map[string]any{
		"eventId": ev.ID,
		"event":   model.CalendarEvent{Title: "캠핑", Date: "2026-10-31", EndDate: "2026-11-01"},
		// The operator changed the proposed date before saying yes.
		"answers": map[string]string{"date": "2026-11-07"},
	})
	if _, _, err := (CalendarActuator{Cal: cal}).Execute(ctx, "calendar.update", payload); err != nil {
		t.Fatal(err)
	}
	got, _ := cal.Get(ctx, ev.ID)
	if got.Date != "2026-11-07" || got.EndDate != "2026-11-08" {
		t.Fatalf("moved %+v", got)
	}
}

// The place lives in the memo. A move to another place puts it on the
// memo's first line and keeps what the memo said: the assistant's "퇴근 후 ·
// 망원" stayed under 모퉁이맥주. The old place, alone on its line, gives way; a
// plan that knows no place takes none off.
func TestAMoveKeepsTheMemo(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, memo, prevPlace, was, place, want string
	}{
		{"new place on top", "퇴근 후 · 망원", "", "망원", "모퉁이맥주", "모퉁이맥주\n퇴근 후 · 망원"},
		{"said already", "퇴근 후 · 망원", "", "", "망원", "퇴근 후 · 망원"},
		{"old place alone gives way", "망원\n퇴근 후", "", "망원", "모퉁이맥주", "모퉁이맥주\n퇴근 후"},
		{"empty memo", "", "", "", "모퉁이맥주", "모퉁이맥주"},
		{"no place known", "퇴근 후", "", "", "", "퇴근 후"},
		{"old 📍 line kept", "퇴근 후", "망원", "", "", "망원\n퇴근 후"},
		{"old 📍 line moved", "퇴근 후", "망원", "", "모퉁이맥주", "모퉁이맥주\n퇴근 후"},
	} {
		cal := NewMemoryCalendar()
		ev, _ := cal.Put(ctx, model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Memo: c.memo, Place: c.prevPlace, Owner: "shared"})
		was := model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Place: c.was}
		payload, _ := json.Marshal(model.CalendarAction{EventID: ev.ID, Was: &was,
			Event: model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Start: "19:00", Place: c.place, Memo: "jarvis 가 대화에서 찾은 일정 · “그래용”", Owner: "shared"}})
		if _, _, err := (CalendarActuator{Cal: cal}).Execute(ctx, "calendar.update", payload); err != nil {
			t.Fatal(err)
		}
		got, _ := cal.Get(ctx, ev.ID)
		if got.Memo != c.want || got.Place != "" || got.Start != "19:00" {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// An entry already there is taken for the one a card would add when the rules
// call it the same event. One they leave open - its title holds the card's or
// is held in it, or is the same at another hour - is Same's to settle, and
// without that answer it is not taken: the all-day "지수 생일" does not swallow
// the cake pickup at 10:30 on it, nor "회의" at 10:00 the one at 15:00. Parts
// of one outing are not asked about at all.
func TestAnEntryAlreadyThereIsTheSameEvent(t *testing.T) {
	ctx := context.Background()
	cal := NewMemoryCalendar()
	put := func(e model.CalendarEvent) model.CalendarEvent {
		t.Helper()
		saved, err := cal.Put(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	birthday := put(model.CalendarEvent{ID: "5d0c1e2a-7b3f-4c1d-9e8a-2f6b4a1c0d9e#2026-11-21", Title: "지수 생일", Date: "2026-11-21", Owner: "shared"})
	put(model.CalendarEvent{Title: "회의", Date: "2026-11-17", Start: "10:00"})
	dentist := put(model.CalendarEvent{Title: "치과 진료", Date: "2026-11-18", Start: "15:00"})
	put(model.CalendarEvent{Title: "지수와 점심", Date: "2026-11-19", Start: "12:00"})
	netflix := put(model.CalendarEvent{Title: "Netflix 플랜 변경·해지", Date: "2026-11-15", Start: "20:00"})

	pickup := model.CalendarEvent{Title: "지수 생일 케이크 픽업", Date: "2026-11-21", Start: "10:30", Place: "케이크가게"}
	meeting := model.CalendarEvent{Title: "회의", Date: "2026-11-17", Start: "15:00"}
	vague := model.CalendarEvent{Title: "치과", Date: "2026-11-18"}
	cafe := model.CalendarEvent{Title: "지수와 카페", Date: "2026-11-19", Start: "14:00"}
	renewal := model.CalendarEvent{Title: "Netflix 플랜 변경·해지", Date: "2026-11-15"}

	var asked []string
	answering := func(same bool) CalendarActuator {
		asked = nil
		return CalendarActuator{Cal: cal, Same: func(_ context.Context, a, b model.State) bool {
			asked = append(asked, a.Title+" / "+b.Title)
			return same
		}}
	}
	// already is the entry Preview links the event to, if any.
	already := func(c CalendarActuator, e model.CalendarEvent) string {
		t.Helper()
		payload, _ := json.Marshal(model.CalendarAction{Event: e})
		_, err := c.Preview(ctx, model.CalendarCreate, payload)
		var a *Already
		if errors.As(err, &a) {
			return a.ID
		}
		if err != nil {
			t.Fatal(err)
		}
		return ""
	}

	// The rules alone.
	rules := CalendarActuator{Cal: cal}
	for _, c := range []struct {
		name string
		e    model.CalendarEvent
		want string
	}{
		{"종일 생일은 그날의 픽업이 아님", pickup, ""},
		{"다섯 시간 떨어진 같은 이름", meeting, ""},
		{"이름을 품기만 한 항목은 판단 없이는 아님", vague, ""},
		{"한 만남의 다른 부분", cafe, ""},
		{"시각 없이 말한 같은 이름", renewal, netflix.ID},
	} {
		if got := already(rules, c.e); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	// What the rules leave open is asked, and the answer stands.
	if got := already(answering(false), pickup); got != "" || len(asked) != 1 || asked[0] != "지수 생일 케이크 픽업 / 지수 생일" {
		t.Fatalf("생일과 픽업은 판단에 묻고, 다르다면 따로: %q %v", got, asked)
	}
	if got := already(answering(false), meeting); got != "" || len(asked) != 1 {
		t.Fatalf("같은 이름의 다른 시각은 판단에 묻습니다: %q %v", got, asked)
	}
	if got := already(answering(true), vague); got != dentist.ID {
		t.Fatalf("같다고 하면 이미 있는 항목에 잇습니다: %q %v", got, asked)
	}
	if got := already(answering(true), cafe); got != "" || len(asked) != 0 {
		t.Fatalf("한 만남의 다른 부분은 묻지도 않습니다: %q %v", got, asked)
	}
	if got := already(answering(false), renewal); got != netflix.ID || len(asked) != 0 {
		t.Fatalf("규칙이 같다고 하면 묻지 않습니다: %q %v", got, asked)
	}

	// Approved, the pickup goes on the calendar beside the birthday.
	payload, _ := json.Marshal(model.CalendarAction{Event: pickup})
	res, _, err := answering(false).Execute(ctx, model.CalendarCreate, payload)
	if err != nil {
		t.Fatalf("픽업은 새 항목입니다: %v", err)
	}
	if day, _ := cal.Range(ctx, "2026-11-21", "2026-11-21"); len(day) != 2 || string(res) == "" {
		t.Fatalf("생일 옆에 픽업이 들어가야 합니다: %v %s", day, res)
	}
	if got, _ := cal.Get(ctx, birthday.ID); got.Title != "지수 생일" || got.Start != "" {
		t.Fatalf("생일은 그대로: %+v", got)
	}
}

// A move the entry already says is no question: the place in the memo, the
// same day and hour. One that changes anything is asked.
func TestAMoveAlreadyThereIsNotAsked(t *testing.T) {
	ctx := context.Background()
	cal := NewMemoryCalendar()
	ev, _ := cal.Put(ctx, model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Memo: "퇴근 후 · 망원", Owner: "shared"})
	ask := func(e model.CalendarEvent) error {
		payload, _ := json.Marshal(model.CalendarAction{EventID: ev.ID, Event: e})
		_, err := (CalendarActuator{Cal: cal}).Preview(ctx, "calendar.update", payload)
		return err
	}
	var already *Already
	if err := ask(model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Place: "망원", Memo: "jarvis 가 대화에서 찾은 일정", Owner: "shared"}); !errors.As(err, &already) || already.ID != ev.ID {
		t.Fatalf("메모에 있는 장소는 묻지 않습니다: %v", err)
	}
	for _, e := range []model.CalendarEvent{
		{Title: "은주와 저녁", Date: "2026-11-04", Place: "모퉁이맥주", Owner: "shared"},
		{Title: "은주와 저녁", Date: "2026-11-04", Start: "19:00", Place: "망원", Owner: "shared"},
		{Title: "은주와 저녁", Date: "2026-11-05", Place: "망원", Owner: "shared"},
		{Title: "은주와 저녁", Date: "2026-11-04", Place: "망원", Owner: "me"},
	} {
		if err := ask(e); err != nil {
			t.Fatalf("바뀌는 것은 묻습니다 %+v: %v", e, err)
		}
	}
	// Gone from the calendar: asked as before, and its run says so.
	payload, _ := json.Marshal(model.CalendarAction{EventID: "cal-404", Event: model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04"}})
	if _, err := (CalendarActuator{Cal: cal}).Preview(ctx, "calendar.update", payload); err != nil {
		t.Fatalf("읽지 못하면 전처럼 묻습니다: %v", err)
	}
}
