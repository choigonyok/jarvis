package sensor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

func takeSetup(t *testing.T) (*Structured, *store.Memory, time.Time) {
	t.Helper()
	st := store.NewMemory()
	now := time.Date(2026, 10, 31, 10, 0, 0, 0, seoul)
	s := &Structured{Store: st, Observer: observer{st}, World: world.New(st, func() time.Time { return now }), Loc: seoul, Now: func() time.Time { return now }}
	return s, st, now
}

// A stay comes as a record and is an occurrence, as it was when mind went for it.
func TestTakeVisits(t *testing.T) {
	s, st, now := takeSetup(t)
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"visits": []map[string]any{{"id": 7, "region": "회사", "kind": "work", "enteredAt": now.Add(-time.Hour)}}})
	if err := s.Take(ctx, queue.Message{Kind: KindVisits, Payload: raw, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	o, err := st.Occurrence(ctx, "visit:7")
	if err != nil || o.Kind != "visit" || o.Place != "회사" || o.Class != "work" || o.Area != "회사" {
		t.Fatalf("%+v %v", o, err)
	}
}

// What is only kept is there for whoever asks, and the older of two does not
// take the place of the newer.
func TestTakeKeepsTheLatest(t *testing.T) {
	s, st, now := takeSetup(t)
	ctx := context.Background()
	newer := json.RawMessage(`{"visits":[{"region":"집"}]}`)
	older := json.RawMessage(`{"visits":[{"region":"회사"}]}`)
	if err := s.Take(ctx, queue.Message{Kind: KindNow, Payload: newer, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.Take(ctx, queue.Message{Kind: KindNow, Payload: older, OccurredAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, at, err := st.Latest(ctx, KindNow)
	if err != nil || string(got) != string(newer) || !at.Equal(now) {
		t.Fatalf("%s %v %v", got, at, err)
	}
	if _, _, err := st.Latest(ctx, KindRegions); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

// What cannot be read will not be readable next time.
func TestTakeRefusesWhatItCannotRead(t *testing.T) {
	s, _, now := takeSetup(t)
	for _, kind := range []string{KindVisits, KindCalendarNear, KindCalendarYear, KindHoldings} {
		err := s.Take(context.Background(), queue.Message{Kind: kind, Payload: json.RawMessage(`[1]`), OccurredAt: now})
		if !errors.Is(err, queue.ErrRefused) {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}

// The calendar's read makes the plans, and an entry gone from a later read
// takes its plan with it - as when mind read the calendar itself.
func TestTakeCalendarKeepsPlansInStep(t *testing.T) {
	s, _, now := takeSetup(t)
	ctx := context.Background()
	from, to := now.AddDate(0, 0, -7).Format("2006-01-02"), now.AddDate(0, 2, 0).Format("2006-01-02")
	read := func(events ...map[string]any) queue.Message {
		raw, _ := json.Marshal(map[string]any{"from": from, "to": to, "events": events})
		return queue.Message{Kind: KindCalendarNear, Payload: raw, OccurredAt: now}
	}
	day := now.AddDate(0, 0, 3).Format("2006-01-02")
	if err := s.Take(ctx, read(map[string]any{"id": "e1", "title": "치과", "date": day, "start": "10:00"})); err != nil {
		t.Fatal(err)
	}
	plans, err := s.World.Entities(ctx, model.KindPlan, CalendarRoom, true)
	if err != nil || len(plans) != 1 {
		t.Fatalf("%+v %v", plans, err)
	}
	if err := s.Take(ctx, read()); err != nil {
		t.Fatal(err)
	}
	plans, _ = s.World.Entities(ctx, model.KindPlan, CalendarRoom, true)
	if len(plans) != 0 {
		t.Fatalf("지워진 일정의 계획이 남았습니다: %+v", plans)
	}
}
