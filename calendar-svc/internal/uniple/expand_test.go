package uniple

import (
	"reflect"
	"testing"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

func p(s string) *string { return &s }

func ids(rows []row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestExpandMonthlyClampsAndSkips(t *testing.T) {
	rows := []row{
		{ID: "pay", StartDate: "2026-01-31", Recurrence: p("monthly"), RecurrenceExdates: []string{"2026-03-31"}},
		// 4월 30일 하루를 대신하는 수정본. 그 자체는 평범한 일정이다.
		{ID: "pay-apr", StartDate: "2026-04-29", RecurrenceParentID: p("pay"), RecurrenceOverrideDate: p("2026-04-30")},
	}
	got := ids(expand(rows, "2026-02-01", "2026-05-31"))
	want := []string{"pay-apr", "pay#2026-02-28", "pay#2026-05-31"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandWeeklyKeepsLengthAndReachesBackIntoWindow(t *testing.T) {
	// 금-일 사흘짜리 주간 반복. 창의 첫날(월)에는 걸리지 않고, 일요일에 시작하는
	// 창이라면 전 주 금요일분이 걸쳐 들어와야 한다.
	rows := []row{{ID: "trip", StartDate: "2026-10-02", EndDate: p("2026-10-04"), Recurrence: p("weekly")}}
	got := expand(rows, "2026-10-11", "2026-10-17")
	if len(got) != 2 || got[0].ID != "trip#2026-10-09" || got[1].ID != "trip#2026-10-16" {
		t.Fatalf("got %v", ids(got))
	}
	if *got[0].EndDate != "2026-10-11" {
		t.Fatalf("occurrence lost its length: %v", *got[0].EndDate)
	}
}

func TestExpandYearlyRespectsUntilAndFrom(t *testing.T) {
	rows := []row{{ID: "bday", StartDate: "2020-02-29", Recurrence: p("yearly"),
		RecurrenceFrom: p("2025-01-01"), RecurrenceUntil: p("2027-12-31")}}
	got := ids(expand(rows, "2020-01-01", "2030-12-31"))
	want := []string{"bday#2025-02-28", "bday#2026-02-28", "bday#2027-02-28"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestToEventOwnerAndPlace(t *testing.T) {
	me := "u-me"
	r := row{ID: "x", Title: "데이트", StartDate: "2026-10-09", StartTime: p("19:00:00"),
		Memo: p("📍 성수\n7시 반까지"), CreatedBy: "u-her", OwnerUserID: nil}
	e := r.toEvent(me)
	if e.Start != "19:00" || e.Place != "성수" || e.Memo != "7시 반까지" {
		t.Fatalf("fields: %+v", e)
	}
	if e.Owner != event.OwnerShared || e.Source != event.SourcePartner {
		t.Fatalf("owner/source: %+v", e)
	}
	if joinMemo(e.Place, e.Memo) != "📍 성수\n7시 반까지" {
		t.Fatal("place does not round-trip through memo")
	}
}

func TestOwnerID(t *testing.T) {
	me, her := "u-me", "u-her"
	if o, _ := ownerID("", me, true, nil); o == nil || *o != me {
		t.Fatal("new entry should default to mine")
	}
	if o, _ := ownerID("", me, false, nil); o != nil {
		t.Fatal("editing a shared entry must keep it shared")
	}
	if _, err := ownerID(event.OwnerPartner, me, true, nil); err == nil {
		t.Fatal("must not create on the partner's name")
	}
	if o, _ := ownerID(event.OwnerPartner, me, false, &her); o == nil || *o != her {
		t.Fatal("keeping the partner's entry theirs should be allowed")
	}
}

func TestConflictSpan(t *testing.T) {
	a := event.Event{Start: "19:00", End: "22:00"}
	b := event.Event{Start: "21:00"}
	a0, a1 := span(a)
	b0, b1 := span(b)
	if !(a0 < b1 && b0 < a1) {
		t.Fatal("expected overlap")
	}
}
