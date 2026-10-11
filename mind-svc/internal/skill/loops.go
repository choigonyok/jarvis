package skill

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Loops owns open loops - what I promised, what I owe a reply on, what
// someone owes me - and brings one up before its deadline.
type Loops struct{}

func (Loops) Name() string                                { return "loops" }
func (Loops) Wants(e model.Entity) bool                   { return e.Kind == model.KindLoop }
func (Loops) OnTick(context.Context, View, string) Result { return Result{} }

func (Loops) OnChange(_ context.Context, v View, e model.Entity, _ []model.Change) Result {
	r := Result{CancelTimers: true}
	if !e.Live() || e.State.Due == "" {
		return r
	}
	if at, ok := dueAt(e.State.Due, v.Loc()); ok {
		remind := at.Add(-3 * time.Hour)
		if !strings.Contains(e.State.Due, "T") {
			remind = at.Add(-9 * time.Hour) // 09:00 on the day
		}
		if remind.Before(v.Now()) {
			remind = v.Now()
		}
		r.Timers = []model.Timer{{Skill: "loops", EntityID: e.ID, Name: "due", FireAt: remind}}
	}
	return r
}

func (Loops) OnTimer(_ context.Context, v View, e model.Entity, t model.Timer) Result {
	if t.Name != "due" || !e.Live() {
		return Result{}
	}
	title := e.State.Title
	if e.State.Owner == "them" {
		who := strings.Join(e.State.People, ", ")
		if who == "" {
			who = "상대"
		}
		title = who + "가 하기로 한 것: " + title
	}
	return Result{Intents: []model.Intent{{
		Kind: "info", EntityID: e.ID, Title: title, Body: "기한 " + dueText(e.State.Due, v.Loc()),
		Reason: "기한이 다가오는 열린 고리", Confidence: 1, Urgent: true,
	}}}
}

// dueAt is a due date's end: midnight after a date, or the time given.
func dueAt(due string, loc *time.Location) (time.Time, bool) {
	if t, err := time.ParseInLocation("2006-01-02T15:04", due, loc); err == nil {
		return t, true
	}
	if d, err := time.ParseInLocation("2006-01-02", due, loc); err == nil {
		return d.Add(18 * time.Hour), true
	}
	return time.Time{}, false
}

func dueText(due string, loc *time.Location) string {
	if t, ok := dueAt(due, loc); ok {
		s := fmt.Sprintf("%d/%d(%s)", int(t.Month()), t.Day(), weekdays[t.Weekday()])
		if strings.Contains(due, "T") {
			s += " " + t.Format("15:04")
		}
		return s
	}
	return due
}

// Brief is the morning: what is on today, and what is still open and due.
type Brief struct{}

func (Brief) Name() string                                                        { return "brief" }
func (Brief) Wants(model.Entity) bool                                             { return false }
func (Brief) OnChange(context.Context, View, model.Entity, []model.Change) Result { return Result{} }
func (Brief) OnTimer(context.Context, View, model.Entity, model.Timer) Result     { return Result{} }

func (Brief) OnTick(ctx context.Context, v View, tick string) Result {
	if tick != "morning" {
		return Result{}
	}
	now := v.Now().In(v.Loc())
	today := now.Format("2006-01-02")
	tomorrow := now.AddDate(0, 0, 1).Format("2006-01-02")

	plans, _ := v.Entities(ctx, model.KindPlan, "", true)
	var lines []string
	n := 0
	for _, p := range plans {
		if p.State.Status == model.Confirmed && (p.State.Date == today || (p.State.Date < today && p.State.EndDate >= today)) {
			n++
			line := "· " + p.State.Title
			if p.State.Start != "" {
				line = "· " + p.State.Start + " " + p.State.Title
			}
			if p.State.Place != "" {
				line += " @" + p.State.Place
			}
			lines = append(lines, line)
		}
	}
	loops, _ := v.Entities(ctx, model.KindLoop, "", true)
	m := 0
	for _, l := range loops {
		if l.State.Due != "" && l.State.Due[:10] <= tomorrow {
			m++
			lines = append(lines, "· 할 일: "+l.State.Title+" (기한 "+dueText(l.State.Due, v.Loc())+")")
		}
	}
	if n+m == 0 {
		return Result{}
	}
	return Result{Intents: []model.Intent{{
		Kind: "info", Title: fmt.Sprintf("오늘 일정 %d개 · 챙길 일 %d개", n, m), Body: strings.Join(lines, "\n"),
		Reason: "아침 브리핑", Confidence: 1, Urgent: true,
	}}}
}
