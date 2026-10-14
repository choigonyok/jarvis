package world

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Situation is what is believed about right now: the plans that may be
// going on, the latest hints of where the operator is, and a guess with how
// sure it is. It is computed by rule, not by a model, and kept small enough
// to put in front of the conversation every turn.
type Situation struct {
	At    time.Time `json:"at"`
	Plans []Ongoing `json:"plans"`
	Next  *Ongoing  `json:"next,omitempty"` // the next plan today, if none is going on
	// Today is today's plans with no time: on the day, but not "going on"
	// at any particular moment.
	Today []Ongoing `json:"today,omitempty"`
	Clues []Clue    `json:"clues"`
	Guess *Guess    `json:"guess,omitempty"`
	Text  string    `json:"text"`
}

// Ongoing is a plan that may be going on now.
type Ongoing struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Type      string    `json:"type,omitempty"`
	Place     string    `json:"place,omitempty"`
	PlaceKind string    `json:"placeKind,omitempty"`
	Area      string    `json:"area,omitempty"`
	People    []string  `json:"people,omitempty"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	EndKnown  bool      `json:"endKnown"`
	EndWhy    string    `json:"endWhy,omitempty"` // how the end was guessed
}

// Clue is a hint of where the operator was: a payment, a photo.
type Clue struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	Title string    `json:"title,omitempty"`
	Place string    `json:"place,omitempty"`
	Area  string    `json:"area,omitempty"`
}

// Guess is where the operator probably is and with whom.
type Guess struct {
	Place      string   `json:"place,omitempty"`
	PlaceKind  string   `json:"placeKind,omitempty"`
	Area       string   `json:"area,omitempty"`
	With       []string `json:"with,omitempty"`
	Confidence float64  `json:"confidence"`
	Basis      []string `json:"basis,omitempty"` // "15:40 망원동 사진"
	PlanID     string   `json:"planId,omitempty"`
}

// Sure is the confidence below which an answer should ask rather than
// assume.
const Sure = 0.5

// How long a kind of plan usually lasts, before there is any history of
// the operator's own to go by.
var usualLength = map[string]time.Duration{
	"데이트": 6 * time.Hour, "식사": 2 * time.Hour, "회식": 3 * time.Hour, "모임": 3 * time.Hour,
	"운동": 90 * time.Minute, "병원": time.Hour, "업무": time.Hour, "공연": 150 * time.Minute,
	"쇼핑": 2 * time.Hour, "경조사": 2 * time.Hour,
}

const (
	clueWindow = 3 * time.Hour
	// early: a hint this long before a plan starts still counts for it -
	// arriving a little early is arriving.
	early = 30 * time.Minute
	// lengths are the operator's own average lengths by plan type, recounted
	// at most this often rather than on every question.
	lengthsFor = 10 * time.Minute
)

type lengths struct {
	mu  sync.Mutex
	at  time.Time
	avg map[string]time.Duration
	n   map[string]int
}

// Situation works out the situation at now.
func (w *World) Situation(ctx context.Context, now time.Time, loc *time.Location) (Situation, error) {
	now = now.In(loc)
	s := Situation{At: now, Plans: []Ongoing{}, Clues: []Clue{}}
	plans, err := w.st.Entities(ctx, model.KindPlan, "", true)
	if err != nil {
		return s, err
	}
	avg, n := w.lengths(ctx, now, loc)
	today := now.Format("2006-01-02")
	for _, p := range plans {
		st := p.State
		if st.Status != model.Confirmed || st.Date == "" {
			continue
		}
		o, ok := span(p, loc, avg, n)
		if !ok {
			continue
		}
		if st.Start == "" && st.EndDate == "" {
			if st.Date == today {
				s.Today = append(s.Today, o)
			}
			continue
		}
		switch {
		case !now.Before(o.Start) && now.Before(o.End):
			s.Plans = append(s.Plans, o)
		case o.Start.After(now) && st.Date == today && (s.Next == nil || o.Start.Before(s.Next.Start)):
			next := o
			s.Next = &next
		}
	}
	sort.Slice(s.Plans, func(i, j int) bool { return s.Plans[i].Start.After(s.Plans[j].Start) })

	occ, err := w.st.Occurrences(ctx, now.Add(-clueWindow), now.Add(time.Minute), "purchase", "photo")
	if err != nil {
		return s, err
	}
	for i := len(occ) - 1; i >= 0; i-- { // newest first
		o := occ[i]
		if o.Area == "" && o.Place == "" {
			continue
		}
		s.Clues = append(s.Clues, Clue{At: o.At.In(loc), Kind: o.Kind, Title: o.Title, Place: o.Place, Area: o.Area})
		if len(s.Clues) == 4 {
			break
		}
	}
	s.Guess = guess(s)
	s.Text = s.text()
	return s, nil
}

// Span is when a plan starts and ends: its own end, or the usual length of
// its kind (the operator's own average once there are a few). Untimed
// one-day plans span the day.
func (w *World) Span(ctx context.Context, e model.Entity, now time.Time, loc *time.Location) (Ongoing, bool) {
	avg, n := w.lengths(ctx, now, loc)
	return span(e, loc, avg, n)
}

// span is when a plan starts and (by its own end, or the usual length of
// its kind) ends.
func span(p model.Entity, loc *time.Location, avg map[string]time.Duration, n map[string]int) (Ongoing, bool) {
	st := p.State
	day, err := time.ParseInLocation("2006-01-02", st.Date, loc)
	if err != nil {
		return Ongoing{}, false
	}
	o := Ongoing{ID: p.ID, Title: st.Title, Type: st.Type, Place: st.Place, PlaceKind: st.PlaceKind, Area: st.Area, People: st.People}
	timed := false
	if t, err := time.ParseInLocation("2006-01-02 15:04", st.Date+" "+st.Start, loc); err == nil {
		o.Start, timed = t, true
	} else {
		o.Start = day
	}
	last := day
	if st.EndDate != "" {
		if d, err := time.ParseInLocation("2006-01-02", st.EndDate, loc); err == nil {
			last = d
		}
	}
	switch {
	case st.End != "":
		if t, err := time.ParseInLocation("2006-01-02 15:04", last.Format("2006-01-02")+" "+st.End, loc); err == nil {
			o.End, o.EndKnown = t, true
		}
	case !timed || st.EndDate != "":
		o.End, o.EndKnown = last.AddDate(0, 0, 1), st.EndDate != ""
		o.EndWhy = "하루 종일"
	}
	if o.End.IsZero() {
		d, why := avg[st.Type], fmt.Sprintf("같은 종류 평균 %s", hours(avg[st.Type]))
		if d == 0 || n[st.Type] < 2 {
			d = usualLength[st.Type]
			if d == 0 {
				d = 2 * time.Hour
			}
			why = fmt.Sprintf("보통 %s으로 잡음", hours(d))
		}
		o.End, o.EndWhy = o.Start.Add(d), why
	}
	return o, true
}

// lengths is the operator's average length of each type of plan, from
// plans with both a start and an end - cached, since it does not move by
// the minute.
func (w *World) lengths(ctx context.Context, now time.Time, loc *time.Location) (map[string]time.Duration, map[string]int) {
	w.len.mu.Lock()
	defer w.len.mu.Unlock()
	if w.len.avg != nil && now.Sub(w.len.at) < lengthsFor && !now.Before(w.len.at) {
		return w.len.avg, w.len.n
	}
	sum, n := map[string]time.Duration{}, map[string]int{}
	plans, _ := w.st.Entities(ctx, model.KindPlan, "", false)
	for _, p := range plans {
		st := p.State
		if st.Type == "" || st.Start == "" || st.End == "" || st.Status == model.Cancelled {
			continue
		}
		a, err1 := time.ParseInLocation("15:04", st.Start, loc)
		b, err2 := time.ParseInLocation("15:04", st.End, loc)
		if err1 != nil || err2 != nil || !b.After(a) || st.EndDate != "" && st.EndDate != st.Date {
			continue
		}
		sum[st.Type] += b.Sub(a)
		n[st.Type]++
	}
	avg := map[string]time.Duration{}
	for k, d := range sum {
		avg[k] = d / time.Duration(n[k])
	}
	w.len.avg, w.len.n, w.len.at = avg, n, now
	return avg, n
}

// guess is where the operator probably is. A plan going on is the start;
// a payment or photo in the same neighbourhood since it started makes it
// surer, one somewhere else makes it doubtful. Without a plan, a fresh clue
// says only where the operator was.
func guess(s Situation) *Guess {
	if len(s.Plans) > 0 && (s.Plans[0].Place != "" || s.Plans[0].Area != "" || len(s.Plans[0].People) > 0) {
		p := s.Plans[0]
		g := &Guess{Place: p.Place, PlaceKind: p.PlaceKind, Area: p.Area, With: p.People, PlanID: p.ID, Confidence: 0.45}
		if p.EndKnown {
			g.Confidence = 0.65
		}
		agree := 0
		for _, c := range s.Clues {
			if c.At.Before(p.Start.Add(-early)) {
				continue
			}
			switch {
			case same(c, p) && agree < 2:
				g.Confidence += 0.2
				agree++
				g.Basis = append(g.Basis, c.line())
			case Known(c.Area, "", p.Area, p.Place) && !same(c, p):
				g.Confidence -= 0.25
				g.Basis = append(g.Basis, c.line()+"(다른 곳)")
			}
		}
		g.Confidence = clamp(g.Confidence)
		return g
	}
	if len(s.Clues) > 0 && s.At.Sub(s.Clues[0].At) <= time.Hour {
		c := s.Clues[0]
		return &Guess{Area: c.Area, Place: c.Place, Confidence: 0.4, Basis: []string{c.line()}}
	}
	return nil
}

func same(c Clue, p Ongoing) bool {
	return SamePlace(c.Area, c.Place, p.Area, p.Place)
}

// SamePlace is whether two places, each a neighbourhood and a name (either
// may be empty), are the same place: the neighbourhoods agree, or the names
// do. A merchant "GS25 망원점" is in 망원동; "CGV 홍대" is "CGV 홍대".
func SamePlace(areaA, placeA, areaB, placeB string) bool {
	near := func(a, b string) bool {
		a, b = Region(a), Region(b)
		return a != "" && b != "" && (strings.Contains(a, b) || strings.Contains(b, a))
	}
	return near(areaA, areaB) || near(placeA, placeB) || near(areaA, placeB) || near(placeA, areaB)
}

// Known is whether two places can be told apart at all: both say where.
func Known(areaA, placeA, areaB, placeB string) bool {
	return (areaA != "" || placeA != "") && (areaB != "" || placeB != "")
}

func clamp(f float64) float64 {
	return min(max(f, 0.05), 0.95)
}

func (c Clue) line() string {
	what := map[string]string{"purchase": "결제", "photo": "사진"}[c.Kind]
	where := c.Area
	if where == "" {
		where = c.Place
	}
	return c.At.Format("15:04") + " " + where + " " + what
}

func hours(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%d시간", int(d.Hours()))
	}
	return fmt.Sprintf("%.1f시간", d.Hours())
}

// text is the situation in a few lines, as the conversation is shown it.
func (s Situation) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[지금 상황 · %s]\n", s.At.Format("15:04"))
	for _, p := range s.Plans {
		line := "- 진행 중일 수 있는 일정: " + label(p) + " " + p.Start.Format("15:04") + "~"
		if p.EndKnown {
			line += p.End.Format("15:04")
		} else {
			line += " (끝 시각 미정, " + p.EndWhy + ")"
		}
		if p.Place != "" {
			line += " · 장소 " + p.Place
			if p.PlaceKind != "" {
				line += "(" + p.PlaceKind + ")"
			}
		}
		if len(p.People) > 0 {
			line += " · 함께: " + strings.Join(p.People, ", ")
		}
		b.WriteString(line + "\n")
	}
	for _, p := range s.Today {
		line := "- 오늘(시간 미정): " + label(p)
		if p.Place != "" {
			line += " @" + p.Place
		}
		b.WriteString(line + "\n")
	}
	if s.Next != nil {
		b.WriteString("- 오늘 다음 일정: " + s.Next.Start.Format("15:04") + " " + label(*s.Next))
		if s.Next.Place != "" {
			b.WriteString(" @" + s.Next.Place)
		}
		b.WriteString("\n")
	}
	if len(s.Clues) > 0 {
		var parts []string
		for i := len(s.Clues) - 1; i >= 0; i-- {
			parts = append(parts, s.Clues[i].line())
		}
		b.WriteString("- 최근 위치 단서: " + strings.Join(parts, ", ") + "\n")
	}
	switch g := s.Guess; {
	case g == nil && len(s.Plans) == 0 && s.Next == nil && len(s.Today) == 0:
		b.WriteString("- 진행 중인 일정도 최근 위치 단서도 없음. 위치·동행이 필요한 답이면 먼저 물어볼 것\n")
	case g == nil:
		b.WriteString("- 추정 없음. 위치·동행이 필요한 답이면 먼저 물어볼 것\n")
	case g.Confidence < Sure:
		fmt.Fprintf(&b, "- 추정: 확실하지 않음 (%.2f) · %s일 수도 있지만 위치 단서가 부족함", g.Confidence, where(g))
		if len(g.Basis) > 0 {
			b.WriteString(" · 근거: " + strings.Join(g.Basis, ", "))
		}
		b.WriteString(". 위치·동행이 필요한 답이면 추측하지 말고 먼저 물어볼 것\n")
	default:
		line := fmt.Sprintf("- 추정: %s", where(g))
		if len(g.With) > 0 {
			line += "에서 " + strings.Join(g.With, ", ") + "와 함께"
		}
		fmt.Fprintf(&b, "%s (%.2f)", line, g.Confidence)
		if len(g.Basis) > 0 {
			b.WriteString(" · 근거: " + strings.Join(g.Basis, ", "))
		}
		b.WriteString("\n")
		if g.PlaceKind != "" && g.PlaceKind != "집" {
			fmt.Fprintf(&b, "- 장소 종류가 %s: 배달·주문 요청이면 근처 가게를 원하는지, 집에 가서 할지 먼저 확인할 것\n", g.PlaceKind)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func label(p Ongoing) string {
	if p.Type != "" && !strings.Contains(p.Title, p.Type) {
		return p.Type + "(" + p.Title + ")"
	}
	return p.Title
}

func where(g *Guess) string {
	switch {
	case g.Place != "":
		return g.Place
	case g.Area != "":
		return g.Area
	}
	return "알 수 없는 곳"
}
