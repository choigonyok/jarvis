package mind

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// "집에 남았어" on a prep card is a leftover: until something of the class is
// bought again, the next plan of the kind does not call for it - its
// prediction is the chance it is bought anyway, and no card brings it up.
// Bought again, the rule holds as before.
func TestALeftoverHoldsUntilBoughtAgain(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, seoul) }
	m, st, clk := testMind(t, at(11, 1, 9))
	ctx := context.Background()
	_ = st.PutNote(ctx, model.Note{ID: "prep", Text: "캠핑 며칠 전에 장작을 산다", Status: model.Hypothesis, Version: 1, Confidence: 0.7,
		Rule: &model.Rule{Kind: "prep", PlanKind: "캠핑", Class: "장작", Support: 3, Total: 3}})
	if err := m.Self.Learn(ctx, "prep", false, "집에 남았어", "plan_old"); err != nil {
		t.Fatal(err)
	}
	n, _ := st.Note(ctx, "prep")
	if len(n.Rule.Exceptions) != 1 || n.Rule.Exceptions[0].Kind != model.ExceptLeftover || n.Rule.Exceptions[0].Plan != "plan_old" ||
		!strings.Contains(n.Text, "직전 캠핑 후 남은 경우 제외") {
		t.Fatalf("남았다는 말은 따질 수 있는 조건으로: %+v %+v", n, n.Rule.Exceptions)
	}
	plan := func(date string) model.Entity {
		e, err := m.World.Create(ctx, model.KindPlan, "calendar", model.State{Title: "지수와 캠핑", Status: model.Confirmed, Date: date, Start: "10:00", Type: "캠핑"},
			world.Meta{Actor: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	clk.Pin(at(11, 3, 9))
	next := plan("2026-11-14")
	prs, err := m.Predict.Prep(ctx, next)
	if err != nil || len(prs) != 1 {
		t.Fatalf("%v %v", prs, err)
	}
	if prs[0].Prob > 0.2 || !strings.Contains(prs[0].Text, "남은 경우") || prs[0].Prob != prs[0].Base {
		t.Fatalf("남은 게 있으면 그냥 살 확률만: %+v", prs[0])
	}
	if _, ok := m.prepIntent(ctx, prs[0]); ok {
		t.Fatal("남은 게 있으면 카드로 꺼내지 않음")
	}
	// Firewood bought since: what was left is not what there is now.
	_ = st.PutThingClass(ctx, "참나무 장작 10kg", "장작")
	_ = st.PutOccurrence(ctx, model.Occurrence{ID: "s1#1", Kind: "purchase.item", At: at(11, 20, 21), Title: "참나무 장작 10kg", Ref: "s1"})
	clk.Pin(at(11, 22, 9))
	later := plan("2026-12-05")
	prs, err = m.Predict.Prep(ctx, later)
	if err != nil || len(prs) != 1 || prs[0].Prob < 0.6 || strings.Contains(prs[0].Text, "남은 경우") {
		t.Fatalf("다시 산 뒤엔 그대로: %+v %v", prs, err)
	}
	if m.Predict.LeftOver(ctx, n.Rule) != nil {
		t.Fatal("다시 사면 남은 것은 다 쓴 것")
	}
}
