package mind

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/goals"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// A goal is kept by the person who set it; what mind adds is the word at the
// right moment, on their phone and in their conversation, each person's own:
//
//   - the noon after a day a goal asked of and nobody marked: did you?
//   - the evening a week's target can only just be made: which days it takes
//   - the evening of a run worth keeping, when today is not marked yet
//   - at the hour the goal asked to be asked
//   - Sunday night, how the week went
//
// A goal left unanswered for days stops asking: nobody wants a daily question
// about something they let go of.

const (
	goalNoonAt  = 12*60 + 30 // the day after, once the morning is over
	goalNightAt = 21*60 + 30 // late enough to know, early enough to do it
	goalTick    = time.Minute
	// goalGuard is the run worth a word before the day ends.
	goalGuard = 7
	// goalLapse is how many asked days in a row unmarked before it stops asking,
	// goalQuiet how many days of no word on a goal that asks of no day.
	goalLapse = 3
	goalQuiet = 21
	// goalRemindFor is how late a goal's own hour may still be said: down at the
	// time (a deploy), and not long after.
	goalRemindFor = 2 * time.Hour
)

// goalAsk is one question a notice card asks: how did this day go. The card
// answers it with 했다 and 안 했다.
type goalAsk struct {
	Goal  string `json:"goal"`
	Title string `json:"title"`
	Day   string `json:"day"`
}

// goalSay rings the person's phone and lays a card in their conversation.
func (m *Mind) goalSay(ctx context.Context, who, key, title, body, why string, asks []goalAsk) error {
	data := map[string]any{"why": why, "body": body}
	if len(asks) > 0 {
		data["goals"] = asks
	}
	n := act.Notice{Kind: "remind.goal", Title: title, Body: strings.TrimSpace(body + "\n" + why), URL: "/goals", Tier: "now", Level: "warn",
		At: m.Opts.Now(), Key: key, Card: true, Data: data}
	if who == goals.Guest {
		n.Audience, data["who"] = goals.Guest, goals.Guest
	}
	if s, ok := m.notifier.(act.Sender); ok {
		return s.Send(ctx, n)
	}
	m.notifier.Notify(ctx, n)
	return nil
}

// goalsGoing is the goals going, everyone's, with their marks.
func (m *Mind) goalsGoing(ctx context.Context) ([]goals.Goal, map[string]map[string]goals.Mark, error) {
	gs, err := m.Goals.AllOf(ctx, "", false)
	if err != nil {
		return nil, nil, err
	}
	going := gs[:0]
	marks := map[string]map[string]goals.Mark{}
	for _, g := range gs {
		if g.Status != goals.Active {
			continue
		}
		ms, err := m.Goals.Marks(ctx, g.ID)
		if err != nil {
			return nil, nil, err
		}
		going = append(going, g)
		marks[g.ID] = ms
	}
	return going, marks, nil
}

// lapsed is whether the goal has been let go of, and is not asked about: its
// last asked days all went unmarked - or, for one that asks of no day, three
// weeks went by with no word on it.
func lapsed(g goals.Goal, marks map[string]goals.Mark, today string) bool {
	seen := 0
	day, err := time.Parse("2006-01-02", today)
	if err != nil {
		return false
	}
	if g.Cadence == goals.Weekly || g.Cadence == goals.Monthly {
		since := day.AddDate(0, 0, -goalQuiet).Format("2006-01-02")
		if g.Start > since {
			return false
		}
		for d := range marks {
			if d >= since {
				return false
			}
		}
		return true
	}
	for i := 1; i <= 21 && seen < goalLapse; i++ {
		d := day.AddDate(0, 0, -i).Format("2006-01-02")
		if d < g.Start {
			break
		}
		if !g.Asks(d) {
			continue
		}
		if _, ok := marks[d]; ok {
			return false
		}
		seen++
	}
	return seen >= goalLapse
}

// goalNoon asks about yesterday: a goal that asked of it, with no word on it,
// may only have been forgotten.
func (m *Mind) goalNoon(ctx context.Context) error {
	if m.Goals == nil {
		return nil
	}
	today := m.Goals.Today()
	day, err := time.Parse("2006-01-02", today)
	if err != nil {
		return err
	}
	yesterday := day.AddDate(0, 0, -1).Format("2006-01-02")
	gs, marks, err := m.goalsGoing(ctx)
	if err != nil {
		return err
	}
	asks := map[string][]goalAsk{}
	for _, g := range gs {
		if _, said := marks[g.ID][yesterday]; said || !g.Asks(yesterday) || lapsed(g, marks[g.ID], today) {
			continue
		}
		asks[g.Person()] = append(asks[g.Person()], goalAsk{Goal: g.ID, Title: g.Title, Day: yesterday})
	}
	for who, list := range asks {
		title := fmt.Sprintf("어제 '%s' 했어요?", list[0].Title)
		if len(list) > 1 {
			title = fmt.Sprintf("어제 목표 %d개에 기록이 없어요", len(list))
		}
		if err := m.goalSay(ctx, who, "goal:noon:"+who+":"+today, title, "", "어제 기록이 없어서 여쭤봐요", list); err != nil {
			return err
		}
	}
	return nil
}

// goalNight says what only the evening knows: a target that takes every day
// left, a run about to end - and, on Sunday, how the week went.
func (m *Mind) goalNight(ctx context.Context) error {
	if m.Goals == nil {
		return nil
	}
	today := m.Goals.Today()
	gs, marks, err := m.goalsGoing(ctx)
	if err != nil {
		return err
	}
	guard := map[string][]goalAsk{}
	week := map[string][]string{}
	tight := map[string][]tightGoal{}
	for _, g := range gs {
		ms := marks[g.ID]
		p := g.Of(ms, today)
		who := g.Person()
		if done, asked := weekSoFar(g, p); asked > 0 {
			week[who] = append(week[who], fmt.Sprintf("%s %d/%d", g.Title, done, asked))
		}
		switch {
		case g.Cadence == goals.Weekly || g.Cadence == goals.Monthly:
			left := g.LeftDays(today)
			if p.Need == 0 || p.Need != len(left) || lapsed(g, ms, today) {
				continue
			}
			key := "goal:tight:" + g.ID + ":" + p.Period.From
			if done, err := m.Opts.Store.Cursor(ctx, key); err != nil || done > 0 {
				continue
			}
			var asks []goalAsk
			if p.Mark == "" {
				asks = []goalAsk{{Goal: g.ID, Title: g.Title, Day: today}}
			}
			word := "이번 주"
			if g.Cadence == goals.Monthly {
				word = "이번 달"
			}
			tight[who] = append(tight[who], tightGoal{key: key, title: g.Title, asks: asks,
				says: fmt.Sprintf("%s %d/%d, %s 모두 해야 %d번을 채워요", word, p.Period.Done, p.Period.Target, dayNames(left), p.Period.Target)})
		case p.Asks && p.Mark == "" && p.Streak >= goalGuard:
			guard[who] = append(guard[who], goalAsk{Goal: g.ID, Title: fmt.Sprintf("%s (%d%s째)", g.Title, p.Streak, p.Unit), Day: today})
		}
	}
	for who, list := range tight {
		if err := m.sayTight(ctx, who, list); err != nil {
			return err
		}
	}
	for who, list := range guard {
		title := fmt.Sprintf("오늘 '%s' 기록이 아직 없어요", strings.SplitN(list[0].Title, " (", 2)[0])
		if len(list) > 1 {
			title = fmt.Sprintf("이어 온 목표 %d개에 오늘 기록이 없어요", len(list))
		}
		if err := m.goalSay(ctx, who, "goal:guard:"+who+":"+today, title, "", "이어 온 날이 끊기지 않게", list); err != nil {
			return err
		}
	}
	if now := m.Opts.Now().In(m.Opts.Loc); now.Weekday() == time.Sunday {
		for who, lines := range week {
			if err := m.goalSay(ctx, who, "goal:week:"+who+":"+today, "이번 주 목표", strings.Join(lines, " · "), "한 주를 마치며", nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// tightGoal is a target that takes every day left.
type tightGoal struct {
	key, title, says string
	asks             []goalAsk
}

// sayTight says one person's tight targets in one notice: pushes of one kind
// take each other's place on the phone, so a second would leave only itself.
func (m *Mind) sayTight(ctx context.Context, who string, list []tightGoal) error {
	title, body := fmt.Sprintf("'%s' 남은 날 다 해야 해요", list[0].title), list[0].says
	if len(list) > 1 {
		title = fmt.Sprintf("목표 %d개가 남은 날 다 해야 해요", len(list))
		lines := make([]string, len(list))
		for i, t := range list {
			lines[i] = fmt.Sprintf("- %s: %s", t.title, t.says)
		}
		body = strings.Join(lines, "\n")
	}
	var asks []goalAsk
	for _, t := range list {
		asks = append(asks, t.asks...)
	}
	if err := m.goalSay(ctx, who, list[0].key, title, body, "지금 채우면 겨우 맞출 수 있어요", asks); err != nil {
		return err
	}
	for _, t := range list {
		if err := m.Opts.Store.SetCursor(ctx, t.key, 1); err != nil {
			return err
		}
	}
	return nil
}

// weekSoFar is what this week's row adds up to: done of what the week asks.
func weekSoFar(g goals.Goal, p goals.Progress) (done, asked int) {
	if g.Cadence == goals.Monthly {
		return 0, 0
	}
	if g.Cadence == goals.Weekly {
		return p.Period.Done, p.Period.Target
	}
	for _, c := range p.Week {
		if c.Asks {
			asked++
		}
		if c.Mark == goals.Done {
			done++
		}
	}
	return done, asked
}

// dayNames is days said together: "금·토·일", or how many when there are many.
func dayNames(days []string) string {
	if len(days) > 7 {
		return fmt.Sprintf("남은 %d일", len(days))
	}
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = goals.Name(d)
	}
	return strings.Join(names, "·")
}

// goalHour says the goals whose hour has come, once a day each, for today if
// nothing has been said of it and it still matters. Goals of one hour go out
// together, for the reason sayTight gives.
func (m *Mind) goalHour(ctx context.Context) error {
	if m.Goals == nil {
		return nil
	}
	gs, err := m.Goals.AllOf(ctx, "", false)
	if err != nil {
		return err
	}
	now := m.Opts.Now().In(m.Opts.Loc)
	today := m.Goals.Today()
	stamp, _ := strconv.ParseInt(now.Format("20060102"), 10, 64)
	due := map[string][]goalAsk{}
	says := map[string]string{}
	for _, g := range gs {
		if g.Remind == "" || g.Status != goals.Active {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04", today+" "+g.Remind, m.Opts.Loc)
		// Made after its hour had passed, it starts with tomorrow's.
		if err != nil || now.Before(at) || now.Sub(at) > goalRemindFor || g.CreatedAt.After(at) {
			continue
		}
		key := "goal:hour:" + g.ID
		if last, err := m.Opts.Store.Cursor(ctx, key); err != nil || last >= stamp {
			continue
		}
		marks, err := m.Goals.Marks(ctx, g.ID)
		if err != nil {
			return err
		}
		p := g.Of(marks, today)
		weekly := g.Cadence == goals.Weekly || g.Cadence == goals.Monthly
		if err := m.Opts.Store.SetCursor(ctx, key, stamp); err != nil {
			return err
		}
		if p.Mark != "" || !p.Asks && !weekly || weekly && p.Need == 0 {
			continue
		}
		due[g.Person()] = append(due[g.Person()], goalAsk{Goal: g.ID, Title: g.Title, Day: today})
		says[g.Person()] = p.Says
	}
	for who, list := range due {
		title, body := fmt.Sprintf("오늘 '%s' 했어요?", list[0].Title), says[who]
		if len(list) > 1 {
			title, body = fmt.Sprintf("오늘 목표 %d개, 했어요?", len(list)), ""
		}
		if err := m.goalSay(ctx, who, "goal:hour:"+list[0].Goal+":"+today, title, body, "정해 둔 시각이에요", list); err != nil {
			return err
		}
	}
	return nil
}

// goalWorkout marks the goals tied to the workout log: a day with a workout was
// done, unless the operator said otherwise.
func (m *Mind) goalWorkout(ctx context.Context, o model.Occurrence) error {
	if m.Goals == nil || o.Kind != "workout" {
		return nil
	}
	gs, err := m.Goals.AllOf(ctx, goals.Owner, false)
	if err != nil {
		return err
	}
	day := o.At.In(m.Opts.Loc).Format("2006-01-02")
	for _, g := range gs {
		if g.Auto == goals.AutoWorkout && g.Status == goals.Active && day >= g.Start {
			if err := m.Goals.Unmarked(ctx, g.ID, day, goals.ByWorkout); err != nil {
				return err
			}
		}
	}
	return nil
}

// endGoals ends, at night, the goals whose last day has passed.
func (m *Mind) endGoals(ctx context.Context) error {
	if m.Goals == nil {
		return nil
	}
	gs, err := m.Goals.AllOf(ctx, "", false)
	if err != nil {
		return err
	}
	today := m.Goals.Today()
	on := true
	for _, g := range gs {
		if g.End != "" && g.End < today {
			if _, err := m.Goals.Change(ctx, g.ID, goals.Change{Finish: &on}); err != nil {
				return err
			}
		}
	}
	return nil
}

// goalLineOf is who's goals, for the conversation's situation: what it answers
// "오늘 뭐 해야 돼?" from, and the ids its tools take.
func (m *Mind) goalLineOf(ctx context.Context, who string) string {
	if m.Goals == nil {
		return ""
	}
	gs, err := m.Goals.AllOf(ctx, who, false)
	if err != nil {
		m.Opts.Logger.Warn("목표를 읽지 못했습니다", "err", err)
		return ""
	}
	today := m.Goals.Today()
	var parts []string
	for i, g := range gs {
		if i == 8 {
			parts = append(parts, fmt.Sprintf("… 그 밖에 %d개", len(gs)-8))
			break
		}
		marks, err := m.Goals.Marks(ctx, g.ID)
		if err != nil {
			m.Opts.Logger.Warn("목표의 기록을 읽지 못했습니다", "goal", g.ID, "err", err)
			continue
		}
		parts = append(parts, g.Line(g.Of(marks, today)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "- 목표(운영자가 했다·안 했다고 말하면 change 의 goal.check, 이 id 로): " + strings.Join(parts, " / ")
}
