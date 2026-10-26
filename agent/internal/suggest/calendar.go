package suggest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
)

// One calendar change, one card. mind reads the conversation with the
// assistant like any room, so what the operator asks the assistant to put
// on the calendar becomes mind's plan too - and with the assistant's own
// card still waiting, mind found the entry missing and asked again: one
// entry, two cards, minutes apart (2026-10-26). Both cards meet here, so
// here the second one stands aside: mind's card is not opened while the
// conversation's waits, and a card the conversation raises takes the place
// of mind's still waiting. mind is told either way (covered), so it does
// not wait on a card nobody sees or ask it again.

// chatCard is a card raised in the conversation, still waiting, that asks
// the change a mind card asks.
func (e *Engine) chatCard(kind string, cal *mind.Calendar) (proposal.Proposal, bool) {
	return e.proposals.FindPendingWhere(func(p proposal.Proposal) bool {
		return p.Origin == proposal.OriginChat && sameChange(kind, cal, p.Action)
	})
}

// OnOpen hears every card raised. One the conversation raised for a
// calendar change takes the place of mind's card for the same change.
// Off the producer's goroutine: telling mind is a call away.
func (e *Engine) OnOpen(p proposal.Proposal) {
	if p.Origin != proposal.OriginChat || !strings.HasPrefix(p.Action.Kind, calendar.Name+".") {
		return
	}
	go e.standAside(p)
}

// standAside puts away mind's waiting cards that ask what chat, raised in
// the conversation, asks.
func (e *Engine) standAside(chat proposal.Proposal) {
	waiting := e.proposals.PendingWhere(func(p proposal.Proposal) bool {
		if p.Origin != proposal.OriginSuggest || p.Action.Kind != mind.Approve {
			return false
		}
		var in mind.Input
		return json.Unmarshal(p.Action.Input, &in) == nil && sameChange(in.Kind, in.Calendar, chat.Action)
	})
	for _, p := range waiting {
		var in mind.Input
		_ = json.Unmarshal(p.Action.Input, &in)
		e.proposals.Abandon(p.ID, "대화에서 같은 일정을 다시 물어서 그 카드("+chat.ID+")로 바꿨어요")
		if err := e.cover(in.IntentID, chat.ID); err != nil {
			e.log.Warn("mind 에 대화의 카드로 바꿨다고 알리지 못했습니다", "intent", in.IntentID, "card", chat.ID, "err", err)
			continue
		}
		e.log.Info("대화의 일정 카드로 mind 카드를 바꿨습니다", "intent", in.IntentID, "mindCard", p.ID, "card", chat.ID)
	}
}

// cover tells mind its intent is asked in the conversation already, on the
// card by.
func (e *Engine) cover(intentID, by string) error {
	if e.cfg.Mind == nil {
		return errors.New("mind 에 알릴 곳이 없습니다")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return e.cfg.Mind.Covered(ctx, intentID, by)
}

// sameChange is whether a mind card's calendar change (kind, cal) and an
// action raised in the conversation are one change: a new entry on the
// same day under the same title, the same entry moved to the same day and
// time, or the same entry taken off. Two different changes to one entry
// are two questions, both asked.
func sameChange(kind string, cal *mind.Calendar, a action.Action) bool {
	if cal == nil {
		return false
	}
	switch {
	case kind == "calendar.create" && a.Kind == calendar.KindCreate:
		var in calendar.CreateInput
		return json.Unmarshal(a.Input, &in) == nil && sameEvent(cal.Event, mind.Event{Title: in.Title, Date: in.Date, Start: in.Start})
	case kind == "calendar.update" && a.Kind == calendar.KindUpdate:
		var in calendar.UpdateInput
		if cal.EventID == "" || json.Unmarshal(a.Input, &in) != nil || in.ID != cal.EventID || in.Date == "" && in.Start == "" {
			return false
		}
		return (in.Date == "" || in.Date == cal.Event.Date) && (in.Start == "" || in.Start == cal.Event.Start)
	case kind == "calendar.delete" && a.Kind == calendar.KindDelete:
		var in calendar.DeleteInput
		return cal.EventID != "" && json.Unmarshal(a.Input, &in) == nil && in.ID == cal.EventID
	}
	return false
}

// sameEvent is whether two entries are one event told twice - the rule
// mind-svc ties a plan to its calendar entry by (internal/model/
// sameevent.go), copied: the two share no code, and a card put aside here
// for the conversation's must be the plan mind ties to the entry that card
// makes. Same day; a time on both within half an hour and a word shared,
// or no time on one and the same title.
func sameEvent(a, b mind.Event) bool {
	if a.Date == "" || a.Date != b.Date {
		return false
	}
	if a.Start == "" || b.Start == "" {
		return sameTitle(a.Title, b.Title)
	}
	ta, errA := time.Parse("15:04", a.Start)
	tb, errB := time.Parse("15:04", b.Start)
	if errA != nil || errB != nil {
		return false
	}
	if d := ta.Sub(tb); d > 30*time.Minute || d < -30*time.Minute {
		return false
	}
	if sameTitle(a.Title, b.Title) {
		return true
	}
	wa, wb := words(a.Title), words(b.Title)
	for w := range wa {
		if wb[w] {
			return true
		}
	}
	return false
}

// sameTitle is whether two titles name one thing: the same letters, or one
// holding the other, spaces and marks aside. A single letter held is no
// title.
func sameTitle(a, b string) bool {
	ra, rb := []rune(bare(a)), []rune(bare(b))
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	return len(ra) >= 2 && strings.Contains(string(rb), string(ra))
}

func bare(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// words are what two titles can share: two-letter pieces of Hangul and
// whole Latin words of three letters or more.
func words(title string) map[string]bool {
	out := map[string]bool{}
	var run []rune
	hangul := false
	flush := func() {
		switch {
		case hangul:
			for i := 0; i+1 < len(run); i++ {
				out[string(run[i:i+2])] = true
			}
		case len(run) >= 3:
			out[string(run)] = true
		}
		run = run[:0]
	}
	for _, r := range strings.ToLower(title) {
		isHangul := unicode.Is(unicode.Hangul, r)
		if !(isHangul || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			flush()
			continue
		}
		if len(run) > 0 && isHangul != hangul {
			flush()
		}
		hangul = isHangul
		run = append(run, r)
	}
	flush()
	return out
}
