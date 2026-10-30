// Package backtest holds judgements to what came of them: how sure the
// assistant said it was against how often it was right, by what made the
// judgement. It is how a constant is measured instead of argued about, and
// what a rule goes through before it is turned on.
//
// A case is one judgement with its result, from anywhere: a card the operator
// answered, a prediction that was scored, later a row of the result ledger.
// scripts/harness cases exports them from a running system.
package backtest

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// Case is one judgement and what came of it.
type Case struct {
	// Source is what kind of judgement: "card", "prediction".
	Source string `json:"source"`
	// Group is what made it: a skill and context, a prediction's key.
	Group string `json:"group"`
	// P is how sure it was. Hit is whether it came true: the card approved,
	// the prediction met.
	P   float64 `json:"p"`
	Hit bool    `json:"hit"`
	// Base, when there is one, is what knowing only how often the thing
	// happens would have said.
	Base *float64  `json:"base,omitempty"`
	At   time.Time `json:"at"`
}

// Read takes cases as JSON lines.
func Read(r io.Reader) ([]Case, error) {
	var out []Case
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for line := 1; sc.Scan(); line++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var c Case
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("%d번째 줄: %w", line, err)
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

// Line is a set of cases scored.
type Line struct {
	Group string
	N     int
	Hits  int
	// Rate is how often it came true; MeanP how sure it said it was.
	Rate  float64
	MeanP float64
	// Brier is the judgement's score, lower better. Plain is what always
	// saying the rate (or the case's own Base) would have scored: a judgement
	// worth having is below it.
	Brier float64
	Plain float64
}

// Gap is how far the stated confidence is from what happened: above zero it
// says more than it knows.
func (l Line) Gap() float64 { return l.MeanP - l.Rate }

// Score adds cases up. Plain uses each case's Base where it has one and the
// rate of the whole set where it does not.
func Score(group string, cases []Case) Line {
	l := Line{Group: group, N: len(cases)}
	if l.N == 0 {
		return l
	}
	for _, c := range cases {
		if c.Hit {
			l.Hits++
		}
		l.MeanP += c.P
	}
	l.Rate = float64(l.Hits) / float64(l.N)
	l.MeanP /= float64(l.N)
	for _, c := range cases {
		out := 0.0
		if c.Hit {
			out = 1
		}
		l.Brier += (c.P - out) * (c.P - out)
		base := l.Rate
		if c.Base != nil {
			base = *c.Base
		}
		l.Plain += (base - out) * (base - out)
	}
	l.Brier /= float64(l.N)
	l.Plain /= float64(l.N)
	return l
}

// ByGroup scores each group of a source, the most cases first.
func ByGroup(cases []Case) []Line {
	groups := map[string][]Case{}
	for _, c := range cases {
		key := c.Source + " · " + c.Group
		groups[key] = append(groups[key], c)
	}
	out := make([]Line, 0, len(groups))
	for key, cs := range groups {
		out = append(out, Score(key, cs))
	}
	slices.SortFunc(out, func(a, b Line) int {
		return cmp.Or(cmp.Compare(b.N, a.N), cmp.Compare(a.Group, b.Group))
	})
	return out
}

// Bin is a span of stated confidence and how often that came true.
type Bin struct {
	From, To float64
	N, Hits  int
}

// Calibration splits cases by stated confidence into n equal spans. A
// well-calibrated judgement's rate falls inside each span.
func Calibration(cases []Case, n int) []Bin {
	bins := make([]Bin, n)
	for i := range bins {
		bins[i].From = float64(i) / float64(n)
		bins[i].To = float64(i+1) / float64(n)
	}
	for _, c := range cases {
		i := min(int(c.P*float64(n)), n-1)
		if i < 0 {
			i = 0
		}
		bins[i].N++
		if c.Hit {
			bins[i].Hits++
		}
	}
	return bins
}

// Cut is what a threshold would have done with these cases.
type Cut struct {
	Threshold float64
	// Kept is how many would have gone ahead, Hits how many of those were
	// right; Missed is how many it would have held back that were right.
	Kept, Hits, Missed int
}

// Precision is the share of what went ahead that was right.
func (c Cut) Precision() float64 {
	if c.Kept == 0 {
		return 0
	}
	return float64(c.Hits) / float64(c.Kept)
}

// At tries a threshold: what would a constant set to t have done?
func At(cases []Case, t float64) Cut {
	cut := Cut{Threshold: t}
	for _, c := range cases {
		switch {
		case c.P >= t:
			cut.Kept++
			if c.Hit {
				cut.Hits++
			}
		case c.Hit:
			cut.Missed++
		}
	}
	return cut
}

// fewCases is below where a line says more about chance than the judgement.
const fewCases = 5

// Report is the run in words: the whole, each group, and the calibration of
// the cards and of the predictions.
func Report(cases []Case) string {
	var b strings.Builder
	whole := Score("전체", cases)
	fmt.Fprintf(&b, "판단 %d건 · 맞은 비율 %.0f%% · 말한 확신 %.0f%% · Brier %.3f (평소 비율로만 말했으면 %.3f)\n\n",
		whole.N, 100*whole.Rate, 100*whole.MeanP, whole.Brier, whole.Plain)
	fmt.Fprintf(&b, "%-44s %4s %6s %6s %7s %7s\n", "무엇이 판단했나", "건수", "맞음", "확신", "Brier", "평소")
	for _, l := range ByGroup(cases) {
		note := ""
		if l.N < fewCases {
			note = "  (표본 적음)"
		}
		fmt.Fprintf(&b, "%-44s %4d %5.0f%% %5.0f%% %7.3f %7.3f%s\n", l.Group, l.N, 100*l.Rate, 100*l.MeanP, l.Brier, l.Plain, note)
	}
	for _, source := range []string{"card", "prediction"} {
		var set []Case
		for _, c := range cases {
			if c.Source == source {
				set = append(set, c)
			}
		}
		if len(set) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s: 확신 구간별\n", source)
		for _, bin := range Calibration(set, 5) {
			if bin.N == 0 {
				continue
			}
			fmt.Fprintf(&b, "  %3.0f~%3.0f%%  %3d건  실제 %3.0f%%\n", 100*bin.From, 100*bin.To, bin.N, 100*float64(bin.Hits)/float64(bin.N))
		}
	}
	return b.String()
}
