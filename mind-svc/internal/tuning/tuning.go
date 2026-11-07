// Package tuning is the numbers the loop judges and reads by - the judgement
// engine's, the gate's, the interpreter's and the result ledger's - gathered
// in one struct: what each one decides, and the value it ships with.
//
// They are code, not the registry's rows. The registry is what the operator
// changes: a row of mind.registry, written by the registry.write command and
// read at once (registry/seed.json is only what ships with the code, for the
// rows nobody has changed). These move with a deploy, once a trial
// (engine.Walk, backtest, the scripted eval) shows a new value doing better.
// Four of them an environment variable sets on a machine (MIND_IDLE,
// MIND_IDLE_SELF, MIND_IDLE_SIM, MIND_WEB_SEARCH); their defaults are here.
package tuning

import "time"

// Numbers is every tuning number, by the layer that reads it.
type Numbers struct {
	Engine    Engine
	Gate      Gate
	Interpret Interpret
	Ledger    Ledger
	Turned    Turned
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

// Gate is what the gate weighs beside its registry row, which holds the day's
// limits, what a card costs and how long a topic stays quiet (gate).
type Gate struct {
	// LowValue is the value below which a thing done and told does not ring
	// now: it waits for the evening (a charge filed the usual way).
	LowValue float64
	// BusyCost is how many times its cost a card has to be worth while the
	// operator is busy - driving, in a meeting, in a focus - to ring then;
	// what is worth less waits for the evening.
	BusyCost float64
	// AlikeWithin is how far back an open calendar question is looked for by
	// its entry: one older than this is stale whatever it asked.
	AlikeWithin time.Duration
	// AskedWithin is how long a card raised in the conversation speaks for its
	// question, answered or not: mind reads the same conversation a minute
	// later, and turned down there, it is not asked again here.
	AskedWithin time.Duration
}

// Interpret is how much the interpreter reads at once, when it reads, what a
// read is shown, what it believes, and how it backs off a read that failed
// (interpret).
type Interpret struct {
	// A room is read once it has been quiet this long: a conversation with
	// someone (Idle), a notes-to-self room, where nobody is going to answer
	// (IdleSelf), the console's simulator (IdleSim). MIND_IDLE,
	// MIND_IDLE_SELF and MIND_IDLE_SIM set them on a machine.
	Idle, IdleSelf, IdleSim time.Duration
	// MaxBatch is how many waiting messages are read at once - and how many
	// waiting make a room due at once, since a busy group chat may never fall
	// quiet. Context is how many earlier messages a read is given beside them,
	// and MinConf the confidence a change needs to be applied. These three are
	// where interpret.Config starts when it leaves them zero.
	MaxBatch int
	Context  int
	MinConf  float64
	// Search is how many web searches a read may make, offered only when a
	// calendar plan shown has no place (MIND_WEB_SEARCH; 0 offers none).
	Search int
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
	// EndedDays is how many days a plan that ended is still shown to a read of
	// its room, so "다시 가기로 했어" revives it instead of starting another.
	EndedDays int
	// RecentFacts is how many of the newest facts a new one is held against:
	// the same words in the same room are not written twice.
	RecentFacts int
	// HearsayConfidence caps a fact about the operator that only someone else
	// said: kept, so it can be asked about or confirmed later, but never taken
	// for something the operator said.
	HearsayConfidence float64
	// WeightyConfidence is what a weighty relation (married to, a parent of …)
	// needs to stand, besides its subject's own words.
	WeightyConfidence float64
	// SettleDays is how many days after a shared bill's plan the money each
	// person owes for it is due: the loop that waits for it is due then.
	SettleDays int
}

// Ledger is when the result ledger takes a judgement as answered, which is
// what the engine is corrected by (ledger).
type Ledger struct {
	// IgnoredAfter: a card nobody answered for this long was ignored, which is
	// not refused.
	IgnoredAfter time.Duration
	// KeptAfter: an action done and told stands once this long has gone by
	// without an undo.
	KeptAfter time.Duration
	// ManualWithin is how long after a held-back purchase buying it by hand
	// still counts as that purchase.
	ManualWithin time.Duration
	// DueWindow is how long before its deadline a prediction that names no
	// window of its own (Prediction.From) is held to.
	DueWindow time.Duration
}

// Turned is how much of what the operator said turning calendar cards down
// is read again, by the interpreter on every read and by the gate before the
// conversation raises a card (gate.TurnedDown): from as far back as Within,
// the newest Max.
type Turned struct {
	Within time.Duration
	Max    int
}

// Default is the numbers as they ship.
func Default() Numbers {
	return Numbers{
		Engine: Engine{
			MinCases: 5, Strength: 8,
			SilentBelow: 0.10, FillAbove: 0.50, ActAbove: 0.90,
			NoticeStated: 0.8, ExploreEvery: 10,
		},
		Gate: Gate{
			LowValue: 0.5, BusyCost: 2,
			AlikeWithin: 30 * 24 * time.Hour, AskedWithin: 48 * time.Hour,
		},
		Interpret: Interpret{
			Idle: 3 * time.Minute, IdleSelf: 45 * time.Second, IdleSim: 4 * time.Second,
			MaxBatch: 40, Context: 24, MinConf: 0.5,
			Search:          2,
			QuarantineAfter: 4,
			RetryFirst:      time.Minute, RetryMax: 6 * time.Hour, RetryNoBudget: 30 * time.Minute,
			CalendarDays: 61, CalendarMax: 40,
			EndedDays: 14, RecentFacts: 300,
			HearsayConfidence: 0.4, WeightyConfidence: 0.8,
			SettleDays: 3,
		},
		Ledger: Ledger{
			IgnoredAfter: 7 * 24 * time.Hour, KeptAfter: 24 * time.Hour,
			ManualWithin: 14 * 24 * time.Hour, DueWindow: 24 * time.Hour,
		},
		Turned: Turned{Within: 60 * 24 * time.Hour, Max: 12},
	}
}
