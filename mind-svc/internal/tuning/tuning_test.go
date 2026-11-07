package tuning

import (
	"testing"
	"time"
)

// A number moves only on purpose: each one changes how the loop judges or
// reads, so a new value comes with the trial that chose it, and this test is
// changed with it.
func TestTheNumbersAreTheShippedOnes(t *testing.T) {
	want := Numbers{
		Engine: Engine{MinCases: 5, Strength: 8, SilentBelow: 0.10, FillAbove: 0.50, ActAbove: 0.90, NoticeStated: 0.8, ExploreEvery: 10},
		Interpret: Interpret{MaxBatch: 40, Context: 24, MinConf: 0.5, QuarantineAfter: 4,
			RetryFirst: time.Minute, RetryMax: 6 * time.Hour, RetryNoBudget: 30 * time.Minute,
			CalendarDays: 61, CalendarMax: 40, HearsayConfidence: 0.4, WeightyConfidence: 0.8},
	}
	if got := Default(); got != want {
		t.Fatalf("%+v", got)
	}
}
