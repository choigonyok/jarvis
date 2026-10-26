package claudecode

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/config"
)

// Every turn's message carries mind's situation next to the date - values
// only. How to use them, and the operator's taste, are in the instructions:
// they change seldom, and every request carries the instructions anyway. A
// mind that does not answer gives no situation.
func TestSituationInEveryTurn(t *testing.T) {
	var calls atomic.Int32
	mind := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/now" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"text":"[지금 상황 · 16:00]\n- 추정: 지수네 집에서 지수와 함께 (0.85) · 근거: 15:40 망원동 사진","prefs":"- 소음에 민감하다 (확정 · 2곳에서 소음이(가) 아쉬웠음)"}`))
	}))
	defer mind.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	r := &Runner{cfg: config.Config{SystemPrompt: "base", MindURL: mind.URL, APIToken: "tok"}, log: quiet}
	head := r.now(r.situation())
	for _, want := range []string{"현재 시각", "지수네 집에서 지수와 함께 (0.85)"} {
		if !strings.Contains(head, want) {
			t.Fatalf("턴 앞머리에 %q 가 없습니다:\n%s", want, head)
		}
	}
	for _, rule := range []string{"0.5 미만", "첫 줄에 가정을", "알고 있는 취향", "상대 날짜"} {
		if strings.Contains(head, rule) {
			t.Fatalf("규칙 %q 는 턴마다 붙이지 않습니다:\n%s", rule, head)
		}
	}
	system := r.systemPrompt()
	for _, want := range []string{"base", "0.5 미만", "첫 줄에 가정을", "알고 있는 취향", "소음에 민감하다 (확정", "상대 날짜"} {
		if !strings.Contains(system, want) {
			t.Fatalf("지시에 %q 가 없습니다:\n%s", want, system)
		}
	}
	_ = r.situation()
	if calls.Load() != 1 {
		t.Fatalf("잠깐 사이에는 다시 묻지 않아야 합니다: %d", calls.Load())
	}

	// Shown once in a session, the same situation is not repeated.
	r.shown = r.showing
	if again := r.now(r.situation()); strings.Contains(again, "지수와 함께") || !strings.Contains(again, "앞 메시지 때와 같음") {
		t.Fatalf("같은 상황은 다시 붙이지 않습니다:\n%s", again)
	}

	down := &Runner{cfg: config.Config{SystemPrompt: "base", MindURL: "http://127.0.0.1:1", APIToken: "tok"}, log: quiet}
	if p := down.now(down.situation()); strings.Contains(p, "지금 상황") || !strings.Contains(p, "현재 시각") {
		t.Fatalf("mind 가 없으면 상황 없이: %s", p)
	}
}

// The guest's agent asks mind and context with the guest's own token - mind
// answers it with the guest's situation and taste, context with the guest's
// phone - and is given the guest's rules for them, which say nothing of the
// operator's tools or of the operator as the one being talked to.
func TestGuestSituation(t *testing.T) {
	mind := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/now" || r.Header.Get("Authorization") != "Bearer guest-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"text":"[지금 상황 · 21:30]\n- 위치: 21:03부터 집","prefs":"- 손님(나)은 답에 이모티콘을 빼 달라고 했다. (확정 · 대화 10/8)"}`))
	}))
	defer mind.Close()
	phone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/now/guest" || r.Header.Get("Authorization") != "Bearer guest-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"visits":[{"region":"집","kind":"home"}],"summary":"지금 집 (21:03부터) · 마지막 위치 2분 전"}`))
	}))
	defer phone.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	guest := &Runner{cfg: config.Config{SystemPrompt: "base", MindURL: mind.URL, ContextURL: phone.URL, APIToken: "guest-tok", Guest: true}, log: quiet}
	head := guest.now(guest.situation())
	for _, want := range []string{"21:03부터 집", "- 기기·위치 상태: 지금 집 (21:03부터) · 마지막 위치 2분 전"} {
		if !strings.Contains(head, want) {
			t.Fatalf("손님 턴 앞머리에 %q 가 없습니다:\n%s", want, head)
		}
	}
	system := guest.systemPrompt()
	for _, want := range []string{"이 사용자 폰(OwnTracks)", "직접 말하거나 정한 것", "이모티콘을 빼 달라고"} {
		if !strings.Contains(system, want) {
			t.Fatalf("손님 지시에 %q 가 없습니다:\n%s", want, system)
		}
	}
	for _, not := range []string{"find_nearby", "회고 카드", "운영자가 정정하면"} {
		if strings.Contains(system, not) {
			t.Fatalf("손님 지시에 운영자의 규칙 %q:\n%s", not, system)
		}
	}
	if guest.person() != "사용자" {
		t.Fatalf("손님 대화의 상대: %q", guest.person())
	}
}

// The taste list is part of the instructions, so mind not answering - or
// answering without it, when it could not read it - must not change them:
// the list a session was opened with stays. An answer of none does.
func TestPrefsOutliveAMindThatIsDown(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &Runner{cfg: config.Config{SystemPrompt: "base", MindURL: "http://127.0.0.1:1"}, log: quiet, prefs: "- 조용한 곳 (확정 · 소음)"}
	before := r.systemPrompt()
	_ = r.situation()
	if r.systemPrompt() != before {
		t.Fatalf("mind 가 답하지 않았다고 지시가 바뀌었습니다:\n%s", r.systemPrompt())
	}

	body := `{"text":"[지금 상황 · 16:00]\n- 추정 없음"}`
	mind := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer mind.Close()
	r.cfg.MindURL = mind.URL
	_ = r.situation()
	if r.systemPrompt() != before {
		t.Fatalf("취향을 못 읽었다는 답에 지시가 바뀌었습니다:\n%s", r.systemPrompt())
	}
	body = `{"text":"[지금 상황 · 16:01]\n- 추정 없음","prefs":""}`
	r.nowAt = time.Time{}
	_ = r.situation()
	if strings.Contains(r.systemPrompt(), "조용한 곳") {
		t.Fatalf("취향이 없다는 답이면 지웁니다:\n%s", r.systemPrompt())
	}
}

// A new session is shown the situation in full, even when the session before
// had already seen the same one - or the turn before it had none to show.
func TestNewSessionSeesTheSituation(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &Runner{cfg: config.Config{SystemPrompt: "base"}, log: quiet, shown: "- 추정 없음", showing: "- 추정 없음"}
	r.shown, r.showing = "", "" // what turn() does for a fresh session
	_ = r.now("")               // mind did not answer this turn
	r.account(&tally{}, true, "", 0, true)
	if got := r.now("[지금 상황 · 16:00]\n- 추정 없음"); !strings.Contains(got, "- 추정 없음") || strings.Contains(got, "같음") {
		t.Fatalf("새 세션은 상황을 다 봐야 합니다:\n%s", got)
	}
}

// Confirmed tastes first, then the surer estimates; whole lines only, within
// the bound, and how many were left out.
func TestCapPrefs(t *testing.T) {
	in := strings.Join([]string{
		"- 매운 걸 좋아한다 (추정 55% · 두 번)",
		"- 조용한 곳 (확정 · 소음 지적)",
		"- 바다 전망 (추정 80% · 사진)",
		"- 이건 근거 없음",
	}, "\n")
	got := capPrefs(in, 1000)
	want := strings.Join([]string{
		"- 조용한 곳 (확정 · 소음 지적)",
		"- 바다 전망 (추정 80% · 사진)",
		"- 매운 걸 좋아한다 (추정 55% · 두 번)",
		"- 이건 근거 없음",
	}, "\n")
	if got != want {
		t.Fatalf("순서:\n%s", got)
	}
	short := capPrefs(in, 40)
	if !strings.HasPrefix(short, "- 조용한 곳 (확정") || !strings.Contains(short, "3개는 생략") || strings.Contains(short, "바다") {
		t.Fatalf("상한:\n%s", short)
	}
	if capPrefs("", 100) != "" {
		t.Fatal("취향이 없으면 빈 문자열")
	}
}
