// Package sleep makes nights of what a phone's health store says about sleep
// - the samples of iOS's sleep analysis (in bed, asleep, awake, core, deep,
// REM), or nights an app has summed already - and decides, day by day, which
// sleep was the day's night.
//
// A sleep is samples no more than an hour apart. It belongs to the day it
// ended on, in Seoul: the night of the 15th to the 16th is the 16th's. The
// longest sleep of a day is its night; another one is a nap, kept beside the
// night and counted in no average. A night sent again, or more of it (sent
// again once the watch has caught up), replaces what was kept; less of it (a
// query whose window began in the middle of the night) does not.
package sleep

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"
)

// Stage is what a stretch of a night was.
type Stage string

const (
	InBed  Stage = "inbed"  // in bed, nothing said of sleep
	Awake  Stage = "awake"  // awake in the night
	Asleep Stage = "asleep" // asleep, the stage not known
	Core   Stage = "core"
	Deep   Stage = "deep"
	REM    Stage = "rem"
)

// rank is which of two stretches that overlap the night is taken to be in:
// the more particular word wins, so the phone's "in bed" under the watch's
// stages is the stages, and a stage under "asleep" is that stage. Awake
// outranks a plain asleep: the source that tells awake from asleep is the
// one that watched more closely.
func (s Stage) rank() int {
	switch s {
	case Deep:
		return 6
	case REM:
		return 5
	case Core:
		return 4
	case Awake:
		return 3
	case Asleep:
		return 2
	case InBed:
		return 1
	}
	return 0
}

func (s Stage) asleep() bool { return s == Asleep || s == Core || s == Deep || s == REM }

func (s Stage) staged() bool { return s == Core || s == Deep || s == REM }

// Sample is one stretch as the health store keeps it.
type Sample struct {
	Start, End time.Time
	Stage      Stage
	Source     string
}

// Segment is a stretch of a night, in minutes from when the night began.
type Segment struct {
	From  int   `json:"from"`
	To    int   `json:"to"`
	Stage Stage `json:"stage"`
}

// Nap is a sleep of a day other than its night.
type Nap struct {
	BedAt         time.Time `json:"bedAt"`
	WakeAt        time.Time `json:"wakeAt"`
	AsleepMinutes int       `json:"asleepMinutes"`
}

// Night is one night's sleep.
type Night struct {
	// Night is the day it ended on (YYYY-MM-DD, Seoul): its key.
	Night string `json:"night"`
	// BedAt and WakeAt are when it began and ended: in bed and out of it, or
	// the first and the last of sleep when nothing said when the bed was.
	BedAt         time.Time `json:"bedAt"`
	WakeAt        time.Time `json:"wakeAt"`
	AsleepMinutes int       `json:"asleepMinutes"`
	// InBedMinutes is the time the night covers; AwakeMinutes the part of it
	// not asleep (lying awake before sleep and waking in the night).
	InBedMinutes int `json:"inBedMinutes"`
	AwakeMinutes int `json:"awakeMinutes"`
	// The stages, when the source tells them (a watch).
	DeepMinutes *int `json:"deepMinutes,omitempty"`
	CoreMinutes *int `json:"coreMinutes,omitempty"`
	REMMinutes  *int `json:"remMinutes,omitempty"`
	// BedOnly: the source said when the bed was and nothing of sleep (an
	// iPhone without a watch), so AsleepMinutes is the time in bed.
	BedOnly bool   `json:"bedOnly,omitempty"`
	Source  string `json:"source,omitempty"`
	Naps    []Nap  `json:"naps"`
	// Timeline is the night stretch by stretch, when it came as samples. It
	// is kept as long as the raw reports are (CONTEXT_RAW_DAYS): the totals
	// above are what lasts.
	Timeline  []Segment `json:"timeline,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// In is the night with its times on loc's clock.
func (n Night) In(loc *time.Location) Night {
	n.BedAt, n.WakeAt = n.BedAt.In(loc), n.WakeAt.In(loc)
	naps := make([]Nap, len(n.Naps))
	for i, p := range n.Naps {
		naps[i] = Nap{BedAt: p.BedAt.In(loc), WakeAt: p.WakeAt.In(loc), AsleepMinutes: p.AsleepMinutes}
	}
	n.Naps = naps
	if !n.UpdatedAt.IsZero() {
		n.UpdatedAt = n.UpdatedAt.In(loc)
	}
	return n
}

func (n Night) span() time.Duration { return n.WakeAt.Sub(n.BedAt) }

func overlaps(aFrom, aTo, bFrom, bTo time.Time) bool { return aFrom.Before(bTo) && bFrom.Before(aTo) }

// DayOf is the day a sleep that ended at t belongs to, on loc's clock.
func DayOf(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

const (
	// Gap: samples further apart than this are two sleeps.
	Gap = time.Hour
	// NightFloor: a sleep with fewer minutes asleep than this is no day's
	// night (a doze on the sofa, yesterday afternoon's nap at the edge of
	// the window asked for) - only a nap beside one.
	NightFloor = 60
	// NapFloor: fewer minutes asleep than this is not kept at all.
	NapFloor = 10
	// Slack: two views of one night whose lengths differ by less than this
	// saw as much of it - the later one stands.
	Slack = 10 * time.Minute
)

// Sleeps gathers samples into sleeps - samples no more than Gap apart - and
// makes each one a night, keyed by the day it ended on (loc). Which of a
// day's sleeps is its night is Merge's to say. A sample that ends before it
// starts, or lasts more than a day, is left out, and so is a sleep of awake
// alone.
func Sleeps(samples []Sample, loc *time.Location) []Night {
	ss := slices.DeleteFunc(slices.Clone(samples), func(s Sample) bool {
		return !s.End.After(s.Start) || s.End.Sub(s.Start) > 24*time.Hour || s.Stage.rank() == 0
	})
	slices.SortFunc(ss, func(a, b Sample) int {
		return cmp.Or(a.Start.Compare(b.Start), a.End.Compare(b.End), strings.Compare(a.Source, b.Source), cmp.Compare(a.Stage, b.Stage))
	})
	var out []Night
	for i := 0; i < len(ss); {
		j, end := i, ss[i].End
		for j+1 < len(ss) && !ss[j+1].Start.After(end.Add(Gap)) {
			j++
			if ss[j].End.After(end) {
				end = ss[j].End
			}
		}
		if n, ok := sleepOf(ss[i:j+1], loc); ok {
			out = append(out, n)
		}
		i = j + 1
	}
	return out
}

// stretch is a piece of a sleep that is one thing throughout.
type stretch struct {
	from, to time.Time
	stage    Stage
}

// sleepOf is one sleep's night: between any two neighbouring edges of its
// samples it is the highest-ranked stage that covers both, so samples that
// overlap - the phone's in bed under the watch's stages, two apps at once -
// are counted once.
func sleepOf(group []Sample, loc *time.Location) (Night, bool) {
	edges := make([]time.Time, 0, 2*len(group))
	for _, s := range group {
		edges = append(edges, s.Start, s.End)
	}
	slices.SortFunc(edges, func(a, b time.Time) int { return a.Compare(b) })
	edges = slices.CompactFunc(edges, func(a, b time.Time) bool { return a.Equal(b) })
	var parts []stretch
	for k := 0; k+1 < len(edges); k++ {
		a, b := edges[k], edges[k+1]
		best := Stage("")
		for _, s := range group {
			if !s.Start.After(a) && !s.End.Before(b) && s.Stage.rank() > best.rank() {
				best = s.Stage
			}
		}
		if best == "" {
			continue // nothing said of it: a gap
		}
		if last := len(parts) - 1; last >= 0 && parts[last].stage == best && parts[last].to.Equal(a) {
			parts[last].to = b
			continue
		}
		parts = append(parts, stretch{a, b, best})
	}
	var asleep, covered, deep, core, rem time.Duration
	staged, slept, bed := false, false, false
	for _, p := range parts {
		d := p.to.Sub(p.from)
		covered += d
		switch p.stage {
		case Deep:
			deep += d
		case Core:
			core += d
		case REM:
			rem += d
		case InBed:
			bed = true
		}
		if p.stage.asleep() {
			asleep += d
			slept = true
		}
		if p.stage.staged() {
			staged = true
		}
	}
	if len(parts) == 0 || (!slept && !bed) {
		return Night{}, false // awake alone is no sleep
	}
	n := Night{
		BedAt: parts[0].from, WakeAt: parts[len(parts)-1].to,
		AsleepMinutes: minutes(asleep), InBedMinutes: minutes(covered),
		Source: sources(group), Naps: []Nap{},
	}
	n.Night = DayOf(n.WakeAt, loc)
	if !slept {
		n.BedOnly, n.AsleepMinutes = true, n.InBedMinutes
		return n, true
	}
	n.AwakeMinutes = max(0, n.InBedMinutes-n.AsleepMinutes)
	if staged {
		n.DeepMinutes, n.CoreMinutes, n.REMMinutes = ptr(minutes(deep)), ptr(minutes(core)), ptr(minutes(rem))
	}
	n.Timeline = timeline(parts, n.BedAt)
	return n, true
}

// timeline is the stretches in whole minutes from the night's start: a
// stretch that rounds to nothing is left out, and neighbours of one stage
// after rounding are one.
func timeline(parts []stretch, start time.Time) []Segment {
	out := []Segment{}
	for _, p := range parts {
		from, to := minutes(p.from.Sub(start)), minutes(p.to.Sub(start))
		if to <= from {
			continue
		}
		if last := len(out) - 1; last >= 0 && out[last].Stage == p.stage && out[last].To == from {
			out[last].To = to
			continue
		}
		out = append(out, Segment{From: from, To: to, Stage: p.stage})
	}
	return out
}

// sources is who said it: each source once, in the order they first spoke.
func sources(group []Sample) string {
	var seen []string
	for _, s := range group {
		if name := strings.TrimSpace(s.Source); name != "" && !slices.Contains(seen, name) {
			seen = append(seen, name)
		}
	}
	return Clip(strings.Join(seen, ", "), 120)
}

// Clip cuts a string to n runes.
func Clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func minutes(d time.Duration) int { return int(math.Round(d.Minutes())) }

func ptr(v int) *int { return &v }

// Result is what became of one sleep sent.
type Result string

const (
	Saved  Result = "saved" // it is the day's night now
	Napped Result = "nap"   // it is kept as a nap of that day
	Kept   Result = "kept"  // it is part of the night kept, which saw more of it
	Short  Result = "short" // too short to keep: no night (NightFloor) for a day with none, no nap (NapFloor)
)

// Merge decides one day's night from the one kept for it (nil when none)
// and the sleeps sent for it. The longest sent is looked at first:
//
//   - with no night kept, it is the night - unless it is shorter than
//     NightFloor, then nothing is kept;
//   - one that overlaps the night is another view of it, and replaces it if
//     it is as long or longer (Slack): the same night sent again, or with the
//     end the watch had not yet given; a shorter view - the tail of a night
//     cut by the window it was asked for in - leaves the night as it is. A
//     view that knows sleep replaces one of the bed alone, and never the
//     other way round;
//   - one apart from the night and longer than it is the night, and the
//     night kept becomes a nap (a nap that came before its night);
//   - any other is a nap: the same nap sent again replaces it on the same
//     terms as a night.
//
// It returns the day as it now stands (nil when it has no night) and what
// became of each sleep, in the order they were given.
func Merge(kept *Night, sent []Night) (*Night, []Result) {
	results := make([]Result, len(sent))
	order := make([]int, len(sent))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Or(cmp.Compare(sent[b].AsleepMinutes, sent[a].AsleepMinutes), sent[a].BedAt.Compare(sent[b].BedAt))
	})
	var cur *Night
	if kept != nil {
		k := *kept
		k.Naps = slices.Clone(k.Naps)
		cur = &k
	}
	for _, i := range order {
		c := sent[i]
		switch {
		case cur == nil:
			if c.AsleepMinutes < NightFloor {
				results[i] = Short
				continue
			}
			n := c
			n.Naps = []Nap{}
			cur, results[i] = &n, Saved
		case overlaps(c.BedAt, c.WakeAt, cur.BedAt, cur.WakeAt):
			// What knows sleep beats the bed alone, whichever is longer: the
			// watch's night over the phone's time in bed.
			if c.BedOnly && !cur.BedOnly || c.BedOnly == cur.BedOnly && c.span() < cur.span()-Slack {
				results[i] = Kept
				continue
			}
			n := c
			n.Naps = cur.Naps
			cur, results[i] = &n, Saved
		case c.AsleepMinutes > cur.AsleepMinutes && c.AsleepMinutes >= NightFloor:
			n := c
			n.Naps, _ = withNap(cur.Naps, *cur)
			cur, results[i] = &n, Saved
		default:
			cur.Naps, results[i] = withNap(cur.Naps, c)
		}
		// A nap that turns out to be part of the night is the night's.
		cur.Naps = slices.DeleteFunc(cur.Naps, func(p Nap) bool { return overlaps(p.BedAt, p.WakeAt, cur.BedAt, cur.WakeAt) })
	}
	return cur, results
}

// withNap puts a sleep among a day's naps: in place of the naps it overlaps
// if it saw as much of them (Napped), else not at all (Kept). Shorter than
// NapFloor, it is not kept (Short).
func withNap(naps []Nap, s Night) ([]Nap, Result) {
	if s.AsleepMinutes < NapFloor {
		return naps, Short
	}
	nap := Nap{BedAt: s.BedAt, WakeAt: s.WakeAt, AsleepMinutes: s.AsleepMinutes}
	var longest time.Duration
	for _, p := range naps {
		if overlaps(p.BedAt, p.WakeAt, nap.BedAt, nap.WakeAt) {
			longest = max(longest, p.WakeAt.Sub(p.BedAt))
		}
	}
	if nap.WakeAt.Sub(nap.BedAt) < longest-Slack {
		return naps, Kept
	}
	out := slices.DeleteFunc(slices.Clone(naps), func(p Nap) bool { return overlaps(p.BedAt, p.WakeAt, nap.BedAt, nap.WakeAt) })
	out = append(out, nap)
	slices.SortFunc(out, func(a, b Nap) int { return a.BedAt.Compare(b.BedAt) })
	return out, Napped
}

// Window is the nights of the last days, summed up: how long on average,
// and when, as a clock - the usual bedtime and waking, and how far a night
// strays from them (the mean distance, in minutes): how regular the nights are.
type Window struct {
	Days              int    `json:"days"`
	Nights            int    `json:"nights"`
	AsleepMinutes     int    `json:"asleepMinutes,omitempty"`
	BedAt             string `json:"bedAt,omitempty"`
	WakeAt            string `json:"wakeAt,omitempty"`
	BedSpreadMinutes  int    `json:"bedSpreadMinutes"`
	WakeSpreadMinutes int    `json:"wakeSpreadMinutes"`
}

// Summary is the last week and the last month up to a day.
type Summary struct {
	Week  Window `json:"week"`
	Month Window `json:"month"`
}

// Summarize sums up the nights of the 7 and the 30 days ending on day.
func Summarize(nights []Night, day string, loc *time.Location) Summary {
	return Summary{Week: window(nights, day, 7, loc), Month: window(nights, day, 30, loc)}
}

func window(nights []Night, day string, days int, loc *time.Location) Window {
	w := Window{Days: days}
	end, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return w
	}
	first := end.AddDate(0, 0, 1-days).Format("2006-01-02")
	var beds, wakes []time.Time
	total := 0
	for _, n := range nights {
		if n.Night < first || n.Night > day {
			continue
		}
		w.Nights++
		total += n.AsleepMinutes
		beds, wakes = append(beds, n.BedAt), append(wakes, n.WakeAt)
	}
	if w.Nights == 0 {
		return w
	}
	w.AsleepMinutes = int(math.Round(float64(total) / float64(w.Nights)))
	var bed, wake int
	bed, w.BedSpreadMinutes = clockMean(beds, loc)
	wake, w.WakeSpreadMinutes = clockMean(wakes, loc)
	w.BedAt, w.WakeAt = clock(bed), clock(wake)
	return w
}

// clockMean is the mean of times of day on loc's clock, as minutes past
// midnight, taken round the clock (23:30 and 00:30 average to midnight, not
// noon), and the mean distance of each from it.
func clockMean(ts []time.Time, loc *time.Location) (int, int) {
	var x, y float64
	mins := make([]float64, len(ts))
	for i, t := range ts {
		t = t.In(loc)
		mins[i] = float64(t.Hour()*60 + t.Minute())
		a := mins[i] / 1440 * 2 * math.Pi
		x, y = x+math.Cos(a), y+math.Sin(a)
	}
	mean := mins[0]
	if math.Hypot(x, y) > 1e-9 {
		mean = math.Mod(math.Atan2(y, x)/(2*math.Pi)*1440+1440, 1440)
	}
	var off float64
	for _, m := range mins {
		d := math.Mod(math.Abs(m-mean), 1440)
		off += min(d, 1440-d)
	}
	return int(math.Round(mean)) % 1440, int(math.Round(off / float64(len(mins))))
}

func clock(m int) string {
	return time.Date(2000, 1, 1, m/60, m%60, 0, 0, time.UTC).Format("15:04")
}
