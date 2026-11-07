package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

// Loops owns open loops - what I promised, what I owe a reply on, what
// someone owes me - and brings one up before its deadline: someone else's
// as a push, the operator's own as a card offering a reminder.
type Loops struct{}

func (Loops) Name() string                                { return "loops" }
func (Loops) Wants(e model.Entity) bool                   { return e.Kind == model.KindLoop }
func (Loops) OnTick(context.Context, View, string) Result { return Result{} }

func (Loops) OnChange(_ context.Context, v View, e model.Entity, changes []model.Change) Result {
	// The assistant's own promise is told by the reminder or the job it
	// became - not again, as "Jarvis가 하기로 한 것" at 09:00 on the day,
	// knowing neither its time nor its changes.
	if !e.Live() || e.State.Due == "" || e.AssistantsPromise() {
		return Result{CancelTimers: true}
	}
	// A loop is told about once for its deadline. A note or a title changed is
	// the same loop and the same deadline: its timer stays as it was, fired or
	// not. Setting it anew put a deadline already gone by at "now", so the
	// order list added to a "사 가기" at 18:31 rang its 18:00 push again at
	// 18:32.
	if !newDeadline(changes) {
		return Result{}
	}
	at, ok := DueAt(e.State.Due, v.Loc())
	if !ok {
		return Result{CancelTimers: true}
	}
	if operators(e) {
		// The operator's own promise is offered as a reminder and rings only as
		// one: a card, never a push of its own. The operator's rule: "내가 명시적으로
		// 말한 게 아니면 리마인드 생성 카드를 띄우는 쪽으로" - and the 18:00 push went out past it.
		r := Result{CancelTimers: true}
		if in, ok := remindCard(e, at, v); ok {
			r.Intents = []model.Intent{in}
		}
		return r
	}
	remind := at.Add(-3 * time.Hour)
	if !strings.Contains(e.State.Due, "T") {
		remind = at.Add(-9 * time.Hour) // 09:00 on the day
	}
	if e.State.Type == "정산" {
		remind = at // the evening of the third day
	}
	if remind.Before(v.Now()) {
		remind = v.Now()
	}
	return Result{CancelTimers: true, Timers: []model.Timer{{Skill: "loops", EntityID: e.ID, Name: "due", FireAt: remind}}}
}

// newDeadline is whether changes gave a loop a deadline to keep: made with
// one, given another, opened again, or passed to someone else. None at all is
// the loop as it stands, asked about (a settle, a test).
func newDeadline(changes []model.Change) bool {
	if len(changes) == 0 {
		return true
	}
	for _, c := range changes {
		if c.Before == nil {
			return true
		}
		b, a := *c.Before, c.After
		if b.Due != a.Due || b.Owner != a.Owner || !live(b.Status) && live(a.Status) {
			return true
		}
	}
	return false
}

func live(status string) bool {
	switch status {
	case model.Cancelled, model.Done, model.Closed:
		return false
	}
	return true
}

// operators is whether a loop is the operator's own to do: what they
// promised. Money owed is someone else's to send, whoever the interpreter
// named.
func operators(e model.Entity) bool {
	return e.State.Owner == "me" && e.State.Type != "정산"
}

// remindCard offers a reminder for the operator's own promise: when (three
// hours before a time, 09:00 on a day, or the next half hour when that has
// gone by) and in what words, both theirs to change on the card. A deadline a
// day gone is not offered: the morning brief counts it.
func remindCard(e model.Entity, due time.Time, v View) (model.Intent, bool) {
	now, loc := v.Now(), v.Loc()
	if due.Before(now.Add(-24 * time.Hour)) {
		return model.Intent{}, false
	}
	first := due.Add(-3 * time.Hour)
	if !strings.Contains(e.State.Due, "T") {
		first = due.Add(-9 * time.Hour)
	}
	if first.Before(now.Add(10 * time.Minute)) {
		first = nextHalfHour(now.Add(20 * time.Minute))
	}
	choices := map[string]string{}
	var options []string
	for i := range 3 {
		t := first.Add(time.Duration(i) * time.Hour).In(loc)
		label := stamp(t)
		options = append(options, label)
		choices[label] = t.Format("2006-01-02T15:04")
	}
	payload, _ := json.Marshal(map[string]any{
		"loop": e.ID, "text": e.State.Title, "body": e.State.Note, "at": choices[options[0]], "choices": choices,
	})
	body := "기한 " + dueText(e.State.Due, loc)
	if len(e.State.People) > 0 {
		body += " · " + strings.Join(e.State.People, ", ")
	}
	return model.Intent{
		Kind: model.RemindSet, EntityID: e.ID, Payload: payload,
		Title:  e.State.Title + " - 알려 드릴까요?",
		Body:   body,
		Reason: "하기로 하신 일이에요. 알려 달라고 하신 건 아니라 리마인드로 걸어 둘지 먼저 물어요",
		// Approved, it is a reminder - cancelled again by undoing.
		Confidence: 1, Reversible: true,
		Fields: []model.Field{
			{Key: "at", Label: "언제 알릴까요", Kind: "select", Value: options[0], Options: options, Required: true},
			{Key: "text", Label: "알릴 말", Kind: "text", Value: e.State.Title, Required: true},
		},
	}, true
}

// nextHalfHour is t, or the next :00 or :30 after it.
func nextHalfHour(t time.Time) time.Time {
	r := t.Truncate(30 * time.Minute)
	if r.Before(t) {
		r = r.Add(30 * time.Minute)
	}
	return r
}

// stamp is "11/6(금) 18:30".
func stamp(t time.Time) string {
	return fmt.Sprintf("%d/%d(%s) %s", int(t.Month()), t.Day(), weekdays[t.Weekday()], t.Format("15:04"))
}

func (Loops) OnTimer(_ context.Context, v View, e model.Entity, t model.Timer) Result {
	if t.Name != "due" || !e.Live() || e.AssistantsPromise() {
		return Result{}
	}
	if operators(e) {
		// A timer kept from before the operator's own promises became cards:
		// offered now as one, not pushed.
		at, ok := DueAt(e.State.Due, v.Loc())
		if !ok {
			return Result{}
		}
		in, ok := remindCard(e, at, v)
		if !ok {
			return Result{}
		}
		return Result{Intents: []model.Intent{in}}
	}
	title := e.State.Title
	if e.State.Owner == "them" {
		who := strings.Join(e.State.People, ", ")
		if who == "" {
			who = "상대"
		}
		title = who + "가 하기로 한 것: " + title
	}
	if e.State.Type == "정산" {
		// Money owed is a line in the evening, not a push: it is the
		// other person's to do.
		return Result{Intents: []model.Intent{{
			Kind: model.Info, EntityID: e.ID, Title: e.State.Title + " - 아직이에요", Body: "기한 " + dueText(e.State.Due, v.Loc()),
			Reason: "정산이 3일째 들어오지 않음", Confidence: 1,
		}}}
	}
	return Result{Intents: []model.Intent{{
		Kind: model.Info, EntityID: e.ID, Title: title, Body: "기한 " + dueText(e.State.Due, v.Loc()),
		Reason: "기한이 다가오는 열린 고리", Confidence: 1, Urgent: true,
	}}}
}

// DueAt is a due date's end: 18:00 on a date, or the time given.
func DueAt(due string, loc *time.Location) (time.Time, bool) {
	if t, err := time.ParseInLocation("2006-01-02T15:04", due, loc); err == nil {
		return t, true
	}
	if d, err := time.ParseInLocation("2006-01-02", due, loc); err == nil {
		return d.Add(18 * time.Hour), true
	}
	return time.Time{}, false
}

func dueText(due string, loc *time.Location) string {
	if t, ok := DueAt(due, loc); ok {
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

// briefOverdueDays is how many days past its deadline a loop is still named
// in the brief.
const briefOverdueDays = 3

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
		// The partner's own entries are theirs, not the operator's day
		// (the sensor marks them so, and protect and rain leave them be).
		if p.State.Owner == "partner" {
			continue
		}
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
	m, older := 0, 0
	// A loop is listed from a day before it is due until three days after.
	// Older than that it is counted, not named: one from the 13th was read
	// out every morning for a fortnight, burying the day's.
	since := now.AddDate(0, 0, -briefOverdueDays).Format("2006-01-02")
	for _, l := range loops {
		if len(l.State.Due) < 10 || l.AssistantsPromise() {
			continue
		}
		due := l.State.Due[:10]
		switch {
		case due > tomorrow:
		case due < since:
			older++
		default:
			m++
			lines = append(lines, "· 할 일: "+l.State.Title+" (기한 "+dueText(l.State.Due, v.Loc())+")")
		}
	}
	if older > 0 {
		lines = append(lines, fmt.Sprintf("· 기한 지난 할 일 %d개 더 (2주가 지나면 접어요)", older))
	}
	// The weather at home, first: a wet day is worth a morning's word even
	// with nothing on.
	sky, hours, known := v.Weather(ctx, "", "", today)
	wet := known && sky.Wet()
	if n+m == 0 && !wet {
		return Result{}
	}
	title := fmt.Sprintf("오늘 일정 %d개 · 챙길 일 %d개", n, m)
	if known {
		lines = append([]string{"· 날씨: " + weatherLine(sky, hours, now)}, lines...)
	}
	if wet && n+m == 0 {
		title = "오늘 " + sky.Fall() + " · " + umbrella(sky)
	} else if wet {
		title += " · " + sky.Fall()
	}
	return Result{Intents: []model.Intent{{
		Kind: model.Info, Title: title, Body: strings.Join(lines, "\n"),
		Reason: "아침 브리핑", Confidence: 1, Urgent: true,
	}}}
}

// weatherLine is the day at home: "비 80% · 12mm (15시부터) · 11~18° - 우산
// 챙기세요" when it is wet, "맑음 · 11~18°" when it is not.
func weatherLine(d weather.Day, hours []weather.Hour, now time.Time) string {
	if !d.Wet() {
		return d.Line()
	}
	fall := d.Fall()
	if at, ok := weather.From(hours, now); ok && at.Format("2006-01-02") == d.Date && at.Hour() > now.Hour() {
		fall += fmt.Sprintf(" (%d시부터)", at.Hour())
	}
	return fall + " · " + d.Temps() + " - " + umbrella(d)
}

func umbrella(d weather.Day) string {
	if d.Snowy() {
		return "눈길 조심하세요"
	}
	return "우산 챙기세요"
}
