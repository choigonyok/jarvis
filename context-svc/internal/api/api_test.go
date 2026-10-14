package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/store"
)

// Against a throwaway Postgres with migration 046:
// CONTEXT_TEST_DATABASE_URL=postgres://... go test ./internal/api/
func TestFlow(t *testing.T) {
	dsn := os.Getenv("CONTEXT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CONTEXT_TEST_DATABASE_URL 이 없어 건너뜁니다")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Pool().Exec(ctx, `truncate context_raw, context_regions, context_visits, context_state restart identity`); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(st, Config{Token: "api", SignalToken: "sig", User: "phone", Pass: "pw"}, nil, quiet).Handler())
	defer srv.Close()

	do := func(method, path, body string, set func(*http.Request)) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if set != nil {
			set(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	api := func(r *http.Request) { r.Header.Set("Authorization", "Bearer api") }
	phone := func(r *http.Request) { r.SetBasicAuth("phone", "pw") }

	if code, _ := do("PUT", "/regions", `{"name":"집","kind":"home","lat":37.5559,"lon":126.9104,"radiusM":120,"area":"망원동"}`, api); code != 200 {
		t.Fatalf("영역: %d", code)
	}
	if code, _ := do("POST", "/owntracks", `{"_type":"location","lat":37.5559,"lon":126.9104,"acc":10,"tst":1760500000}`, nil); code != 401 {
		t.Fatalf("인증 없는 위치: %d", code)
	}
	if code, _ := do("POST", "/owntracks", `{"_type":"location","lat":37.5559,"lon":126.9104,"acc":10,"tst":1760500000}`, func(r *http.Request) { r.SetBasicAuth("phone", "nope") }); code != 401 {
		t.Fatalf("틀린 비밀번호: %d", code)
	}
	// At home: a visit opens from the fix alone.
	code, body := do("POST", "/owntracks", `{"_type":"location","lat":37.5560,"lon":126.9105,"acc":10,"tst":1760500000,"batt":80}`, phone)
	if code != 200 || body != "[]\n" {
		t.Fatalf("집 위치: %d %s", code, body)
	}
	// Leaving home (the transition): the phone is told to watch closely.
	code, body = do("POST", "/owntracks", `{"_type":"transition","event":"leave","desc":"집","lat":37.557,"lon":126.912,"tst":1760503600}`, phone)
	if code != 200 || !strings.Contains(body, `"monitoring":2`) {
		t.Fatalf("집을 나섬 → Move: %d %s", code, body)
	}
	// Back home, but the enter never arrived: the next fix opens the visit.
	code, body = do("POST", "/owntracks", `{"_type":"location","lat":37.5559,"lon":126.9104,"acc":15,"tst":1760540000,"batt":70}`, phone)
	if code != 200 || !strings.Contains(body, `"monitoring":1`) {
		t.Fatalf("다시 집 → Significant: %d %s", code, body)
	}
	if code, _ := do("POST", "/signals", `{"source":"shortcut","kind":"device.carplay","state":"connected"}`, nil); code != 401 {
		t.Fatalf("토큰 없는 신호: %d", code)
	}
	if code, _ := do("POST", "/signals", `{"source":"shortcut","kind":"device.carplay","state":"disconnected"}`, func(r *http.Request) { r.Header.Set("Authorization", "Bearer sig") }); code != 204 {
		t.Fatalf("신호: %d", code)
	}
	if code, _ := do("POST", "/signals", `{"kind":"Bad Kind","state":"x"}`, func(r *http.Request) { r.Header.Set("Authorization", "Bearer sig") }); code != 400 {
		t.Fatalf("잘못된 종류: %d", code)
	}
	if code, _ := do("GET", "/now", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer sig") }); code != 401 {
		t.Fatalf("신호 토큰으로는 읽지 못함: %d", code)
	}
	code, body = do("GET", "/now", "", api)
	var now Now
	_ = json.Unmarshal([]byte(body), &now)
	if code != 200 || len(now.Visits) != 1 || now.Visits[0].Region != "집" || now.Signals["device.carplay"].State != "disconnected" || now.Mode != 1 {
		t.Fatalf("지금: %d %s", code, body)
	}
	code, body = do("GET", "/visits?from=2025-10-14&to=2025-10-17", "", api)
	var vs struct{ Visits []store.Visit }
	_ = json.Unmarshal([]byte(body), &vs)
	if code != 200 || len(vs.Visits) != 2 || vs.Visits[0].LeftAt == nil || vs.Visits[1].LeftAt != nil || vs.Visits[0].Area != "망원동" {
		t.Fatalf("체류 둘(나감, 지금): %d %s", code, body)
	}
	if n, _ := st.Purge(ctx, time.Unix(1760540000, 0)); n != 2 {
		t.Fatalf("그보다 이른 원본 둘을 지움: %d", n)
	}
}
