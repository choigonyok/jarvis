// Package engine turns how sure a judgement says it is into how far it may
// go. A stated confidence is an opinion: the result ledger says how such
// opinions have gone, and the engine corrects each kind of judgement by that
// record - the 일정 추가 card that says 66% and is approved 21% of the time -
// and grades it: stay silent, ask with the form empty, ask with it filled in,
// or do it and say so.
//
// The rules are the design's, in code the model cannot reach:
//
//   - A new kind asks. Nothing is promoted past asking before MinCases.
//   - What cannot be undone is asked forever, whatever its record.
//   - Ignored is not refused: only answers move a kind's record.
//   - A kind turned down again and again goes silent, but keeps a small chance
//     of being asked, or it could never come back.
//
// It reads the ledger and holds no state of its own: a Book is built from the
// rows and replaced whole.
package engine

import (
	"hash/fnv"
	"math"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Grade is how far an action may go, in order.
type Grade string

const (
	Silent    Grade = "silent"     // said to no one, and scored later
	AskBlank  Grade = "ask-blank"  // asked, with the form empty
	AskFilled Grade = "ask-filled" // asked, with the answer filled in
	Act       Grade = "act"        // done, then told
)

func (g Grade) rank() int {
	switch g {
	case AskBlank:
		return 1
	case AskFilled:
		return 2
	case Act:
		return 3
	}
	return 0
}

func lower(a, b Grade) Grade {
	if a.rank() < b.rank() {
		return a
	}
	return b
}

// Spec is what a kind of action is, for grading.
type Spec struct {
	Reversible bool
	// Cap is the most it may ever do; empty is no limit.
	Cap Grade
}

const (
	// MinCases is how many answers a kind needs before it may be promoted
	// past asking, or go silent.
	MinCases = 5
	// strength is how many answers it takes for a group to speak half for
	// itself against what it pools with: a few answers move a judgement a
	// little, many move it to what they say.
	strength = 8
	// Below SilentBelow a settled kind is not raised; from FillAbove the
	// answer is filled in; from ActAbove it is done.
	SilentBelow = 0.10
	FillAbove   = 0.50
	ActAbove    = 0.90
	// exploreEvery is one silent judgement in so many asked anyway. Without
	// it a kind gone silent has no answers left to come back on.
	exploreEvery = 10
)

// tally is one group's record.
type tally struct {
	n, hits int
	stated  float64
}

func (t *tally) add(p float64, hit bool) {
	t.n++
	t.stated += p
	if hit {
		t.hits++
	}
}

// shift is how far the group's answers sit from what it said, in log-odds.
// The half keeps a group that was always right or always wrong finite.
func (t tally) shift() float64 {
	rate := (float64(t.hits) + 0.5) / (float64(t.n) + 1)
	return logit(rate) - logit(t.stated/float64(t.n))
}

func logit(p float64) float64 {
	p = math.Min(math.Max(p, 0.02), 0.98)
	return math.Log(p / (1 - p))
}

func logistic(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

// Key is what a judgement is, broad to narrow: how sure did judgements of this
// kind turn out to be, and those of this skill in this situation.
type Key struct {
	Source  string // card | told
	Kind    string
	Skill   string
	Context string
}

func (k Key) levels() [2]string {
	return [2]string{k.Source + "/" + k.Kind, k.Source + "/" + k.Skill + "/" + k.Context}
}

func keyOf(o model.Outcome) Key {
	return Key{Source: o.Source, Kind: o.Kind, Skill: o.Skill, Context: o.Context}
}

// Book is the ledger's record, by group. A nil Book knows nothing, and leaves
// every judgement as it was stated.
type Book struct {
	groups map[string]*tally
}

// NewBook reads the ledger. Only answers count: what was ignored, replaced or
// not yet answered says nothing about the judgement. Predictions are not
// here: they are scored against the world and already calibrated by the rule
// that makes them.
func NewBook(rows []model.Outcome) *Book {
	b := &Book{groups: map[string]*tally{}}
	for _, o := range rows {
		b.Add(o)
	}
	return b
}

// Add counts one row. What was held back and the operator then did by hand
// counts as a card that would have been approved: without it a judgement that
// stays quiet could never be wrong, and nothing would bring it back.
func (b *Book) Add(o model.Outcome) {
	hit, ok := o.Scored()
	if o.Source == "quiet" && o.Manual {
		o.Source, hit, ok = "card", true, true
	}
	if !ok || o.Source == "prediction" {
		return
	}
	for _, key := range keyOf(o).levels() {
		t := b.groups[key]
		if t == nil {
			t = &tally{}
			b.groups[key] = t
		}
		t.add(o.P, hit)
	}
}

// Reading is a judgement after correction: the probability to act on, and how
// many answers stand behind it.
type Reading struct {
	P float64
	N int
}

// Read corrects a stated confidence by the record of its group. Each level
// pulls the correction toward its own record as far as its answers allow, so
// a kind with no history stays what it said.
func (b *Book) Read(k Key, stated float64) Reading {
	if b == nil {
		return Reading{P: stated}
	}
	shift, n := 0.0, 0
	for _, key := range k.levels() {
		t := b.groups[key]
		if t == nil {
			continue
		}
		weight := float64(t.n) / (float64(t.n) + strength)
		shift = weight*t.shift() + (1-weight)*shift
		n = t.n
	}
	if n == 0 {
		return Reading{P: stated}
	}
	return Reading{P: logistic(logit(stated) + shift), N: n}
}

// Explored says whether this silent judgement is the one in exploreEvery that
// is asked anyway. The id decides, so a replay comes to the same answer.
func Explored(id string) bool {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return h.Sum32()%exploreEvery == 0
}

// Decide grades a reading for a kind of action.
func Decide(s Spec, r Reading, explore bool) Grade {
	var g Grade
	switch {
	case r.P >= ActAbove:
		g = Act
	case r.P >= FillAbove:
		g = AskFilled
	case r.P >= SilentBelow || r.N < MinCases:
		g = AskBlank
	case explore:
		g = AskBlank
	default:
		g = Silent
	}
	if r.N < MinCases {
		g = lower(g, AskFilled)
	}
	if !s.Reversible {
		g = lower(g, AskFilled)
	}
	if s.Cap != "" {
		g = lower(g, s.Cap)
	}
	return g
}
