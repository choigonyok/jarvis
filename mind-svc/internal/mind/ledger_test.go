package mind

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// The ledger comes from what is already kept, and a rebuild changes nothing
// else: it is the measuring stage, and nothing reads it to decide.
func TestRebuildLedger(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	now := time.Date(2026, 10, 30, 3, 0, 0, 0, seoul)
	m, st, _ := testMind(t, now)
	ctx := context.Background()
	day := now.AddDate(0, 0, -2)
	from := day.Add(-7 * time.Hour)
	if err := st.PutPrediction(ctx, model.Prediction{ID: "p1", Key: "purchase/식비", Prob: 0.9, From: &from, Due: day.Add(16 * time.Hour), Outcome: new(bool), CreatedAt: day}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutOccurrence(ctx, model.Occurrence{ID: "s1", Kind: "purchase", At: day.Add(5 * time.Hour), Title: "밥집", Class: "식비"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutIntent(ctx, model.Intent{ID: "i1", Skill: "schedule", Kind: "calendar.create", Decision: model.Ask, Status: model.Rejected, DecidedBy: "user", Confidence: 0.8, Reason: "대화에서 확정됐는데 캘린더에 없어요", CreatedAt: day}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutFact(ctx, model.Fact{ID: "f1", Subject: model.Party{Self: true}, Relation: "LIVES_IN", Object: model.Party{Name: "서울"}, Status: model.FactStored, Confidence: 0.7, CreatedAt: day}); err != nil {
		t.Fatal(err)
	}

	rows, facts, err := m.RebuildLedger(ctx)
	if err != nil || rows != 2 || facts != 1 {
		t.Fatalf("%d %d %v", rows, facts, err)
	}
	got, _ := st.Outcomes(ctx)
	byID := map[string]model.Outcome{}
	for _, o := range got {
		byID[o.ID] = o
	}
	if o := byID["prediction:p1"]; o.Result != model.ResultHit || o.Live == nil {
		t.Fatalf("예측은 실제 발생으로 채점합니다: %+v", o)
	}
	if o := byID["intent:i1"]; o.Result != model.ResultRejected || o.Skill != "schedule" || o.Context != "calendar.create" || o.Rule == "" {
		t.Fatalf("카드: %+v", o)
	}
	// It reads and does not write back.
	pr, _ := st.Prediction(ctx, "p1")
	if pr.Outcome == nil || *pr.Outcome {
		t.Fatalf("예측 자체는 그대로여야 합니다: %+v", pr)
	}
	// And it is a night's step.
	if _, err := st.Outcomes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Night(ctx); err != nil {
		t.Fatalf("밤 작업: %v", err)
	}
	if after, _ := st.Outcomes(ctx); len(after) != 2 {
		t.Fatalf("밤에 다시 만들어야 합니다: %+v", after)
	}
}
