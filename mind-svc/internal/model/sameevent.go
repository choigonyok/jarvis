package model

import (
	"slices"
	"strings"
	"time"
	"unicode"
)

// Verdict is what the rules can say of two plans.
type Verdict int

const (
	Different Verdict = iota
	Unsure            // something in common, and the rules cannot say which: for a judgement
	Same
)

// Compare is the rule for whether two plans are one event, one question to the
// operator: told twice (SameEvent), or close together on one day under the same
// title, at the same place or with the same person - one outing is one thing to
// have on the calendar. A word or a name shared with hours between, or with no
// hour to go by, may be either way, and is left to a judgement: "지수 생일" all
// day is not the dinner that evening, nor the cake pickup at 10:30 whose title
// holds it, and calling either the same would leave it off the calendar - while
// "치과 진료" at 15:00 may well be the "치과" told without a time.
func Compare(a, b State) Verdict {
	if !Overlap(a, b) {
		return Different
	}
	named := sameTitle(a.Title, b.Title) || samePlace(a, b)
	timed := a.Start != "" && b.Start != ""
	switch {
	case SameEvent(a, b), timed && near(a, b) && (named || sharePerson(a, b)):
		return Same
	case a.Start == "" && b.Start == "" && samePlace(a, b):
		return Same
	case named || titlesMeet(a, b):
		return Unsure
	}
	return Different
}

// Overlap is whether two plans share a day.
func Overlap(a, b State) bool {
	if a.Date == "" || b.Date == "" {
		return false
	}
	return a.Date <= lastDay(b) && b.Date <= lastDay(a)
}

func lastDay(s State) string {
	if s.EndDate > s.Date {
		return s.EndDate
	}
	return s.Date
}

// SameEvent is whether two plans are one event told twice - a plan heard in a
// conversation and an entry put on the calendar another way - by the rules
// alone. Both start on one day, and what each says of when decides how much
// the titles must agree:
//   - A stay of days with the same first and last day is one trip when the
//     titles meet at all ("지수와 캠핑 2박3일" and "숲속 캠핑 2박3일").
//   - Both with a time, starting within half an hour: titles that share a word
//     or a person ("은주와 합주" and "기타 합주 (은주)", both 15:00). At places
//     known to be different, a shared word is not enough and the titles must
//     name the same thing: "지수 생일 케이크 픽업" at 10:30 at 케이크가게 and "지수
//     생일 꽃 픽업" at 11:00 at 꽃가게 are two errands, while the same dinner
//     said at another place is one, moved. Two things at one time that share
//     nothing are a collision, which is not this.
//   - One with a time and one without: there is no time to agree on, so the
//     name must be the same ("Netflix 플랜 변경·해지" all day and at 20:00). A
//     name only held in the other's is not enough: "지수 생일" all day is the
//     day, and "지수 생일 케이크 픽업" at 10:30 an errand on it. Compare leaves
//     such a pair to a judgement.
//   - Neither with a time: one title holding the other ("재택" and "재택근무").
func SameEvent(a, b State) bool {
	if a.Date == "" || a.Date != b.Date {
		return false
	}
	if a.EndDate != "" && a.EndDate != a.Date && a.EndDate == b.EndDate && titlesMeet(a, b) {
		return true
	}
	switch {
	case a.Start == "" && b.Start == "":
		return sameTitle(a.Title, b.Title)
	case a.Start == "" || b.Start == "":
		return sameName(a.Title, b.Title)
	}
	ta, errA := time.Parse("15:04", a.Start)
	tb, errB := time.Parse("15:04", b.Start)
	if errA != nil || errB != nil {
		return false
	}
	if d := ta.Sub(tb); d > halfHour || d < -halfHour {
		return false
	}
	if placesDiffer(a, b) {
		return sameTitle(a.Title, b.Title)
	}
	return titlesMeet(a, b)
}

const halfHour = 30 * time.Minute

// outingGap is the longest gap between two parts of one outing: lunch after
// a morning together is the same day out, dinner six hours later is not.
const outingGap = 2 * time.Hour

// SameOuting is whether two plans are parts of one outing told apart: the
// same day, with the same person, and no long gap between them. "톤 세팅" at
// ten and "점심" at eleven with one friend are one thing to have on the
// calendar, and asking about each is how one morning became four cards.
// A part told without a time is taken to be of it too.
func SameOuting(a, b State) bool {
	return sharePerson(a, b) && near(a, b)
}

// near is whether two plans are on one day with no long gap between them. One
// told without a time may be any time of it.
func near(a, b State) bool {
	if a.Date == "" || a.Date != b.Date {
		return false
	}
	if a.Start == "" || b.Start == "" {
		return true
	}
	as, ae, ok := clock(a)
	bs, be, ok2 := clock(b)
	if !ok || !ok2 {
		return false
	}
	return max(as, bs)-min(ae, be) <= outingGap
}

// clock is when a plan starts and ends on a day's clock: an hour when no end
// was said.
func clock(s State) (time.Duration, time.Duration, bool) {
	start, err := time.Parse("15:04", s.Start)
	if err != nil {
		return 0, 0, false
	}
	from := time.Duration(start.Hour())*time.Hour + time.Duration(start.Minute())*time.Minute
	to := from + time.Hour
	if end, err := time.Parse("15:04", s.End); err == nil {
		if t := time.Duration(end.Hour())*time.Hour + time.Duration(end.Minute())*time.Minute; t > from {
			to = t
		}
	}
	return from, to, true
}

func samePlace(a, b State) bool {
	return a.Place != "" && bare(a.Place) == bare(b.Place)
}

// placesDiffer is whether two plans are said to be at different places: both
// places told, and neither name holding the other - "망원" and "망원동 모퉁이맥주"
// may be one place, said more or less fully.
func placesDiffer(a, b State) bool {
	pa, pb := bare(a.Place), bare(b.Place)
	return pa != "" && pb != "" && pa != pb && !sameTitle(pa, pb)
}

func sharePerson(a, b State) bool {
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
	return false
}

func titlesMeet(a, b State) bool {
	if sharePerson(a, b) {
		return true
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

// Fill adds to a plan what another telling of it says and it lacked - a time,
// a place, an end, who - and leaves what it had. It reports whether anything
// changed.
func Fill(cur *State, s State) bool {
	changed := false
	set := func(dst *string, v string) {
		if *dst == "" && v != "" {
			*dst = v
			changed = true
		}
	}
	set(&cur.Start, s.Start)
	set(&cur.End, s.End)
	set(&cur.EndDate, s.EndDate)
	set(&cur.Place, s.Place)
	set(&cur.Type, s.Type)
	set(&cur.PlaceKind, s.PlaceKind)
	set(&cur.Area, s.Area)
	if len(cur.People) == 0 && len(s.People) > 0 {
		cur.People = s.People
		changed = true
	}
	return changed
}

// SameHour is whether two tellings of a plan can be one by when alone: sharing a
// day, and on it no two hours said half an hour apart.
func SameHour(a, b State) bool {
	if !Overlap(a, b) {
		return false
	}
	if a.Date != b.Date || a.Start == "" || b.Start == "" {
		return true
	}
	ta, errA := time.Parse("15:04", a.Start)
	tb, errB := time.Parse("15:04", b.Start)
	if errA != nil || errB != nil {
		return false
	}
	d := ta.Sub(tb)
	return -halfHour <= d && d <= halfHour
}

// CompareEntry is the rule for whether an entry on the calendar is the one a
// plan would add, asked before adding it: the same event (SameEvent) is. An
// entry on the plan's day whose title holds the plan's, or is held in it, or is
// the same at another hour, may be it or another thing, and is left to a
// judgement: "치과 진료" at 15:00 may well be the "치과" told without a time, but
// "지수 생일" all day is not the cake pickup at 10:30 on it, nor "회의" at 10:00
// the "회의" at 15:00 - and taking either for the entry already there keeps the
// one asked about off the calendar. Parts of one outing are not one entry to
// write down.
func CompareEntry(a, b State) Verdict {
	switch {
	case SameEvent(a, b):
		return Same
	case a.Date != "" && a.Date == b.Date && sameTitle(a.Title, b.Title):
		return Unsure
	}
	return Different
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

// sameName is whether two titles are one name: the same letters and digits,
// spaces, marks and case aside. A single letter is no name.
func sameName(a, b string) bool {
	x := bare(a)
	return len([]rune(x)) >= 2 && x == bare(b)
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
