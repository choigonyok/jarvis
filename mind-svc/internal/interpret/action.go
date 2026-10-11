package interpret

import (
	"context"
	"encoding/json"
	"errors"

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
