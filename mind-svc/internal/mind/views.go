package mind

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
)

// What the conversation reads of the world model and of mind's own word: the
// loops still open, the plans around now, and what mind asked and told lately
// with why. The situation in front of every turn says what is going on now;
// these are what the operator asks after ("지수한테 뭐 보내주기로 했지?", "왜
// 그 카드 올렸어?"), which no tool answered. Read here, kept nowhere else.

// LoopView is a loop as the conversation reads it.
type LoopView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Owner  string `json:"owner"` // me: the operator owes it | them: owed to the operator
	Due    string `json:"due,omitempty"`
	Status string `json:"status"`
	// Plan is the plan it belongs to, by title.
	Plan string `json:"plan,omitempty"`
	// Where is the conversation it was read from; Said the words it rests on.
	Where string    `json:"where"`
	Said  string    `json:"said,omitempty"`
	Since time.Time `json:"since"`
}

// loopsClosedFor is how long a closed loop is still shown with all.
const loopsClosedFor = 14 * 24 * time.Hour

// Loops is the open loops, the soonest due first - with all, and those closed
// in the last two weeks.
func (m *Mind) Loops(ctx context.Context, all bool) ([]LoopView, error) {
	loops, err := m.Opts.Store.Entities(ctx, model.KindLoop, "", !all)
	if err != nil {
		return nil, err
	}
	now := m.Opts.Now()
	rooms := m.roomNames(ctx)
	out := []LoopView{}
	for _, l := range loops {
		if !l.Live() && now.Sub(l.UpdatedAt) > loopsClosedFor {
			continue
		}
		v := LoopView{ID: l.ID, Title: l.State.Title, Owner: l.State.Owner, Due: l.State.Due, Status: l.State.Status,
			Where: rooms.label(l.Room), Said: m.saidOf(ctx, l.ID), Since: l.CreatedAt}
		if l.State.PlanID != "" {
			if p, err := m.Opts.Store.Entity(ctx, l.State.PlanID); err == nil {
				v.Plan = p.State.Title
			}
		}
		out = append(out, v)
	}
	slices.SortStableFunc(out, func(a, b LoopView) int {
		switch {
		case a.Due == b.Due:
			return 0
		case a.Due == "":
			return 1
		case b.Due == "":
			return -1
		}
		return strings.Compare(a.Due, b.Due)
	})
	return out, nil
}

// PlanView is a plan as the conversation reads it.
type PlanView struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Status  string   `json:"status"`
	Date    string   `json:"date,omitempty"`
	EndDate string   `json:"endDate,omitempty"`
	Start   string   `json:"start,omitempty"`
	End     string   `json:"end,omitempty"`
	Place   string   `json:"place,omitempty"`
	People  []string `json:"people,omitempty"`
	Type    string   `json:"type,omitempty"`
	// Calendar: the plan is an entry on the calendar (read from it, or put
	// there). Declined is what the operator said turning its card down.
	Calendar bool   `json:"calendar"`
	Declined string `json:"declined,omitempty"`
	Where    string `json:"where"`
	Said     string `json:"said,omitempty"`
}

// Plans is every plan whose days touch [from, to] (YYYY-MM-DD), by date: the
// calendar's and those only said in a conversation - agreed, proposed, called
// off, or turned down for the calendar. A second telling of one plan (merged)
// is left out; the plan it is speaks for it.
func (m *Mind) Plans(ctx context.Context, from, to string) ([]PlanView, error) {
	plans, err := m.Opts.Store.PlansOn(ctx, from, to)
	if err != nil {
		return nil, err
	}
	rooms := m.roomNames(ctx)
	out := []PlanView{}
	for _, p := range plans {
		st := p.State
		if st.MergedInto != "" {
			continue
		}
		v := PlanView{ID: p.ID, Title: st.Title, Status: st.Status, Date: st.Date, EndDate: st.EndDate, Start: st.Start, End: st.End,
			Place: st.Place, People: st.People, Type: st.Type, Calendar: p.Room == skill.CalendarRoom || st.CalendarID != "",
			Declined: st.Declined, Where: rooms.label(p.Room)}
		if p.Room != skill.CalendarRoom {
			v.Said = m.saidOf(ctx, p.ID)
		}
		out = append(out, v)
	}
	slices.SortStableFunc(out, func(a, b PlanView) int {
		return strings.Compare(a.Date+" "+a.Start, b.Date+" "+b.Start)
	})
	return out, nil
}

// AskedView is one thing mind asked or told, as the conversation reads it.
type AskedView struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Skill string    `json:"skill"`
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	Body  string    `json:"body,omitempty"`
	// Reason is why the skill wanted it; Why what the gate made of it (rang,
	// kept for the evening, dropped as asked lately) and why.
	Reason     string   `json:"reason,omitempty"`
	Why        string   `json:"why,omitempty"`
	Decision   string   `json:"decision"`
	Status     string   `json:"status"`
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence,omitempty"`
	// Answer is what the operator said turning it down, if they did.
	Answer string `json:"answer,omitempty"`
	// Card: it was a card the operator could answer; Chat, one the
	// conversation raised and mind only keeps the ledger of.
	Card bool `json:"card"`
	Chat bool `json:"chat,omitempty"`
}

// Asked is what mind asked and told since, newest first: every intent,
// whatever the gate did with it - a card, a notice, the evening summary, or
// dropped as raised lately.
func (m *Mind) Asked(ctx context.Context, since time.Time) ([]AskedView, error) {
	ins, err := m.Opts.Store.IntentsSince(ctx, since)
	if err != nil {
		return nil, err
	}
	out := []AskedView{}
	for _, in := range ins {
		v := AskedView{ID: in.ID, At: in.CreatedAt, Skill: in.Skill, Kind: in.Kind, Title: in.Title, Body: clip(in.Body, 300),
			Reason: in.Reason, Why: in.Why, Decision: in.Decision, Status: in.Status, Confidence: in.Confidence,
			Card: in.Card, Chat: in.Holder != ""}
		for _, e := range in.Evidence {
			if q := strings.TrimSpace(e.Quote); q != "" && len(v.Evidence) < 3 {
				v.Evidence = append(v.Evidence, clip(q, 120))
			}
		}
		if in.Status == model.Rejected || in.Status == model.Undone {
			v.Answer = strings.TrimSpace(in.Error)
		}
		out = append(out, v)
	}
	return out, nil
}

// saidOf is the words an entity was first read from: the earliest change of
// it that quotes the conversation.
func (m *Mind) saidOf(ctx context.Context, id string) string {
	changes, err := m.Opts.Store.Changes(ctx, id, 50)
	if err != nil {
		return ""
	}
	for i := len(changes) - 1; i >= 0; i-- { // oldest first
		for _, e := range changes[i].Evidence {
			if q := strings.TrimSpace(e.Quote); q != "" {
				return clip(q, 120)
			}
		}
	}
	return ""
}

// rooms names conversations as a card does: "카카오톡 '지수' 대화".
type rooms map[string]string

func (r rooms) label(room string) string {
	switch {
	case room == skill.CalendarRoom:
		return "캘린더"
	case r[room] != "":
		return r[room]
	case room == "":
		return "기록"
	}
	return "대화"
}

func (m *Mind) roomNames(ctx context.Context) rooms {
	out := rooms{}
	list, err := m.Opts.Store.Rooms(ctx)
	if err != nil {
		return out
	}
	for _, r := range list {
		app := map[string]string{"kakao": "카카오톡", "imessage": "iMessage", "chat": "jarvis", "sim": "시뮬레이터"}[r.Source]
		if app == "" {
			app = r.Source
		}
		out[r.Room] = fmt.Sprintf("%s '%s' 대화", app, r.Name)
	}
	return out
}
