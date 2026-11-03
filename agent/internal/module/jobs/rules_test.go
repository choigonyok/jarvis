package jobs

import (
	"strings"
	"testing"
)

// A run keeps to the operator's rules as the conversation does: they come
// first in its message, and nothing is added when there are none.
func TestARunReadsTheRules(t *testing.T) {
	if got := withRules("작업 1: 장작 사기", nil); got != "작업 1: 장작 사기" {
		t.Fatalf("규칙이 없으면 그대로: %q", got)
	}
	got := withRules("작업 1: 장작 사기", []string{"결제는 하나카드로 한다", "판매 글에 연락처는 넣지 않는다"})
	if !strings.HasPrefix(got, "운영자가 정한 규칙") || !strings.Contains(got, "- 결제는 하나카드로 한다\n- 판매 글에 연락처는 넣지 않는다") || !strings.HasSuffix(got, "작업 1: 장작 사기") {
		t.Fatalf("규칙이 앞에: %q", got)
	}
}
