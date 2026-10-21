// Package moments is what the operator is told at a moment rather than at
// once: what they asked to be reminded of - at a time, on coming home or
// leaving it - and what the assistant keeps for such a moment (a rebalance
// for the evening at home).
//
// A reminder waits for its moment and is said once:
//
//	at           a time                                     "10/24 08:00"
//	place, move  the next arrival at, or leaving of, a      "집에 오면"
//	             region: its kind (home, work) or its name
//	after        a place counts from then, and being there  "금요일 19시 넘어 집에 있으면"
//	             already when it comes counts as arriving
//	at + place   the latest: with no arrival by then it is  "늦어도 23시엔"
//	             said at that time anyway - the phone may
//	             not have told
//	until        a place reminder with no latest stops
//	             waiting then
//
// What is stored is what is said, and the conversation answers from what is
// stored - never from what it meant to store.
package moments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Moves.
const (
	Arrive = "arrive"
	Leave  = "leave"
)

// Statuses.
const (
	Waiting   = "waiting"
	Sent      = "sent"
	Cancelled = "cancelled"
	Expired   = "expired"
)

// Sources: who wanted it said.
const (
	Asked     = "chat"      // the operator, in the conversation
	Console   = "console"   // the operator, on mind's console
	Rebalance = "rebalance" // the assistant: a portfolio out of band, for the evening at home
	Lists     = "checklist" // the assistant: what is left on a list, the evening before its day
)

// What brought the moment (SentBy).
const (
	ByTime   = "time"   // its time came
	ByArrive = "arrive" // the operator arrived
	ByLeave  = "leave"  // the operator left
	ByThere  = "there"  // the operator was already there when it began to count
	ByLatest = "latest" // no arrival or leaving by its latest time
)

// Reminder is one thing to say at one moment.
type Reminder struct {
	ID     string `json:"id"`
	Text   string `json:"text"`           // what is said: "내일 캠핑 장갑 챙기세요"
	Body   string `json:"body,omitempty"` // a line more, under it
	Source string `json:"source"`
	// Topic: one reminder waits per topic; setting another changes it.
	Topic string `json:"topic,omitempty"`
	// Ref is what it came from, for the one who says it: the band event a
	// rebalance was raised by.
	Ref   string     `json:"ref,omitempty"`
	At    *time.Time `json:"at,omitempty"`
	Place string     `json:"place,omitempty"` // home | work | a region's name
	Move  string     `json:"move,omitempty"`  // arrive | leave
	After *time.Time `json:"after,omitempty"`
	Until *time.Time `json:"until,omitempty"`
	URL   string     `json:"url,omitempty"` // where a tap goes
	// Checklist is a list shown with it, as it stands then (checklist): what
	// to pack, what to do on getting home.
	Checklist string `json:"checklist,omitempty"`
	// Status, and what came of saying it: "푸시", "카드", or why nothing
	// was said.
	Status    string     `json:"status"`
	SentAt    *time.Time `json:"sentAt,omitempty"`
	SentBy    string     `json:"sentBy,omitempty"`
	Result    string     `json:"result,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// Visit is a region the operator is in now (context-svc's open visits).
type Visit struct {
	Region string `json:"region"`
	Kind   string `json:"kind"`
}

// Store keeps the reminders.
type Store interface {
	PutReminder(ctx context.Context, r Reminder) error
	Reminder(ctx context.Context, id string) (Reminder, error)
	// Reminders is those waiting, and with since set those said,
	// cancelled or expired since then as well.
	Reminders(ctx context.Context, since time.Time) ([]Reminder, error)
}

var ErrNotFound = errors.New("그런 알림이 없습니다")

// ErrInvalid marks a reminder the caller has to fix; its text says how.
type ErrInvalid string

func (e ErrInvalid) Error() string { return string(e) }

// Service waits for the moments and says the reminders.
type Service struct {
	St  Store
	Now func() time.Time
	Loc *time.Location
	// Here is the regions the operator is in now; nil or an error: not
	// known, and nobody is taken to be anywhere.
	Here func(ctx context.Context) ([]Visit, error)
	// Say says a reminder whose moment came, and what came of it ("푸시");
	// an error leaves it waiting, to be tried again.
	Say func(ctx context.Context, r Reminder) (string, error)
	Out *slog.Logger

	mu sync.Mutex
	// looked: reminders whose start was looked at for "already there" -
	// once each (again after a restart, which is harmless: being there
	// while it counts is its moment).
	looked map[string]bool
}

// Set stores a new reminder; one with the topic of a reminder still waiting
// changes that one instead.
func (s *Service) Set(ctx context.Context, r Reminder) (Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	r.ID, r.Status, r.SentAt, r.SentBy, r.Result = "", Waiting, nil, "", ""
	r.CreatedAt, r.UpdatedAt = now, now
	if r.Source == "" {
		r.Source = Asked
	}
	if r.Topic != "" {
		waiting, err := s.St.Reminders(ctx, time.Time{})
		if err != nil {
			return r, err
		}
		for _, w := range waiting {
			if w.Topic == r.Topic && w.Status == Waiting {
				r.ID, r.CreatedAt = w.ID, w.CreatedAt
			}
		}
	}
	if r.ID == "" {
		r.ID = newID()
	}
	if err := s.check(&r, now); err != nil {
		return r, err
	}
	delete(s.looked, r.ID)
	return r, s.St.PutReminder(ctx, r)
}

// Change is what a change gives: the fields set replace the reminder's, and
// Clear names those to empty (at, place, after, until, checklist).
type Change struct {
	Text      *string    `json:"text,omitempty"`
	Body      *string    `json:"body,omitempty"`
	Checklist *string    `json:"checklist,omitempty"`
	At        *time.Time `json:"at,omitempty"`
	Place     *string    `json:"place,omitempty"`
	Move      *string    `json:"move,omitempty"`
	After     *time.Time `json:"after,omitempty"`
	Until     *time.Time `json:"until,omitempty"`
	Clear     []string   `json:"clear,omitempty"`
}

// Change changes a reminder. One already said or cancelled waits again, for
// its new moment.
func (s *Service) Change(ctx context.Context, id string, c Change) (Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.St.Reminder(ctx, id)
	if err != nil {
		return r, err
	}
	for _, f := range c.Clear {
		switch f {
		case "at":
			r.At = nil
		case "place":
			r.Place, r.Move = "", ""
		case "after":
			r.After = nil
		case "until":
			r.Until = nil
		case "checklist":
			r.Checklist = ""
		default:
			return r, ErrInvalid(fmt.Sprintf("비울 수 없는 칸입니다: %s (at, place, after, until, checklist 만)", f))
		}
	}
	if c.Checklist != nil {
		r.Checklist = strings.TrimSpace(*c.Checklist)
	}
	if c.Text != nil {
		r.Text = *c.Text
	}
	if c.Body != nil {
		r.Body = *c.Body
	}
	if c.At != nil {
		r.At = c.At
	}
	if c.Place != nil {
		r.Place = *c.Place
	}
	if c.Move != nil {
		r.Move = *c.Move
	}
	if c.After != nil {
		r.After = c.After
	}
	if c.Until != nil {
		r.Until = c.Until
	}
	now := s.Now()
	r.Status, r.SentAt, r.SentBy, r.Result, r.UpdatedAt = Waiting, nil, "", "", now
	if err := s.check(&r, now); err != nil {
		return r, err
	}
	delete(s.looked, r.ID)
	return r, s.St.PutReminder(ctx, r)
}

// Cancel stops a reminder from being said.
func (s *Service) Cancel(ctx context.Context, id string) (Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.St.Reminder(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Status != Waiting {
		return r, ErrInvalid("기다리는 알림이 아닙니다 (" + StatusName(r.Status) + ")")
	}
	r.Status, r.UpdatedAt = Cancelled, s.Now()
	return r, s.St.PutReminder(ctx, r)
}

// CancelTopic stops the reminder of this topic still waiting, if one is.
func (s *Service) CancelTopic(ctx context.Context, topic string) error {
	if topic == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.St.Reminders(ctx, time.Time{})
	if err != nil {
		return err
	}
	for _, r := range rs {
		if r.Topic == topic && r.Status == Waiting {
			r.Status, r.UpdatedAt = Cancelled, s.Now()
			if err := s.St.PutReminder(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// recent is how far back List shows the reminders no longer waiting.
const recent = 3 * 24 * time.Hour

// List is the reminders waiting, soonest first - and, with all, those said,
// cancelled or expired in the last three days after them, newest first.
func (s *Service) List(ctx context.Context, all bool) ([]Reminder, error) {
	since := time.Time{}
	if all {
		since = s.Now().Add(-recent)
	}
	rs, err := s.St.Reminders(ctx, since)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if (a.Status == Waiting) != (b.Status == Waiting) {
			return a.Status == Waiting
		}
		if a.Status == Waiting {
			return a.soonest().Before(b.soonest())
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
	return rs, nil
}

// soonest is the earliest the reminder can be said: for ordering.
func (r Reminder) soonest() time.Time {
	for _, t := range []*time.Time{r.After, r.At, r.Until} {
		if t != nil {
			return *t
		}
	}
	return r.CreatedAt.Add(100 * 365 * 24 * time.Hour)
}

// stale is how old a move may be and still be one: a move delivered late
// (the service restarting) is not the moment it was.
const stale = 30 * time.Minute

// Moved is the operator arriving at a region or leaving it: the reminders
// waiting for that move are said.
func (s *Service) Moved(ctx context.Context, move, region, kind string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Now().Sub(at) > stale {
		return nil
	}
	rs, err := s.St.Reminders(ctx, time.Time{})
	if err != nil {
		return err
	}
	for _, r := range rs {
		if r.Status != Waiting || r.Place == "" || r.Move != move || !matches(r.Place, region, kind) {
			continue
		}
		// A move before it counts, or before it was set as it is.
		if r.After != nil && at.Before(*r.After) || at.Before(r.UpdatedAt) {
			continue
		}
		s.say(ctx, r, map[string]string{Arrive: ByArrive, Leave: ByLeave}[move])
	}
	return nil
}

// Tick says what its time brought: a time reached, a latest reached with no
// move, a start that finds the operator already there. A place reminder
// past its until stops waiting.
func (s *Service) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	rs, err := s.St.Reminders(ctx, time.Time{})
	if err != nil {
		return err
	}
	var here []Visit
	known, asked := false, false
	for _, r := range rs {
		if r.Status != Waiting {
			continue
		}
		switch {
		case r.Place == "" && r.At != nil && !r.At.After(now):
			s.say(ctx, r, ByTime)
		case r.Place != "" && r.At != nil && !r.At.After(now):
			s.say(ctx, r, ByLatest)
		case r.Place != "" && r.Until != nil && !r.Until.After(now):
			r.Status, r.UpdatedAt = Expired, now
			if err := s.St.PutReminder(ctx, r); err != nil {
				return err
			}
		case r.Place != "" && r.Move == Arrive && r.After != nil && !r.After.After(now) && !s.looked[r.ID]:
			if !asked {
				here, known = s.where(ctx)
				asked = true
			}
			if !known {
				continue // looked at again on the next tick
			}
			if s.looked == nil {
				s.looked = map[string]bool{}
			}
			s.looked[r.ID] = true
			for _, v := range here {
				if matches(r.Place, v.Region, v.Kind) {
					s.say(ctx, r, ByThere)
					break
				}
			}
		}
	}
	return nil
}

// where is the regions the operator is in now, and whether that is known.
func (s *Service) where(ctx context.Context) ([]Visit, bool) {
	if s.Here == nil {
		return nil, false
	}
	vs, err := s.Here(ctx)
	if err != nil {
		if s.Out != nil {
			s.Out.Warn("지금 있는 곳을 읽지 못했습니다", "err", err)
		}
		return nil, false
	}
	return vs, true
}

// Run ticks until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil && s.Out != nil {
			s.Out.Warn("알림 때를 보지 못했습니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// say says one reminder and keeps that it was said, and how. A failure
// leaves it waiting.
func (s *Service) say(ctx context.Context, r Reminder, by string) {
	r.SentBy = by
	result, err := s.Say(ctx, r)
	if err != nil {
		if s.Out != nil {
			s.Out.Warn("알림을 보내지 못했습니다", "id", r.ID, "err", err)
		}
		return
	}
	now := s.Now()
	r.Status, r.SentAt, r.Result, r.UpdatedAt = Sent, &now, result, now
	if err := s.St.PutReminder(ctx, r); err != nil && s.Out != nil {
		s.Out.Warn("보낸 알림을 기록하지 못했습니다", "id", r.ID, "err", err)
	}
}

func (s *Service) loc() *time.Location {
	if s.Loc == nil {
		return time.Local
	}
	return s.Loc
}

// check makes a reminder whole or says what it lacks.
func (s *Service) check(r *Reminder, now time.Time) error {
	r.Text = strings.TrimSpace(r.Text)
	r.Body = strings.TrimSpace(r.Body)
	r.Place = Place(r.Place)
	r.Move = strings.TrimSpace(r.Move)
	switch {
	case r.Text == "":
		return ErrInvalid("무엇을 알릴지(text)가 필요합니다")
	case len([]rune(r.Text)) > 200:
		return ErrInvalid("알릴 말은 200자까지입니다")
	}
	if r.Place == "" {
		r.Move, r.After, r.Until = "", nil, nil
		if r.At == nil {
			return ErrInvalid("언제 알릴지가 필요합니다: 시각(at)이나 장소(place)")
		}
		if r.At.Before(now.Add(-time.Minute)) {
			return ErrInvalid("이미 지난 시각입니다: " + Stamp(*r.At, s.loc()))
		}
		return nil
	}
	if r.Move == "" {
		r.Move = Arrive
	}
	if !slices.Contains([]string{Arrive, Leave}, r.Move) {
		return ErrInvalid("move 는 arrive(도착) 나 leave(출발) 입니다")
	}
	if r.At != nil && r.At.Before(now.Add(-time.Minute)) {
		return ErrInvalid("늦어도 알릴 시각(at)이 이미 지났습니다: " + Stamp(*r.At, s.loc()))
	}
	if r.At != nil && r.After != nil && !r.At.After(*r.After) {
		return ErrInvalid("늦어도 알릴 시각(at)은 세기 시작하는 때(after)보다 뒤여야 합니다")
	}
	if r.Until != nil && !r.Until.After(now) {
		return ErrInvalid("기다리는 기한(until)이 이미 지났습니다")
	}
	if r.At != nil {
		r.Until = nil // the latest is a later moment of its own, not an end
	}
	return nil
}

// Place is a place as kept: the kinds for 집 and 회사, a region's name as
// it is.
func Place(p string) string {
	p = strings.TrimSpace(p)
	switch strings.ToLower(p) {
	case "집", "home":
		return "home"
	case "회사", "직장", "사무실", "work":
		return "work"
	}
	return p
}

// PlaceName is a place in words.
func PlaceName(p string) string {
	switch p {
	case "home":
		return "집"
	case "work":
		return "회사"
	}
	return p
}

func matches(place, region, kind string) bool {
	return place == kind || place == region || Place(region) == place
}

// When is a reminder's moment in words: "10/24(토) 08:00", "집에 도착할 때",
// "10/23(금) 19:00부터 집에 있거나 도착할 때 · 늦어도 23:00".
func (r Reminder) When(loc *time.Location) string {
	if r.Place == "" {
		if r.At == nil {
			return ""
		}
		return Stamp(*r.At, loc)
	}
	place := PlaceName(r.Place)
	s := place + "에 도착할 때"
	switch {
	case r.Move == Leave:
		s = place + "에서 나갈 때"
	case r.After != nil:
		s = place + "에 있거나 도착할 때"
	}
	if r.After != nil {
		s = Stamp(*r.After, loc) + "부터 " + s
	}
	switch {
	case r.At != nil && r.After != nil && sameDay(*r.At, *r.After, loc):
		s += " · 늦어도 " + r.At.In(loc).Format("15:04")
	case r.At != nil:
		s += " · 늦어도 " + Stamp(*r.At, loc)
	case r.Until != nil:
		s += " · " + Stamp(*r.Until, loc) + "까지만"
	}
	return s
}

// Why is why it is said now, in words, for under it: "집에 도착하셔서
// 알려 드려요".
func (r Reminder) Why(loc *time.Location) string {
	place := PlaceName(r.Place)
	switch r.SentBy {
	case ByArrive:
		return place + "에 도착하셔서 알려 드려요"
	case ByThere:
		return place + "에 계셔서 알려 드려요"
	case ByLeave:
		return place + "에서 나가셔서 알려 드려요"
	case ByLatest:
		if r.Move == Leave {
			return place + "에서 나가신 소식이 없어 정해 둔 시각에 알려 드려요"
		}
		return place + " 도착 소식이 없어 정해 둔 시각에 알려 드려요"
	}
	return "부탁하신 알림이에요"
}

// Line is a reminder in one line, for a list and for the conversation's
// situation: "[rem_1a2b] 10/24(토) 08:00 · 베개 챙기기".
func (r Reminder) Line(loc *time.Location) string {
	s := "[" + r.ID + "] " + r.When(loc) + " · " + r.Text
	if r.Checklist != "" {
		s += " · 체크리스트 " + r.Checklist
	}
	if r.Status != Waiting {
		s += " (" + StatusName(r.Status)
		if r.SentAt != nil {
			s += " " + Stamp(*r.SentAt, loc)
		}
		if r.Result != "" {
			s += ", " + r.Result
		}
		s += ")"
	}
	return s
}

// StatusName is a status in words.
func StatusName(st string) string {
	switch st {
	case Waiting:
		return "기다리는 중"
	case Sent:
		return "보냄"
	case Cancelled:
		return "취소함"
	case Expired:
		return "기한 지남"
	}
	return st
}

var weekdays = []string{"일", "월", "화", "수", "목", "금", "토"}

// Stamp is "10/24(토) 08:00".
func Stamp(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	return fmt.Sprintf("%d/%d(%s) %s", int(t.Month()), t.Day(), weekdays[t.Weekday()], t.Format("15:04"))
}

func sameDay(a, b time.Time, loc *time.Location) bool {
	return a.In(loc).Format("2006-01-02") == b.In(loc).Format("2006-01-02")
}

// ParseTime reads a time as the conversation gives it: "2026-10-24T08:00"
// or "2026-10-24 08:00" on the operator's clock, or RFC 3339.
func ParseTime(v string, loc *time.Location) (time.Time, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrInvalid(fmt.Sprintf("시각을 읽지 못했습니다: %q (예: 2026-10-24T08:00)", v))
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "rem_" + hex.EncodeToString(b)
}
