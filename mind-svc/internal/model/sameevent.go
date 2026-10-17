package model

import (
	"slices"
	"strings"
	"time"
	"unicode"
)

// SameEvent is whether two plans are one event told twice - a plan heard in a
// conversation and an entry put on the calendar another way ("은주와 합주" and
// "기타 합주 (은주)", both 15:00 on the 17th). Same day, starts within half an
// hour, and the titles share a word or a person. Two things at one time that
// share nothing are a collision, which is not this.
func SameEvent(a, b State) bool {
	if a.Date == "" || a.Date != b.Date || a.Start == "" || b.Start == "" {
		return false
	}
	ta, errA := time.Parse("15:04", a.Start)
	tb, errB := time.Parse("15:04", b.Start)
	if errA != nil || errB != nil {
		return false
	}
	if d := ta.Sub(tb); d > 30*time.Minute || d < -30*time.Minute {
		return false
	}
	return titlesMeet(a, b)
}

func titlesMeet(a, b State) bool {
	for _, p := range a.People {
		if p != "" && (slices.Contains(b.People, p) || strings.Contains(b.Title, p)) {
			return true
		}
	}
	for _, p := range b.People {
		if p != "" && strings.Contains(a.Title, p) {
			return true
		}
	}
	wa, wb := words(a.Title), words(b.Title)
	for w := range wa {
		if wb[w] {
			return true
		}
	}
	return false
}

// words are what two titles can share: two-letter pieces of Hangul - a Korean
// word survives its particles that way ("은주와" and "(은주)" share 은주) - and
// whole Latin words of three letters or more.
func words(title string) map[string]bool {
	out := map[string]bool{}
	var run []rune
	hangul := false
	flush := func() {
		switch {
		case hangul:
			for i := 0; i+1 < len(run); i++ {
				out[string(run[i:i+2])] = true
			}
		case len(run) >= 3:
			out[string(run)] = true
		}
		run = run[:0]
	}
	for _, r := range strings.ToLower(title) {
		isHangul := unicode.Is(unicode.Hangul, r)
		if !(isHangul || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			flush()
			continue
		}
		if len(run) > 0 && isHangul != hangul {
			flush()
		}
		hangul = isHangul
		run = append(run, r)
	}
	flush()
	return out
}
