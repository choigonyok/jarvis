package model

import (
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// QuickReasons are the card's own one-tap answers (web, chat/reject-form.tsx):
// they say nothing beside the card they were on.
var QuickReasons = []string{"지금은 필요 없어", "이미 했어", "틀렸어", "다시 묻지 마"}

// Turned is a calendar card the operator turned down in words of their own.
type Turned struct {
	Title  string    `json:"title"` // the entry, as the card had it
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// TurnedDown is what the operator said turning down calendar cards, the newest
// first and at most max: mind's own (mine) and the conversation's (held, the
// assistant's cards). A no given on the conversation's card reached that
// session's model alone, and "차 예약은 일정 카드 필요없어" was asked again in the next
// one. One of mind's that stood aside for the conversation's card is answered
// by that card, and said once.
func TurnedDown(mine, held []Intent, max int) []Turned {
	var all []Turned
	for _, in := range mine {
		if in.DecidedBy == ByConversation || !saidWhy(in) {
			continue
		}
		all = append(all, Turned{Title: strings.TrimPrefix(in.Title, "캘린더에 추가: "), Reason: strings.TrimSpace(in.Error), At: in.CreatedAt})
	}
	for _, in := range held {
		var a CalendarAction
		if !saidWhy(in) || json.Unmarshal(in.Payload, &a) != nil || strings.TrimSpace(a.Event.Title) == "" {
			continue
		}
		all = append(all, Turned{Title: a.Event.Title, Reason: strings.TrimSpace(in.Error), At: in.CreatedAt})
	}
	slices.SortStableFunc(all, func(a, b Turned) int { return b.At.Compare(a.At) })
	var out []Turned
	for _, t := range all {
		if len(out) == max {
			break
		}
		if !slices.ContainsFunc(out, func(o Turned) bool { return o.Title == t.Title && o.Reason == t.Reason }) {
			out = append(out, t)
		}
	}
	return out
}

// saidWhy is whether a calendar card was turned down with words of the
// operator's own.
func saidWhy(in Intent) bool {
	why := strings.TrimSpace(in.Error)
	return in.Status == Rejected && why != "" && IsCalendar(in.Kind) && !slices.Contains(QuickReasons, why)
}
