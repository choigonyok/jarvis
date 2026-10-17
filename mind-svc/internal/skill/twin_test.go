package skill

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

type view struct{ plans []model.Entity }

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
func (view) Now() time.Time                                              { return time.Date(2026, 10, 17, 11, 57, 0, 0, seoul) }
func (view) Loc() *time.Location                                         { return seoul }
func (view) Recall(context.Context, []string) []string                   { return nil }
func (view) Links(context.Context, string) ([]model.Link, error)         { return nil, nil }
func (view) Occurrence(context.Context, string) (model.Occurrence, error) {
	return model.Occurrence{}, nil
}
func (view) Span(context.Context, model.Entity) (time.Time, time.Time, bool) {
	return time.Time{}, time.Time{}, false
}
func (view) Travel(context.Context, model.State, model.State) (int, string, bool) { return 0, "", false }

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

