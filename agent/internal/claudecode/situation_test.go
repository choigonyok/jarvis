package claudecode

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/config"
)

// Every turn's system prompt carries mind's situation next to the date, with
// the rules for using it; a guest, or a mind that does not answer, gets none.
func TestSituationInSystemPrompt(t *testing.T) {
	calls := 0
	mind := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/now" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"text":"[지금 상황 · 16:00]\n- 추정: 지수네 집에서 지수와 함께 (0.85) · 근거: 15:40 망원동 사진","prefs":"- 소음에 민감하다 (확정 · 2곳에서 소음이(가) 아쉬웠음)"}`))
	}))
	defer mind.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	r := &Runner{cfg: config.Config{SystemPrompt: "base", MindURL: mind.URL, APIToken: "tok"}, log: quiet}
	got := r.systemPrompt()
	for _, want := range []string{"현재 시각", "지수네 집에서 지수와 함께 (0.85)", "0.5 미만", "첫 줄에 가정을", "알고 있는 취향", "소음에 민감하다 (확정"} {
		if !strings.Contains(got, want) {
			t.Fatalf("시스템 프롬프트에 %q 가 없습니다:\n%s", want, got)
		}
	}
	_ = r.systemPrompt()
	if calls != 1 {
		t.Fatalf("잠깐 사이에는 다시 묻지 않아야 합니다: %d", calls)
	}

	guest := &Runner{cfg: config.Config{SystemPrompt: "base", MindURL: mind.URL, APIToken: "tok", Guest: true}, log: quiet}
	if strings.Contains(guest.systemPrompt(), "지금 상황") {
		t.Fatal("손님에게 운영자의 상황이 갔습니다")
	}
	down := &Runner{cfg: config.Config{SystemPrompt: "base", MindURL: "http://127.0.0.1:1", APIToken: "tok"}, log: quiet}
	if p := down.systemPrompt(); strings.Contains(p, "지금 상황") || !strings.Contains(p, "현재 시각") {
		t.Fatalf("mind 가 없으면 상황 없이: %s", p)
	}
}
