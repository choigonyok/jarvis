package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/sleep"
	"github.com/choigonyok/jarvis/context-svc/internal/store"
)

// sleepServer is a server on a throwaway Postgres with migration 106, its
// clock at 10:00 on the 16th in Seoul, and a way to call it.
func sleepServer(t *testing.T) (*store.Store, func(method, path, body, token string) (int, string)) {
	t.Helper()
	dsn := os.Getenv("CONTEXT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CONTEXT_TEST_DATABASE_URL 이 없어 건너뜁니다")
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state, context_sleep restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	seoul, _ := time.LoadLocation("Asia/Seoul")
	server := New(st, Config{Token: "api", SignalToken: "sig", GuestToken: "guest", RawDays: 30}, nil, quiet)
	server.now = func() time.Time { return time.Date(2026, 11, 16, 10, 0, 0, 0, seoul) }
	srv := httptest.NewServer(server.Handler())
	t.Cleanup(srv.Close)
	return st, func(method, path, body, token string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
}

// The night of the 15th to the 16th as a Korean iPhone's shortcut sends it:
// the phone's 침대에 있음 under the watch's stages.
const koreanNight = `{"source":"shortcut","samples":[
	{"start":"2026-11-15T23:30:00+09:00","end":"2026-11-16T07:10:00+09:00","value":"침대에 있음","source":"홍길동의 iPhone"},
	{"start":"2026-11-15T23:42:00+09:00","end":"2026-11-16T01:30:00+09:00","value":"코어 수면","source":"홍길동의 Apple Watch"},
	{"start":"2026-11-16T01:30:00+09:00","end":"2026-11-16T02:20:00+09:00","value":"깊은 수면","source":"홍길동의 Apple Watch"},
	{"start":"2026-11-16T02:20:00+09:00","end":"2026-11-16T02:30:00+09:00","value":"깨어 있음","source":"홍길동의 Apple Watch"},
	{"start":"2026-11-16T02:30:00+09:00","end":"2026-11-16T05:00:00+09:00","value":"코어 수면","source":"홍길동의 Apple Watch"},
	{"start":"2026-11-16T05:00:00+09:00","end":"2026-11-16T07:05:00+09:00","value":"렘 수면","source":"홍길동의 Apple Watch"}]}`

type sleepReply struct {
	Nights  []slept  `json:"nights"`
	Unknown []string `json:"unknown"`
}

type sleepList struct {
	Nights   []sleep.Night `json:"nights"`
	Summary  sleep.Summary `json:"summary"`
	KeepDays int           `json:"keepDays"`
}

func TestSleepDoor(t *testing.T) {
	st, do := sleepServer(t)
	ctx := context.Background()

	// Only the signal token (or the stack's) posts; neither the guest's nor none.
	for _, token := range []string{"", "guest", "nope"} {
		if code, _ := do("POST", "/sleep", koreanNight, token); code != 401 {
			t.Fatalf("토큰 %q 로 수면: %d", token, code)
		}
	}
	code, body := do("POST", "/sleep", koreanNight, "sig")
	var reply sleepReply
	_ = json.Unmarshal([]byte(body), &reply)
	if code != 200 || len(reply.Nights) != 1 || reply.Nights[0].Result != sleep.Saved || reply.Nights[0].Night != "2026-11-16" ||
		reply.Nights[0].AsleepMinutes != 433 || len(reply.Unknown) != 0 {
		t.Fatalf("수면: %d %s", code, body)
	}
	// Reading is the stack's token's alone: not the phone's, not the guest's.
	for _, token := range []string{"sig", "guest", ""} {
		if code, _ := do("GET", "/sleep", "", token); code != 401 {
			t.Fatalf("토큰 %q 로 읽기: %d", token, code)
		}
	}
	read := func(query string) sleepList {
		t.Helper()
		code, body := do("GET", "/sleep"+query, "", "api")
		var l sleepList
		if err := json.Unmarshal([]byte(body), &l); code != 200 || err != nil {
			t.Fatalf("읽기: %d %s %v", code, body, err)
		}
		return l
	}
	l := read("")
	if len(l.Nights) != 1 || l.KeepDays != 30 {
		t.Fatalf("밤 하나: %+v", l)
	}
	n := l.Nights[0]
	// 23:42-07:05 asleep less the ten minutes awake; 23:30-07:10 in bed.
	if n.AsleepMinutes != 433 || n.InBedMinutes != 460 || n.AwakeMinutes != 27 || *n.DeepMinutes != 50 || *n.CoreMinutes != 258 ||
		*n.REMMinutes != 125 || len(n.Timeline) != 7 || n.Source != "홍길동의 iPhone, 홍길동의 Apple Watch" ||
		n.BedAt.Format(time.RFC3339) != "2026-11-15T23:30:00+09:00" || n.WakeAt.Format(time.RFC3339) != "2026-11-16T07:10:00+09:00" {
		t.Fatalf("밤: %+v", n)
	}
	if l.Summary.Week.Nights != 1 || l.Summary.Week.AsleepMinutes != 433 || l.Summary.Week.BedAt != "23:30" || l.Summary.Month.Nights != 1 {
		t.Fatalf("요약: %+v", l.Summary)
	}

	// The same send again: one night, and one reading of it for the baseline.
	if code, body := do("POST", "/sleep", koreanNight, "sig"); code != 200 || !strings.Contains(body, `"result":"saved"`) {
		t.Fatalf("다시: %d %s", code, body)
	}
	if l := read(""); len(l.Nights) != 1 || l.Nights[0].AsleepMinutes != 433 {
		t.Fatalf("같은 밤은 하나: %+v", l.Nights)
	}
	rs, err := st.Readings(ctx, "health.sleep", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(rs) != 1 || rs[0].Value != 7.22 || !rs[0].At.Equal(n.WakeAt) {
		t.Fatalf("기저선의 수면은 한 번: %v %+v", err, rs)
	}

	// The tail of that night, as a shortcut run at 06:00 the next day would
	// ask for it: the night stays.
	tail := `{"samples":[{"start":"2026-11-16T06:00:00+09:00","end":"2026-11-16T07:05:00+09:00","value":"REM"}]}`
	if code, body := do("POST", "/sleep", tail, "sig"); code != 200 || !strings.Contains(body, `"result":"kept"`) {
		t.Fatalf("꼬리: %d %s", code, body)
	}
	// The day's nap, sent with a value nobody knows: a nap, and the word said back.
	nap := `{"samples":[{"start":"2026-11-16 14:00:00 +0900","end":"2026-11-16 14:45:00 +0900","value":"Asleep"},
		{"start":"2026-11-16 14:45:00 +0900","end":"2026-11-16 14:50:00 +0900","value":"코골이"}]}`
	code, body = do("POST", "/sleep", nap, "sig")
	reply = sleepReply{}
	_ = json.Unmarshal([]byte(body), &reply)
	if code != 200 || len(reply.Nights) != 1 || reply.Nights[0].Result != sleep.Napped || len(reply.Unknown) != 1 || reply.Unknown[0] != "코골이" {
		t.Fatalf("낮잠: %d %s", code, body)
	}
	if l := read(""); len(l.Nights) != 1 || l.Nights[0].AsleepMinutes != 433 || len(l.Nights[0].Naps) != 1 || l.Nights[0].Naps[0].AsleepMinutes != 45 {
		t.Fatalf("낮잠은 그날 밤 곁에: %+v", l.Nights)
	}

	// Health Auto Export's summary of the night before: a second night, the
	// newest first; a range asked for.
	hae := `{"data":{"metrics":[{"name":"sleep_analysis","units":"hr","data":[{"date":"2026-11-15 00:00:00 +0900","totalSleep":6.5,
		"core":4,"deep":1,"rem":1.5,"asleep":0,"inBed":7,"awake":0.5,"sleepStart":"2026-11-15 00:20:00 +0900","sleepEnd":"2026-11-15 07:00:00 +0900",
		"inBedStart":"2026-11-15 00:05:00 +0900","inBedEnd":"2026-11-15 07:05:00 +0900","source":"Apple Watch"}]}]}}`
	if code, body := do("POST", "/sleep", hae, "sig"); code != 200 || !strings.Contains(body, `"night":"2026-11-15"`) {
		t.Fatalf("Health Auto Export: %d %s", code, body)
	}
	l = read("?from=2026-11-15&to=2026-11-16")
	if len(l.Nights) != 2 || l.Nights[0].Night != "2026-11-16" || l.Nights[1].Night != "2026-11-15" || l.Nights[1].AsleepMinutes != 390 ||
		l.Nights[1].Timeline != nil || l.Summary.Week.Nights != 2 {
		t.Fatalf("둘, 새것부터: %+v", l)
	}
	if l := read("?from=2026-11-16&to=2026-11-16"); len(l.Nights) != 1 || l.Summary.Week.Nights != 2 {
		t.Fatalf("하루만 물어도 요약은 그 주: %+v", l)
	}
	for _, q := range []string{"?from=어제", "?from=2026-11-17&to=2026-11-16", "?from=2024-01-01&to=2026-11-16"} {
		if code, _ := do("GET", "/sleep"+q, "", "api"); code != 400 {
			t.Fatalf("%s: %d", q, code)
		}
	}

	// What is not sleep: said so, and nothing kept.
	for _, b := range []string{`{"samples":`, `{"kind":"device.carplay"}`, `{"samples":[{"start":"2026-11-16T01:00:00+09:00","end":"2026-11-16T02:00:00+09:00","value":"수면 분석"}]}`} {
		if code, body := do("POST", "/sleep", b, "sig"); code != 400 {
			t.Fatalf("%s: %d %s", b, code, body)
		}
	}

	// The timeline goes with the raw reports; the night stays.
	if n, err := st.ForgetTimelines(ctx, time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)); err != nil || n != 1 {
		t.Fatalf("타임라인 비우기: %d %v", n, err)
	}
	if l := read(""); l.Nights[0].Timeline != nil || l.Nights[0].AsleepMinutes != 433 || *l.Nights[0].DeepMinutes != 50 {
		t.Fatalf("합계는 남음: %+v", l.Nights[0])
	}
}

// A night stored is the baseline's health.sleep: the 수면 시간 line of /now
// without a shortcut of its own.
func TestSleepFeedsBaseline(t *testing.T) {
	_, do := sleepServer(t)
	var nights []string
	for d := 8; d <= 15; d++ {
		nights = append(nights, fmt.Sprintf(`{"start":"2026-11-%02dT23:30:00+09:00","end":"2026-11-%02dT06:30:00+09:00","asleepMinutes":420}`, d-1, d))
	}
	nights = append(nights, `{"start":"2026-11-16T01:30:00+09:00","end":"2026-11-16T06:30:00+09:00","asleepMinutes":300}`)
	if code, body := do("POST", "/sleep", `{"source":"자동화 앱","nights":[`+strings.Join(nights, ",")+`]}`, "sig"); code != 200 || strings.Count(body, `"saved"`) != 9 {
		t.Fatalf("아흐레: %d %s", code, body)
	}
	code, body := do("GET", "/now", "", "api")
	var now Now
	_ = json.Unmarshal([]byte(body), &now)
	if code != 200 || len(now.Health) != 1 || now.Health[0].Kind != "health.sleep" || now.Health[0].Value != 5 || now.Health[0].Baseline != 7 ||
		!strings.Contains(now.Summary, "수면 시간 5 (평소 7보다 29% 낮음)") {
		t.Fatalf("지금: %d %s", code, body)
	}
}
