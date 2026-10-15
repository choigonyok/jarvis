package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Protect keeps the operator's time from colliding: a plan that lands on
// another of theirs, or follows one too closely to get there in time (a
// travel note says how long the way takes), is asked about with a move -
// by a form whose date and time the operator may change before saying
// yes. A plan not on the calendar yet is only pointed out. Only what is
// known counts: an end time said, or an hour when none was; the expected
// length of a kind of plan is a guess and moves nothing.
type Protect struct{}

func (Protect) Name() string                                                    { return "protect" }
func (Protect) Wants(e model.Entity) bool                                       { return e.Kind == model.KindPlan }
func (Protect) OnTick(context.Context, View, string) Result                     { return Result{} }
func (Protect) OnTimer(context.Context, View, model.Entity, model.Timer) Result { return Result{} }

// minLength is how long a plan without an end is taken to last.
const minLength = time.Hour

func (p Protect) OnChange(ctx context.Context, v View, e model.Entity, _ []model.Change) Result {
	st := e.State
	start, end, ok := known(st, v.Loc())
	if !ok || st.Status != model.Confirmed || st.Owner == "partner" || !start.After(v.Now()) {
		return Result{Withdraw: true}
	}
	plans, err := v.Entities(ctx, model.KindPlan, "", true)
	if err != nil {
		return Result{}
	}
	for _, o := range plans {
		if o.ID == e.ID || o.State.Status != model.Confirmed || o.State.Owner == "partner" ||
			o.State.CalendarID != "" && o.State.CalendarID == st.CalendarID {
			continue
		}
		os, oe, ok := known(o.State, v.Loc())
		if !ok || os.Format("2006-01-02") != start.Format("2006-01-02") {
			continue
		}
		length := end.Sub(start)
		if start.Before(oe) && os.Before(end) {
			// They collide: move this one - the one that just changed - to
			// clear the other, keeping their order.
			at := oe
			if start.Before(os) {
				at = os.Add(-length)
			}
			why := fmt.Sprintf("'%s' %s~%s 와 겹쳐요", o.State.Title, os.Format("15:04"), oe.Format("15:04"))
			return Result{Intents: []model.Intent{p.move(e, o, roundUp(at, start.Before(os)), length, v, why, 0.9)}}
		}
		if !oe.After(start) && start.Sub(oe) < 3*time.Hour {
			mins, note, ok := v.Travel(ctx, o.State, st)
			if !ok {
				continue
			}
			arrive := oe.Add(time.Duration(mins) * time.Minute)
			if arrive.Sub(start) < 5*time.Minute {
				continue
			}
			why := fmt.Sprintf("'%s'가 %s에 끝나면 %d분 걸려 %s쯤 도착 (%s)", o.State.Title, oe.Format("15:04"), mins, arrive.Format("15:04"), note)
			return Result{Intents: []model.Intent{p.move(e, o, roundUp(arrive, false), length, v, why, 0.8)}}
		}
	}
	return Result{Withdraw: true}
}

// move is the card: calendar.update when the plan is on the calendar, a
// line otherwise.
func (Protect) move(e, other model.Entity, at time.Time, length time.Duration, v View, why string, conf float64) model.Intent {
	st := e.State
	newStart := at.In(v.Loc())
	title := fmt.Sprintf("'%s'를 %s로 옮길까요?", st.Title, newStart.Format("15:04"))
	if newStart.Format("2006-01-02") != st.Date {
		title = fmt.Sprintf("'%s'를 %d/%d %s로 옮길까요?", st.Title, int(newStart.Month()), newStart.Day(), newStart.Format("15:04"))
	}
	in := model.Intent{
		EntityID: e.ID, Topic: "protect:" + e.ID + ":" + other.ID, Context: "protect",
		Title: title, Body: Day(st, v.Loc()) + " " + st.Title + " · " + why,
		// Times said outright collide for certain; a travel note is a
		// habit. Either way it is the operator's own day at stake.
		Reason: "일정이 겹치거나 이동 시간이 모자라요", Confidence: conf, Value: 1, Reversible: true,
	}
	if st.CalendarID == "" {
		// A line, not the question: the question comes once it is on the
		// calendar, under its own topic.
		in.Kind, in.Title, in.Confidence, in.Topic = "info", "일정이 겹쳐요: "+st.Title, 1, "protect-note:"+e.ID+":"+other.ID
		return in
	}
	ev := event(st, nil)
	ev.Date, ev.Start = newStart.Format("2006-01-02"), newStart.Format("15:04")
	if st.End != "" {
		ev.End = newStart.Add(length).Format("15:04")
	}
	payload, _ := json.Marshal(model.CalendarAction{EventID: st.CalendarID, Event: ev})
	in.Kind, in.Payload = "calendar.update", payload
	in.Fields = []model.Field{
		{Key: "date", Label: "날짜", Kind: "text", Value: ev.Date, Required: true, Hint: "YYYY-MM-DD"},
		{Key: "start", Label: "시작", Kind: "text", Value: ev.Start, Required: true, Hint: "HH:MM"},
	}
	return in
}

// known is when a plan starts and ends by what was said: its end, or an
// hour after it starts.
func known(s model.State, loc *time.Location) (time.Time, time.Time, bool) {
	if s.Date == "" || s.Start == "" || s.EndDate != "" && s.EndDate != s.Date {
		return time.Time{}, time.Time{}, false
	}
	start, err := time.ParseInLocation("2006-01-02 15:04", s.Date+" "+s.Start, loc)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	end := start.Add(minLength)
	if t, err := time.ParseInLocation("2006-01-02 15:04", s.Date+" "+s.End, loc); err == nil && t.After(start) {
		end = t
	}
	return start, end, true
}

// roundUp is t on a ten-minute mark: later, or earlier when moving back.
func roundUp(t time.Time, back bool) time.Time {
	m := float64(t.Hour()*60+t.Minute()) / 10
	if back {
		m = math.Floor(m)
	} else {
		m = math.Ceil(m)
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return day.Add(time.Duration(m*10) * time.Minute)
}
