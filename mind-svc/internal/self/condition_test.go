package self

import (
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// A no to "buy before camping" that says something is left over is a condition
// of the rule; any other reason is kept in its own words - "일정이 있어" is not
// leftovers.
func TestLeftoversAreSaidSo(t *testing.T) {
	n := model.Note{Rule: &model.Rule{Kind: "prep", PlanKind: "캠핑"}}
	for _, r := range []string{"집에 남았어", "아직 있어", "집에 있어", "이미 있어요"} {
		if got := Condition(n, r); got != "직전 캠핑 후 남은 경우 제외" {
			t.Fatalf("%q: %q", r, got)
		}
	}
	for _, r := range []string{"일정이 있어", "필요 있어?", "다른 약속 있어"} {
		if got := Condition(n, r); got != "제외: "+r {
			t.Fatalf("%q: %q", r, got)
		}
	}
}
