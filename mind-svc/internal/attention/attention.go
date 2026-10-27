// Package attention decides what the assistant thinks about, and when.
//
// Four things call for attention: a change in the world model, a timer
// coming due (a skill's look again before a plan, or an expectation's due),
// the time of day (the morning), and context such as arriving home. Changes are not handed on one by one: a
// plan that goes proposed → confirmed → moved within a minute is one thing
// to think about, so changes to an entity settle for a while and are handed
// to its owning skill together. A skill is not handed back the changes it
// made itself.
package attention

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

type Attention struct {
	skills  []skill.Skill
	world   *world.World
	st      store.Store
	gate    *gate.Gate
	settle  time.Duration
	morning int // minutes after midnight
	loc     *time.Location
	now     func() time.Time
	log     *slog.Logger
	// Recall is what long-term memory holds about people, for skills to
	// mention (nil: nothing).
	Recall func(ctx context.Context, people []string) []string
	// Expect takes the timers of expectations (skill "expect"): a
	// prediction due, a prep purchase to bring up ahead of it. The day-before
	// and two-hours-before looks of a plan are skills' timers; these are the
	// same thing for anything predicted.
	Expect func(ctx context.Context, t model.Timer)
	// Weather is the day's weather where a plan is, or at home (nil: not
	// known).
	Weather func(ctx context.Context, area, place, date string) (weather.Day, []weather.Hour, bool)

	mu      sync.Mutex
	pending map[string]*batch
	busy    sync.Mutex // one dispatch at a time
}

type batch struct {
	changes []model.Change
	due     time.Time
}

func New(skills []skill.Skill, w *world.World, st store.Store, g *gate.Gate, settle time.Duration, loc *time.Location, now func() time.Time, log *slog.Logger) *Attention {
	a := &Attention{skills: skills, world: w, st: st, gate: g, settle: settle, morning: 8 * 60, loc: loc, now: now, log: log, pending: map[string]*batch{}}
	w.Listen(a.OnChange)
	return a
}

// OnChange queues a change; it is dispatched once the entity has been still
// for the settle time.
func (a *Attention) OnChange(e model.Entity, c model.Change) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := a.pending[e.ID]
	if b == nil {
		b = &batch{}
		a.pending[e.ID] = b
	}
	b.changes = append(b.changes, c)
	b.due = a.now().Add(a.settle)
}

// OnLink hands a new link to the skills that own its ends, as a change to
// the entity would be: a payment during a dinner is news about the dinner.
func (a *Attention) OnLink(_ context.Context, l model.Link) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range []string{l.From, l.To} {
		if !strings.HasPrefix(id, model.KindPlan+"_") && !strings.HasPrefix(id, model.KindLoop+"_") {
			continue
		}
		b := a.pending[id]
		if b == nil {
			b = &batch{}
			a.pending[id] = b
		}
		b.changes = append(b.changes, model.Change{EntityID: id, Actor: "linker", Why: "link " + l.Rel + " " + l.From, At: a.now()})
		b.due = a.now().Add(a.settle)
	}
}

// Waiting is how many entities are settling, for the console.
func (a *Attention) Waiting() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

// Run dispatches settled changes, due timers and the morning tick.
func (a *Attention) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.step(ctx, false)
			a.morningTick(ctx)
		}
	}
}

// Settle dispatches everything now, settled or not, until nothing more
// comes of it - for tests, evaluation and the console's "now" button.
func (a *Attention) Settle(ctx context.Context) {
	for range 10 {
		if a.step(ctx, true) == 0 {
			return
		}
	}
}

// step dispatches what is due (or everything, if all) and reports how much
// it handled.
func (a *Attention) step(ctx context.Context, all bool) int {
	a.busy.Lock()
	defer a.busy.Unlock()
	n := 0
	now := a.now()
	a.mu.Lock()
	var ready []string
	for id, b := range a.pending {
		if all || !now.Before(b.due) {
			ready = append(ready, id)
		}
	}
	slices.Sort(ready)
	batches := map[string][]model.Change{}
	for _, id := range ready {
		batches[id] = a.pending[id].changes
		delete(a.pending, id)
	}
	a.mu.Unlock()
	for _, id := range ready {
		a.dispatch(ctx, id, batches[id])
		n++
	}

	timers, err := a.st.DueTimers(ctx, now)
	if err != nil {
		a.log.Warn("타이머를 읽지 못했습니다", "err", err)
	}
	for _, t := range timers {
		if err := a.st.FireTimer(ctx, t.ID, now); err != nil {
			a.log.Warn("타이머를 처리하지 못했습니다", "err", err)
			continue
		}
		if t.Skill == "expect" {
			if a.Expect != nil {
				a.Expect(ctx, t)
			}
			n++
			continue
		}
		e, err := a.world.Entity(ctx, t.EntityID)
		if err != nil {
			continue
		}
		for _, s := range a.skills {
			if s.Name() == t.Skill {
				a.apply(ctx, s, e.ID, s.OnTimer(ctx, a.view(), e, t))
			}
		}
		n++
	}
	return n
}

func (a *Attention) dispatch(ctx context.Context, id string, changes []model.Change) {
	e, err := a.world.Entity(ctx, id)
	if err != nil {
		return
	}
	linksOnly := true
	for _, c := range changes {
		if c.Actor != "linker" {
			linksOnly = false
		}
	}
	for _, s := range a.skills {
		if !s.Wants(e) {
			continue
		}
		if l, ok := s.(skill.LinkAware); linksOnly && (!ok || !l.WantsLinks()) {
			continue // a link is news only to a skill that reads links
		}
		mine := true
		for _, c := range changes {
			if c.Actor != "skill:"+s.Name() {
				mine = false
			}
		}
		if mine {
			continue
		}
		a.apply(ctx, s, e.ID, s.OnChange(ctx, a.view(), e, changes))
	}
}

// Tick hands a time of day to every skill.
func (a *Attention) Tick(ctx context.Context, name string) {
	a.busy.Lock()
	defer a.busy.Unlock()
	for _, s := range a.skills {
		a.apply(ctx, s, "", s.OnTick(ctx, a.view(), name))
	}
}

// morningTick ticks "morning" once a day, at 08:00 or the first moment
// after it, remembering across restarts that it did.
func (a *Attention) morningTick(ctx context.Context) {
	now := a.now().In(a.loc)
	if now.Hour()*60+now.Minute() < a.morning {
		return
	}
	today, _ := strconv.ParseInt(now.Format("20060102"), 10, 64)
	last, err := a.st.Cursor(ctx, "tick:morning")
	if err != nil || last >= today {
		return
	}
	if err := a.st.SetCursor(ctx, "tick:morning", today); err != nil {
		return
	}
	a.Tick(ctx, "morning")
}

func (a *Attention) apply(ctx context.Context, s skill.Skill, entityID string, r skill.Result) {
	actor := "skill:" + s.Name()
	if r.CancelTimers && entityID != "" {
		if err := a.st.CancelTimers(ctx, s.Name(), entityID); err != nil {
			a.log.Warn("타이머를 지우지 못했습니다", "err", err)
		}
	}
	for _, t := range r.Timers {
		t.Skill = s.Name()
		if err := a.st.SetTimer(ctx, t); err != nil {
			a.log.Warn("타이머를 걸지 못했습니다", "err", err)
		}
	}
	if r.Withdraw && entityID != "" {
		if err := a.gate.Withdraw(ctx, entityID, s.Name()); err != nil {
			a.log.Warn("제안을 거두지 못했습니다", "err", err)
		}
	}
	for _, c := range r.Creates {
		if _, err := a.world.Create(ctx, c.Kind, c.Room, c.State, world.Meta{Actor: actor, Why: c.Why}); err != nil {
			a.log.Warn("세계 모델에 더하지 못했습니다", "err", err)
		}
	}
	for _, u := range r.Updates {
		if _, _, err := a.world.Update(ctx, u.ID, u.Set, world.Meta{Actor: actor, Why: u.Why}); err != nil {
			a.log.Warn("세계 모델을 고치지 못했습니다", "err", err)
		}
	}
	for _, in := range r.Intents {
		in.Skill = s.Name()
		if _, err := a.gate.Submit(ctx, in); err != nil {
			a.log.Warn("의도를 넘기지 못했습니다", "err", err)
		}
	}
}

func (a *Attention) view() skill.View { return view{a} }

type view struct{ a *Attention }

func (v view) Entity(ctx context.Context, id string) (model.Entity, error) {
	return v.a.world.Entity(ctx, id)
}
func (v view) Entities(ctx context.Context, kind, room string, liveOnly bool) ([]model.Entity, error) {
	return v.a.world.Entities(ctx, kind, room, liveOnly)
}
func (v view) IntentsFor(ctx context.Context, id string) ([]model.Intent, error) {
	return v.a.st.IntentsFor(ctx, id)
}

func (v view) IntentsSince(ctx context.Context, t time.Time) ([]model.Intent, error) {
	return v.a.st.IntentsSince(ctx, t)
}
func (v view) Now() time.Time      { return v.a.now() }
func (v view) Loc() *time.Location { return v.a.loc }

func (v view) Links(ctx context.Context, id string) ([]model.Link, error) {
	return v.a.st.LinksOf(ctx, id)
}

func (v view) Occurrence(ctx context.Context, id string) (model.Occurrence, error) {
	return v.a.st.Occurrence(ctx, id)
}

func (v view) Span(ctx context.Context, e model.Entity) (time.Time, time.Time, bool) {
	sp, ok := v.a.world.Span(ctx, e, v.a.now(), v.a.loc)
	return sp.Start, sp.End, ok
}

func (v view) Recall(ctx context.Context, people []string) []string {
	if v.a.Recall == nil || len(people) == 0 {
		return nil
	}
	return v.a.Recall(ctx, people)
}

func (v view) Weather(ctx context.Context, area, place, date string) (weather.Day, []weather.Hour, bool) {
	if v.a.Weather == nil {
		return weather.Day{}, nil, false
	}
	return v.a.Weather(ctx, area, place, date)
}

func (v view) Travel(ctx context.Context, from, to model.State) (int, string, bool) {
	notes, err := v.a.st.Notes(ctx)
	if err != nil {
		return 0, "", false
	}
	for _, n := range notes {
		r := n.Rule
		if n.Status == model.Rejected || r == nil || r.Kind != "travel" || r.Minutes <= 0 {
			continue
		}
		if world.SamePlace(from.Area, from.Place, r.From, "") && world.SamePlace(to.Area, to.Place, r.To, "") {
			return r.Minutes, n.Text, true
		}
	}
	return 0, "", false
}
