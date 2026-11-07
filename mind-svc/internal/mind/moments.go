package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

// The moments: what the operator asked to be told at a time, or on coming
// home or leaving it, said then by the moments service; and the
// assistant's own judgements that are worth more at such a moment than at
// once - a rebalance for the evening at home, the rain at the door.

// HereLatest is where the phones are, from what context-svc last said.
type HereLatest struct{ Store store.Store }

func (h HereLatest) read(ctx context.Context, kind string) ([]moments.Visit, error) {
	raw, _, err := h.Store.Latest(ctx, kind)
	if err != nil {
		return nil, err
	}
	var out struct {
		Visits []moments.Visit `json:"visits"`
	}
	return out.Visits, json.Unmarshal(raw, &out)
}

// Visits is where the operator is now: the open visits.
func (h HereLatest) Visits(ctx context.Context) ([]moments.Visit, error) {
	return h.read(ctx, model.KindContextNow)
}

// GuestVisits is where the guest's phone is now: the guest's own regions.
func (h HereLatest) GuestVisits(ctx context.Context) ([]moments.Visit, error) {
	return h.read(ctx, model.KindContextNowGuest)
}

// say is a reminder's moment come. The operator's own go out in their words
// as remind.asked, which rings even in quiet hours: they asked for this
// moment. The assistant's own go through the gate like any judgement.
func (m *Mind) say(ctx context.Context, r moments.Reminder) (moments.Said, error) {
	if r.Source == moments.Rebalance {
		wording, _ := json.Marshal(map[string]string{"title": r.Text, "body": r.Body})
		in, err := m.portfolioBand(ctx, obs.Observation{Source: "assets", Type: model.KindAssetsBand, Key: r.Ref, Notify: wording}, true)
		if err != nil {
			return moments.Said{}, err
		}
		return outcome(in), nil
	}
	kind := model.KindRemindAsked
	if r.Source == moments.Lists {
		// mind's own: what is left on a list, the evening before its day -
		// and nothing at all when nothing is left, or the list was closed.
		l, ok := m.reminderList(ctx, r)
		switch {
		case !ok || l.Status != checklist.Active:
			return moments.Said{How: moments.HowSilent, Why: "목록이 닫힘"}, nil
		case len(l.Left()) == 0:
			return moments.Said{How: moments.HowSilent, Why: "다 함"}, nil
		}
		kind = model.KindRemindList
	}
	why := r.Why(m.Opts.Loc)
	if now := m.Opts.Now(); r.SentBy == moments.ByTime && r.At != nil && now.Sub(*r.At) > 10*time.Minute {
		// Down at its time (a restart, a deploy): said late, and so.
		why = moments.Stamp(*r.At, m.Opts.Loc) + "에 알려 드렸어야 했는데 늦었어요"
	}
	// In the conversation too, as a card, not on the phone and in the inbox
	// only - with the reminder's list, if it has one, as it stands now; the
	// push says what is left on it.
	data := map[string]any{"reminder": r.ID, "why": why, "by": r.SentBy, "body": r.Body}
	lines := []string{r.Body}
	if l, ok := m.reminderList(ctx, r); ok {
		data["checklist"] = l.ID
		if left := l.Left(); len(left) > 0 {
			lines = append(lines, "남은 것: "+checklist.Names(left, 5))
		} else if len(l.Items) > 0 {
			lines = append(lines, l.Title+": 다 했어요")
		}
	}
	n := act.Notice{
		Kind: kind, Title: r.Text, Body: strings.TrimSpace(strings.Join(append(lines, why), "\n")),
		URL: firstNonEmpty(r.URL, "/"), Tier: "now", Level: "warn", At: m.Opts.Now(),
		// Changed and said again, it is a notice of its own.
		Key:  "remind:" + r.ID + ":" + strconv.FormatInt(r.UpdatedAt.Unix(), 10),
		Card: true, Data: data,
	}
	if r.Person() == moments.Guest {
		// The guest's own: their devices and inbox, and not a card in the
		// operator's conversation.
		n.Audience, data["who"] = moments.Guest, moments.Guest
	}
	if s, ok := m.notifier.(act.Sender); ok {
		return moments.Said{How: moments.HowPush}, s.Send(ctx, n)
	}
	m.notifier.Notify(ctx, n)
	return moments.Said{How: moments.HowPush}, nil
}

// outcome is what the gate made of an intent.
func outcome(in model.Intent) moments.Said {
	switch {
	case in.Status == model.Dropped:
		return moments.Said{How: moments.HowSilent, Why: in.Why}
	case in.Status == model.Failed:
		return moments.Said{How: moments.HowFailed, Why: in.Error}
	case in.Card:
		return moments.Said{How: moments.HowCard}
	case in.Tier == "now":
		return moments.Said{How: moments.HowPush}
	}
	return moments.Said{How: moments.HowDigest}
}

// onLocation is the operator arriving at a region or leaving it - or the
// guest's phone at one of the guest's regions, which moves only the guest's
// own reminders.
func (m *Mind) onLocation(ctx context.Context, o obs.Observation) error {
	var d struct {
		Region string `json:"region"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(o.Data, &d); err != nil || d.Region == "" {
		return nil
	}
	move := moments.Arrive
	if o.Type == model.KindLocationLeave || o.Type == model.KindLocationGuestLeave {
		move = moments.Leave
	}
	at := o.OccurredAt
	if at.IsZero() {
		at = m.Opts.Now()
	}
	if o.Type == model.KindLocationGuestEnter || o.Type == model.KindLocationGuestLeave {
		if m.Moments == nil {
			return nil
		}
		return m.Moments.MovedAs(ctx, moments.Guest, move, d.Region, d.Kind, at)
	}
	if m.Moments != nil {
		if err := m.Moments.Moved(ctx, move, d.Region, d.Kind, at); err != nil {
			return err
		}
	}
	if move == moments.Leave && d.Kind == "home" {
		return m.rainOut(ctx, at)
	}
	return nil
}

// onFix is a fix of the operator's phone: the reminders waiting for a place on
// the map hear where it is now.
func (m *Mind) onFix(ctx context.Context, o obs.Observation) error {
	if m.Moments == nil {
		return nil
	}
	var d struct {
		Lat *float64 `json:"lat"`
		Lon *float64 `json:"lon"`
		Acc float64  `json:"acc"`
	}
	if err := json.Unmarshal(o.Data, &d); err != nil || d.Lat == nil || d.Lon == nil {
		return nil
	}
	at := o.OccurredAt
	if at.IsZero() {
		at = m.Opts.Now()
	}
	return m.Moments.Fixed(ctx, moments.Owner, moments.Fix{Lat: *d.Lat, Lon: *d.Lon, Acc: d.Acc, At: at})
}

// rainOut is leaving home on a day it will still rain: when, and the
// umbrella - said at the door, once a day. Not at night, when a leaving is
// more likely the phone's position drifting than the operator going out.
func (m *Mind) rainOut(ctx context.Context, at time.Time) error {
	loc := m.Opts.Loc
	now := at.In(loc)
	if m.Opts.Weather == nil || m.Opts.Now().Sub(at) > 30*time.Minute || now.Hour() < 6 || now.Hour() >= 22 {
		return nil
	}
	today := now.Format("2006-01-02")
	day, hours, ok := m.Opts.Weather.At(ctx, "", "", today)
	if !ok || !day.Wet() {
		return nil
	}
	words, ok := rainAhead(day, hours, now, loc)
	if !ok {
		return nil
	}
	take := "우산 챙기세요"
	if day.Snowy() {
		take = "눈길 조심하세요"
	}
	_, err := m.Gate.Submit(ctx, model.Intent{
		Skill: "moments", Kind: model.Info, Topic: "rain.out:" + today,
		Title: words + " · " + take, Body: "오늘 집 날씨: " + day.Line(),
		Reason: "비 오는 날 집을 나섬", Confidence: 1, Urgent: true, Moment: "rain",
	})
	return err
}

// rainAhead is the rain still to come today from now, said short: "15시부터
// 비", "15~18시 비", "지금 비 · 18시까지". ok is false when none is.
func rainAhead(day weather.Day, hours []weather.Hour, now time.Time, loc *time.Location) (string, bool) {
	from, ok := weather.From(hours, now)
	if !ok {
		return "", false
	}
	from, today := from.In(loc), now.Format("2006-01-02")
	if from.Format("2006-01-02") != today {
		return "", false
	}
	end := from // the last wet hour of the run, today
	for _, h := range hours {
		t := h.At.In(loc)
		if !t.After(end) {
			continue
		}
		if t.Sub(end) > time.Hour || h.Prob < 50 || t.Format("2006-01-02") != today {
			break
		}
		end = t
	}
	what := "비"
	if day.Snowy() {
		what = "눈"
	}
	until := fmt.Sprintf("%d시까지", end.Hour()+1)
	if end.Hour() == 23 {
		until = "밤까지"
	}
	switch {
	case from.Hour() <= now.Hour():
		return "지금 " + what + " · " + until, true
	case end.Hour() == 23:
		return fmt.Sprintf("%d시부터 %s", from.Hour(), what), true
	}
	return fmt.Sprintf("%d~%d시 %s", from.Hour(), end.Hour()+1, what), true
}

// outOfBand is the rebalance's title when more than one bucket is out.
const outOfBand = "자산 비중이 목표 범위를 벗어났어요"

// band is a bucket of the portfolio out of band. It is brought up in the
// evening at home - after work, with time to look at it - and not at once:
// a reminder waits for arriving home from 17:00 (being home then counts),
// at 22:00 at the latest. Another bucket out before then joins it. The
// question itself is made at that moment, with that moment's orders
// (portfolioBand). Without reminders, it is raised now.
func (m *Mind) band(ctx context.Context, o obs.Observation) error {
	if m.Moments == nil {
		_, err := m.portfolioBand(ctx, o, false)
		return err
	}
	var n struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	_ = json.Unmarshal(o.Notify, &n)
	r := moments.Reminder{
		Source: moments.Rebalance, Topic: "rebalance", Ref: o.Key,
		Text: firstNonEmpty(n.Title, outOfBand), Body: bucketLine(n.Title, n.Body),
		Place: "home", Move: moments.Arrive, URL: "/assets?focus=allocation",
	}
	waiting, err := m.Moments.List(ctx, false)
	if err != nil {
		return err
	}
	for _, w := range waiting {
		if w.Topic != r.Topic {
			continue
		}
		lines := []string{}
		label, _, _ := strings.Cut(r.Body, ":")
		for _, l := range strings.Split(w.Body, "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, label+":") {
				lines = append(lines, l)
			}
		}
		if len(lines) > 0 {
			r.Text, r.Body = outOfBand, strings.Join(append(lines, r.Body), "\n")
		}
	}
	from, latest := evening(m.Opts.Now(), m.Opts.Loc)
	r.After, r.At = &from, &latest
	_, err = m.Moments.Set(ctx, r)
	return err
}

// bucketLine is one bucket out of band in a line: "코인: 지금 30% · 목표 20%".
func bucketLine(title, body string) string {
	label := strings.TrimSuffix(strings.TrimSpace(title), " 비중이 범위를 벗어났어요")
	if label == "" {
		return strings.TrimSpace(body)
	}
	return strings.TrimSpace(label + ": " + body)
}

// evening is when a rebalance is brought up: from 17:00 (or now, if later),
// 22:00 at the latest; past 22:00, the next evening.
func evening(now time.Time, loc *time.Location) (from, latest time.Time) {
	t := now.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	if t.Hour() >= 22 {
		day = day.AddDate(0, 0, 1)
	}
	from, latest = day.Add(17*time.Hour), day.Add(22*time.Hour)
	if now.After(from) {
		from = now
	}
	return from, latest
}

// reminderLines is the reminders waiting, for the conversation's situation:
// it answers from what is stored, not from what it meant to store.
func (m *Mind) reminderLines(ctx context.Context) string {
	return m.reminderLinesOf(ctx, moments.Owner)
}

// reminderLinesOf is reminderLines of who's reminders.
func (m *Mind) reminderLinesOf(ctx context.Context, who string) string {
	if m.Moments == nil {
		return ""
	}
	rs, err := m.Moments.ListOf(ctx, who, false)
	if err != nil {
		m.Opts.Logger.Warn("기다리는 알림을 읽지 못했습니다", "err", err)
		return ""
	}
	var asked, held []string
	for _, r := range rs {
		if r.Source == moments.Asked || r.Source == moments.Console {
			asked = append(asked, r.Line(m.Opts.Loc))
		} else if r.Source == moments.Lists {
			held = append(held, r.When(m.Opts.Loc)+" · "+r.Text+" (체크리스트 "+r.Checklist+" 의 남은 것, id 는 답에 쓰지 않는다)")
		} else {
			held = append(held, r.When(m.Opts.Loc)+" · "+r.Text)
		}
	}
	var b strings.Builder
	if len(asked) > 0 {
		if len(asked) > 8 {
			asked = append(asked[:8], fmt.Sprintf("… 그 밖에 %d개", len(asked)-8))
		}
		b.WriteString("- 맡아 둔 알림(그때 폰 푸시. 괄호 속 id 는 고칠 때 도구에 넣는 값이라 답에 쓰지 않는다): " + strings.Join(asked, " / ") + "\n")
	}
	if len(held) > 0 {
		b.WriteString("- 자비스가 그때로 미뤄 둔 알림: " + strings.Join(held, " / ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
