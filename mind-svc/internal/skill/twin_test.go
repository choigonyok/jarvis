package skill

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

type view struct {
	plans []model.Entity
	// sky is the weather by "place date" ("" for home); now, when set, the
	// clock.
	sky   map[string]weather.Day
	hours []weather.Hour
	now   time.Time
}

func (v view) Entity(_ context.Context, id string) (model.Entity, error) {
	for _, p := range v.plans {
		if p.ID == id {
			return p, nil
		}
	}
	return model.Entity{}, nil
}
func (v view) Entities(context.Context, string, string, bool) ([]model.Entity, error) {
	return v.plans, nil
}
func (view) IntentsFor(context.Context, string) ([]model.Intent, error) { return nil, nil }
func (v view) Now() time.Time {
	if !v.now.IsZero() {
		return v.now
	}
	return time.Date(2026, 10, 17, 11, 57, 0, 0, seoul)
}
func (view) Loc() *time.Location                                 { return seoul }
func (view) Recall(context.Context, []string) []string           { return nil }
func (view) Links(context.Context, string) ([]model.Link, error) { return nil, nil }
func (view) Occurrence(context.Context, string) (model.Occurrence, error) {
	return model.Occurrence{}, nil
}
func (view) Span(context.Context, model.Entity) (time.Time, time.Time, bool) {
	return time.Time{}, time.Time{}, false
}
func (view) Travel(context.Context, model.State, model.State) (int, string, bool) {
	return 0, "", false
}
func (v view) Weather(_ context.Context, area, place, date string) (weather.Day, []weather.Hour, bool) {
	where := area
	if where == "" {
		where = place
	}
	d, ok := v.sky[where+" "+date]
	return d, v.hours, ok
}

// The incident of 2026-10-17: the assistant put "기타 합주 (은주)" 15:00 on the
// calendar while the plan heard in conversation was "은주와 합주" 15:00. That
// is one event - tie them, and say neither "not on the calendar" nor "they
// collide".
func TestAPlanAndItsCalendarEntryAreOneEvent(t *testing.T) {
	talked := model.Entity{ID: "p1", Kind: model.KindPlan, Room: "kakao:7", State: model.State{
		Title: "은주와 합주", Status: model.Confirmed, Date: "2026-10-17", Start: "15:00", People: []string{"은주"}}}
	entry := model.Entity{ID: "p2", Kind: model.KindPlan, Room: "calendar", State: model.State{
		Title: "기타 합주 (은주)", Status: model.Confirmed, Date: "2026-10-17", Start: "15:00", End: "16:00", CalendarID: "c9"}}
	v := view{plans: []model.Entity{talked, entry}}
	changes := []model.Change{{Before: &model.State{Title: "은주와 합주", Status: model.Confirmed, Date: "2026-10-17", Start: "16:00"}, After: talked.State}}

	r := Schedule{}.OnChange(context.Background(), v, talked, changes)
	if len(r.Intents) != 0 {
		t.Fatalf("캘린더에 있는 일정을 '없다'고 하면 안 됩니다: %+v", r.Intents)
	}
	if len(r.Updates) != 1 {
		t.Fatalf("캘린더 일정에 이어야 합니다: %+v", r.Updates)
	}
	st := talked.State
	if !r.Updates[0].Set(&st) || st.CalendarID != "c9" {
		t.Fatalf("이은 결과: %+v", st)
	}

	if r := (Protect{}).OnChange(context.Background(), v, talked, changes); len(r.Intents) != 0 {
		t.Fatalf("같은 일정끼리는 겹치지 않습니다: %+v", r.Intents)
	}

	// Something else at that time is still a collision.
	other := model.Entity{ID: "p3", Kind: model.KindPlan, Room: "calendar", State: model.State{
		Title: "치과", Status: model.Confirmed, Date: "2026-10-17", Start: "15:00", End: "16:00", CalendarID: "c10"}}
	v.plans = append(v.plans, other)
	if r := (Protect{}).OnChange(context.Background(), v, talked, changes); len(r.Intents) != 1 {
		t.Fatalf("다른 일과는 겹친다고 해야 합니다: %+v", r.Intents)
	}
}

// The calendar's own copy dropped as a duplicate (or an entry deleted on the
// calendar) is not "called off in conversation": the entry stays.
func TestACopyDroppedByTheCalendarIsNotDeleted(t *testing.T) {
	cp := model.Entity{ID: "p2", Kind: model.KindPlan, Room: "calendar", State: model.State{
		Title: "기타 합주 (은주)", Status: model.Cancelled, Date: "2026-10-17", Start: "15:00", CalendarID: "c9"}}
	before := cp.State
	before.Status = model.Confirmed
	byCal := []model.Change{{Before: &before, After: cp.State, Actor: "sensor:calendar"}}
	if r := (Schedule{}).OnChange(context.Background(), view{plans: []model.Entity{cp}}, cp, byCal); len(r.Intents) != 0 {
		t.Fatalf("캘린더가 정리한 복사본으로 캘린더 일정을 지우면 안 됩니다: %+v", r.Intents)
	}
	inTalk := []model.Change{{Before: &before, After: cp.State, Actor: "interpret"}}
	if r := (Schedule{}).OnChange(context.Background(), view{plans: []model.Entity{cp}}, cp, inTalk); len(r.Intents) != 1 || r.Intents[0].Kind != "calendar.delete" {
		t.Fatalf("대화에서 취소된 일정은 지우자고 해야 합니다: %+v", r.Intents)
	}
}

// Tied to its entry, a plan's open "add it to the calendar" goes.
func TestLinkingWithdrawsTheAddQuestion(t *testing.T) {
	linked := model.Entity{ID: "p1", Kind: model.KindPlan, Room: "kakao:7", State: model.State{
		Title: "은주와 합주", Status: model.Confirmed, Date: "2026-10-17", Start: "15:00", CalendarID: "c9"}}
	before := linked.State
	before.CalendarID = ""
	r := (Schedule{}).OnChange(context.Background(), view{plans: []model.Entity{linked}}, linked,
		[]model.Change{{Before: &before, After: linked.State, Actor: "sensor:calendar"}})
	if !r.Withdraw {
		t.Fatalf("이어졌으면 '추가할까요'를 거둬야 합니다: %+v", r)
	}
}

// 2026-10-26: "전날에 플랜 바꾸게 일정 추가해 줘" in the conversation with the
// assistant was read as an all-day plan while the assistant's own card put
// the same title at 20:00. Once that entry is on the calendar, the plan is
// it - tied, with the entry's time - and not "missing from the calendar".
func TestAPlanToldWithoutATimeIsItsEntry(t *testing.T) {
	talked := model.Entity{ID: "p1", Kind: model.KindPlan, Room: model.OwnerRoom, State: model.State{
		Title: "Netflix 플랜 변경·해지", Status: model.Confirmed, Date: "2026-11-15"}}
	entry := model.Entity{ID: "p2", Kind: model.KindPlan, Room: "calendar", State: model.State{
		Title: "Netflix 플랜 변경·해지", Status: model.Confirmed, Date: "2026-11-15", Start: "20:00", End: "20:30", CalendarID: "c7"}}
	v := view{plans: []model.Entity{talked, entry}}
	changes := []model.Change{{After: talked.State, Actor: "interpreter"}}

	r := Schedule{}.OnChange(context.Background(), v, talked, changes)
	if len(r.Intents) != 0 || len(r.Updates) != 1 || !r.Withdraw {
		t.Fatalf("캘린더의 같은 일정에 이어야 합니다: %+v", r)
	}
	st := talked.State
	if !r.Updates[0].Set(&st) || st.CalendarID != "c7" || st.Start != "20:00" || st.End != "20:30" {
		t.Fatalf("이은 결과(시각은 캘린더 것): %+v", st)
	}
}

// intents is a view that remembers what was asked about its plans.
type intents struct {
	view
	asked []model.Intent
}

func (v intents) IntentsFor(context.Context, string) ([]model.Intent, error) { return v.asked, nil }

// Asked in the conversation already - the assistant's own card was up, so
// this one was not shown - is asked no more than turned down: the same
// entry is not asked about again when the plan changes in another way.
func TestNotAskedAgainAfterTheConversationAsked(t *testing.T) {
	plan := model.Entity{ID: "p1", Kind: model.KindPlan, Room: model.OwnerRoom, State: model.State{
		Title: "Netflix 플랜 변경·해지", Status: model.Confirmed, Date: "2026-11-15"}}
	changes := []model.Change{{After: plan.State, Actor: "interpreter"}}
	first := Schedule{}.OnChange(context.Background(), view{plans: []model.Entity{plan}}, plan, changes)
	if len(first.Intents) != 1 || first.Intents[0].Kind != "calendar.create" {
		t.Fatalf("캘린더에 없으면 넣자고 해야 합니다: %+v", first.Intents)
	}
	covered := first.Intents[0]
	covered.Status, covered.DecidedBy = model.Superseded, model.ByConversation
	v := intents{view: view{plans: []model.Entity{plan}}, asked: []model.Intent{covered}}

	plan.State.People = []string{"지수"}
	if r := (Schedule{}).OnChange(context.Background(), v, plan, changes); len(r.Intents) != 0 {
		t.Fatalf("대화에서 이미 물은 것을 또 물으면 안 됩니다: %+v", r.Intents)
	}
	// Superseded by a newer judgement is not the same: asked again.
	covered.DecidedBy = ""
	v.asked = []model.Intent{covered}
	if r := (Schedule{}).OnChange(context.Background(), v, plan, changes); len(r.Intents) != 1 {
		t.Fatalf("다른 이유로 거둔 것은 다시 물어야 합니다: %+v", r.Intents)
	}
	// A plan that now differs is a new question.
	plan.State.Date = "2026-11-14"
	v.asked = []model.Intent{{Kind: covered.Kind, Payload: covered.Payload, Status: model.Superseded, DecidedBy: model.ByConversation}}
	if r := (Schedule{}).OnChange(context.Background(), v, plan, changes); len(r.Intents) != 1 {
		t.Fatalf("날짜가 바뀌면 새 질문입니다: %+v", r.Intents)
	}
}
