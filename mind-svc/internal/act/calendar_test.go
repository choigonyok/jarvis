package act

import (
	"context"
	"encoding/json"
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
