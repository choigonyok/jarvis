package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The guest's agent's token writes the couple's calendar as the guest's
// side, whatever side the request names.
func TestGuestTokenIsAlwaysTheGuestsSide(t *testing.T) {
	s := New(nil, "owner-token", "guest-token", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	var side string
	h := s.auth(func(w http.ResponseWriter, r *http.Request) { side = r.Header.Get("X-Jarvis-Side") })
	call := func(token, claimed string) int {
		req := httptest.NewRequest(http.MethodPut, "/events", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Jarvis-Side", claimed)
		rec := httptest.NewRecorder()
		side = "-"
		h(rec, req)
		return rec.Code
	}
	if call("guest-token", "owner"); side != "partner" {
		t.Fatalf("손님 토큰은 손님 쪽이어야 합니다: %q", side)
	}
	if call("owner-token", ""); side != "" {
		t.Fatalf("운영자 쪽은 그대로: %q", side)
	}
	if code := call("nope", ""); code != http.StatusUnauthorized || side != "-" {
		t.Fatalf("모르는 토큰은 거절: %d %q", code, side)
	}
}
