package claudecode

import (
	"strings"
	"testing"
)

// The operator's rules go in front of a turn in full when they changed, in a
// line when the session saw them already, and once more when they emptied.
func TestRulesAreToldAsTheyChange(t *testing.T) {
	r := runner(t, "")
	if got := r.rulesBlock(); got != "" {
		t.Fatalf("규칙이 없으면 아무것도: %q", got)
	}
	r.rules = "- 렌터카 대여는 일정 카드로 올리지 않는다"
	got := r.rulesBlock()
	if !strings.Contains(got, "렌터카 대여는 일정 카드로 올리지 않는다") || !strings.Contains(got, "rule.end") {
		t.Fatalf("처음엔 전부: %q", got)
	}
	r.rulesShown = r.rulesShowing // the turn went through
	if got := r.rulesBlock(); !strings.Contains(got, "앞 메시지 때와 같음") {
		t.Fatalf("같으면 한 줄: %q", got)
	}
	r.rulesShown = r.rulesShowing
	r.rules = ""
	if got := r.rulesBlock(); !strings.Contains(got, "거둬졌습니다") {
		t.Fatalf("비면 한 번 알립니다: %q", got)
	}
	r.rulesShown = r.rulesShowing
	if got := r.rulesBlock(); got != "" {
		t.Fatalf("그 뒤로는 아무것도: %q", got)
	}
}
