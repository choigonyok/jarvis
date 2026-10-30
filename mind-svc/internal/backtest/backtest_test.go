package backtest

import (
	"math"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func base(v float64) *float64 { return &v }

func TestScoreHoldsConfidenceToWhatHappened(t *testing.T) {
	// Sure of itself, and right one time in four.
	cases := []Case{
		{P: 0.8, Hit: true}, {P: 0.8}, {P: 0.8}, {P: 0.8},
	}
	l := Score("x", cases)
	if l.N != 4 || l.Hits != 1 || !near(l.Rate, 0.25) || !near(l.MeanP, 0.8) {
		t.Fatalf("%+v", l)
	}
	if !near(l.Gap(), 0.55) {
		t.Fatalf("어긋남: %v", l.Gap())
	}
	if !near(l.Brier, (0.04+3*0.64)/4) {
		t.Fatalf("Brier: %v", l.Brier)
	}
	// Always saying 25% would have scored the variance of the outcome.
	if !near(l.Plain, 0.1875) || l.Brier <= l.Plain {
		t.Fatalf("평소: %v, Brier %v", l.Plain, l.Brier)
	}
}

func TestPlainUsesACaseOwnBase(t *testing.T) {
	l := Score("x", []Case{{P: 0.5, Hit: true, Base: base(0.9)}})
	if !near(l.Plain, 0.01) {
		t.Fatalf("%v", l.Plain)
	}
}

func TestAnEmptySetScoresNothing(t *testing.T) {
	if l := Score("x", nil); l.N != 0 || l.Brier != 0 {
		t.Fatalf("%+v", l)
	}
}

func TestByGroupKeepsSourcesApart(t *testing.T) {
	lines := ByGroup([]Case{
		{Source: "card", Group: "a"}, {Source: "card", Group: "a"}, {Source: "prediction", Group: "a"},
	})
	if len(lines) != 2 || lines[0].Group != "card · a" || lines[0].N != 2 {
		t.Fatalf("%+v", lines)
	}
}

func TestCalibrationPutsTheEdgesInSpans(t *testing.T) {
	bins := Calibration([]Case{{P: 0, Hit: true}, {P: 1}, {P: 0.5}}, 5)
	if bins[0].N != 1 || bins[0].Hits != 1 || bins[4].N != 1 || bins[2].N != 1 {
		t.Fatalf("%+v", bins)
	}
}

// What a constant set elsewhere would have done, without running anything.
func TestAtTriesAThreshold(t *testing.T) {
	cases := []Case{{P: 0.9, Hit: true}, {P: 0.8}, {P: 0.6, Hit: true}, {P: 0.5}}
	cut := At(cases, 0.7)
	if cut.Kept != 2 || cut.Hits != 1 || cut.Missed != 1 || !near(cut.Precision(), 0.5) {
		t.Fatalf("%+v", cut)
	}
	if none := At(nil, 0.7); none.Precision() != 0 {
		t.Fatalf("%+v", none)
	}
}

func TestReadAndReport(t *testing.T) {
	cases, err := Read(strings.NewReader(`{"source":"card","group":"schedule/calendar.create","p":0.8,"hit":false,"at":"2026-10-30T01:00:00Z"}

{"source":"prediction","group":"purchase","p":0.6,"hit":true,"base":0.7,"at":"2026-10-30T01:00:00Z"}
`))
	if err != nil || len(cases) != 2 || cases[1].Base == nil {
		t.Fatalf("%v %+v", err, cases)
	}
	out := Report(cases)
	for _, want := range []string{"판단 2건", "card · schedule/calendar.create", "표본 적음", "확신 구간별"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, out)
		}
	}
	if _, err := Read(strings.NewReader("{")); err == nil || !strings.Contains(err.Error(), "1번째 줄") {
		t.Fatalf("어느 줄인지 말해야 합니다: %v", err)
	}
}
