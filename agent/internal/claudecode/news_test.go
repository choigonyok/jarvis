package claudecode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// What was laid in the conversation without a model turn reaches the next
// one: a job's word, and what was told at its moment (with the list it
// showed) - not a checklist card, which the session laid itself or which
// the situation lists anyway. Each once.
func TestNewsTellsJobsAndNotices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"turns": []thread.Turn{
			{ID: "a-1", Role: thread.RoleAgent, At: "21:15", AtISO: "2026-10-21T21:15:00+09:00", Paragraphs: []string{"결제 승인 대기"},
				Origin: &thread.Origin{Kind: thread.OriginJob, Job: 30, Run: 134, Title: "캠핑 소모품 구매"}},
			{ID: "a-2", Role: thread.RoleAgent, At: "22:10", AtISO: "2026-10-21T22:10:55+09:00", Paragraphs: []string{"집에 가서 할 일"},
				Origin: &thread.Origin{Kind: thread.OriginNotice, Title: "집에 도착하셔서 알려 드려요", Checklist: "chk_2"}},
			{ID: "a-3", Role: thread.RoleAgent, At: "22:11", AtISO: "2026-10-21T22:11:00+09:00", Paragraphs: []string{"내일 출발이에요"},
				Origin: &thread.Origin{Kind: thread.OriginChecklist, Checklist: "chk_1"}},
		}})
	}))
	defer srv.Close()
	r := &Runner{store: thread.NewStore(srv.URL, "", bus.New())}
	news := r.jobNews()
	for _, want := range []string{"캠핑 소모품 구매 (21:15): 결제 승인 대기", "알림(집에 도착하셔서 알려 드려요) (22:10): 집에 가서 할 일 [체크리스트 chk_2 카드와 함께]"} {
		if !strings.Contains(news, want) {
			t.Errorf("소식에 %q 가 없습니다:\n%s", want, news)
		}
	}
	if strings.Contains(news, "내일 출발이에요") {
		t.Errorf("체크리스트 카드는 소식으로 넘기지 않습니다:\n%s", news)
	}
	if again := r.jobNews(); again != "" {
		t.Errorf("한 번 넘긴 소식을 또 넘깁니다: %s", again)
	}
}
