package skill

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Schedule owns plans and keeps the calendar in step with them: a plan
// agreed on goes on the calendar, a plan that moves moves there too, a plan
// called off comes off. Before a plan it looks again - the day before, for
// what is still undone about it, and two hours before, to say it is coming.
type Schedule struct{}

func (Schedule) Name() string                                { return "schedule" }
func (Schedule) Wants(e model.Entity) bool                   { return e.Kind == model.KindPlan }
func (Schedule) OnTick(context.Context, View, string) Result { return Result{} }

func (s Schedule) OnChange(ctx context.Context, v View, e model.Entity, changes []model.Change) Result {
	st := e.State
	var r Result
	switch st.Status {
	case model.Cancelled, model.Done:
		r.CancelTimers, r.Withdraw = true, true
		r.Updates = closeLoops(ctx, v, e)
		// Taking the entry off the calendar is for a plan called off in a
		// conversation. One the calendar itself ended - the entry deleted
		// there, or this copy of it dropped because a conversation's plan
		// speaks for the same entry now - must not take the entry with it.
		if st.Status == model.Cancelled && st.CalendarID != "" && !byCalendar(changes) {
			r.Intents = append(r.Intents, s.remove(e, changes))
		}
		return r

	case model.Proposed:
		// Nothing is agreed yet: nothing to put anywhere, and an earlier
		// question about it no longer stands.
		r.Withdraw = true
		return r
	}

	// Confirmed.
	if st.Date == "" {
		// Agreed, but not when: keep that open until a date is set.
		if !hasLoop(ctx, v, e, "날짜") {
			r.Creates = append(r.Creates, Create{Kind: model.KindLoop, Room: e.Room, State: model.State{
				Title: st.Title + " 날짜 정하기", Status: model.Open, Owner: "me", PlanID: e.ID,
			}, Why: "확정됐지만 날짜가 없음"})
		}
		return r
	}
	// A date was set: a "날짜 정하기" loop for it is done.
	r.Updates = append(r.Updates, closeLoopsTitled(ctx, v, e, "날짜 정하기")...)

	if st.CalendarID == "" {
		// Put on the calendar some other way - by the assistant in a
		// conversation, by hand - under a title of its own: that entry is
		// this plan, not a gap to fill (nor, in protect, a collision).
		if twin, ok := calendarTwin(ctx, v, e); ok {
			r.Withdraw = true
			r.Updates = append(r.Updates, Update{ID: e.ID, Why: "캘린더의 같은 일정(" + twin.State.Title + ")에 이음",
				Set: func(st *model.State) bool {
					if st.CalendarID != "" {
						return false
					}
					st.CalendarID = twin.State.CalendarID
					model.TakeTime(st, twin.State)
					return true
				}})
			return r
		}
		if in, ok := s.add(ctx, v, e, changes); ok {
			r.Intents = append(r.Intents, in)
		}
		return r
	}

	// On the calendar already: follow it there if it moved, and look again
	// before it whenever it was just put there or moved.
	base := changes[0].Before
	linked := false
	for _, c := range changes {
		if c.Before != nil && c.Before.CalendarID == "" && c.After.CalendarID != "" {
			linked = true
		}
	}
	moved := base != nil && base.CalendarID != "" && calendarFields(*base) != calendarFields(st)
	// Moved on the calendar itself (by hand, or by an approved move): the
	// calendar already says so.
	fromCalendar := true
	for _, c := range changes {
		fromCalendar = fromCalendar && c.Actor == "sensor:calendar"
	}
	if moved && !fromCalendar {
		r.Intents = append(r.Intents, s.move(e, *base, changes))
	}
	if linked || moved {
		r.CancelTimers = true
		r.Timers = timersFor(e, v)
	}
	if linked {
		// On the calendar now, however it got there: an open "add it" is moot.
		r.Withdraw = true
	}
	return r
}

// byCalendar is whether every change came from reading the calendar.
func byCalendar(changes []model.Change) bool {
	if len(changes) == 0 {
		return false
	}
	for _, c := range changes {
		if c.Actor != "sensor:calendar" {
			return false
		}
	}
	return true
}

func (s Schedule) OnTimer(ctx context.Context, v View, e model.Entity, t model.Timer) Result {
	if !e.Live() || e.State.Status != model.Confirmed {
		return Result{}
	}
	st := e.State
	switch t.Name {
	case "prep":
		loops := openLoops(ctx, v, e)
		body := Day(st, v.Loc())
		if st.Place != "" {
			body += " · " + st.Place
		}
		in := model.Intent{Kind: "info", EntityID: e.ID, Title: "내일 " + st.Title, Reason: "하루 전 확인", Confidence: 1, Link: CalendarLink(st)}
		if len(loops) > 0 {
			titles := make([]string, len(loops))
			for i, l := range loops {
				titles[i] = l.State.Title
			}
			body += "\n아직 남은 일: " + strings.Join(titles, ", ")
			in.Urgent = true // there is something to do about it
		}
		in.Body = withMemory(body, v.Recall(ctx, st.People))
		return Result{Intents: []model.Intent{in}}
	case "soon":
		body := "곧 시작해요"
		if st.Place != "" {
			body = st.Place + " · " + body
		}
		body = withMemory(body, v.Recall(ctx, st.People))
		return Result{Intents: []model.Intent{{
			Kind: "info", EntityID: e.ID, Title: st.Start + " " + st.Title, Body: body,
			Reason: "두 시간 전 알림", Confidence: 1, Urgent: true, Link: CalendarLink(st),
		}}}
	}
	return Result{}
}

// withMemory adds what memory holds about the people a plan is with - the
// thing worth knowing before seeing them.
func withMemory(body string, lines []string) string {
	if len(lines) == 0 {
		return body
	}
	return body + "\n기억: " + strings.Join(lines, " · ")
}

// calendarTwin is a plan on the calendar that is this one told another way
// (model.SameEvent).
func calendarTwin(ctx context.Context, v View, e model.Entity) (model.Entity, bool) {
	plans, err := v.Entities(ctx, model.KindPlan, "", true)
	if err != nil {
		return model.Entity{}, false
	}
	for _, p := range plans {
		if p.ID != e.ID && p.State.CalendarID != "" && p.State.Status == model.Confirmed && model.SameEvent(e.State, p.State) {
			return p, true
		}
	}
	return model.Entity{}, false
}

// add is "put it on the calendar" - unless the operator already turned this
// exact entry down or took it back, or was asked it in the conversation by
// the assistant's own card: none is asked twice.
func (s Schedule) add(ctx context.Context, v View, e model.Entity, changes []model.Change) (model.Intent, bool) {
	ev := event(e.State, changes)
	payload, _ := json.Marshal(model.CalendarAction{Event: ev})
	past, _ := v.IntentsFor(ctx, e.ID)
	for _, p := range past {
		if p.Kind == "calendar.create" && (p.Status == model.Rejected || p.Status == model.Undone || p.Covered()) && samePayload(p.Payload, payload) {
			return model.Intent{}, false
		}
	}
	return model.Intent{
		Kind: "calendar.create", EntityID: e.ID, Payload: payload,
		Title:  "캘린더에 추가: " + Day(e.State, v.Loc()) + " " + e.State.Title,
		Reason: "대화에서 확정됐는데 캘린더에 없어요", Confidence: confidence(changes),
		Evidence: evidence(changes), Reversible: true, Link: CalendarLink(e.State),
	}, true
}

// CalendarLink is the console's calendar on the plan's day, with its entry
// marked when it is on the calendar already (web/src/lib/focus.ts).
func CalendarLink(st model.State) string {
	q := url.Values{}
	if st.Date != "" {
		q.Set("date", st.Date)
	}
	q.Set("focus", "day")
	if st.CalendarID != "" {
		q.Set("focus", "event:"+st.CalendarID)
	}
	return "/calendar?" + q.Encode()
}

func (s Schedule) move(e model.Entity, before model.State, changes []model.Change) model.Intent {
	ev := event(e.State, changes)
	payload, _ := json.Marshal(model.CalendarAction{EventID: e.State.CalendarID, Event: ev})
	return model.Intent{
		Kind: "calendar.update", EntityID: e.ID, Payload: payload,
		Title:  "캘린더 수정: " + e.State.Title + " (" + diff(before, e.State) + ")",
		Reason: "대화에서 일정이 바뀌었어요", Confidence: confidence(changes),
		Evidence: evidence(changes), Reversible: true, Link: CalendarLink(e.State),
	}
}

func (s Schedule) remove(e model.Entity, changes []model.Change) model.Intent {
	payload, _ := json.Marshal(model.CalendarAction{EventID: e.State.CalendarID, Event: event(e.State, nil)})
	return model.Intent{
		Kind: "calendar.delete", EntityID: e.ID, Payload: payload,
		Title: "캘린더에서 삭제: " + e.State.Title, Body: readWhy(changes),
		Reason: "대화에서 취소됐어요", Confidence: confidence(changes),
		Evidence: evidence(changes), Reversible: true,
	}
}

// readWhy is how the conversation was read, latest last - what ties the
// words to this plan when they do not name it ("본사는 판교 - 검색").
func readWhy(changes []model.Change) string {
	why := ""
	for _, c := range changes {
		if c.Actor == "interpreter" && c.Why != "" {
			why = c.Why
		}
	}
	return why
}

// timersFor looks again a day before (an evening before an all-day plan)
// and, for a plan with a time, two hours before.
func timersFor(e model.Entity, v View) []model.Timer {
	at, timed, ok := When(e.State, v.Loc())
	if !ok {
		return nil
	}
	var out []model.Timer
	prep := at.Add(-24 * time.Hour)
	if !timed {
		prep = at.Add(-13 * time.Hour) // 20:00 the evening before
	}
	if prep.After(v.Now()) {
		out = append(out, model.Timer{Skill: "schedule", EntityID: e.ID, Name: "prep", FireAt: prep})
	}
	if soon := at.Add(-2 * time.Hour); timed && soon.After(v.Now()) {
		out = append(out, model.Timer{Skill: "schedule", EntityID: e.ID, Name: "soon", FireAt: soon})
	}
	return out
}

func event(s model.State, changes []model.Change) model.CalendarEvent {
	owner := "me"
	if s.Shared {
		owner = "shared"
	}
	memo := "jarvis 가 대화에서 찾은 일정"
	if ev := evidence(changes); len(ev) > 0 {
		memo += " · “" + ev[len(ev)-1].Quote + "”"
	}
	return model.CalendarEvent{Title: s.Title, Date: s.Date, EndDate: s.EndDate, Start: s.Start, End: s.End, Place: s.Place, Memo: memo, Owner: owner}
}

type calFields struct{ title, date, endDate, start, end, place string }

func calendarFields(s model.State) calFields {
	return calFields{s.Title, s.Date, s.EndDate, s.Start, s.End, s.Place}
}

func diff(a, b model.State) string {
	var parts []string
	add := func(label, x, y string) {
		if x != y {
			if x == "" {
				x = "없음"
			}
			if y == "" {
				y = "없음"
			}
			parts = append(parts, label+" "+x+" → "+y)
		}
	}
	add("제목", a.Title, b.Title)
	add("날짜", a.Date, b.Date)
	add("시작", a.Start, b.Start)
	add("장소", a.Place, b.Place)
	if len(parts) == 0 {
		return "세부 변경"
	}
	return strings.Join(parts, ", ")
}

// evidence is what the batch of changes rested on, latest last.
func evidence(changes []model.Change) []model.Evidence {
	var out []model.Evidence
	for _, c := range changes {
		out = append(out, c.Evidence...)
	}
	return out
}

func confidence(changes []model.Change) float64 {
	conf := 1.0
	for _, c := range changes {
		if c.Actor == "interpreter" && c.Confidence < conf {
			conf = c.Confidence
		}
	}
	return conf
}

func samePayload(a, b json.RawMessage) bool {
	var x, y model.CalendarAction
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	x.Event.Memo, y.Event.Memo = "", ""
	return x == y
}

func openLoops(ctx context.Context, v View, e model.Entity) []model.Entity {
	loops, _ := v.Entities(ctx, model.KindLoop, "", true)
	var out []model.Entity
	for _, l := range loops {
		if l.State.PlanID == e.ID {
			out = append(out, l)
		}
	}
	return out
}

func hasLoop(ctx context.Context, v View, e model.Entity, word string) bool {
	for _, l := range openLoops(ctx, v, e) {
		if strings.Contains(l.State.Title, word) {
			return true
		}
	}
	return false
}

func closeLoops(ctx context.Context, v View, e model.Entity) []Update {
	var out []Update
	for _, l := range openLoops(ctx, v, e) {
		out = append(out, Update{ID: l.ID, Why: "일정이 끝남", Set: func(s *model.State) bool { s.Status = model.Closed; return true }})
	}
	return out
}

func closeLoopsTitled(ctx context.Context, v View, e model.Entity, suffix string) []Update {
	var out []Update
	for _, l := range openLoops(ctx, v, e) {
		if strings.HasSuffix(l.State.Title, suffix) {
			out = append(out, Update{ID: l.ID, Why: "날짜가 정해짐", Set: func(s *model.State) bool { s.Status = model.Closed; return true }})
		}
	}
	return out
}
