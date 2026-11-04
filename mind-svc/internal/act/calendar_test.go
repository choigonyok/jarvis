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

// The place lives in the memo (calendar-svc, 2026-10-30). A move to another
// place puts it on the memo's first line and keeps what the memo said: the
// assistant's "퇴근 후 · 망원" stayed under 모퉁이맥주 (2026-11-04). The old place,
// alone on its line, gives way; a plan that knows no place takes none off.
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
