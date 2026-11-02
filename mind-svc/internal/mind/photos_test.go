package mind

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

func shot(id string, at time.Time) model.Occurrence {
	return model.Occurrence{ID: "photo:" + id, Kind: "photo", At: at, Count: 1, Area: "Gapyeong", Ref: "immich:" + id}
}

// Photos in no plan's hours are asked about once they have stopped, with the
// plans near them to choose from; choosing ties them for sure and tells memory.
func TestPhotosInNoPlanAreAskedAbout(t *testing.T) {
	ctx := context.Background()
	m, st, _ := chargeMind(t, "")
	seoul := m.Opts.Loc
	now := m.Opts.Now().In(seoul) // 10/31 09:00
	plan, err := m.World.Create(ctx, model.KindPlan, "r", model.State{Title: "지수와 캠핑", Status: model.Confirmed, Date: "2026-10-30", EndDate: "2026-10-31"}, world.Meta{Actor: "interpreter"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.World.Create(ctx, model.KindPlan, "r", model.State{Title: "반려한 일정", Status: model.Confirmed, Date: "2026-10-30", Declined: "틀렸어"}, world.Meta{Actor: "interpreter"}); err != nil {
		t.Fatal(err)
	}
	base := now.Add(-20 * time.Hour)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		if err := st.PutOccurrence(ctx, shot(id, base.Add(time.Duration(i)*10*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.askPhotos(ctx); err != nil {
		t.Fatal(err)
	}
	asked, _ := st.Intents(ctx, 10)
	if len(asked) != 1 || asked[0].Kind != act.KindPhotoPlace || !asked[0].Card {
		t.Fatalf("%+v", asked)
	}
	in := asked[0]
	if !strings.Contains(in.Title, "사진 5장") || len(in.Photos) != photosShown || len(in.Fields) != 2 {
		t.Fatalf("%+v", in)
	}
	opts := in.Fields[0].Options
	if len(opts) != 2 || !strings.Contains(opts[0], "지수와 캠핑") || opts[1] != noPlan {
		t.Fatalf("일정과 '일정 아님', 반려한 일정은 빼고: %v", opts)
	}
	// One a day, and not the same batch twice.
	if err := m.askPhotos(ctx); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.Intents(ctx, 10); len(again) != 1 {
		t.Fatalf("%+v", again)
	}
	got, err := m.Gate.ApproveWith(ctx, in.ID, map[string]string{"plan": opts[0], "note": "장작 사진"})
	if err != nil || got.Status != model.Executed {
		t.Fatalf("%+v %v", got, err)
	}
	links, _ := st.LinksOf(ctx, plan.ID)
	if len(links) != 5 || links[0].State != model.Confirmed || links[0].By != "user" {
		t.Fatalf("%+v", links)
	}
	facts, _ := st.Facts(ctx, 10)
	if len(facts) != 1 || !strings.Contains(facts[0].Text, "지수와 캠핑 사진 5장: 장작 사진") || strings.Count(facts[0].Text, "10/30") != 1 {
		t.Fatalf("날짜는 한 번만: %+v", facts)
	}
	// Tied now, they are not asked about again.
	batches, err := m.unplaced(ctx, now)
	if err != nil || len(batches) != 0 {
		t.Fatalf("%+v %v", batches, err)
	}
	// And they come back when the place is looked for.
	ws, err := m.PhotoWindows(ctx, "지수와 캠핑")
	if err != nil || len(ws) != 1 || ws[0].From != "2026-10-30" || ws[0].To != "2026-10-31" || len(ws[0].Photos) != 5 {
		t.Fatalf("%+v %v", ws, err)
	}
}

// A card shows six plans, nearest the photos first: 14:53's photos were asked
// about with the day's other plans and not the rehearsal at 15:00 (2026-11-02).
func TestTheNearestPlansAreOffered(t *testing.T) {
	ctx := context.Background()
	m, st, _ := chargeMind(t, "")
	seoul := m.Opts.Loc
	base := time.Date(2026, 10, 30, 14, 53, 0, 0, seoul)
	for _, p := range []struct{ title, start string }{{"아침 약속", "07:00"}, {"늦은 약속", "23:30"}, {"밤 약속", "22:00"}, {"새벽 약속", "05:00"},
		{"저녁 약속", "20:00"}, {"이른 약속", "08:00"}, {"합주", "15:00"}, {"시간 없는 약속", ""}} {
		if _, err := m.World.Create(ctx, model.KindPlan, "r", model.State{Title: p.title, Status: model.Confirmed, Date: "2026-10-30", Start: p.start}, world.Meta{}); err != nil {
			t.Fatal(err)
		}
	}
	batch := []model.Occurrence{shot("a", base), shot("b", base.Add(time.Minute)), shot("c", base.Add(2*time.Minute))}
	for _, o := range batch {
		if err := st.PutOccurrence(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	in, ok, err := m.photoQuestion(ctx, batch)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	opts := in.Fields[0].Options
	if len(opts) != 7 || !strings.Contains(opts[0], "합주") || opts[6] != noPlan {
		t.Fatalf("가까운 일정부터 여섯, 끝에 '일정 아님': %v", opts)
	}
	if got := farFrom(model.State{Date: "2026-10-29", EndDate: "2026-10-31"}, base, seoul); got != 0 {
		t.Fatalf("이어지는 일정 안의 사진은 그 일정이 가장 가깝습니다: %v", got)
	}
}

// A few photos are not worth a question; a burst still going waits; with no plan
// near it takes more, and it asks what they were. Nights are left alone.
func TestWhenPhotosAreNotAskedAbout(t *testing.T) {
	ctx := context.Background()
	m, st, _ := chargeMind(t, "")
	now := m.Opts.Now().In(m.Opts.Loc)
	put := func(id string, ago time.Duration) {
		t.Helper()
		if err := st.PutOccurrence(ctx, shot(id, now.Add(-ago))); err != nil {
			t.Fatal(err)
		}
	}
	put("x", 30*time.Hour)
	put("y", 29*time.Hour)
	if err := m.askPhotos(ctx); err != nil {
		t.Fatal(err)
	}
	if in, _ := st.Intents(ctx, 10); len(in) != 0 {
		t.Fatalf("두 장: %+v", in)
	}
	for i, id := range []string{"a", "b", "c", "d", "e", "f"} {
		put(id, 20*time.Minute-time.Duration(i)*time.Minute)
	}
	batches, _ := m.unplaced(ctx, now)
	if len(batches) != 1 {
		t.Fatalf("찍는 중인 묶음은 기다립니다: %+v", batches)
	}
	if err := m.askPhotos(ctx); err != nil {
		t.Fatal(err)
	}
	if in, _ := st.Intents(ctx, 10); len(in) != 0 {
		t.Fatalf("%+v", in)
	}
	batch, ok, err := m.photoQuestion(ctx, []model.Occurrence{shot("1", now), shot("2", now), shot("3", now), shot("4", now), shot("5", now), shot("6", now)})
	if err != nil || !ok || len(batch.Fields) != 1 || !batch.Fields[0].Required || !strings.Contains(batch.Title, "뭐 찍은") {
		t.Fatalf("%+v %v %v", batch, ok, err)
	}
	var a photoAsk
	if json.Unmarshal(batch.Payload, &a) != nil || len(a.Photos) != 6 {
		t.Fatalf("%s", batch.Payload)
	}
}

func TestSpread(t *testing.T) {
	if got := spread([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, 4); strings.Join(got, "") != "aceg" {
		t.Fatal(got)
	}
	if got := spread([]string{"a"}, 4); len(got) != 1 {
		t.Fatal(got)
	}
}

// A card to add what is over, and a question left three days, are taken back.
func TestOldQuestionsAreTakenBack(t *testing.T) {
	ctx := context.Background()
	m, st, _ := chargeMind(t, "")
	now := m.Opts.Now()
	old, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "지수와 커피", Date: "2026-10-30"}})
	ahead, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "지수와 저녁", Date: "2026-11-30"}})
	for _, in := range []model.Intent{
		{ID: "past", Kind: "calendar.create", Status: model.Pending, Payload: old, CreatedAt: now.Add(-time.Hour)},
		{ID: "ahead", Kind: "calendar.create", Status: model.Pending, Payload: ahead, CreatedAt: now.Add(-time.Hour)},
		{ID: "photos", Kind: act.KindPhotoPlace, Status: model.Pending, CreatedAt: now.Add(-reviewExpires)},
	} {
		if err := st.PutIntent(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.expireQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"past": model.Superseded, "ahead": model.Pending, "photos": model.Superseded} {
		if got, _ := st.Intent(ctx, id); got.Status != want {
			t.Fatalf("%s: %s", id, got.Status)
		}
	}
}
