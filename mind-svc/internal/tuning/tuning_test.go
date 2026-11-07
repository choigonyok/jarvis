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
		Gate:   Gate{LowValue: 0.5, BusyCost: 2, AlikeWithin: 30 * 24 * time.Hour, AskedWithin: 48 * time.Hour},
		Interpret: Interpret{Idle: 3 * time.Minute, IdleSelf: 45 * time.Second, IdleSim: 4 * time.Second,
			MaxBatch: 40, Context: 24, MinConf: 0.5, Search: 2, QuarantineAfter: 4,
			RetryFirst: time.Minute, RetryMax: 6 * time.Hour, RetryNoBudget: 30 * time.Minute,
			CalendarDays: 61, CalendarMax: 40, EndedDays: 14, RecentFacts: 300,
			HearsayConfidence: 0.4, WeightyConfidence: 0.8, SettleDays: 3},
		Ledger: Ledger{IgnoredAfter: 7 * 24 * time.Hour, KeptAfter: 24 * time.Hour, ManualWithin: 14 * 24 * time.Hour, DueWindow: 24 * time.Hour},
		Turned: Turned{Within: 60 * 24 * time.Hour, Max: 12},
	}
	if got := Default(); got != want {
		t.Fatalf("%+v", got)
	}
}
