package predict

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// The days carry the weather at home when it is known - the past as it
// was, a coming day as forecast - and "rain" holds on a day with enough
// of it.
func TestDaysCarryTheWeather(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	from := time.Date(2026, 10, 16, 0, 0, 0, 0, loc)
	asked := ""
	p := &Predictor{St: store.NewMemory(), Loc: loc, Now: func() time.Time { return from },
		Weather: func(_ context.Context, a, b string) (map[string]weather.Day, error) {
			asked = a + ".." + b
			return map[string]weather.Day{
				"2026-10-16": {Date: "2026-10-16", Precip: 12},
				"2026-10-17": {Date: "2026-10-17", Precip: 0.3},
			}, nil
		}}
	days, err := p.Days(context.Background(), from, from.AddDate(0, 0, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 || days[0].Weather == nil || days[1].Weather == nil || days[2].Weather != nil {
		t.Fatalf("days %+v (asked %s)", days, asked)
	}
	if days[0].Weather.Precip != 12 || days[1].Weather.Precip != 0.3 {
		t.Fatalf("each day its own weather: %+v %+v", days[0].Weather, days[1].Weather)
	}
	for _, c := range []struct {
		arg  string
		day  int
		want bool
	}{{"1", 0, true}, {"1", 1, false}, {"", 0, true}, {"20", 0, false}, {"1", 2, false}} {
		if got := Holds("rain", c.arg, days[c.day], loc); got != c.want {
			t.Errorf("rain(%q) on %s = %v", c.arg, days[c.day].Date, got)
		}
	}
	p.Weather = nil
	days, _ = p.Days(context.Background(), from, from.AddDate(0, 0, 1))
	if days[0].Weather != nil || Holds("rain", "1", days[0], loc) {
		t.Fatal("without the weather no day is rainy")
	}
}

// A day's features beyond the first seven: who it was with, where, what kind of
// spending, a workout - each tested on what the day holds.
func TestADayHoldsWhoWhereWhatAndAWorkout(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	d := Day{
		Occ: []model.Occurrence{
			{Kind: "purchase", Title: "망원 국숫집", Class: "식비", Area: "망원"},
			{Kind: "visit", Title: "가평"},
			{Kind: "workout", Title: "러닝 5km"},
		},
		Plans: []model.Entity{{State: model.State{Title: "민수와 저녁", People: []string{"민수"}, Place: "합정"}}},
	}
	for _, c := range []struct {
		feature, arg string
		want         bool
	}{
		{"with", "민수", true}, {"with", "지수", false}, {"place", "망원", true}, {"place", "가평", true}, {"place", "합정", true},
		{"place", "강남", false}, {"place", "", false}, {"class", "식비", true}, {"class", "교통", false},
		{"workout", "", true}, {"workout", "러닝", true}, {"workout", "수영", false},
	} {
		if got := Holds(c.feature, c.arg, d, seoul); got != c.want {
			t.Errorf("%s(%q): %v", c.feature, c.arg, got)
		}
	}
	for _, f := range []string{"with", "place", "class", "workout"} {
		if Features[f] == "" {
			t.Fatalf("%s 를 성찰이 써야 합니다", f)
		}
	}
}
