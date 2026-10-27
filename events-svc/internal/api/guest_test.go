package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The guest's agent's token posts a notification for the guest and
// registers the guest's own subscription, and nothing else: no reading the
// log, no services' subscription - not even one not registered yet - no
// plain events. (The store is not reached in these cases - the refusal comes
// first.)
func TestGuestTokenPostsNotificationsOnly(t *testing.T) {
	h := New(nil, func() {}, func() {}, "owner-token", "guest-token", nil, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	call := func(method, target, body string) int {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer guest-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, c := range []struct {
		name, method, target, body string
		want                       int
	}{
		{"이벤트 읽기", http.MethodGet, "/events", "", http.StatusUnauthorized},
		{"구독 목록", http.MethodGet, "/subscriptions", "", http.StatusUnauthorized},
		{"서비스 쪽 구독 바꾸기", http.MethodPut, "/subscriptions/agent", `{"url":"http://agent-guest:8081/consume","types":["*"]}`, http.StatusForbidden},
		{"아직 없는 서비스 쪽 이름", http.MethodPut, "/subscriptions/notify", `{"url":"http://agent-guest:8081/consume"}`, http.StatusForbidden},
		{"손님 쪽이 아닌 이름", http.MethodPut, "/subscriptions/guest", `{"url":"http://agent-guest:8081/consume"}`, http.StatusForbidden},
		{"구독 지우기", http.MethodDelete, "/subscriptions/agent-guest", "", http.StatusUnauthorized},
		{"알림 없는 이벤트", http.MethodPost, "/events", `{"source":"agent","type":"memory.plan"}`, http.StatusForbidden},
		{"격리함", http.MethodGet, "/held", "", http.StatusUnauthorized},
		{"격리함 버리기", http.MethodDelete, "/held/x/1", "", http.StatusUnauthorized},
	} {
		if got := call(c.method, c.target, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
