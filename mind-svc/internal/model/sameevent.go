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
//
// A plan told without a time has no time to agree on: it is the entry of
// its day under the same title. "전날에 플랜 바꾸게 일정 추가해 줘" was read as
// an all-day "Netflix 플랜 변경·해지" while the assistant put the same
// title at 20:00, and the two were never tied (2026-10-26).
func SameEvent(a, b State) bool {
	if a.Date == "" || a.Date != b.Date {
		return false
	}
	if a.Start == "" || b.Start == "" {
		return sameTitle(a.Title, b.Title)
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

// TakeTime gives a plan told without a time the time of the entry it is:
// tied by title alone, the entry is what says when - and a time said later
// in the conversation then reads as the entry's own, not as a move to ask
// about. It reports whether it changed anything.
func TakeTime(st *State, entry State) bool {
	if st.Start != "" || entry.Start == "" || st.Date != entry.Date {
		return false
	}
	st.Start, st.End = entry.Start, entry.End
	return true
}

// sameTitle is whether two titles name one thing: the same letters, or one
// holding the other ("재택" and "재택근무"), spaces and marks aside. A single
// letter held is no title.
func sameTitle(a, b string) bool {
	ra, rb := []rune(bare(a)), []rune(bare(b))
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	return len(ra) >= 2 && strings.Contains(string(rb), string(ra))
}

// bare is a title's letters and digits, lowercased.
func bare(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
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
