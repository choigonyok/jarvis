package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The guest's agent's token posts a notification for the guest and nothing
// else: no reading the log, no subscriptions, no plain events. (The store is
// not reached in these cases - the refusal comes first.)
func TestGuestTokenPostsNotificationsOnly(t *testing.T) {
	h := New(nil, func() {}, func() {}, "owner-token", "guest-token", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
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
		{"구독 바꾸기", http.MethodPut, "/subscriptions/x", `{}`, http.StatusUnauthorized},
		{"알림 없는 이벤트", http.MethodPost, "/events", `{"source":"agent","type":"memory.plan"}`, http.StatusForbidden},
	} {
		if got := call(c.method, c.target, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}
