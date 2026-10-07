package uniple

import (
	"fmt"
	"time"
)

// expand turns series into the occurrences that touch [from, to].
//
// A port of the app's own expandRecurrences, kept step for step - a second
// opinion on which days a series lands on would show the operator a calendar
// that disagrees with the one on their phone. In particular:
//
//   - an occurrence keeps the series' length (a two-day trip repeats as two
//     days) and is kept if any of it reaches the window;
//   - exceptions are the series' exdates plus every day an override row
//     replaces; the override rows themselves are ordinary entries;
//   - monthly and yearly repeats clamp to the end of a short month, so a
//     series on the 31st lands on the 30th in April.
//
// Occurrence ids are "<series id>#<date>", the app's own form.
func expand(rows []row, from, to string) []row {
	lo, hi := days(from), days(to)

	var series, out []row
	for _, r := range rows {
		if r.Recurrence != nil && r.RecurrenceParentID == nil {
			series = append(series, r)
		} else {
			out = append(out, r)
		}
	}

	skip := map[string]map[string]bool{}
	mark := func(id, date string) {
		if skip[id] == nil {
			skip[id] = map[string]bool{}
		}
		skip[id][date] = true
	}
	for _, s := range series {
		for _, d := range s.RecurrenceExdates {
			mark(s.ID, d)
		}
	}
	for _, r := range rows {
		if r.RecurrenceParentID != nil && r.RecurrenceOverrideDate != nil {
			mark(*r.RecurrenceParentID, *r.RecurrenceOverrideDate)
		}
	}

	for _, s := range series {
		start := days(s.StartDate)
		length := days(effEnd(s)) - start
		var since, until *int
		if s.RecurrenceFrom != nil {
			v := days(*s.RecurrenceFrom)
			since = &v
		}
		if s.RecurrenceUntil != nil {
			v := days(*s.RecurrenceUntil)
			until = &v
		}

		// visit returns false once the series has run past the window.
		visit := func(date string) bool {
			d := days(date)
			if until != nil && d > *until || d > hi {
				return false
			}
			if since != nil && d < *since {
				return true
			}
			if d+length >= lo && !skip[s.ID][date] {
				out = append(out, occurrence(s, date))
			}
			return true
		}

		switch *s.Recurrence {
		case "daily", "weekly", "biweekly":
			step := map[string]int{"daily": 1, "weekly": 7, "biweekly": 14}[*s.Recurrence]
			d := start + max(0, (lo-length-start)/step)*step
			for d+length < lo && d <= hi {
				d += step
			}
			for ; visit(ymd(d)); d += step {
			}
		case "monthly":
			y, m, day := parts(s.StartDate)
			first := 12*y + (m - 1)
			fy, fm, _ := parts(from)
			k := max(first, 12*fy+(fm-1)-(ceilDiv(length, 28)+1))
			for ; visit(clamped(k/12, k%12+1, day)); k++ {
			}
		case "yearly":
			y, m, day := parts(s.StartDate)
			fy, _, _ := parts(from)
			k := max(y, fy-(ceilDiv(length, 365)+1))
			for ; visit(clamped(k, m, day)); k++ {
			}
		}
	}
	return out
}

// occurrence is one day of a series, shaped like an ordinary entry.
func occurrence(s row, date string) row {
	o := s
	length := days(effEnd(s)) - days(s.StartDate)
	o.ID = s.ID + "#" + date
	o.StartDate = date
	o.EndDate = nil
	if length > 0 {
		end := ymd(days(date) + length)
		o.EndDate = &end
	}
	return o
}

func effEnd(r row) string {
	if r.EndDate != nil && *r.EndDate != "" {
		return *r.EndDate
	}
	return r.StartDate
}

// days counts from the Unix epoch in UTC, as the app does, so date arithmetic
// never meets a timezone or a DST jump.
func days(date string) int {
	t, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return 0
	}
	return int(t.Unix() / 86400)
}

func ymd(d int) string {
	return time.Unix(int64(d)*86400, 0).UTC().Format(time.DateOnly)
}

func parts(date string) (y, m, d int) {
	t, _ := time.Parse(time.DateOnly, date)
	return t.Year(), int(t.Month()), t.Day()
}

// clamped is the date y-m-d, with d pulled back to the month's last day.
func clamped(y, m, d int) string {
	last := time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return fmt.Sprintf("%04d-%02d-%02d", y, m, min(d, last))
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
