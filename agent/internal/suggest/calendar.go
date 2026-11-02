package suggest

import (
	"context"
	"encoding/json"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
)

// One calendar change, one card. mind reads the conversation with the
// assistant like any room, so what the operator asks the assistant to put
// on the calendar becomes mind's plan too, and with the entry not there yet
// mind asked again: one entry, two cards, minutes apart (2026-10-26). The
// card raised in the conversation is the question. mind is told what it asks,
// puts its own card away and raises none: whether two cards are about one
// entry is mind's to say, in one place, and not a copy of its rule kept here
// (mind-svc gate.Asked).

// OnOpen hears every card raised. One the conversation raised for a
// calendar change is told to mind, off the producer's goroutine: telling
// mind is a call away.
func (e *Engine) OnOpen(p proposal.Proposal) {
	if e.cfg.Mind == nil || p.Origin != proposal.OriginChat {
		return
	}
	if _, ok := calendarChange(p.Action); ok {
		go e.tellAsked(p)
	}
}

// tellAsked tells mind a card in the conversation asks a calendar change.
func (e *Engine) tellAsked(p proposal.Proposal) {
	cal, ok := calendarChange(p.Action)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.cfg.Mind.Asked(ctx, p.ID, p.Action.Kind, p.Card.Title, cal); err != nil {
		e.log.Warn("mind 에 대화의 일정 카드를 알리지 못했습니다", "card", p.ID, "err", err)
	}
}

// calendarChange is what a calendar action asks, as mind says it.
func calendarChange(a action.Action) (mind.Calendar, bool) {
	switch a.Kind {
	case calendar.KindCreate:
		var in calendar.CreateInput
		if json.Unmarshal(a.Input, &in) == nil {
			return mind.Calendar{Event: mind.Event{Title: in.Title, Date: in.Date, EndDate: in.EndDate, Start: in.Start, End: in.End}}, true
		}
	case calendar.KindUpdate:
		var in calendar.UpdateInput
		if json.Unmarshal(a.Input, &in) == nil {
			return mind.Calendar{EventID: in.ID, Event: mind.Event{Title: in.Title, Date: in.Date, EndDate: in.EndDate, Start: in.Start, End: in.End}}, true
		}
	case calendar.KindDelete:
		var in calendar.DeleteInput
		if json.Unmarshal(a.Input, &in) == nil {
			return mind.Calendar{EventID: in.ID}, true
		}
	}
	return mind.Calendar{}, false
}
