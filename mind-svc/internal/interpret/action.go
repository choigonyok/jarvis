package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// Actions reads what the gate did back into the world model. The result of
// an action is an observation like any other: a plan put on the calendar now
// carries its calendar id because the calendar said so, not because a skill
// assumed it would.
type Actions struct {
	World *world.World
}

type calendarResult struct {
	CalendarID string `json:"calendarId"`
}

func (a *Actions) Handle(ctx context.Context, o obs.Observation) error {
	var d obs.ActionData
	if err := json.Unmarshal(o.Data, &d); err != nil || d.EntityID == "" {
		return nil
	}
	var r calendarResult
	_ = json.Unmarshal(d.Result, &r)
	meta := world.Meta{Actor: "observer", ObservationID: o.ID, Why: o.Type + " " + d.Kind}
	if d.Kind == "settle.request" {
		return a.settle(ctx, o, d, meta)
	}

	var set func(*model.State) bool
	switch {
	case o.Type == obs.ActionExecuted && d.Kind == "calendar.create" && r.CalendarID != "":
		set = func(s *model.State) bool { s.CalendarID = r.CalendarID; return true }
	case o.Type == obs.ActionExecuted && d.Kind == "calendar.delete",
		o.Type == obs.ActionUndone && d.Kind == "calendar.create":
		set = func(s *model.State) bool { s.CalendarID = ""; return true }
	case o.Type == obs.ActionUndone && d.Kind == "calendar.delete" && r.CalendarID != "":
		// Put back under a new id.
		set = func(s *model.State) bool { s.CalendarID = r.CalendarID; return true }
	default:
		return nil
	}
	_, _, err := a.World.Update(ctx, d.EntityID, set, meta)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	return err
}

// settle keeps, per person, what they owe once the operator chose to ask
// for it: an open loop until it is paid (a transfer closes it), closed again
// if the request is taken back.
func (a *Actions) settle(ctx context.Context, o obs.Observation, d obs.ActionData, meta world.Meta) error {
	plan, err := a.World.Entity(ctx, d.EntityID)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	loops, err := a.World.Entities(ctx, model.KindLoop, "", true)
	if err != nil {
		return err
	}
	open := map[string]string{} // person → loop
	for _, l := range loops {
		if l.State.PlanID == plan.ID && l.State.Type == Settlement && len(l.State.People) == 1 {
			open[l.State.People[0]] = l.ID
		}
	}
	if o.Type == obs.ActionUndone {
		for _, id := range open {
			if _, _, err := a.World.Update(ctx, id, func(s *model.State) bool { s.Status = model.Closed; return true }, meta); err != nil {
				return err
			}
		}
		return nil
	}
	var s struct {
		PerPerson int64    `json:"perPerson"`
		People    []string `json:"people"`
		Date      string   `json:"date"`
	}
	if json.Unmarshal(d.Result, &s) != nil || s.PerPerson <= 0 {
		return nil
	}
	due := ""
	if day, err := time.Parse("2006-01-02", s.Date); err == nil {
		due = day.AddDate(0, 0, 3).Format("2006-01-02")
	}
	for _, p := range s.People {
		if open[p] != "" {
			continue
		}
		st := model.State{Title: fmt.Sprintf("%s에게 정산 %s원 받기", p, wonText(s.PerPerson)), Status: model.Open, Owner: "them",
			People: []string{p}, Due: due, PlanID: plan.ID, Type: Settlement, Note: fmt.Sprint(s.PerPerson)}
		if _, err := a.World.Create(ctx, model.KindLoop, plan.Room, st, meta); err != nil {
			return err
		}
	}
	return nil
}

// Settlement is the type of a loop that is money someone owes the operator
// for a shared bill.
const Settlement = "정산"

func wonText(n int64) string {
	s := fmt.Sprint(n)
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += ","
		}
		out += string(c)
	}
	return out
}
