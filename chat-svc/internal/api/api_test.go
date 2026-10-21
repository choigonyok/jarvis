package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/chat-svc/internal/turn"
)

// The guest's agent holds a token of its own, and it opens the guest's
// thread only: the operator's conversation is neither read nor written with
// it. (The store is not reached in these cases - the refusal comes first.)
func TestGuestTokenOpensTheGuestThreadOnly(t *testing.T) {
	h := New(nil, "owner-token", "guest-token", "Asia/Seoul", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	call := func(method, target, token, body string) int {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, c := range []struct {
		name, method, target, token, body string
		want                              int
	}{
		{"손님이 운영자 대화 읽기", http.MethodGet, "/turns?thread=", "guest-token", "", http.StatusForbidden},
		{"손님이 다른 대화 읽기", http.MethodGet, "/turns?thread=other", "guest-token", "", http.StatusForbidden},
		{"손님이 운영자 대화에 쓰기", http.MethodPost, "/turns", "guest-token", `{"thread":"","role":"user","text":"x"}`, http.StatusForbidden},
		{"토큰 없음", http.MethodGet, "/turns?thread=guest", "", "", http.StatusUnauthorized},
		{"모르는 토큰", http.MethodGet, "/turns?thread=guest", "nope", "", http.StatusUnauthorized},
	} {
		if got := call(c.method, c.target, c.token, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

// A turn with nothing to show is refused - but a checklist card is its list,
// with or without a line above it (2026-10-21: refused, the card reached the
// screen unsaved and was gone after the next read).
func TestChecklistCardIsNotEmpty(t *testing.T) {
	for _, c := range []struct {
		name string
		t    turn.Turn
		want bool
	}{
		{"아무것도 없음", turn.Turn{Role: turn.RoleAgent}, true},
		{"작업 소식 칸만", turn.Turn{Role: turn.RoleAgent, Origin: &turn.Origin{Kind: "job", Run: 1}}, true},
		{"체크리스트 카드", turn.Turn{Role: turn.RoleAgent, Origin: &turn.Origin{Kind: "checklist", Checklist: "chk_1"}}, false},
		{"말", turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"네"}}, false},
		{"결재 카드", turn.Turn{Role: turn.RoleAgent, ProposalID: "p-1"}, false},
	} {
		if got := empty(c.t); got != c.want {
			t.Errorf("%s: empty=%v, want %v", c.name, got, c.want)
		}
	}
}
