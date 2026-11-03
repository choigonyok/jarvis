package claudecode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// The operator's marks on answers are told once, newest last, and only those
// of the last hours: a mark left on the screen is news the session has not
// seen, and an old one is history.
func TestMarksAreToldOnce(t *testing.T) {
	now := time.Date(2026, 11, 3, 21, 0, 0, 0, time.Local)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }
	turns := []thread.Turn{
		{ID: "a-1-1", Role: thread.RoleAgent, At: "01:00", Paragraphs: []string{"오래된 답"}, Feedback: &thread.Feedback{Mark: "bad", At: at(20 * time.Hour)}},
		{ID: "u-2-2", Role: thread.RoleUser, At: "19:00", Text: "물음", Feedback: &thread.Feedback{Mark: "bad", At: at(time.Hour)}},
		{ID: "a-3-3", Role: thread.RoleAgent, At: "19:01", Paragraphs: []string{"토요일은 비가 와요.", "우산 챙기세요."}, Feedback: &thread.Feedback{Mark: "bad", Note: "일요일 얘기였어", At: at(50 * time.Minute)}},
		{ID: "a-4-4", Role: thread.RoleAgent, At: "20:10", Paragraphs: []string{"일요일은 맑아요."}, Feedback: &thread.Feedback{Mark: "good", At: at(10 * time.Minute)}},
		{ID: "a-5-5", Role: thread.RoleAgent, At: "20:20", Paragraphs: []string{"표시 없음"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"turns": turns})
	}))
	defer srv.Close()
	r := runner(t, "")
	r.store = thread.NewStore(srv.URL, "", bus.New())
	got := r.marks(now)
	for _, want := range []string{"19:01 의 답 「토요일은 비가 와요. 우산 챙기세요.」: 틀렸다고 표시 - 일요일 얘기였어", "20:10 의 답 「일요일은 맑아요.」: 좋았다고 표시"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, got)
		}
	}
	if strings.Index(got, "19:01") > strings.Index(got, "20:10") {
		t.Fatalf("오래된 것부터:\n%s", got)
	}
	if strings.Contains(got, "오래된 답") || strings.Contains(got, "물음") {
		t.Fatalf("지난 표시와 운영자 말의 표시는 빼야 합니다:\n%s", got)
	}
	if again := r.marks(now); again != "" {
		t.Fatalf("한 번만 알립니다:\n%s", again)
	}
	turns[4].Feedback = &thread.Feedback{Mark: "bad", At: at(time.Minute)}
	if next := r.marks(now); !strings.Contains(next, "20:20") || strings.Contains(next, "19:01") {
		t.Fatalf("새 표시만:\n%s", next)
	}
}
