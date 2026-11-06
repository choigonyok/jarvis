package predict

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
)

// A reflection's rate note is held to what it moved: each workout
// prediction it moved is scored against what the comparable days said
// before it (Prior). One whose moves keep missing stops being applied; one
// whose moves came true moves further. The workout predictor's own record
// is kept as it was.
func TestRateNotesAreHeldToWhatTheyMoved(t *testing.T) {
	now := at(2026, 11, 7, 3, 0)
	p, st := engine(&now)
	ctx := context.Background()
	note := func(id string, conf float64) {
		_ = st.PutNote(ctx, model.Note{ID: id, Text: id, Status: model.Hypothesis, Version: 1, Confidence: conf, Scope: "운동",
			Rule: &model.Rule{Kind: "rate", Target: "workout", Feature: "weekday", Arg: "월", Rate: 0.1}})
	}
	note("hurts", 0.9)
	note("helps", 0.4)
	prior := 0.6
	scored := func(id string, i int, happened bool) {
		day := at(2026, 10, 1, 0, 0).AddDate(0, 0, i)
		due := day.Add(24*time.Hour - time.Minute)
		_ = st.PutPrediction(ctx, model.Prediction{ID: fmt.Sprintf("%s%d", id, i), Target: fmt.Sprintf("workout:%s-%d", id, i), Prob: 0.2, Prior: &prior, Base: 0.5,
			Due: due, CreatedAt: day, ScoredAt: &due, Outcome: &happened, Basis: []string{id}})
	}
	for i := range 12 {
		scored("hurts", i, true) // moved to 20%, and the workout came
		scored("helps", i, false)
	}
	tracks, err := p.Tracks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tr := tracks["hurts"]; tr.N != 12 || tr.Skill() >= 0 {
		t.Fatalf("옮겨서 빗나간 기록: %+v %.2f", tr, tr.Skill())
	}
	if tr := tracks["workout"]; tr.N != 24 {
		t.Fatalf("운동 예측기 자신의 기록은 그대로: %+v", tr)
	}
	if err := p.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	h, _ := st.Note(ctx, "hurts")
	if !h.Rule.Off || h.Status != model.Candidate || !strings.Contains(h.Reason, "더는 쓰지 않음") || h.Track == nil || h.Track.N != 12 {
		t.Fatalf("옮길수록 빗나간 조건은 더 쓰지 않음: %+v %+v", h, h.Rule)
	}
	g, _ := st.Note(ctx, "helps")
	if g.Rule.Off || g.Status != model.Hypothesis || g.Confidence <= 0.4 || !strings.HasPrefix(g.Reason, "예측 기록: 이 조건으로 옮긴 예측 12번 중 12번 맞음") {
		t.Fatalf("맞아 온 조건은 더 세게: %+v", g)
	}
	// What is no longer applied moves nothing.
	monday := at(2026, 11, 9, 0, 0)
	for i := range 28 {
		d := monday.AddDate(0, 0, -28+i)
		if i%2 == 0 {
			_ = st.PutOccurrence(ctx, model.Occurrence{ID: fmt.Sprint("w", i), Kind: "workout", At: d.Add(19 * time.Hour), Title: "하체"})
		}
	}
	pr, ok, err := p.Workout(ctx, monday)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(pr.Basis) != 1 || pr.Basis[0] != "helps" || pr.Prior == nil {
		t.Fatalf("멈춘 조건은 예측을 옮기지 않음: %+v", pr)
	}
}

// What reached mind after its prediction was scored a miss meets it, and the
// prediction is scored again; a rule stopped for those misses predicts again.
func TestALateOccurrenceIsScoredAgain(t *testing.T) {
	now := at(2026, 10, 28, 7, 0)
	p, st := engine(&now)
	ctx := context.Background()
	since := at(2026, 9, 1, 0, 0)
	_ = st.PutNote(ctx, model.Note{ID: "rule", Text: "배달", Status: model.Candidate, Version: 3, Confidence: 0.3, UpdatedAt: since,
		Reason: reasonFailing + ": 예측 10번 중 0번 맞음",
		Rule:   &model.Rule{Kind: Daily, Key: Delivery, Rates: []float64{0.8, 0.8, 0.8, 0.8, 0.8, 0.8, 0.8}, Base: 0.4, Since: &since, Off: true}})
	var ids []string
	for i := range 10 {
		day := at(2026, 10, 19, 0, 0).AddDate(0, 0, i)
		now = day.Add(7 * time.Hour)
		pr, err := p.Put(ctx, model.Prediction{Target: "day:" + Delivery + ":" + day.Format(dateFmt), Prob: 0.8, Base: 0.4, Key: Delivery,
			From: &day, Due: day.Add(24*time.Hour - time.Minute), Basis: []string{"rule"}}, Delivery)
		if err != nil {
			t.Fatal(err)
		}
		now = pr.Due.Add(time.Minute)
		if pr, err = p.ScoreOne(ctx, pr.ID); err != nil || *pr.Outcome || !pr.Surprise {
			t.Fatalf("그날엔 빗나감: %+v %v", pr, err)
		}
		ids = append(ids, pr.ID)
	}
	// KakaoTalk signs in again on 10/30 and passes on what it held. One from
	// before every window meets nothing.
	now = at(2026, 10, 30, 3, 7)
	first, _ := st.Prediction(ctx, ids[0])
	if met, _ := p.Match(ctx, pay("before", "주식회사 우아한형제", "식비", first.From.Add(-time.Hour))); len(met) != 0 {
		t.Fatalf("창 밖의 결제는 아무 예측도 맞히지 않음: %v", met)
	}
	if pr, _ := st.Prediction(ctx, ids[0]); *pr.Outcome || pr.RescoredAt != nil {
		t.Fatalf("%+v", pr)
	}
	for i, id := range ids {
		pr, _ := st.Prediction(ctx, id)
		o := pay(fmt.Sprint("late", i), "주식회사 우아한형제", "식비", pr.From.Add(19*time.Hour))
		_ = st.PutOccurrence(ctx, o)
		met, err := p.Match(ctx, o)
		if err != nil || len(met) != 1 || met[0] != id {
			t.Fatalf("늦게 온 결제도 그날의 예측을 맞힘: %v %v", met, err)
		}
		again, _ := st.Prediction(ctx, id)
		if !*again.Outcome || again.RescoredAt == nil || again.Surprise || *again.Score > 0.05 || again.ScoredAt.After(*again.RescoredAt) {
			t.Fatalf("다시 채점: %+v", again)
		}
	}
	if err := p.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	// Taken up again, and - its record what it is now - shown.
	if n, _ := st.Note(ctx, "rule"); n.Rule.Off || n.Version != 4 || n.Status != model.Hypothesis {
		t.Fatalf("빗나간 게 아니었으면 다시 예측: %+v %+v", n, n.Rule)
	}
}

// The operator's own sentence and confirmation are theirs: the night's
// numbers move the rule and its record under them, never the words, and a
// confirmed note keeps the confidence they gave it.
func TestTheOperatorsWordsStay(t *testing.T) {
	now := at(2026, 11, 7, 3, 0)
	p, st := engine(&now)
	ctx := context.Background()
	s := &self.Self{St: st, Now: func() time.Time { return now }}
	mined := func(text string) []model.Note {
		test := model.Track{}
		for range 6 {
			test = test.Add(0.8, 0.3, true)
		}
		return []model.Note{{ID: "daily", Text: text, Rule: &model.Rule{Kind: Daily, Key: Delivery, Rates: make([]float64, 7), Base: 0.3, Test: &test}}}
	}
	if err := p.Keep(ctx, mined("배달은 주로 금요일")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, "daily", model.Confirmed, "금요일 밤엔 배달을 시켜 먹어요", ""); err != nil {
		t.Fatal(err)
	}
	if err := p.Keep(ctx, mined("배달은 주로 금요일 · 주로 저녁")); err != nil {
		t.Fatal(err)
	}
	day := at(2026, 10, 30, 0, 0)
	for i := range 12 {
		d := day.AddDate(0, 0, -i)
		due, miss := d.Add(24*time.Hour-time.Minute), false
		_ = st.PutPrediction(ctx, model.Prediction{ID: fmt.Sprint("d", i), Target: fmt.Sprint("day:x:", i), Prob: 0.9, Base: 0.3,
			Due: due, CreatedAt: now, ScoredAt: &due, Outcome: &miss, Basis: []string{"daily"}})
	}
	if err := p.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	n, _ := st.Note(ctx, "daily")
	if n.Text != "금요일 밤엔 배달을 시켜 먹어요" || !n.Written || n.Status != model.Confirmed || n.Confidence < 0.9 || n.Rule.Off {
		t.Fatalf("운영자의 문장과 확인은 그대로: %+v", n)
	}
	// A reflection's note found again keeps the operator's sentence too, and
	// what its record made of it.
	rate := model.Note{ID: "rate", Text: "늦게 퇴근한 날은 운동을 거의 안 해요", Confidence: 0.8,
		Rule: &model.Rule{Kind: "rate", Target: "workout", Feature: "late_leave", Arg: "21:00", Rate: 0.1, Days: 6}}
	if _, _, err := s.Hold(ctx, rate, "밤 성찰"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, "rate", "", "야근한 날엔 운동을 쉬어요", ""); err != nil {
		t.Fatal(err)
	}
	held, _ := st.Note(ctx, "rate")
	held.Rule.Off = true
	_ = st.PutNote(ctx, held)
	again := rate
	again.Text, again.Rule = "21시 이후 퇴근하면 운동을 안 한다", &model.Rule{Kind: "rate", Target: "workout", Feature: "late_leave", Arg: "21:00", Rate: 0.05, Days: 9}
	got, _, err := s.Hold(ctx, again, "밤 성찰")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "야근한 날엔 운동을 쉬어요" || !got.Rule.Off || got.Rule.Days != 9 {
		t.Fatalf("다시 찾은 노트도 운영자의 문장, 멈춘 것은 멈춘 대로: %+v %+v", got, got.Rule)
	}
}
