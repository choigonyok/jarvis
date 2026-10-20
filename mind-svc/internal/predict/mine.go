package predict

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
)

// The engine: rules found in what happened, from any source, each tried on
// the days before it was found (each day predicted from the days before it
// only) and then on the days after, by its own predictions. A new rule is a
// candidate: it predicts in silence, and its predictions are scored. Once
// they have come true - more often than knowing only how often the thing
// happens would have said - it is shown; when they stop coming true it goes
// quiet again. Nothing here asks a model.
//
//	daily  what happens on some weekdays more than on others
//	cycle  what comes round every so many days
//	after  what follows something else within hours

// What the engine reads, and how sure it must be to say anything.
const (
	dailyWindow  = 56 // days a daily rule's shares are read from
	dailyMinDays = 14 // days seen, at least, before a daily rule
	dailyTest    = 28 // days a daily rule is tried on, walking forward
	dailyEffect  = 0.2
	dailyP       = 0.1

	cycleWindow = 210 // days a cycle is read from
	cycleMin    = 4   // times seen, at least
	cycleShare  = 0.75

	afterWindow = 90 // days what follows what is read from
	afterMin    = 4  // times the anchor was seen, at least
	afterP      = 0.001
	afterLift   = 2.5
	maxAfter    = 30 // after-rules kept a night, the surest
	// afterGap: what follows within this is the same moment seen twice (a
	// payment and the app's notice of it), not one thing after another.
	afterGap = 10 * time.Minute

	// mergeWithin: occurrences of a key this close are one (a burst of
	// photos, a split payment).
	mergeWithin = 30 * time.Minute
)

// AfterHours are the windows "after" is tried with.
var AfterHours = []int{3, 24, 72}

// Engine rule kinds.
const (
	Daily = "daily"
	Cycle = "cycle"
	After = "after"
)

// IsEngine is whether a rule is one the engine found (and keeps).
func IsEngine(r *model.Rule) bool {
	return r != nil && (r.Kind == Daily || r.Kind == Cycle || r.Kind == After)
}

// stream is one key's occurrences, oldest first, and since when its source
// was being read at all.
type stream struct {
	key    string
	events []event
	since  time.Time
}

type event struct {
	at time.Time
	id string
}

// streams reads every occurrence in [from, now) under its keys.
func (p *Predictor) streams(ctx context.Context, from, now time.Time) (map[string]*stream, error) {
	occ, err := p.St.Occurrences(ctx, from, now)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(occ, func(i, j int) bool { return occ[i].At.Before(occ[j].At) })
	thing := p.thingOf(ctx)
	first := map[string]time.Time{}
	out := map[string]*stream{}
	for _, o := range occ {
		if o.At.After(now) {
			continue
		}
		if _, ok := first[o.Kind]; !ok {
			first[o.Kind] = o.At
		}
		for _, k := range Keys(o, thing) {
			s := out[k]
			if s == nil {
				s = &stream{key: k}
				out[k] = s
			}
			if n := len(s.events); n > 0 && o.At.Sub(s.events[n-1].at) < mergeWithin {
				continue
			}
			s.events = append(s.events, event{at: o.At, id: o.ID})
		}
	}
	for k, s := range out {
		s.since = first[Source(k)]
		if s.since.IsZero() || s.events[0].at.Before(s.since) {
			s.since = s.events[0].at
		}
	}
	dedupe(out)
	return out, nil
}

// dedupe drops a key that says no more than a narrower one: when nearly
// every "card payment" was a "transit payment", the two would find the same
// rule twice, and the narrower name says more. Food delivery is kept over
// the one app it was all ordered from - "배달 주문" is what it is.
func dedupe(all map[string]*stream) {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, wide := range keys {
		for _, narrow := range keys {
			w, n := all[wide], all[narrow]
			if w == nil || n == nil || wide == narrow || !widens(wide, narrow) {
				continue
			}
			if float64(len(n.events)) < 0.9*float64(len(w.events)) || !within(n.events, w.events) {
				continue
			}
			if wide == Delivery {
				delete(all, narrow)
				continue
			}
			delete(all, wide)
		}
	}
}

// widens is whether one key holds every occurrence of another: "purchase"
// holds "purchase/교통", which holds "purchase/교통@버스 7016"; food
// delivery holds the delivery app's own payments.
func widens(wide, narrow string) bool {
	if wide == Delivery {
		_, _, title := Parts(narrow)
		return Source(narrow) == "purchase" && IsDelivery(title)
	}
	if narrow == Delivery {
		return wide == "purchase" || wide == "purchase/식비"
	}
	return Source(wide) == Source(narrow) && (strings.HasPrefix(narrow, wide+"/") || strings.HasPrefix(narrow, wide+"@"))
}

// within is whether every event of a is one of b's.
func within(a, b []event) bool {
	at := map[int64]bool{}
	for _, e := range b {
		at[e.at.Unix()] = true
	}
	for _, e := range a {
		if !at[e.at.Unix()] {
			return false
		}
	}
	return true
}

// thingOf is an item's class as the judge already said it (no model is
// asked here), remembered for the call.
func (p *Predictor) thingOf(ctx context.Context) func(string) string {
	seen := map[string]string{}
	return func(name string) string {
		if c, ok := seen[name]; ok {
			return c
		}
		c, err := p.St.ThingClass(ctx, name)
		if err != nil {
			c = ""
		}
		seen[name] = c
		return c
	}
}

// Mine finds what the occurrences hold that is worth expecting: each rule
// as a note (a candidate, for Keep to reconcile with what is known), with
// how it would have done on the days before.
func (p *Predictor) Mine(ctx context.Context) ([]model.Note, error) {
	now := p.Now()
	all, err := p.streams(ctx, now.AddDate(0, 0, -cycleWindow), now)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	today := dayStart(now, p.Loc)
	var out []model.Note
	for _, k := range keys {
		s := all[k]
		daily, isDaily := p.mineDaily(s, today)
		if isDaily {
			out = append(out, daily)
		}
		// A week or less between times, on the weekdays a daily rule
		// already names, is that rule said again.
		if n, ok := p.mineCycle(s, today); ok && (!isDaily || n.Rule.Gap > 7) {
			out = append(out, n)
		}
	}
	recent := map[string]*stream{}
	for k, s := range all {
		c := &stream{key: k, since: s.since}
		if lo := now.AddDate(0, 0, -afterWindow); c.since.Before(lo) {
			c.since = lo
		}
		for _, e := range s.events {
			if !e.at.Before(c.since) && (Source(k) != "calendar" || hasTime(e.at, p.Loc)) {
				c.events = append(c.events, e)
			}
		}
		if len(c.events) > 0 {
			recent[k] = c
		}
	}
	return append(out, p.mineAfter(recent, now)...), nil
}

// hasTime is whether a calendar entry's time says when it starts: one with
// only a date is not a moment anything follows.
func hasTime(t time.Time, loc *time.Location) bool {
	t = t.In(loc)
	return t.Hour() != 0 || t.Minute() != 0
}

const dateFmt = "2006-01-02"

// --- daily -------------------------------------------------------------------

// dailyFit is how often a key happened on each weekday of some days.
type dailyFit struct {
	rates  [7]float64
	base   float64
	n, k   [7]int
	N, K   int
	split  bool    // weekends against weekdays, rather than one weekday
	p      float64 // how likely so big a difference is by chance
	effect float64
	day    time.Weekday // the weekday that stands out (not split)
}

func (f dailyFit) informative() bool { return f.p <= dailyP && f.effect >= dailyEffect }

// fitDaily reads the shares of the days a key happened on, by weekday, and
// how much one weekday - or the weekend - stands out from the rest.
func fitDaily(days []time.Time, hit map[string]bool) dailyFit {
	var f dailyFit
	for _, d := range days {
		w := d.Weekday()
		f.n[w]++
		f.N++
		if hit[d.Format(dateFmt)] {
			f.k[w]++
			f.K++
		}
	}
	if f.N == 0 {
		return f
	}
	f.base = float64(f.K) / float64(f.N)
	for w := range 7 {
		f.rates[w] = self.Shrunk(f.k[w], f.n[w], f.base, 2)
	}
	f.p = 1
	for w := range 7 {
		if f.n[w] < 2 {
			continue
		}
		p := Fisher(f.k[w], f.n[w]-f.k[w], f.K-f.k[w], (f.N-f.n[w])-(f.K-f.k[w]))
		eff := math.Abs(f.rates[w] - f.base)
		if p < f.p || p == f.p && eff > f.effect {
			f.p, f.effect, f.day = p, eff, time.Weekday(w)
		}
	}
	nE, kE := f.n[0]+f.n[6], f.k[0]+f.k[6]
	nD, kD := f.N-nE, f.K-kE
	if nE >= 2 && nD >= 2 {
		rE, rD := self.Shrunk(kE, nE, f.base, 2), self.Shrunk(kD, nD, f.base, 2)
		p := Fisher(kE, nE-kE, kD, nD-kD)
		if eff := math.Abs(rE - rD); p <= f.p && eff >= dailyEffect {
			f.p, f.effect, f.split = p, eff, true
			for w := range 7 {
				f.rates[w] = rD
				if w == 0 || w == 6 {
					f.rates[w] = rE
				}
			}
		}
	}
	return f
}

// days is every local day in [from, to).
func days(from, to time.Time) []time.Time {
	var out []time.Time
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

func (p *Predictor) hitDays(s *stream) map[string]bool {
	hit := map[string]bool{}
	for _, e := range s.events {
		hit[e.at.In(p.Loc).Format(dateFmt)] = true
	}
	return hit
}

func (p *Predictor) mineDaily(s *stream, today time.Time) (model.Note, bool) {
	// Whether a day has a workout is the workout prediction's (Workout),
	// with the reflection's notes; the engine keeps to the kinds of it.
	if !Targetable(s.key) || s.key == "workout" {
		return model.Note{}, false
	}
	// The source's first day was likely read from part way through.
	first := dayStart(s.since, p.Loc).AddDate(0, 0, 1)
	start := first
	if lo := today.AddDate(0, 0, -dailyWindow); start.Before(lo) {
		start = lo
	}
	ds := days(start, today)
	hit := p.hitDays(s)
	if len(ds) < dailyMinDays {
		return model.Note{}, false
	}
	f := fitDaily(ds, hit)
	if f.K < 4 || f.N-f.K < 2 || !f.informative() {
		return model.Note{}, false
	}
	// Tried on the last weeks, each day from the eight weeks before it.
	var test model.Track
	from := today.AddDate(0, 0, -dailyTest)
	if lo := first.AddDate(0, 0, dailyMinDays); from.Before(lo) {
		from = lo
	}
	for _, d := range days(from, today) {
		lo := d.AddDate(0, 0, -dailyWindow)
		if lo.Before(first) {
			lo = first
		}
		g := fitDaily(days(lo, d), hit)
		if g.N < dailyMinDays || !g.informative() {
			continue // the rule would not have been there that day
		}
		test = test.Add(g.rates[d.Weekday()], g.base, hit[d.Format(dateFmt)])
	}
	label := Label(s.key)
	text, ev := dailyText(label, f)
	if u := p.usual(s); u != "" {
		text += " · 주로 " + u
	}
	rates := make([]float64, 7)
	for w := range 7 {
		rates[w] = round2(f.rates[w])
	}
	n := model.Note{ID: self.ID(Daily, s.key), Text: text, Scope: scope(s.key), Evidence: append(ev, testLine(test)...),
		Rule: &model.Rule{Kind: Daily, Key: s.key, Rates: rates, Base: round3(f.base), Days: f.N, Support: f.K, Usual: p.usualAt(s), Test: &test}}
	return n, true
}

// dailyText says what stands out: "일요일엔 배달 주문이 있는 편이다 (그날
// 80%, 다른 날 20%)", or "주말엔 회사 도착이 거의 없다 (…)".
func dailyText(label string, f dailyFit) (string, []string) {
	var in []int
	if f.split {
		in = []int{0, 6}
	} else {
		// Every weekday that stands out the same way as the one that
		// stands out most: "일·수요일", not only the surest of the two.
		up := f.rates[f.day] > f.base
		for _, w := range []int{1, 2, 3, 4, 5, 6, 0} {
			if d := f.rates[w] - f.base; f.n[w] >= 2 && (up && d >= dailyEffect || !up && -d >= dailyEffect) || w == int(f.day) {
				in = append(in, w)
			}
		}
	}
	n, k := 0, 0
	for _, w := range in {
		n += f.n[w]
		k += f.k[w]
	}
	on := self.Shrunk(k, n, f.base, 2)
	off := self.Shrunk(f.K-k, f.N-n, f.base, 2)
	if f.split {
		on, off = f.rates[0], f.rates[1]
	}
	when := weekdays(in)
	ev := []string{fmt.Sprintf("최근 %d일 중 %s %d일에 %d번, 다른 날 %d일에 %d번", f.N, when, n, k, f.N-n, f.K-k)}
	if on > off {
		return fmt.Sprintf("%s엔 %s 있는 편이다 (%s %.0f%%, 다른 날 %.0f%%)", when, Subject(label), when, on*100, off*100), ev
	}
	return fmt.Sprintf("%s엔 %s 거의 없다 (%s %.0f%%, 다른 날 %.0f%%)", when, Subject(label), when, on*100, off*100), ev
}

var weekdayKo = []string{"일", "월", "화", "수", "목", "금", "토"}

func weekdays(ws []int) string {
	switch {
	case slices.Equal(ws, []int{0, 6}) || slices.Equal(ws, []int{6, 0}):
		return "주말"
	case slices.Equal(ws, []int{1, 2, 3, 4, 5}):
		return "평일"
	}
	var names []string
	for _, w := range ws {
		names = append(names, weekdayKo[w])
	}
	return strings.Join(names, "·") + "요일"
}

// usual is the time a key usually happens at ("12시 반쯤"), when its
// times agree within three hours; usualAt the same as HH:MM.
func (p *Predictor) usual(s *stream) string {
	at := p.usualAt(s)
	if at == "" {
		return ""
	}
	h := strings.TrimPrefix(at[:2], "0")
	if h == "" {
		h = "0"
	}
	if at[3:] == "30" {
		return h + "시 반쯤"
	}
	return h + "시쯤"
}

func (p *Predictor) usualAt(s *stream) string {
	if len(s.events) < 3 || Source(s.key) == "calendar" {
		return ""
	}
	var mins []int
	for _, e := range s.events {
		t := e.at.In(p.Loc)
		mins = append(mins, t.Hour()*60+t.Minute())
	}
	slices.Sort(mins)
	q1, q3 := mins[len(mins)/4], mins[(3*len(mins))/4]
	if q3-q1 > 180 {
		return ""
	}
	// To the half hour: 12:20 is "12시 반쯤", 12:50 "13시쯤".
	m := (median(mins) + 15) / 30 * 30
	return fmt.Sprintf("%02d:%02d", m/60%24, m%60)
}

// --- cycle -------------------------------------------------------------------

// cycleFit is the usual gap between the days a key happened on.
type cycleFit struct {
	gap, tol        int
	within, reached int // gaps that reached the window, and of them those within it
	ok              bool
}

func fitCycle(gaps []int) cycleFit {
	if len(gaps) < 3 {
		return cycleFit{}
	}
	g := median(gaps)
	dev := make([]int, len(gaps))
	for i, x := range gaps {
		dev[i] = abs(x - g)
	}
	tol := int(math.Ceil(1.5 * float64(median(dev))))
	tol = max(1, min(tol, max(1, g/4)))
	f := cycleFit{gap: g, tol: tol}
	in := 0
	for _, x := range gaps {
		if x >= g-tol {
			f.reached++
			if x <= g+tol {
				f.within++
			}
		}
		if x >= g-tol && x <= g+tol {
			in++
		}
	}
	f.ok = g >= 3 && in >= 3 && float64(in) >= cycleShare*float64(len(gaps))
	return f
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (p *Predictor) mineCycle(s *stream, today time.Time) (model.Note, bool) {
	if !Targetable(s.key) {
		return model.Note{}, false
	}
	var dates []time.Time
	for _, e := range s.events {
		d := dayStart(e.at, p.Loc)
		if len(dates) == 0 || !d.Equal(dates[len(dates)-1]) {
			dates = append(dates, d)
		}
	}
	if len(dates) < cycleMin {
		return model.Note{}, false
	}
	gaps := make([]int, 0, len(dates)-1)
	for i := 1; i < len(dates); i++ {
		gaps = append(gaps, int(math.Round(dates[i].Sub(dates[i-1]).Hours()/24)))
	}
	f := fitCycle(gaps)
	if !f.ok {
		return model.Note{}, false
	}
	covered := today.Sub(dayStart(s.since, p.Loc)).Hours()/24 + 1
	daily := math.Min(1, float64(len(dates))/math.Max(covered, 1))
	var test model.Track
	for i := 3; i < len(gaps); i++ {
		g := fitCycle(gaps[:i])
		if !g.ok || gaps[i] < g.gap-g.tol {
			continue // came round early: nothing was predicted yet
		}
		base := windowBase(daily, g.tol)
		test = test.Add(self.Shrunk(g.within, g.reached, base, 1), base, gaps[i] <= g.gap+g.tol)
	}
	last := dates[len(dates)-1]
	label := Label(s.key)
	text := fmt.Sprintf("%s, 보통 %d일마다 (±%d일 · 최근 %d번)", label, f.gap, f.tol, len(dates))
	if dom, ok := monthly(dates, f.gap); ok {
		text = fmt.Sprintf("매달 %d일쯤 %s (최근 %d번)", dom, label, len(dates))
	}
	shown := gaps
	if len(shown) > 8 {
		shown = shown[len(shown)-8:]
	}
	var gs []string
	for _, g := range shown {
		gs = append(gs, fmt.Sprint(g))
	}
	ev := []string{fmt.Sprintf("간격(일): %s · 마지막 %d/%d", strings.Join(gs, ", "), int(last.Month()), last.Day())}
	n := model.Note{ID: self.ID(Cycle, s.key), Text: text, Scope: scope(s.key), Evidence: append(ev, testLine(test)...),
		Rule: &model.Rule{Kind: Cycle, Key: s.key, Gap: f.gap, Tol: f.tol, Last: last.Format(dateFmt), Days: len(dates),
			Support: f.within, Total: f.reached, Base: round3(daily), Test: &test}}
	return n, true
}

// windowBase is the chance a thing that happens on a share daily of days
// happens at least once in a window of 2·tol+1 days anyway.
func windowBase(daily float64, tol int) float64 {
	return 1 - math.Pow(1-math.Min(daily, 0.999), float64(2*tol+1))
}

// monthly is the day of the month a monthly thing comes on, when the days
// agree within three.
func monthly(dates []time.Time, gap int) (int, bool) {
	if gap < 27 || gap > 32 {
		return 0, false
	}
	var doms []int
	for _, d := range dates {
		doms = append(doms, d.Day())
	}
	if slices.Max(doms)-slices.Min(doms) > 3 {
		return 0, false
	}
	return median(doms), true
}

// --- after -------------------------------------------------------------------

type afterCand struct {
	a, b  string
	hours int
	n, k  int
	q, p  float64
	test  model.Track
}

// mineAfter finds what follows what: of the times A happened, how many had B
// within some hours - against how often B happens anyway. Each B counts for
// the latest A before it only, so one B never stands behind many As.
func (p *Predictor) mineAfter(all map[string]*stream, now time.Time) []model.Note {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var cands []afterCand
	for _, ak := range keys {
		a := all[ak]
		if len(a.events) < afterMin {
			continue
		}
		for _, bk := range keys {
			b := all[bk]
			if bk == ak || !Targetable(bk) || len(b.events) < 3 {
				continue
			}
			var best *afterCand
			for _, h := range AfterHours {
				if c, ok := tryAfter(a, b, h, now); ok && (best == nil || c.p < best.p) {
					best = &c
				}
			}
			if best != nil {
				cands = append(cands, *best)
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].p < cands[j].p || cands[i].p == cands[j].p && cands[i].a+cands[i].b < cands[j].a+cands[j].b
	})
	if len(cands) > maxAfter {
		cands = cands[:maxAfter]
	}
	var out []model.Note
	for _, c := range cands {
		text := fmt.Sprintf("%s 뒤 %s 안에 %s 있다 (%d번 중 %d번)", Label(c.a), hoursWord(c.hours), Subject(Label(c.b)), c.n, c.k)
		ev := []string{fmt.Sprintf("%s %d번 중 %d번, %s 안에 %s · 우연이면 %.0f%%", Label(c.a), c.n, c.k, hoursWord(c.hours), Label(c.b), c.q*100)}
		test := c.test
		out = append(out, model.Note{ID: self.ID(After, c.a, c.b), Text: text, Scope: scope(c.b), Evidence: append(ev, testLine(test)...),
			Rule: &model.Rule{Kind: After, Key: c.b, Anchor: c.a, Hours: c.hours, Support: c.k, Total: c.n, Base: round3(c.q), Test: &test}})
	}
	return out
}

// followed is, for each A since both were read (oldest first), whether a B
// followed it within hours - each B counted for the latest A at least
// afterGap before it, and for no other - with how many Bs there were and
// since when.
func followed(a, b *stream, hours int) (anchors []time.Time, hit []bool, nb int, since time.Time) {
	w := time.Duration(hours) * time.Hour
	since = a.since
	if b.since.After(since) {
		since = b.since
	}
	for _, e := range a.events {
		if !e.at.Before(since) {
			anchors = append(anchors, e.at)
		}
	}
	hit = make([]bool, len(anchors))
	for _, e := range b.events {
		if e.at.Before(since) {
			continue
		}
		nb++
		i := sort.Search(len(anchors), func(i int) bool { return anchors[i].After(e.at.Add(-afterGap)) }) - 1
		if i >= 0 && e.at.Sub(anchors[i]) <= w {
			hit[i] = true
		}
	}
	return anchors, hit, nb, since
}

// tryAfter counts, for one window, the times A was followed by B, against
// how often B happens anyway.
func tryAfter(a, b *stream, hours int, now time.Time) (afterCand, bool) {
	w := time.Duration(hours) * time.Hour
	anchors, hit, nb, since := followed(a, b, hours)
	n, k := 0, 0
	var test model.Track
	span := now.Sub(since).Hours()
	q := 1 - math.Exp(-float64(nb)/math.Max(span, 1)*float64(hours))
	for i, at := range anchors {
		if at.Add(w).After(now) {
			break // its window is not over
		}
		if n >= 3 {
			test = test.Add(self.Shrunk(k, n, q, 2), q, hit[i])
		}
		n++
		if hit[i] {
			k++
		}
	}
	if n < afterMin || k < 3 || 2*k < n || float64(k)/float64(n) < afterLift*q {
		return afterCand{}, false
	}
	pv := BinomTail(n, k, q)
	if pv > afterP {
		return afterCand{}, false
	}
	return afterCand{a: a.key, b: b.key, hours: hours, n: n, k: k, q: q, p: pv, test: test}, true
}

func hoursWord(h int) string {
	switch {
	case h%24 == 0 && h > 24:
		return map[int]string{2: "이틀", 3: "사흘"}[h/24]
	case h == 24:
		return "하루"
	}
	return fmt.Sprintf("%d시간", h)
}

// --- words -------------------------------------------------------------------

// Subject is a noun with its subject particle: 배달 주문이, 결제가.
func Subject(noun string) string {
	r := []rune(noun)
	if len(r) == 0 {
		return noun
	}
	last := r[len(r)-1]
	if last >= 0xAC00 && last <= 0xD7A3 && (last-0xAC00)%28 != 0 {
		return noun + "이"
	}
	return noun + "가"
}

// scope is what a key's rule is about, for the notes' grouping.
func scope(key string) string {
	if key == Delivery {
		return "식사"
	}
	switch Source(key) {
	case "purchase":
		return "지출"
	case "purchase.item":
		return "물건"
	case "visit":
		return "장소"
	case "workout":
		return "운동"
	case "photo":
		return "사진"
	case "asset":
		return "자산"
	case "transfer", "deposit", "bill":
		return "돈"
	}
	return Source(key)
}

func testLine(t model.Track) []string {
	if t.N == 0 {
		return nil
	}
	return []string{fmt.Sprintf("지난 날로 시험: 예측 %d번 중 %d번 맞음", t.N, t.Right)}
}
