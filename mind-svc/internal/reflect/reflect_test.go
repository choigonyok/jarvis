package reflect

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
)

func TestFisher(t *testing.T) {
	// 2 of 14 late days with a workout, 30 of 46 other days.
	p := Fisher(2, 12, 30, 16)
	if p > 0.01 || p <= 0 {
		t.Fatalf("p = %v", p)
	}
	if p := Fisher(5, 5, 5, 5); math.Abs(p-1) > 1e-9 {
		t.Fatalf("같은 비율이면 p = 1: %v", p)
	}
}

// A bus taken on every late night - and a few early ones, where it makes no
// difference - is dropped as confounded with the late nights.
func TestConfounded(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	var pool []predict.Day
	day := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	for i := 0; len(pool) < 40; i++ {
		d := day.AddDate(0, 0, i)
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		n := len(pool)
		late := n%10 == 1 || n%10 == 4 || n%10 == 7
		bus, at := "버스 472", d.Add(18*time.Hour+35*time.Minute)
		workout := n%5 != 2
		if late {
			bus, at, workout = "버스 7016", d.Add(21*time.Hour+35*time.Minute), n == 11 || n == 34
		} else if n%8 == 0 {
			bus = "버스 7016"
		}
		pool = append(pool, predict.Day{Date: d.Format("2006-01-02"), Weekday: d.Weekday(), Workout: workout,
			Occ: []model.Occurrence{{Kind: "purchase", Class: "교통", Title: bus, At: at}}})
	}
	r := &Reflector{Loc: loc}
	late := &Hypothesis{Text: "21시 이후 퇴근", Feature: "late_leave", Arg: "21:00"}
	bus := &Hypothesis{Text: "7016 버스", Feature: "merchant", Arg: "7016"}
	r.test([]*Hypothesis{late, bus}, pool)
	if !late.Kept || bus.Kept || !strings.Contains(bus.Why, "교란") {
		t.Fatalf("늦은 퇴근은 남고 7016 은 교란: %+v / %+v", late, bus)
	}
}
