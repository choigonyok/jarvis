package engine

import (
	"cmp"
	"slices"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Trial is the engine held to its own past: every answered judgement, in the
// order it was made, corrected only by what had been answered before it. It
// is the test a correction has to pass before it is turned on: the book must
// beat what the judgements said, on answers it had not seen.
type Trial struct {
	Cases int
	// Stated and Corrected are mean Brier scores, lower better.
	Stated, Corrected float64
	// Plain is what always saying the rate so far would have scored.
	Plain float64
	// Grades is how many would have got each grade, and Right how many of
	// those were approved.
	Grades map[Grade]int
	Right  map[Grade]int
}

// Walk runs the trial on ledger rows.
func Walk(rows []model.Outcome) Trial {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b model.Outcome) int {
		return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID))
	})
	t := Trial{Grades: map[Grade]int{}, Right: map[Grade]int{}}
	book := &Book{groups: map[string]*tally{}}
	var seen, hits int
	for _, o := range sorted {
		hit, ok := o.Scored()
		if !ok || o.Source == "prediction" {
			continue
		}
		out := 0.0
		if hit {
			out = 1
		}
		r := book.Read(keyOf(o), o.P)
		t.Cases++
		t.Stated += (o.P - out) * (o.P - out)
		t.Corrected += (r.P - out) * (r.P - out)
		plain := 0.5
		if seen > 0 {
			plain = float64(hits) / float64(seen)
		}
		t.Plain += (plain - out) * (plain - out)
		g := Decide(Spec{Reversible: true}, r, Explored(o.ID))
		t.Grades[g]++
		if hit {
			t.Right[g]++
			hits++
		}
		seen++
		book.Add(o)
	}
	if t.Cases > 0 {
		n := float64(t.Cases)
		t.Stated, t.Corrected, t.Plain = t.Stated/n, t.Corrected/n, t.Plain/n
	}
	return t
}
