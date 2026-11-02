// Package goals keeps what the operator (and the guest, each their own) means
// to do again and again - every day, on chosen days, so many times a week or a
// month - and whether they did. Marking is theirs: a tap on 했다 or 안 했다, a
// word in the conversation. What it adds up to is worked out here, from the
// marks alone, so there is nothing stored to drift from them: how this week
// stands, whether what is left can still make it, how long it has held, how far
// it has gone.
package goals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Whose a goal is. Each sees and changes only their own.
const (
	Owner = "owner"
	Guest = "guest"
)

// How often a goal asks.
const (
	Daily   = "daily"   // every day
	Days    = "days"    // on chosen weekdays
	Weekly  = "weekly"  // so many times a week, any days
	Monthly = "monthly" // so many times a month, any days
)

// Where a goal stands.
const (
	Active = "active"
	Paused = "paused"
	Ended  = "ended"
)

// What a day was marked.
const (
	Done   = "done"   // 했다
	Missed = "missed" // 안 했다
)

// Who marked it.
const (
	ByCard    = "card"    // a tap
	ByChat    = "chat"    // the conversation
	ByWorkout = "workout" // a workout saved, for a goal tied to them
)

// AutoWorkout ties a goal to the workout log: a day with a workout is done.
const AutoWorkout = "workout"

// Rest is a stretch a goal was paused: its days ask nothing. To is empty while
// it lasts.
type Rest struct {
	From string `json:"from"`
	To   string `json:"to,omitempty"`
}

// Goal is one goal.
type Goal struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Note is why: a line for the day it gets hard.
	Note    string `json:"note,omitempty"`
	Cadence string `json:"cadence"`
	// Times is how many a week or a month.
	Times int `json:"times,omitempty"`
	// Weekdays are the days it asks, Sunday 0, ascending.
	Weekdays []int  `json:"weekdays,omitempty"`
	Start    string `json:"start"`         // the first day it counts, YYYY-MM-DD
	End      string `json:"end,omitempty"` // the last, when it has one
	// Remind is when to ask that day, "21:00"; empty: only when it matters.
	Remind string `json:"remind,omitempty"`
	Auto   string `json:"auto,omitempty"`
	Status string `json:"status"`
	Rests  []Rest `json:"rests,omitempty"`
	// Who is whose it is: "" or owner, the operator's; guest, the guest's.
	Who       string    `json:"who,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Person is whose the goal is: Owner or Guest.
func (g Goal) Person() string {
	if g.Who == Guest {
		return Guest
	}
	return Owner
}

// Mark is one day's word on a goal.
type Mark struct {
	Goal string    `json:"goal"`
	Day  string    `json:"day"` // YYYY-MM-DD
	Mark string    `json:"mark"`
	Note string    `json:"note,omitempty"`
	By   string    `json:"by,omitempty"`
	At   time.Time `json:"at"`
}

// Store keeps the goals and their marks.
type Store interface {
	PutGoal(ctx context.Context, g Goal) error
	Goal(ctx context.Context, id string) (Goal, error)
	// Goals is those not ended, and with ended those ended too.
	Goals(ctx context.Context, ended bool) ([]Goal, error)
	DeleteGoal(ctx context.Context, id string) error
	PutMark(ctx context.Context, m Mark) error
	DeleteMark(ctx context.Context, goal, day string) error
	// Marks is a goal's marks, by day.
	Marks(ctx context.Context, goal string) (map[string]Mark, error)
}

var ErrNotFound = errors.New("그런 목표가 없습니다")

// ErrInvalid is what the caller has to fix; its text says how.
type ErrInvalid string

func (e ErrInvalid) Error() string { return string(e) }

const (
	dayLayout = "2006-01-02"
	maxTitle  = 60
	maxNote   = 200
	maxGoals  = 40
)

var clockRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Service makes and changes the goals.
type Service struct {
	St  Store
	Now func() time.Time
	Loc *time.Location
	mu  sync.Mutex
}

// Today is the day it is here.
func (s *Service) Today() string { return s.Now().In(s.Loc).Format(dayLayout) }

// New is what a goal is made with.
type New struct {
	Title    string `json:"title"`
	Note     string `json:"note,omitempty"`
	Cadence  string `json:"cadence"`
	Times    int    `json:"times,omitempty"`
	Weekdays []int  `json:"weekdays,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	Remind   string `json:"remind,omitempty"`
	Auto     string `json:"auto,omitempty"`
	// Who is whose it is (the caller's token, never the body).
	Who string `json:"-"`
}

// Make stores a new goal, counting from today unless it says otherwise.
func (s *Service) Make(ctx context.Context, n New) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.St.Goals(ctx, false)
	if err != nil {
		return Goal{}, err
	}
	who := Owner
	if n.Who == Guest {
		who = Guest
	}
	mine := 0
	for _, g := range all {
		if g.Person() == who {
			mine++
		}
	}
	if mine >= maxGoals {
		return Goal{}, ErrInvalid(fmt.Sprintf("목표는 %d개까지입니다", maxGoals))
	}
	now := s.Now()
	g := Goal{ID: newID(), Title: n.Title, Note: n.Note, Cadence: n.Cadence, Times: n.Times, Weekdays: n.Weekdays, Start: n.Start, End: n.End,
		Remind: n.Remind, Auto: n.Auto, Status: Active, CreatedAt: now, UpdatedAt: now}
	if n.Who == Guest {
		g.Who = Guest
	}
	if g.Start == "" {
		g.Start = s.Today()
	}
	if err := g.tidy(); err != nil {
		return Goal{}, err
	}
	return g, s.St.PutGoal(ctx, g)
}

// tidy puts a goal in its one form, or says what is wrong with it.
func (g *Goal) tidy() error {
	g.Title = clip(clean(g.Title), maxTitle)
	g.Note = clip(clean(g.Note), maxNote)
	if g.Title == "" {
		return ErrInvalid("목표 이름(title)이 필요합니다")
	}
	switch g.Cadence {
	case Daily:
		g.Times, g.Weekdays = 0, nil
	case Days:
		g.Times = 0
		var days []int
		for _, d := range g.Weekdays {
			if d < 0 || d > 6 {
				return ErrInvalid(fmt.Sprintf("요일(weekdays)은 0(일)~6(토)입니다: %d", d))
			}
			if !slices.Contains(days, d) {
				days = append(days, d)
			}
		}
		slices.Sort(days)
		g.Weekdays = days
		if len(days) == 0 {
			return ErrInvalid("요일(weekdays)을 하나는 골라 주세요")
		}
		if len(days) == 7 {
			g.Cadence, g.Weekdays = Daily, nil
		}
	case Weekly:
		g.Weekdays = nil
		if g.Times < 1 || g.Times > 7 {
			return ErrInvalid("한 주에 몇 번인지(times)는 1~7입니다")
		}
		if g.Times == 7 {
			g.Cadence, g.Times = Daily, 0
		}
	case Monthly:
		g.Weekdays = nil
		if g.Times < 1 || g.Times > 28 {
			return ErrInvalid("한 달에 몇 번인지(times)는 1~28입니다")
		}
	default:
		return ErrInvalid(fmt.Sprintf("주기(cadence)는 daily, days, weekly, monthly 중 하나입니다: %q", g.Cadence))
	}
	if _, err := time.Parse(dayLayout, g.Start); err != nil {
		return ErrInvalid(fmt.Sprintf("시작일(start)은 YYYY-MM-DD 입니다: %q", g.Start))
	}
	if g.End != "" {
		if _, err := time.Parse(dayLayout, g.End); err != nil {
			return ErrInvalid(fmt.Sprintf("끝나는 날(end)은 YYYY-MM-DD 입니다: %q", g.End))
		}
		if g.End < g.Start {
			return ErrInvalid("끝나는 날이 시작일보다 앞입니다")
		}
	}
	if g.Remind != "" && !clockRE.MatchString(g.Remind) {
		return ErrInvalid(fmt.Sprintf("알림 시각(remind)은 HH:MM 입니다: %q", g.Remind))
	}
	if g.Auto != "" && g.Auto != AutoWorkout {
		return ErrInvalid(fmt.Sprintf("모르는 연결입니다: %q", g.Auto))
	}
	return nil
}

// Change is what a change gives: every field is optional.
type Change struct {
	Title    *string `json:"title,omitempty"`
	Note     *string `json:"note,omitempty"`
	Cadence  *string `json:"cadence,omitempty"`
	Times    *int    `json:"times,omitempty"`
	Weekdays *[]int  `json:"weekdays,omitempty"`
	Start    *string `json:"start,omitempty"`
	End      *string `json:"end,omitempty"`
	Remind   *string `json:"remind,omitempty"`
	Auto     *string `json:"auto,omitempty"`
	// Pause rests it from today, or with false picks it up again.
	Pause *bool `json:"pause,omitempty"`
	// Finish ends it today, or with false takes it up again.
	Finish *bool `json:"finish,omitempty"`
}

// Change changes a goal.
func (s *Service) Change(ctx context.Context, id string, c Change) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.St.Goal(ctx, id)
	if err != nil {
		return g, err
	}
	today := s.Today()
	for _, set := range []struct {
		from *string
		to   *string
	}{{c.Title, &g.Title}, {c.Note, &g.Note}, {c.Cadence, &g.Cadence}, {c.Start, &g.Start}, {c.End, &g.End}, {c.Remind, &g.Remind}, {c.Auto, &g.Auto}} {
		if set.from != nil {
			*set.to = *set.from
		}
	}
	if c.Times != nil {
		g.Times = *c.Times
	}
	if c.Weekdays != nil {
		g.Weekdays = *c.Weekdays
	}
	if err := g.tidy(); err != nil {
		return g, err
	}
	if c.Pause != nil {
		g.pause(*c.Pause, today)
	}
	if c.Finish != nil {
		g.finish(*c.Finish, today)
	}
	g.UpdatedAt = s.Now()
	return g, s.St.PutGoal(ctx, g)
}

func (g *Goal) pause(on bool, today string) {
	switch {
	case on && g.Status == Active:
		g.Status = Paused
		g.Rests = append(g.Rests, Rest{From: today})
	case !on && g.Status == Paused:
		g.Status = Active
		if n := len(g.Rests); n > 0 && g.Rests[n-1].To == "" {
			// A rest started and taken back the same day is no rest.
			if g.Rests[n-1].From >= today {
				g.Rests = g.Rests[:n-1]
			} else {
				g.Rests[n-1].To = shift(today, -1)
			}
		}
	}
}

func (g *Goal) finish(on bool, today string) {
	switch {
	case on && g.Status != Ended:
		g.pause(false, today)
		g.Status = Ended
		if g.End == "" || g.End > today {
			g.End = max(today, g.Start)
		}
	case !on && g.Status == Ended:
		g.Status = Active
		if g.End != "" && g.End <= today {
			g.End = ""
		}
	}
}

// MarkDay says how a day went: Done, Missed, or "" to take it back. Today and
// the days before it, back to the goal's first.
func (s *Service) MarkDay(ctx context.Context, id, day, mark, note, by string) (Goal, map[string]Mark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, err := s.St.Goal(ctx, id)
	if err != nil {
		return g, nil, err
	}
	today := s.Today()
	if day == "" {
		day = today
	}
	if _, err := time.Parse(dayLayout, day); err != nil {
		return g, nil, ErrInvalid(fmt.Sprintf("날짜(day)는 YYYY-MM-DD 입니다: %q", day))
	}
	switch {
	case day > today:
		return g, nil, ErrInvalid("아직 오지 않은 날은 기록할 수 없습니다")
	case day < g.Start:
		return g, nil, ErrInvalid("시작일 전의 날은 기록할 수 없습니다. 시작일을 앞당겨 주세요")
	case g.End != "" && day > g.End:
		return g, nil, ErrInvalid("끝난 뒤의 날은 기록할 수 없습니다")
	}
	switch mark {
	case "":
		err = s.St.DeleteMark(ctx, id, day)
	case Done, Missed:
		err = s.St.PutMark(ctx, Mark{Goal: id, Day: day, Mark: mark, Note: clip(clean(note), maxNote), By: by, At: s.Now()})
	default:
		return g, nil, ErrInvalid(fmt.Sprintf("기록(mark)은 done, missed, 또는 빈 값입니다: %q", mark))
	}
	if err != nil {
		return g, nil, err
	}
	marks, err := s.St.Marks(ctx, id)
	return g, marks, err
}

// Unmarked says Done of a day nobody has marked: what a tied source reports
// does not overrule what the person said.
func (s *Service) Unmarked(ctx context.Context, id, day, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	marks, err := s.St.Marks(ctx, id)
	if err != nil {
		return err
	}
	if _, ok := marks[day]; ok {
		return nil
	}
	return s.St.PutMark(ctx, Mark{Goal: id, Day: day, Mark: Done, By: by, At: s.Now()})
}

// Remove deletes a goal and its marks.
func (s *Service) Remove(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.St.DeleteGoal(ctx, id)
}

// Get is one goal.
func (s *Service) Get(ctx context.Context, id string) (Goal, error) { return s.St.Goal(ctx, id) }

// Marks is a goal's marks.
func (s *Service) Marks(ctx context.Context, id string) (map[string]Mark, error) {
	return s.St.Marks(ctx, id)
}

// AllOf is who's goals, those going first, the newest first; everyone's when
// who is empty.
func (s *Service) AllOf(ctx context.Context, who string, ended bool) ([]Goal, error) {
	gs, err := s.St.Goals(ctx, ended)
	if err != nil {
		return nil, err
	}
	if who != "" {
		gs = slices.DeleteFunc(gs, func(g Goal) bool { return g.Person() != who })
	}
	rank := func(g Goal) int {
		switch g.Status {
		case Active:
			return 0
		case Paused:
			return 1
		}
		return 2
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if a, b := rank(gs[i]), rank(gs[j]); a != b {
			return a < b
		}
		return gs[i].CreatedAt.Before(gs[j].CreatedAt)
	})
	return gs, nil
}

// --- what the marks add up to -------------------------------------------------

func parse(day string) time.Time {
	t, _ := time.Parse(dayLayout, day)
	return t
}

// shift is day moved by n days.
func shift(day string, n int) string { return parse(day).AddDate(0, 0, n).Format(dayLayout) }

// monday is the Monday of day's week.
func monday(day string) string {
	t := parse(day)
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7)).Format(dayLayout)
}

// bounds is the period day falls in.
func (g Goal) bounds(day string) (string, string) {
	switch g.Cadence {
	case Weekly:
		from := monday(day)
		return from, shift(from, 6)
	case Monthly:
		t := parse(day)
		first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		return first.Format(dayLayout), first.AddDate(0, 1, -1).Format(dayLayout)
	}
	return day, day
}

// counts is whether a day is one the goal runs on at all: inside its span and
// not rested.
func (g Goal) counts(day string) bool {
	if day < g.Start || g.End != "" && day > g.End {
		return false
	}
	for _, r := range g.Rests {
		if day >= r.From && (r.To == "" || day <= r.To) {
			return false
		}
	}
	return true
}

// Asks is whether the goal asks of that day in particular. A goal of so many a
// week asks of no day: any of them will do.
func (g Goal) Asks(day string) bool {
	if !g.counts(day) {
		return false
	}
	switch g.Cadence {
	case Daily:
		return true
	case Days:
		return slices.Contains(g.Weekdays, int(parse(day).Weekday()))
	}
	return false
}

// Period is one stretch a goal is counted over: a due day, a week, a month.
type Period struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Target int    `json:"target"`
	Done   int    `json:"done"`
}

// Hit is whether the period made its target.
func (p Period) Hit() bool { return p.Target > 0 && p.Done >= p.Target }

// target is how many a period asks: a week or a month the goal runs for only
// part of asks its share, rounded up.
func (g Goal) target(from, to string) int {
	if g.Cadence != Weekly && g.Cadence != Monthly {
		if g.Asks(from) {
			return 1
		}
		return 0
	}
	all, open := 0, 0
	for d := from; d <= to; d = shift(d, 1) {
		all++
		if g.counts(d) {
			open++
		}
	}
	if open == 0 {
		return 0
	}
	return min(open, int(math.Ceil(float64(g.Times)*float64(open)/float64(all))))
}

func (g Goal) period(day string, marks map[string]Mark) Period {
	from, to := g.bounds(day)
	p := Period{From: from, To: to, Target: g.target(from, to)}
	for d := from; d <= to; d = shift(d, 1) {
		if m, ok := marks[d]; ok && m.Mark == Done {
			p.Done++
		}
	}
	return p
}

// Periods is the goal's periods from its first through today, oldest first,
// those that ask anything.
func (g Goal) Periods(marks map[string]Mark, today string) []Period {
	last := today
	if g.End != "" && g.End < last {
		last = g.End
	}
	var out []Period
	for d := g.Start; d <= last; {
		p := g.period(d, marks)
		if p.Target > 0 {
			out = append(out, p)
		}
		d = shift(p.To, 1)
	}
	return out
}

// Cell is one day of a week as a row of the screen draws it.
type Cell struct {
	Day  string `json:"day"`
	Asks bool   `json:"asks,omitempty"` // the goal asks of this day
	Open bool   `json:"open,omitempty"` // it may be marked: not yet, not rested
	Mark string `json:"mark,omitempty"`
}

// Progress is where a goal stands today.
type Progress struct {
	Today string `json:"today"`
	// Asks is whether today is asked of; Mark what today was marked.
	Asks bool   `json:"asks"`
	Mark string `json:"mark,omitempty"`
	// Period is the one today falls in.
	Period Period `json:"period"`
	// Need is how many it still takes, Room how many days could still give it
	// (today among them unless it was marked).
	Need int `json:"need"`
	Room int `json:"room"`
	// Tight: every day left is needed. Lost: there are not enough left.
	Tight bool `json:"tight,omitempty"`
	Lost  bool `json:"lost,omitempty"`
	// Streak is how many in a row it holds now, Best the most it ever did, Unit
	// what they count: 일, 번(chosen days), 주, 달.
	Streak int    `json:"streak"`
	Best   int    `json:"best"`
	Unit   string `json:"unit"`
	// Rate is how much of what it has asked so far was done, 0~1; nil before
	// it has asked anything.
	Rate *float64 `json:"rate"`
	// Times is every day marked done.
	Times int    `json:"times"`
	Week  []Cell `json:"week"`
	// Says is the row's line.
	Says string `json:"says"`
}

// Of is where the goal stands, from its marks.
func (g Goal) Of(marks map[string]Mark, today string) Progress {
	p := Progress{Today: today, Asks: g.Asks(today), Unit: g.unit()}
	if m, ok := marks[today]; ok {
		p.Mark = m.Mark
	}
	for _, m := range marks {
		if m.Mark == Done {
			p.Times++
		}
	}
	p.Period = g.period(today, marks)
	p.Need = max(p.Period.Target-p.Period.Done, 0)
	for d := today; d <= p.Period.To; d = shift(d, 1) {
		if g.counts(d) && (d != today || p.Mark == "") && (g.Cadence == Weekly || g.Cadence == Monthly || g.Asks(d)) {
			p.Room++
		}
	}
	// A day goal is today or not: only a count over days can run short of them.
	if g.Status == Active && p.Need > 0 && (g.Cadence == Weekly || g.Cadence == Monthly) {
		p.Tight = p.Need == p.Room
		p.Lost = p.Need > p.Room
	}
	periods := g.Periods(marks, today)
	p.Streak, p.Best = g.streaks(periods, today, p.Mark)
	p.Rate = g.rate(periods, today, p.Mark)
	week := monday(today)
	for i := range 7 {
		d := shift(week, i)
		c := Cell{Day: d, Asks: g.Asks(d), Open: d <= today && g.counts(d)}
		if m, ok := marks[d]; ok {
			c.Mark = m.Mark
		}
		p.Week = append(p.Week, c)
	}
	p.Says = g.says(p)
	return p
}

func (g Goal) unit() string {
	switch g.Cadence {
	case Weekly:
		return "주"
	case Monthly:
		return "달"
	case Days:
		return "번"
	}
	return "일"
}

// open is whether a period has yet to close: today is in it and it could still
// change. A day marked closes itself.
func open(p Period, today, mark string, single bool) bool {
	if p.To < today {
		return false
	}
	return !single || mark == ""
}

// streaks walks the periods: a run is broken by one that closed short; one still
// open breaks nothing, and counts once it is made.
func (g Goal) streaks(periods []Period, today, mark string) (now, best int) {
	single := g.Cadence == Daily || g.Cadence == Days
	run := 0
	for _, p := range periods {
		switch {
		case p.Hit():
			run++
		case open(p, today, mark, single):
			continue
		default:
			run = 0
		}
		best = max(best, run)
	}
	return run, best
}

// rate is what was done of what has been asked. A period still open is counted
// only when it is made, so a Monday morning does not read as a bad week.
func (g Goal) rate(periods []Period, today, mark string) *float64 {
	single := g.Cadence == Daily || g.Cadence == Days
	got, want := 0, 0
	for _, p := range periods {
		if open(p, today, mark, single) && !p.Hit() {
			continue
		}
		got += min(p.Done, p.Target)
		want += p.Target
	}
	if want == 0 {
		return nil
	}
	r := float64(got) / float64(want)
	return &r
}

// says is a row's line under its title.
func (g Goal) says(p Progress) string {
	var parts []string
	switch g.Status {
	case Paused:
		return "쉬는 중"
	case Ended:
		if p.Rate != nil {
			return fmt.Sprintf("끝남 · 달성률 %.0f%%", *p.Rate*100)
		}
		return "끝남"
	}
	if p.Today < g.Start {
		return fmt.Sprintf("%s부터", shortDay(g.Start))
	}
	switch g.Cadence {
	case Weekly, Monthly:
		word := "이번 주"
		if g.Cadence == Monthly {
			word = "이번 달"
		}
		parts = append(parts, fmt.Sprintf("%s %d/%d", word, p.Period.Done, p.Period.Target))
		switch {
		case p.Need == 0:
			parts = append(parts, "다 채웠어요")
		case p.Lost:
			parts = append(parts, fmt.Sprintf("남은 날로는 %d번까지", p.Period.Done+p.Room))
		case p.Tight:
			parts = append(parts, "남은 날 모두 해야 해요")
		default:
			parts = append(parts, fmt.Sprintf("남은 %d일 중 %d번", p.Room, p.Need))
		}
	default:
		if !p.Asks {
			parts = append(parts, "오늘은 쉬는 날")
		}
		if p.Streak >= 2 {
			parts = append(parts, fmt.Sprintf("%d%s째", p.Streak, p.Unit))
		}
	}
	return strings.Join(parts, " · ")
}

func shortDay(day string) string {
	t := parse(day)
	return fmt.Sprintf("%d/%d", int(t.Month()), t.Day())
}

var weekdayNames = []string{"일", "월", "화", "수", "목", "금", "토"}

// Rule says how often a goal asks: "매일", "월·수·금", "주 3회".
func (g Goal) Rule() string {
	switch g.Cadence {
	case Daily:
		return "매일"
	case Days:
		names := make([]string, 0, len(g.Weekdays))
		// Monday first, as the week is read.
		for i := 1; i <= 7; i++ {
			if d := i % 7; slices.Contains(g.Weekdays, d) {
				names = append(names, weekdayNames[d])
			}
		}
		return strings.Join(names, "·")
	case Weekly:
		return fmt.Sprintf("주 %d회", g.Times)
	case Monthly:
		return fmt.Sprintf("월 %d회", g.Times)
	}
	return ""
}

// Line is a goal in one line, for the conversation's situation and a tool's
// answer: "[goal_1a2b] 운동 (주 3회) 이번 주 1/3 · 남은 4일 중 2번 · 오늘 기록 없음".
func (g Goal) Line(p Progress) string {
	s := "[" + g.ID + "] " + g.Title + " (" + g.Rule() + ")"
	if p.Says != "" {
		s += " " + p.Says
	}
	if g.Status == Active {
		switch {
		case p.Mark == Done:
			s += " · 오늘 했음"
		case p.Mark == Missed:
			s += " · 오늘 안 했음"
		case p.Asks:
			s += " · 오늘 기록 없음"
		}
	}
	return s
}

// LeftDays is the days from after today to the end of the period that could
// still count, for saying which ones it will take.
func (g Goal) LeftDays(today string) []string {
	_, to := g.bounds(today)
	var out []string
	for d := shift(today, 1); d <= to; d = shift(d, 1) {
		if g.counts(d) && (g.Cadence == Weekly || g.Cadence == Monthly || g.Asks(d)) {
			out = append(out, d)
		}
	}
	return out
}

// Name is the weekday of a day, as said aloud.
func Name(day string) string { return weekdayNames[parse(day).Weekday()] }

// Bar is one bar of the goal's graph: a week, or a month for a monthly goal.
type Bar struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Target int    `json:"target"`
	Done   int    `json:"done"`
	// Open: it is still going.
	Open bool `json:"open,omitempty"`
}

// Weekday is how one day of the week has gone.
type Weekday struct {
	Day  int `json:"day"` // Sunday 0
	Done int `json:"done"`
	Asks int `json:"asks"`
}

// Stats is what a goal's page draws.
type Stats struct {
	Bars []Bar `json:"bars"`
	// Weekdays is Monday first.
	Weekdays []Weekday `json:"weekdays"`
	// Marks is every day marked, by day.
	Marks map[string]string `json:"marks"`
}

// barsShown is how far back the graph goes: twelve weeks, or twelve months.
const barsShown = 12

// StatsOf is the goal's graph, from its marks.
func (g Goal) StatsOf(marks map[string]Mark, today string) Stats {
	st := Stats{Bars: []Bar{}, Weekdays: []Weekday{}, Marks: map[string]string{}}
	for d, m := range marks {
		st.Marks[d] = m.Mark
	}
	last := today
	if g.End != "" && g.End < last {
		last = g.End
	}
	// Weeks, but a monthly goal's own months.
	for n := barsShown - 1; n >= 0; n-- {
		var from, to string
		if g.Cadence == Monthly {
			t := parse(last)
			first := time.Date(t.Year(), t.Month()-time.Month(n), 1, 0, 0, 0, 0, time.UTC)
			from, to = first.Format(dayLayout), first.AddDate(0, 1, -1).Format(dayLayout)
		} else {
			from = shift(monday(last), -7*n)
			to = shift(from, 6)
		}
		if to < g.Start {
			continue
		}
		b := Bar{From: from, To: to, Open: to >= today}
		ask := 0
		for d := from; d <= to; d = shift(d, 1) {
			if m, ok := marks[d]; ok && m.Mark == Done {
				b.Done++
			}
			if g.Asks(d) {
				ask++
			}
		}
		switch g.Cadence {
		case Weekly, Monthly:
			b.Target = g.target(from, to)
		default:
			b.Target = ask
		}
		st.Bars = append(st.Bars, b)
	}
	days := [7]Weekday{}
	for i := range days {
		days[i].Day = i
	}
	for d := g.Start; d <= last; d = shift(d, 1) {
		w := int(parse(d).Weekday())
		m, marked := marks[d]
		switch g.Cadence {
		case Weekly, Monthly:
			// Any day will do: what counts is how the days it was done fall.
			if g.counts(d) {
				days[w].Asks++
			}
		default:
			if g.Asks(d) && (d < today || marked) {
				days[w].Asks++
			}
		}
		if marked && m.Mark == Done {
			days[w].Done++
		}
	}
	for i := 1; i <= 7; i++ {
		st.Weekdays = append(st.Weekdays, days[i%7])
	}
	return st
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "goal_" + hex.EncodeToString(b)
}
