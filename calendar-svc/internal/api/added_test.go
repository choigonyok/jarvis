package api

import (
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// A new entry is told to whoever did not write it, and the tap lands on its day.
func TestAddedGoesToTheOtherPerson(t *testing.T) {
	e := event.Event{ID: "ev-1", Date: "2026-10-20", Start: "19:00", Title: "저녁 약속", Owner: event.OwnerShared}

	byGuest := added(e, true)
	if byGuest.Audience != "" || byGuest.Key != "calendar:ev-1" {
		t.Fatalf("손님 jarvis 가 쓴 일정은 운영자에게: %+v", byGuest)
	}
	byOperator := added(e, false)
	if byOperator.Audience != "guest" || byOperator.Key != "guest-calendar:ev-1" {
		t.Fatalf("운영자 쪽이 쓴 일정은 손님에게: %+v", byOperator)
	}
	if !strings.HasPrefix(byOperator.URL, "/calendar?date=2026-10-20&focus=event:ev-1") || byOperator.Body != "10-20 19:00 저녁 약속" {
		t.Fatalf("누르면 그날 그 일정으로: %q / %q", byOperator.URL, byOperator.Body)
	}
}
