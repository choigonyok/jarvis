package suggest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
)

// One calendar change, one card. mind reads the conversation with the
// assistant like any room, so what the operator asks the assistant to put on
// the calendar becomes mind's plan too, and with the entry not there yet mind
// asked again: one entry, two cards, minutes apart. The card raised in the
// conversation is the question. mind is told what it asks, puts its own card
// away and raises none: whether two cards are about one entry is mind's to
// say, in one place, and not a copy of its rule kept here (mind-svc
// gate.Asked).

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

// Consult is asked before a calendar card goes up in the conversation: an entry
// the operator turned down the like of is not asked again. The conversation's
// card passed no gate, and "차 예약은 일정 카드 필요없어" said on one was asked again in the
// next session. mind says which no, if any, speaks for it; what the operator
// said just now (said) is the newer word, and a request in it stands. With no
// answer from mind the card goes up as before.
func (e *Engine) Consult(ctx context.Context, a action.Action, said string) string {
	if e.cfg.Mind == nil || a.Kind != calendar.KindCreate {
		return ""
	}
	var in calendar.CreateInput
	if json.Unmarshal(a.Input, &in) != nil || strings.TrimSpace(in.Title) == "" {
		return ""
	}
	if r := []rune(said); len(r) > saidMax {
		said = string(r[:saidMax])
	}
	ctx, cancel := context.WithTimeout(ctx, consultWait)
	defer cancel()
	t, ok, err := e.cfg.Mind.TurnedDown(ctx, mind.Event{Title: in.Title, Date: in.Date, EndDate: in.EndDate, Start: in.Start, End: in.End}, in.Memo, said)
	if err != nil {
		e.log.Warn("반려한 일정인지 mind 에 묻지 못했습니다", "err", err)
		return ""
	}
	if !ok {
		return ""
	}
	at := t.At.In(time.Local)
	return fmt.Sprintf("승인 카드를 올리지 않았습니다. 운영자가 %d/%d에 비슷한 일정 카드('%s')를 반려하며 이렇게 말했습니다: \"%s\". "+
		"이 일정은 캘린더에 넣지 마세요. 필요하면 그 말 때문에 넣지 않았다고 한 줄로 알리고 이번 응답을 마치세요. "+
		"운영자가 이 일정을 캘린더에 넣어 달라고 직접 말하면 그때 다시 요청하면 됩니다.",
		int(at.Month()), at.Day(), t.Title, strings.Join(strings.Fields(t.Reason), " "))
}

const (
	// consultWait is how long a card waits on mind's word before it goes up
	// without it: mind asks a judgement (up to 20s).
	consultWait = 25 * time.Second
	// saidMax is how much of what was said just now goes with the question.
	saidMax = 1500
)

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
