package predict

import (
	"math"
	"slices"
)

// Fisher is the two-sided p of Fisher's exact test on the table
// [[a b] [c d]]: rows feature / no feature, columns target / no target.
func Fisher(a, b, c, d int) float64 {
	n := a + b + c + d
	row1, col1 := a+b, a+c
	logp := func(x int) float64 {
		return lchoose(row1, x) + lchoose(n-row1, col1-x) - lchoose(n, col1)
	}
	observed := logp(a)
	p := 0.0
	lo, hi := max(0, col1-(n-row1)), min(row1, col1)
	for x := lo; x <= hi; x++ {
		if lp := logp(x); lp <= observed+1e-9 {
			p += math.Exp(lp)
		}
	}
	return math.Min(1, p)
}

// BinomTail is the chance of k or more of n coming true when each does with
// chance q: how unlikely a run of hits is by chance alone.
func BinomTail(n, k int, q float64) float64 {
	if k <= 0 {
		return 1
	}
	if q <= 0 {
		return 0
	}
	if q >= 1 {
		return 1
	}
	p := 0.0
	for x := k; x <= n; x++ {
		p += math.Exp(lchoose(n, x) + float64(x)*math.Log(q) + float64(n-x)*math.Log(1-q))
	}
	return math.Min(1, p)
}

func lchoose(n, k int) float64 {
	if k < 0 || k > n {
		return math.Inf(-1)
	}
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}

// median is the middle of xs (the lower middle of an even count, so it is
// always one of them).
func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[(len(s)-1)/2]
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// round3 keeps a small chance small rather than none: what a rare thing's
// predictions are measured against.
func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
