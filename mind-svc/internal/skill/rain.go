package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

// Rain looks each morning at the plans out of doors in the next two days.
// One the forecast rains (or snows) on is brought up while there is still
// time to pack for it or move it: as a card that moves it to a dry day like
// it, when it is on the calendar and the forecast has one; as a line
// otherwise. Moving a trip for the weather is asked every time (the gate
// keeps the "rain" context fixed), however often it was approved.
type Rain struct{}

func (Rain) Name() string                                                        { return "rain" }
func (Rain) Wants(model.Entity) bool                                             { return false }
func (Rain) OnChange(context.Context, View, model.Entity, []model.Change) Result { return Result{} }
func (Rain) OnTimer(context.Context, View, model.Entity, model.Timer) Result     { return Result{} }

// RainAhead is how many days ahead a plan is looked at: two, so there is a
// day left to do something about it.
const RainAhead = 2

// rainSearch is how far ahead of today a dry day to move to is looked for:
// past it, a forecast says too little to move a trip by.
const rainSearch = 10

func (r Rain) OnTick(ctx context.Context, v View, tick string) Result {
	if tick != "morning" {
		return Result{}
	}
	loc := v.Loc()
	now := v.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	plans, err := v.Entities(ctx, model.KindPlan, "", true)
	if err != nil {
		return Result{}
	}
	var out Result
	for _, p := range plans {
		st := p.State
		if st.Status != model.Confirmed || st.Owner == "partner" || !Outdoors(st) {
			continue
		}
		start, err := time.ParseInLocation("2006-01-02", st.Date, loc)
		if err != nil {
			continue
		}
		if ahead := int(start.Sub(today).Round(24*time.Hour) / (24 * time.Hour)); ahead < 1 || ahead > RainAhead {
			continue
		}
		worst, found := weather.Day{}, false
		for _, d := range spanDays(st, loc) {
			w, _, ok := v.Weather(ctx, st.Area, st.Place, d)
			if ok && w.Wet() && (!found || w.Prob > worst.Prob || w.Prob == worst.Prob && w.Precip > worst.Precip) {
				worst, found = w, true
			}
		}
		if found {
			out.Intents = append(out.Intents, r.intent(ctx, v, p, worst, today))
		}
	}
	return out
}

// intent is the card (or the line) about one plan.
func (Rain) intent(ctx context.Context, v View, p model.Entity, wet weather.Day, today time.Time) model.Intent {
	st, loc := p.State, v.Loc()
	where := firstNonEmpty(st.Place, st.Area)
	day := Day(st, loc)
	head := day + " " + st.Title
	if where != "" {
		head += " · " + where
	}
	pack := "우비·타프를 챙기세요"
	if wet.Snowy() {
		pack = "방한·미끄럼 대비를 챙기세요"
	}
	in := model.Intent{
		EntityID: p.ID, Topic: "rain:" + p.ID + ":" + st.Date, Context: "rain",
		Reason: "야외 일정에 " + strings.SplitN(wet.Fall(), " ", 2)[0] + " 예보", Confidence: 0.8, Value: 1, Reversible: true,
		Link: CalendarLink(st),
	}
	// A day to move to is looked for only when there is an entry to move.
	var alt time.Time
	ok := false
	if st.CalendarID != "" {
		alt, ok = dryDay(ctx, v, st, today)
	}
	if !ok {
		in.Kind, in.Confidence, in.Urgent = "info", 1, true
		in.Title = head + "에 " + wet.Fall() + " 예보"
		in.Body = wet.Line() + ". " + pack + "."
		if st.CalendarID != "" {
			in.Body += " 열흘 안에는 맑은 " + kindOfDay(st, loc) + "이 없어요."
		}
		return in
	}
	ev := event(st, nil)
	shift := alt.Sub(mustDate(st.Date, loc))
	ev.Date = alt.Format("2006-01-02")
	if st.EndDate != "" {
		ev.EndDate = mustDate(st.EndDate, loc).Add(shift).Format("2006-01-02")
	}
	payload, _ := json.Marshal(model.CalendarAction{EventID: st.CalendarID, Event: ev})
	moved := st
	moved.Date, moved.EndDate, moved.Start = ev.Date, ev.EndDate, ""
	in.Kind, in.Payload = "calendar.update", payload
	in.Title = fmt.Sprintf("'%s'를 %s로 옮길까요?", st.Title, Day(moved, loc))
	in.Body = fmt.Sprintf("%s · %s 예보\n%s엔 비·눈 예보가 없어요(예보는 바뀔 수 있어요). 그대로 간다면 %s.", head, wet.Fall(), Day(moved, loc), pack)
	in.Fields = []model.Field{{Key: "date", Label: "날짜", Kind: "text", Value: ev.Date, Required: true, Hint: "YYYY-MM-DD"}}
	return in
}

// dryDay is the first day, after the plan's and within rainSearch of today,
// of the same sort (the same weekday for a trip of several days, a weekend
// day for a weekend plan, a weekday for a weekday one) on which every day
// the plan would take is forecast dry where it is.
func dryDay(ctx context.Context, v View, st model.State, today time.Time) (time.Time, bool) {
	loc := v.Loc()
	start := mustDate(st.Date, loc)
	days := spanDays(st, loc)
	for k := 1; ; k++ {
		cand := start.AddDate(0, 0, k)
		if cand.Sub(today) > rainSearch*24*time.Hour {
			return time.Time{}, false
		}
		if len(days) > 1 && cand.Weekday() != start.Weekday() || len(days) == 1 && weekend(cand) != weekend(start) {
			continue
		}
		dry := true
		for i := range days {
			w, _, ok := v.Weather(ctx, st.Area, st.Place, cand.AddDate(0, 0, i).Format("2006-01-02"))
			if !ok || w.Wet() {
				dry = false
				break
			}
		}
		if dry {
			return cand, true
		}
	}
}

func kindOfDay(st model.State, loc *time.Location) string {
	if weekend(mustDate(st.Date, loc)) {
		return "주말"
	}
	return "평일"
}

func weekend(t time.Time) bool { return t.Weekday() == time.Saturday || t.Weekday() == time.Sunday }

// spanDays is the days a plan takes, first to last (a week at most).
func spanDays(st model.State, loc *time.Location) []string {
	start, err := time.ParseInLocation("2006-01-02", st.Date, loc)
	if err != nil {
		return nil
	}
	out := []string{st.Date}
	end, err := time.ParseInLocation("2006-01-02", st.EndDate, loc)
	for d := start.AddDate(0, 0, 1); err == nil && !d.After(end) && len(out) < 7; d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}

func mustDate(s string, loc *time.Location) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", s, loc)
	return t
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// Outdoors is whether a plan happens under the sky - by its kind (a camping
// trip, a trip), its kind of place (a park, a beach), or a word in its
// title (피크닉, 등산, 야구).
func Outdoors(st model.State) bool {
	if st.Type == "캠핑" || st.Type == "여행" {
		return true
	}
	if slices.Contains(outdoorPlaces, st.PlaceKind) || strings.Contains(st.PlaceKind, "캠핑") {
		return true
	}
	for _, w := range outdoorWords {
		if strings.Contains(st.Title, w) {
			return true
		}
	}
	return false
}

var outdoorPlaces = []string{"공원", "산", "해변", "바다", "해수욕장", "계곡", "강", "섬", "숲", "수목원", "놀이공원", "테마파크", "야구장", "축구장", "경기장", "골프장", "글램핑장", "야외"}

var outdoorWords = []string{"캠핑", "글램핑", "피크닉", "소풍", "등산", "산행", "트레킹", "하이킹", "러닝", "마라톤", "자전거", "라이딩",
	"골프", "라운딩", "야구", "축구", "테니스", "바베큐", "바비큐", "낚시", "물놀이", "해수욕", "벚꽃", "단풍", "꽃구경", "불꽃", "페스티벌", "축제", "야외", "한강"}
