package claudecode

import (
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// 2: the model is handed the quoted message ahead of what the operator wrote.
func TestWithRef(t *testing.T) {
	if got := withRef("그거 사줘", nil, "운영자"); got != "그거 사줘" {
		t.Fatalf("참조가 없으면 그대로: %q", got)
	}
	got := withRef("이 제품 구매해줘", &thread.Ref{ID: "a-1", Role: thread.RoleAgent, At: "10-16 22:31", Text: "알람시계 T872 는 16,900원"}, "운영자")
	if !strings.HasPrefix(got, "[운영자가 참조한 이전 메시지 · Jarvis(당신) · 10-16 22:31]\n알람시계 T872") || !strings.HasSuffix(got, "[참조 끝]\n\n이 제품 구매해줘") {
		t.Fatalf("인용 뒤에 말: %q", got)
	}
	// The guest's own quote is the guest's, not the operator's.
	got = withRef("이거 다시", &thread.Ref{ID: "u-1", Role: thread.RoleUser, At: "10-26 21:00", Text: "그 카페 어때"}, "사용자")
	if !strings.HasPrefix(got, "[사용자가 참조한 이전 메시지 · 사용자 · 10-26 21:00]\n그 카페 어때") || strings.Contains(got, "운영자") {
		t.Fatalf("손님의 인용: %q", got)
	}
}
