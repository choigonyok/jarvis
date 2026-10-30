package weather

import (
	"reflect"
	"testing"
	"time"
)

func TestWet(t *testing.T) {
	for _, c := range []struct {
		d    Day
		want bool
	}{
		{Day{Prob: 80, Precip: 12}, true},
		{Day{Prob: 80, Precip: 0.2}, false}, // likely, but a trace
		{Day{Prob: 40, Precip: 2}, false},   // a little, unlikely
		{Day{Prob: 30, Precip: 8}, true},    // plenty, whatever the odds
		{Day{Prob: 60, Snow: 1.4, Precip: 1, Code: 73}, true},
		{Day{Prob: 0, Precip: 0, Code: 0}, false},
	} {
		if got := c.d.Wet(); got != c.want {
			t.Errorf("%+v: Wet() = %v, want %v", c.d, got, c.want)
		}
	}
}

func TestLine(t *testing.T) {
	if got := (Day{Code: 61, Prob: 80, Precip: 12.4, Min: 10.6, Max: 17.5}).Line(); got != "비 80% · 12mm · 11~18°" {
		t.Errorf("wet line %q", got)
	}
	if got := (Day{Code: 0, Prob: 5, Min: 3, Max: 14}).Line(); got != "맑음 · 3~14°" {
		t.Errorf("dry line %q", got)
	}
	if got := (Day{Code: 73, Prob: 70, Precip: 2, Snow: 3}).Fall(); got != "눈 70%" {
		t.Errorf("snow %q", got)
	}
}

func TestNames(t *testing.T) {
	got := Names("", "자라섬 캠핑장")
	want := []string{"자라섬 캠핑장", "자라섬"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %v, want %v", got, want)
	}
	got = Names("망원동", "카페 하이")
	want = []string{"망원동", "카페 하이"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a first word is not a place: Names = %v, want %v", got, want)
	}
	if got := Names("", "집"); len(got) != 0 {
		t.Errorf("a one-letter place is no name to look up: %v", got)
	}
}

func TestFrom(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	day := time.Date(2026, 10, 18, 0, 0, 0, 0, loc)
	hours := []Hour{
		{At: day.Add(7 * time.Hour), Prob: 70},
		{At: day.Add(8 * time.Hour), Prob: 20},
		{At: day.Add(15 * time.Hour), Prob: 60},
	}
	at, ok := From(hours, day.Add(8*time.Hour+5*time.Minute))
	if !ok || at.Hour() != 15 {
		t.Errorf("From = %v %v, want 15:00", at, ok)
	}
	if _, ok := From(hours[:2], day.Add(8*time.Hour)); ok {
		t.Error("no rain after 8 o'clock")
	}
}
