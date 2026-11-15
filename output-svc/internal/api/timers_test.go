package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/output-svc/internal/live"
	"github.com/choigonyok/jarvis/output-svc/internal/push"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
	"github.com/choigonyok/jarvis/output-svc/internal/timers"
)

// whose is each device's person, as the store would say.
type whose map[string]string

func (w whose) Owns(_ context.Context, audience, endpoint string) (bool, error) {
	return w[endpoint] == audience, nil
}

func (w whose) SendTo(context.Context, string, string, push.Payload) (bool, error) { return true, nil }

const (
	ownerPhone = "https://web.push.apple.com/owner"
	guestPhone = "https://web.push.apple.com/guest"
)

// A console asks for a push to its own device and takes it back; whose the
// device is, is the console proxy's header, and a time comes as unix
// milliseconds or RFC 3339.
func TestTimersOverHTTP(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tm := timers.New(whose{ownerPhone: store.Owner, guestPhone: store.Guest}, log)
	srv := httptest.NewServer(New(nil, nil, nil, live.New(), "tok", log).WithTimers(tm).Handler())
	defer srv.Close()

	call := func(method, path, who, token, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if who != "" {
			req.Header.Set("X-Jarvis-Audience", who)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	ask := func(endpoint, at string) string {
		return fmt.Sprintf(`{"endpoint":%q,"at":%s,"tag":"rest","title":"휴식이 끝났어요","url":"/workout"}`, endpoint, at)
	}
	soon := time.Now().Add(2 * time.Minute)
	ms := fmt.Sprint(soon.UnixMilli())

	if code, _ := call("POST", "/timers", "", "nope", ask(ownerPhone, ms)); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %d", code)
	}
	if code, _ := call("POST", "/timers", store.Guest, "tok", ask(ownerPhone, ms)); code != http.StatusNotFound {
		t.Fatalf("손님이 운영자 폰에: %d", code)
	}
	text := func(v any) string { s, _ := v.(string); return s }
	code, first := call("POST", "/timers", "", "tok", ask(ownerPhone, ms))
	if code != http.StatusCreated || text(first["id"]) == "" || text(first["at"]) == "" {
		t.Fatalf("유닉스 밀리초: %d %v", code, first)
	}
	code, second := call("POST", "/timers", store.Owner, "tok", ask(ownerPhone, fmt.Sprintf("%q", soon.Add(15*time.Second).Format(time.RFC3339))))
	if code != http.StatusCreated {
		t.Fatalf("RFC 3339: %d %v", code, second)
	}
	// The same device and tag: the first was replaced, and is not there to take back.
	if code, _ := call("DELETE", "/timers/"+text(first["id"]), "", "tok", ""); code != http.StatusNotFound {
		t.Fatalf("바뀐 것을 취소: %d", code)
	}
	for _, at := range []string{`"soon"`, `null`, `0`, fmt.Sprint(time.Now().Add(31 * time.Minute).UnixMilli())} {
		if code, out := call("POST", "/timers", "", "tok", ask(ownerPhone, at)); code != http.StatusBadRequest || text(out["error"]) == "" {
			t.Fatalf("at %s: %d %v", at, code, out)
		}
	}
	if code, _ := call("POST", "/timers", "", "tok", `{"endpoint":"`+ownerPhone+`","tag":"rest","title":"x"}`); code != http.StatusBadRequest {
		t.Fatalf("at 없이: %d", code)
	}

	id := text(second["id"])
	if code, _ := call("DELETE", "/timers/"+id, store.Guest, "tok", ""); code != http.StatusNotFound {
		t.Fatalf("손님이 운영자의 예약을 취소: %d", code)
	}
	if code, out := call("DELETE", "/timers/"+id, "", "tok", ""); code != http.StatusOK || out["ok"] != true {
		t.Fatalf("취소: %d %v", code, out)
	}
	if code, _ := call("DELETE", "/timers/"+id, "", "tok", ""); code != http.StatusNotFound {
		t.Fatalf("두 번 취소: %d", code)
	}
	// The guest's own phone is the guest's to ask for.
	if code, _ := call("POST", "/timers", store.Guest, "tok", ask(guestPhone, ms)); code != http.StatusCreated {
		t.Fatalf("손님 자기 폰: %d", code)
	}
}
