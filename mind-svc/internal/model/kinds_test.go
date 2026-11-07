package model

import "testing"

// A calendar change is one of mind's or a card of the conversation's for one;
// a move is mind's update or the card for a move. A notice is neither.
func TestCalendarKindsAreToldApart(t *testing.T) {
	for kind, want := range map[string]struct{ calendar, update bool }{
		CalendarCreate:     {true, false},
		CalendarUpdate:     {true, true},
		CalendarDelete:     {true, false},
		HeldCalendarCreate: {true, false},
		HeldCalendarUpdate: {true, true},
		Info:               {false, false},
		"cart.add":         {false, false},
	} {
		if IsCalendar(kind) != want.calendar || IsCalendarUpdate(kind) != want.update {
			t.Errorf("%s: 캘린더 %v, 옮기기 %v 여야 합니다", kind, want.calendar, want.update)
		}
	}
}
