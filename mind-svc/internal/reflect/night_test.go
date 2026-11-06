package reflect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// fakeModel answers as the test says: not there, failing, or with no hypothesis.
type fakeModel struct {
	ready bool
	err   error
	calls int
}

func (m *fakeModel) Complete(context.Context, string, string) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	return `{"hypotheses":[]}`, nil
}
func (m *fakeModel) Ready() bool    { return m.ready }
func (m *fakeModel) Model() string  { return "test" }
func (m *fakeModel) Spent() float64 { return 0 }

// A surprise is reflected on once the model has answered on it: a night
// without the model, or with a call that failed, leaves it for the next.
func TestASurpriseWaitsForTheModel(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	now := time.Date(2026, 11, 7, 3, 0, 0, 0, loc)
	clock := func() time.Time { return now }
	st := store.NewMemory()
	ctx := context.Background()
	day := time.Date(2026, 11, 5, 0, 0, 0, 0, loc)
	due, miss := day.Add(24*time.Hour-time.Minute), false
	_ = st.PutPrediction(ctx, model.Prediction{ID: "p1", Target: "workout:2026-11-05", Prob: 0.9, Due: due, From: &day, CreatedAt: day,
		ScoredAt: &due, Outcome: &miss, Surprise: true, Key: "workout"})
	llm := &fakeModel{}
	r := &Reflector{St: st, LLM: llm, Predict: &predict.Predictor{St: st, Loc: loc, Now: clock}, Self: &self.Self{St: st, Now: clock}, Loc: loc, Now: clock}
	reflected := func() bool {
		pr, _ := st.Prediction(ctx, "p1")
		return pr.Reflected
	}
	if _, err := r.Night(ctx); err != nil || reflected() {
		t.Fatalf("모델이 없는 밤엔 남겨 둠: %v", err)
	}
	llm.ready, llm.err = true, errors.New("529 overloaded")
	if _, err := r.Night(ctx); err == nil || reflected() || llm.calls != 1 {
		t.Fatalf("호출이 실패한 밤에도 남겨 둠: %v", err)
	}
	llm.err = nil
	if _, err := r.Night(ctx); err != nil || !reflected() || llm.calls != 2 {
		t.Fatalf("모델이 답하면 성찰한 것: %v", err)
	}
	if _, err := r.Night(ctx); err != nil || llm.calls != 2 {
		t.Fatal("한 번 성찰한 것은 다시 묻지 않음")
	}
}

// What the operator said was not so of the target is in the prompt, with why:
// proposed again it would come to nothing.
func TestTheReflectionIsToldWhatWasTurnedDown(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	st := store.NewMemory()
	ctx := context.Background()
	_ = st.PutNote(ctx, model.Note{ID: "r1", Text: "비 오는 날은 운동을 안 한다", Status: model.Rejected, Reason: "운영자: 실내 운동이라 상관없어",
		Rule: &model.Rule{Kind: "rate", Target: "workout", Feature: "rain", Arg: "1"}})
	_ = st.PutNote(ctx, model.Note{ID: "r2", Text: "배달 시킨 날은 운동을 안 한다", Status: model.Rejected, Reason: "운영자가 기각",
		Rule: &model.Rule{Kind: "rate", Target: "workout", Feature: "merchant", Arg: "우아한형제"}})
	_ = st.PutNote(ctx, model.Note{ID: "r3", Text: "다른 대상", Status: model.Rejected, Rule: &model.Rule{Kind: "rate", Target: "delivery", Feature: "rain", Arg: "1"}})
	r := &Reflector{St: st, Loc: loc}
	got := r.turnedDown(ctx, "workout")
	if !strings.Contains(got, "비 오는 날은 운동을 안 한다 (rain 1) · 이유: 실내 운동이라 상관없어") ||
		!strings.Contains(got, "배달 시킨 날은 운동을 안 한다 (merchant 우아한형제)\n") || strings.Contains(got, "다른 대상") {
		t.Fatalf("%s", got)
	}
	if r.turnedDown(ctx, "coffee") != "" {
		t.Fatal("기각된 것이 없으면 아무것도")
	}
}
