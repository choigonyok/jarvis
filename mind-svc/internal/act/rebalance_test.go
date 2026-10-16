package act

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type refusing struct{}

func (refusing) Check(context.Context) ([]Order, error) { return nil, nil }
func (refusing) Place(_ context.Context, orders []Order, _ string) ([]Order, error) {
	out := make([]Order, len(orders))
	for i, o := range orders {
		o.Status, o.Message = "failed", "주문가능금액을 초과 했습니다"
		out[i] = o
	}
	return out, nil
}

// An approval whose orders all came back refused is a failure with the
// broker's reason, not a silent success; and an order is never "undone".
func TestRebalanceSaysWhatTheBrokerSaid(t *testing.T) {
	two := 2.0
	plan, _ := json.Marshal(RebalancePlan{PlanID: "p", Orders: []Order{{Venue: "kis", Symbol: "KO", Side: "buy", Units: &two, AmountKrw: 250000, Name: "코카콜라"}}})
	preview, err := RebalanceActuator{}.Preview(context.Background(), "assets.rebalance", plan)
	if err != nil || !strings.Contains(preview, "코카콜라 2주 매수") {
		t.Fatalf("미리보기: %q %v", preview, err)
	}
	_, _, err = RebalanceActuator{Orders: refusing{}}.Execute(context.Background(), "assets.rebalance", plan)
	if err == nil || !strings.Contains(err.Error(), "주문가능금액") {
		t.Fatalf("거절 이유가 보여야 합니다: %v", err)
	}
	if _, err := (RebalanceActuator{}).Undo(context.Background(), "assets.rebalance", nil); err == nil {
		t.Fatal("주문은 되돌릴 수 없습니다")
	}
}
