// Package skill is where the assistant decides what to do about the world.
//
// A skill owns a kind of thing (the schedule skill owns plans, the loops
// skill owns open loops) and is handed an entity when it changed, when a
// timer it set comes due, or at a tick (the morning). It answers with a
// Result: intents for the gate, timers to look again, and quiet edits to the
// world model (an open loop it starts tracking). It never acts and never
// talks to anyone itself - that is the gate's, so one place keeps the trust
// levels and the day's interruption budget.
package skill

import (
	"context"
	"fmt"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

type Skill interface {
	Name() string
	// Wants is whether this skill owns the entity. Exactly one should.
	Wants(e model.Entity) bool
	OnChange(ctx context.Context, v View, e model.Entity, changes []model.Change) Result
	OnTimer(ctx context.Context, v View, e model.Entity, t model.Timer) Result
	OnTick(ctx context.Context, v View, tick string) Result
}

// LinkAware is a skill that wants to hear when something is linked to an
// entity it owns (a payment during a dinner); others hear only changes to
// the entity itself.
type LinkAware interface {
	WantsLinks() bool
}

// View is the world as a skill may read it.
type View interface {
	Entity(ctx context.Context, id string) (model.Entity, error)
	Entities(ctx context.Context, kind, room string, liveOnly bool) ([]model.Entity, error)
	IntentsFor(ctx context.Context, entityID string) ([]model.Intent, error)
	Now() time.Time
	Loc() *time.Location
	// Recall is a few things long-term memory holds about these people -
	// "지수: 매운 음식을 못 먹음" - to put in front of the operator when it
	// helps. Empty when memory has nothing or is not there.
	Recall(ctx context.Context, people []string) []string
	// Links is every link from or to an id; Occurrence one occurrence.
	Links(ctx context.Context, id string) ([]model.Link, error)
	Occurrence(ctx context.Context, id string) (model.Occurrence, error)
	// Span is when a plan starts and (probably) ends.
	Span(ctx context.Context, e model.Entity) (start, end time.Time, ok bool)
}

type Result struct {
	Intents []model.Intent
	// Timers to set; CancelTimers drops this skill's timers on the entity
	// first.
	Timers       []model.Timer
	CancelTimers bool
	// Withdraw: questions this skill still has open about the entity are no
	// longer wanted (the plan was cancelled before anyone answered).
	Withdraw bool
	Creates  []Create
	Updates  []Update
}

// Create and Update are quiet edits to the world model: nobody is asked, and
// they show in the entity's history under this skill's name.
type Create struct {
	Kind  string
	Room  string
	State model.State
	Why   string
}

type Update struct {
	ID  string
	Set func(*model.State) bool
	Why string
}

// When is a plan's start in loc: its time if it has one, else 09:00 that day.
func When(s model.State, loc *time.Location) (time.Time, bool, bool) {
	d, err := time.ParseInLocation("2006-01-02", s.Date, loc)
	if err != nil {
		return time.Time{}, false, false
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s.Date+" "+s.Start, loc); err == nil {
		return t, true, true
	}
	return d.Add(9 * time.Hour), false, true
}

var weekdays = []string{"일", "월", "화", "수", "목", "금", "토"}

// Day is "10/13(화)" and, with a time, "10/13(화) 19:00".
func Day(s model.State, loc *time.Location) string {
	d, err := time.ParseInLocation("2006-01-02", s.Date, loc)
	if err != nil {
		return ""
	}
	out := fmt.Sprintf("%d/%d(%s)", int(d.Month()), d.Day(), weekdays[d.Weekday()])
	if s.EndDate != "" && s.EndDate != s.Date {
		if e, err := time.ParseInLocation("2006-01-02", s.EndDate, loc); err == nil {
			out += fmt.Sprintf("~%d/%d(%s)", int(e.Month()), e.Day(), weekdays[e.Weekday()])
		}
	}
	if s.Start != "" {
		out += " " + s.Start
	}
	return out
}
