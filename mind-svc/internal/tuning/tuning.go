// Package tuning is the numbers the judgement engine and the interpreter are
// tuned by, gathered in one struct: what each one decides, and the value it
// ships with. They are code, not the registry's rows: the operator edits the
// registry (registry/seed.json, mind.registry), and these move with a deploy,
// once the ledger's trial (engine.Walk, backtest) shows a new value doing
// better.
package tuning

import "time"

// Numbers is every tuning number, by the layer that reads it.
type Numbers struct {
	Engine    Engine
	Interpret Interpret
}

// Engine is how the judgement engine turns a corrected confidence into how far
// an action may go (engine).
type Engine struct {
	// MinCases is how many answers a kind needs before it may be promoted past
	// asking, or go silent.
	MinCases int
	// Strength is how many answers it takes for a group to speak half for
	// itself against what it pools with: a few answers move a judgement a
	// little, many move it to what they say.
	Strength float64
	// Below SilentBelow a settled kind is not raised; from FillAbove the
	// answer is filled in; from ActAbove it is done.
	SilentBelow, FillAbove, ActAbove float64
	// NoticeStated is what a notice is taken to be worth before anyone said:
	// most are wanted. Its record moves it like a card's (ledger: a notice the
	// operator said helped is approved, one not needed rejected).
	NoticeStated float64
	// ExploreEvery is one silent judgement in so many asked anyway. Without it
	// a kind gone silent has no answers left to come back on.
	ExploreEvery uint32
}

// Interpret is how much the interpreter reads at once, what a read is shown,
// what it believes, and how it backs off a read that failed (interpret).
type Interpret struct {
	// MaxBatch is how many waiting messages are read at once - and how many
	// waiting make a room due at once, since a busy group chat may never fall
	// quiet. Context is how many earlier messages a read is given beside them,
	// and MinConf the confidence a change needs to be applied. These three are
	// where interpret.Config starts when it leaves them zero.
	MaxBatch int
	Context  int
	MinConf  float64
	// QuarantineAfter is how many failed reads in a row a single message gets
	// before it is skipped, so one unreadable message cannot spend the day's
	// budget.
	QuarantineAfter int
	// A failed read is tried again after RetryFirst, doubling with each failure
	// in a row up to RetryMax. With the day's budget spent or no key, it waits
	// RetryNoBudget and counts no failure.
	RetryFirst, RetryMax, RetryNoBudget time.Duration
	// CalendarDays is how far ahead the calendar's plans are shown - as far as
	// the calendar is read (two months) - and CalendarMax how many at most.
	CalendarDays, CalendarMax int
	// HearsayConfidence caps a fact about the operator that only someone else
	// said: kept, so it can be asked about or confirmed later, but never taken
	// for something the operator said.
	HearsayConfidence float64
	// WeightyConfidence is what a weighty relation (married to, a parent of …)
	// needs to stand, besides its subject's own words.
	WeightyConfidence float64
}

// Default is the numbers as they ship.
func Default() Numbers {
	return Numbers{
		Engine: Engine{
			MinCases: 5, Strength: 8,
			SilentBelow: 0.10, FillAbove: 0.50, ActAbove: 0.90,
			NoticeStated: 0.8, ExploreEvery: 10,
		},
		Interpret: Interpret{
			MaxBatch: 40, Context: 24, MinConf: 0.5,
			QuarantineAfter: 4,
			RetryFirst:      time.Minute, RetryMax: 6 * time.Hour, RetryNoBudget: 30 * time.Minute,
			CalendarDays: 61, CalendarMax: 40,
			HearsayConfidence: 0.4, WeightyConfidence: 0.8,
		},
	}
}
