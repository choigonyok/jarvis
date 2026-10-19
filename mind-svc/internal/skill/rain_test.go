package skill

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

var (
	wet = weather.Day{Code: 63, Prob: 80, Precip: 12.4, Min: 10.6, Max: 17.5}
	dry = weather.Day{Code: 1, Prob: 10, Min: 9, Max: 19}
)

// on is a day's weather on a date.
func on(d weather.Day, date string) weather.Day { d.Date = date; return d }

// A wet morning: the brief opens with the weather and when the rain comes,
// and says so in its title.
func TestBriefSaysUmbrella(t *testing.T) {
	morning := time.Date(2026, 10, 18, 8, 0, 0, 0, seoul)
	plan := model.Entity{ID: "p1", Kind: model.KindPlan, State: model.State{Title: "합주", Status: model.Confirmed, Date: "2026-10-18", Start: "19:00"}}
	v := view{plans: []model.Entity{plan}, now: morning, sky: map[string]weather.Day{" 2026-10-18": on(wet, "2026-10-18")},
		hours: []weather.Hour{{At: morning.Add(2 * time.Hour), Prob: 30}, {At: morning.Add(7 * time.Hour), Prob: 75}}}
	r := Brief{}.OnTick(context.Background(), v, "morning")
	if len(r.Intents) != 1 {
		t.Fatalf("one brief: %+v", r.Intents)
	}
	in := r.Intents[0]
	if in.Title != "오늘 일정 1개 · 챙길 일 0개 · 비 80% · 12mm" {
		t.Errorf("title %q", in.Title)
	}
	if first := strings.SplitN(in.Body, "\n", 2)[0]; first != "· 날씨: 비 80% · 12mm (15시부터) · 11~18° - 우산 챙기세요" {
		t.Errorf("first line %q", first)
	}
}

// Rain is worth a morning's word even with nothing on; a dry day with
// nothing on is not.
func TestBriefOnWeatherAlone(t *testing.T) {
	morning := time.Date(2026, 10, 18, 8, 0, 0, 0, seoul)
	v := view{now: morning, sky: map[string]weather.Day{" 2026-10-18": on(wet, "2026-10-18")}}
	r := Brief{}.OnTick(context.Background(), v, "morning")
	if len(r.Intents) != 1 || r.Intents[0].Title != "오늘 비 80% · 12mm · 우산 챙기세요" || !r.Intents[0].Urgent {
		t.Fatalf("a wet day alone: %+v", r.Intents)
	}
	v.sky = map[string]weather.Day{" 2026-10-18": on(dry, "2026-10-18")}
	if r := (Brief{}).OnTick(context.Background(), v, "morning"); len(r.Intents) != 0 {
		t.Fatalf("a dry day with nothing on says nothing: %+v", r.Intents)
	}
	plan := model.Entity{ID: "p1", Kind: model.KindPlan, State: model.State{Title: "합주", Status: model.Confirmed, Date: "2026-10-18"}}
	v.plans = []model.Entity{plan}
	r = Brief{}.OnTick(context.Background(), v, "morning")
	if len(r.Intents) != 1 || !strings.HasPrefix(r.Intents[0].Body, "· 날씨: 대체로 맑음 · 9~19°\n") || strings.Contains(r.Intents[0].Title, "비") {
		t.Fatalf("a dry day's brief: %+v", r.Intents)
	}
}

func camping(calendarID string) model.Entity {
	return model.Entity{ID: "p1", Kind: model.KindPlan, State: model.State{Title: "캠핑", Status: model.Confirmed, Type: "캠핑",
		Date: "2026-10-24", EndDate: "2026-10-25", Place: "자라섬 캠핑장", Area: "가평", Shared: true, CalendarID: calendarID}}
}

// Two days before a camping trip the forecast rains on, with a dry weekend
// a week on: a card that moves it there, the whole trip, asked every time.
func TestRainMovesATripOnTheCalendar(t *testing.T) {
	thursday := time.Date(2026, 10, 22, 8, 0, 0, 0, seoul)
	v := view{plans: []model.Entity{camping("c1")}, now: thursday, sky: map[string]weather.Day{
		"가평 2026-10-24": on(wet, "2026-10-24"), "가평 2026-10-25": on(dry, "2026-10-25"),
		// The very next days are dry but not a weekend; Sunday-Monday is
		// not the trip's shape.
		"가평 2026-10-26": on(dry, "2026-10-26"), "가평 2026-10-27": on(dry, "2026-10-27"),
		"가평 2026-10-31": on(dry, "2026-10-31"), "가평 2026-11-01": on(dry, "2026-11-01"),
	}}
	r := Rain{}.OnTick(context.Background(), v, "morning")
	if len(r.Intents) != 1 {
		t.Fatalf("one card: %+v", r.Intents)
	}
	in := r.Intents[0]
	if in.Kind != "calendar.update" || in.Context != "rain" || in.Topic != "rain:p1:2026-10-24" || in.EntityID != "p1" {
		t.Fatalf("card %+v", in)
	}
	if in.Title != "'캠핑'를 10/31(토)~11/1(일)로 옮길까요?" {
		t.Errorf("title %q", in.Title)
	}
	if !strings.Contains(in.Body, "10/24(토)~10/25(일) 캠핑 · 자라섬 캠핑장 · 비 80% · 12mm 예보") || !strings.Contains(in.Body, "우비·타프") {
		t.Errorf("body %q", in.Body)
	}
	var a model.CalendarAction
	_ = json.Unmarshal(in.Payload, &a)
	if a.EventID != "c1" || a.Event.Date != "2026-10-31" || a.Event.EndDate != "2026-11-01" {
		t.Errorf("payload %+v", a)
	}
	if len(in.Fields) != 1 || in.Fields[0].Key != "date" || in.Fields[0].Value != "2026-10-31" {
		t.Errorf("the date can be changed before saying yes: %+v", in.Fields)
	}
}

// Not on the calendar, or no dry weekend in reach: a line to pack for it.
func TestRainLine(t *testing.T) {
	thursday := time.Date(2026, 10, 22, 8, 0, 0, 0, seoul)
	sky := map[string]weather.Day{"가평 2026-10-24": on(wet, "2026-10-24"), "가평 2026-10-25": on(dry, "2026-10-25")}
	for _, cal := range []string{"", "c1"} {
		r := Rain{}.OnTick(context.Background(), view{plans: []model.Entity{camping(cal)}, now: thursday, sky: sky}, "morning")
		if len(r.Intents) != 1 || r.Intents[0].Kind != "info" || !r.Intents[0].Urgent ||
			r.Intents[0].Title != "10/24(토)~10/25(일) 캠핑 · 자라섬 캠핑장에 비 80% · 12mm 예보" {
			t.Fatalf("calendar %q: %+v", cal, r.Intents)
		}
		if said := strings.Contains(r.Intents[0].Body, "맑은 주말이 없어요"); said != (cal != "") {
			t.Errorf("calendar %q: body %q", cal, r.Intents[0].Body)
		}
	}
}

// Only a plan out of doors, of the operator's, two days off at most, that
// the forecast rains on.
func TestRainLooksOnlyAtWhatItShould(t *testing.T) {
	thursday := time.Date(2026, 10, 22, 8, 0, 0, 0, seoul)
	sky := map[string]weather.Day{"가평 2026-10-24": on(wet, "2026-10-24"), "강남 2026-10-24": on(wet, "2026-10-24"),
		"가평 2026-10-27": on(wet, "2026-10-27")}
	dinner := model.Entity{ID: "p2", Kind: model.KindPlan, State: model.State{Title: "회식", Status: model.Confirmed, Type: "회식", Date: "2026-10-24", Area: "강남"}}
	theirs := camping("c1")
	theirs.State.Owner = "partner"
	later := camping("c1")
	later.State.Date, later.State.EndDate = "2026-10-27", ""
	proposed := camping("c1")
	proposed.State.Status = model.Proposed
	for name, p := range map[string]model.Entity{"indoors": dinner, "partner's": theirs, "five days off": later, "not agreed": proposed} {
		if r := (Rain{}).OnTick(context.Background(), view{plans: []model.Entity{p}, now: thursday, sky: sky}, "morning"); len(r.Intents) != 0 {
			t.Errorf("%s: %+v", name, r.Intents)
		}
	}
	dryTrip := view{plans: []model.Entity{camping("c1")}, now: thursday, sky: map[string]weather.Day{"가평 2026-10-24": on(dry, "2026-10-24")}}
	if r := (Rain{}).OnTick(context.Background(), dryTrip, "morning"); len(r.Intents) != 0 {
		t.Errorf("a dry trip: %+v", r.Intents)
	}
	if r := (Rain{}).OnTick(context.Background(), view{plans: []model.Entity{camping("c1")}, now: thursday}, "morning"); len(r.Intents) != 0 {
		t.Errorf("weather not known: %+v", r.Intents)
	}
}

func TestOutdoors(t *testing.T) {
	for _, c := range []struct {
		st   model.State
		want bool
	}{
		{model.State{Title: "캠핑", Type: "캠핑"}, true},
		{model.State{Title: "한강 피크닉", Type: "데이트"}, true},
		{model.State{Title: "저녁", Type: "데이트", PlaceKind: "공원"}, true},
		{model.State{Title: "야구 직관", Type: "공연"}, true},
		{model.State{Title: "회식", Type: "회식", PlaceKind: "식당"}, false},
		{model.State{Title: "PT", Type: "운동", PlaceKind: "헬스장"}, false},
	} {
		if got := Outdoors(c.st); got != c.want {
			t.Errorf("%+v: %v", c.st, got)
		}
	}
}
