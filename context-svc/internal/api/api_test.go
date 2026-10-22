package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/place"
	"github.com/choigonyok/jarvis/context-svc/internal/store"
	"github.com/choigonyok/jarvis/gocommon/notify"
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
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(st, Config{Token: "api", SignalToken: "sig", User: "phone", Pass: "pw", Public: "https://gw.example/context"}, nil, quiet).Handler())
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

	// The way between home and work, from the stays: 30 minutes there,
	// 67 back.
	if code, _ := do("PUT", "/regions", `{"name":"회사","kind":"work","lat":37.4979,"lon":127.0276,"radiusM":150,"area":"강남"}`, api); code != 200 {
		t.Fatalf("영역: %d", code)
	}
	for _, b := range []string{`{"_type":"transition","event":"enter","desc":"회사","tst":1760505400}`, `{"_type":"transition","event":"leave","desc":"회사","tst":1760536000}`} {
		if code, body := do("POST", "/owntracks", b, phone); code != 200 {
			t.Fatalf("회사: %d %s", code, body)
		}
	}
	code, body = do("GET", "/travel?days=400", "", api)
	var tr struct {
		Trips  []any
		Routes []struct {
			From, To string
			Minutes  int
		}
	}
	_ = json.Unmarshal([]byte(body), &tr)
	ways := map[string]int{}
	for _, r := range tr.Routes {
		ways[r.From+"→"+r.To] = r.Minutes
	}
	if code != 200 || len(tr.Trips) != 2 || ways["망원동→강남"] != 30 || ways["강남→망원동"] != 67 {
		t.Fatalf("이동: %d %s", code, body)
	}

	// The state in one place is kept as it was last announced, and moves
	// with each report.
	var derived State
	if _, err := st.Get(ctx, "derived", &derived); err != nil || !derived.Home || derived.Driving {
		t.Fatalf("집, 운전 아님: %+v %v", derived, err)
	}
	if code, _ := do("POST", "/signals", `{"source":"shortcut","kind":"device.carplay","state":"connected"}`, func(r *http.Request) { r.Header.Set("Authorization", "Bearer sig") }); code != 204 {
		t.Fatalf("신호: %d", code)
	}
	if _, err := st.Get(ctx, "derived", &derived); err != nil || !derived.Driving {
		t.Fatalf("운전 시작: %+v %v", derived, err)
	}
}

// The phone's regions come in with its waypoints message, and the console
// can hand the phone its whole configuration as one link.
func TestWaypointsAndSetup(t *testing.T) {
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
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	if err := st.PutRegion(ctx, place.Region{Name: "회사", Kind: "work", Lat: 1, Lon: 1, RadiusM: 100, Area: "판교"}); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(st, Config{Token: "api", SignalToken: "sig", User: "phone", Pass: "pw", Public: "https://gw.example/context"}, nil, quiet).Handler())
	defer srv.Close()
	post := func(body string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/owntracks", strings.NewReader(body))
		req.SetBasicAuth("phone", "pw")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(`{"_type":"waypoints","waypoints":[{"_type":"waypoint","desc":"집","lat":37.55,"lon":126.91,"rad":120,"tst":1},
		{"_type":"waypoint","desc":"회사","lat":37.40,"lon":127.11,"rad":200,"tst":1},{"_type":"waypoint","desc":"지수네 집","lat":37.56,"lon":126.92,"rad":30,"tst":1}]}`); code != 200 {
		t.Fatalf("waypoints: %d", code)
	}
	regions, _ := st.Regions(ctx)
	got := map[string]place.Region{}
	for _, r := range regions {
		got[r.Name] = r
	}
	if got["집"].Kind != "home" || got["집"].RadiusM != 120 || got["회사"].Area != "판교" || got["회사"].Lat != 37.40 || got["지수네 집"].Kind != "other" || got["지수네 집"].RadiusM != 50 {
		t.Fatalf("영역: %+v", got)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/owntracks/setup", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("토큰 없는 설정: %d", resp.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer api")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Link    string
		Signals struct{ URL, Token string }
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	u, err := url.Parse(out.Link)
	if err != nil || u.Scheme != "owntracks" || u.Path != "/config" {
		t.Fatalf("링크: %q %v", out.Link, err)
	}
	raw, err := base64.StdEncoding.DecodeString(u.Query().Get("inline"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	if cfg["_type"] != "configuration" || cfg["mode"] != 3.0 || cfg["url"] != "https://gw.example/context/owntracks" || cfg["username"] != "phone" ||
		cfg["password"] != "pw" || cfg["cmd"] != true || cfg["remoteConfiguration"] != true || cfg["monitoring"] != 1.0 {
		t.Fatalf("설정: %s", raw)
	}
	if out.Signals.URL != "https://gw.example/context/signals" || out.Signals.Token != "sig" {
		t.Fatalf("단축어: %+v", out.Signals)
	}

	// A mode asked for by hand goes out with the next reply, once.
	req, _ = http.NewRequest("PUT", srv.URL+"/owntracks/mode", strings.NewReader(`{"mode":2}`))
	req.Header.Set("Authorization", "Bearer api")
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 202 {
		t.Fatalf("모드 요청: %v %v", resp, err)
	}
	resp.Body.Close()
	reply := func() string {
		req, _ := http.NewRequest("POST", srv.URL+"/owntracks", strings.NewReader(`{"_type":"location","lat":37.1,"lon":127.1,"acc":10,"tst":1760600000,"batt":80}`))
		req.SetBasicAuth("phone", "pw")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if r := reply(); !strings.Contains(r, `"monitoring":2`) || !strings.Contains(r, "setConfiguration") {
		t.Fatalf("요청한 모드가 답에: %s", r)
	}
	if r := reply(); r != "[]\n" {
		t.Fatalf("한 번만: %s", r)
	}
	// The asked mode holds for its hour: at home, the rule would say
	// Significant, but Move stays.
	if err := st.PutRegion(ctx, place.Region{Name: "집", Kind: "home", Lat: 37.1, Lon: 127.1, RadiusM: 100}); err != nil {
		t.Fatal(err)
	}
	say := func(body string) string {
		req, _ := http.NewRequest("POST", srv.URL+"/owntracks", strings.NewReader(body))
		req.SetBasicAuth("phone", "pw")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if r := say(`{"_type":"location","lat":37.1,"lon":127.1,"acc":10,"tst":1760600100,"m":2}`); r != "[]\n" {
		t.Fatalf("요청한 Move 는 한 시간 그대로: %s", r)
	}
	// The hour over: at home, Significant.
	if err := st.Drop(ctx, "owntracks.hold"); err != nil {
		t.Fatal(err)
	}
	if r := say(`{"_type":"location","lat":37.1,"lon":127.1,"acc":10,"tst":1760600150,"m":2}`); !strings.Contains(r, `"monitoring":1`) {
		t.Fatalf("영역 안 → Significant: %s", r)
	}
	// Out and moving: Move - and again while the phone says it did not take it.
	if r := say(`{"_type":"location","lat":37.3,"lon":127.3,"acc":10,"tst":1760600200,"m":1}`); !strings.Contains(r, `"monitoring":2`) {
		t.Fatalf("나가서 움직임 → Move: %s", r)
	}
	if r := say(`{"_type":"location","lat":37.31,"lon":127.31,"acc":10,"tst":1760600300,"m":1}`); !strings.Contains(r, `"monitoring":2`) {
		t.Fatalf("폰이 받지 않은 명령은 다시: %s", r)
	}
	if r := say(`{"_type":"location","lat":37.31,"lon":127.31,"acc":10,"tst":1760600400,"m":2}`); r != "[]\n" {
		t.Fatalf("받았으면 다시 보내지 않음: %s", r)
	}
	// Still in one spot nobody registered for a quarter of an hour: Significant.
	if r := say(`{"_type":"location","lat":37.3101,"lon":127.3101,"acc":10,"tst":1760601000,"m":2,"motionactivities":["stationary"]}`); r != "[]\n" {
		t.Fatalf("10분째는 그대로: %s", r)
	}
	if r := say(`{"_type":"location","lat":37.3102,"lon":127.31,"acc":10,"tst":1760601400,"m":2,"motionactivities":["stationary"]}`); !strings.Contains(r, `"monitoring":1`) {
		t.Fatalf("한자리에 15분 → Significant: %s", r)
	}
	// In a car it is moving, however long the jam.
	if r := say(`{"_type":"location","lat":37.4,"lon":127.4,"acc":10,"tst":1760602000,"m":2,"motionactivities":["automotive"]}`); r != "[]\n" {
		t.Fatalf("차 안: %s", r)
	}
	if r := say(`{"_type":"location","lat":37.4001,"lon":127.4,"acc":10,"tst":1760603000,"m":2,"motionactivities":["automotive"]}`); r != "[]\n" {
		t.Fatalf("막혀 서 있어도 차 안이면 Move 그대로: %s", r)
	}
	// Leaving one region while still in another: settled still.
	if err := st.PutRegion(ctx, place.Region{Name: "동네", Kind: "other", Lat: 37.5, Lon: 127.5, RadiusM: 2000}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRegion(ctx, place.Region{Name: "카페", Kind: "food", Lat: 37.5, Lon: 127.5, RadiusM: 50}); err != nil {
		t.Fatal(err)
	}
	if r := say(`{"_type":"location","lat":37.5,"lon":127.5,"acc":10,"tst":1760604000,"m":2}`); !strings.Contains(r, `"monitoring":1`) {
		t.Fatalf("카페(동네 안) 도착 → Significant: %s", r)
	}
	if r := say(`{"_type":"transition","event":"leave","desc":"카페","tst":1760605000,"m":1}`); r != "[]\n" {
		t.Fatalf("카페를 나와도 동네 안: %s", r)
	}
	if r := say(`{"_type":"transition","event":"leave","desc":"동네","tst":1760606000,"m":1}`); !strings.Contains(r, `"monitoring":2`) {
		t.Fatalf("어느 영역에도 없음 → Move: %s", r)
	}
}

// Health readings a shortcut sends are said against the operator's own
// usual, once there are a week of them.
func TestHealth(t *testing.T) {
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
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(st, Config{Token: "api", SignalToken: "sig"}, nil, quiet).Handler())
	defer srv.Close()
	now := time.Now()
	for i := 9; i >= 0; i-- {
		v := "58"
		if i == 0 {
			v = "70"
		}
		body := fmt.Sprintf(`{"source":"shortcut","kind":"health.hr_rest","state":"%s","at":"%s"}`, v, now.AddDate(0, 0, -i).Format(time.RFC3339))
		req, _ := http.NewRequest("POST", srv.URL+"/signals", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer sig")
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 204 {
			t.Fatalf("신호: %v %v", resp, err)
		}
		resp.Body.Close()
	}
	req, _ := http.NewRequest("GET", srv.URL+"/now", nil)
	req.Header.Set("Authorization", "Bearer api")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var n Now
	_ = json.NewDecoder(resp.Body).Decode(&n)
	resp.Body.Close()
	if len(n.Health) != 1 || n.Health[0].Baseline != 58 || n.Health[0].Value != 70 || !strings.Contains(n.Summary, "안정 시 심박 70 (평소 58보다 21% 높음)") ||
		strings.Contains(n.Summary, "health.hr_rest=") {
		t.Fatalf("건강: %+v %q", n.Health, n.Summary)
	}
}

func TestChanged(t *testing.T) {
	got := Changed(State{Home: true}, State{Driving: true})
	if strings.Join(got, ",") != "운전 시작,집 끝" {
		t.Fatalf("%v", got)
	}
	if got := Changed(State{Meeting: true}, State{Meeting: true}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestDerive(t *testing.T) {
	now := time.Date(2026, 10, 15, 18, 0, 0, 0, time.UTC)
	n := Now{Signals: map[string]Sig{
		"device.carplay": {State: "connected", At: now.Add(-10 * time.Minute)},
		"mac.app":        {State: "meeting", At: now.Add(-2 * time.Hour)},
	}, Visits: []store.Visit{{Region: "집", Kind: "home"}}}
	st := Derive(n, now)
	if !st.Driving || st.Meeting || !st.Home {
		t.Fatalf("운전 중, 회의는 지난 일, 집: %+v", st)
	}
	n.State = st
	if s := Summary(n, now); !strings.Contains(s, "운전 중") || strings.Contains(s, "회의 중") {
		t.Fatalf("요약: %s", s)
	}
}

// resetTables empties tables as the owner when scripts/dbtest gives one
// (JARVIS_TEST_ADMIN_DSN): the service's own role may not TRUNCATE, and the
// code under test runs as that role.
func resetTables(t *testing.T, dsn, sql string) {
	t.Helper()
	if admin := os.Getenv("JARVIS_TEST_ADMIN_DSN"); admin != "" {
		dsn = admin
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}

// B-9: a report that arrives late - a fix or a leave older than the visit
// now open - says nothing about where the phone is now and closes nothing.
func TestLateReportKeepsTheStay(t *testing.T) {
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
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(st, Config{Token: "api", User: "phone", Pass: "pw"}, nil, quiet).Handler())
	defer srv.Close()
	post := func(path, body string, auth func(*http.Request)) int {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		auth(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	phone := func(r *http.Request) { r.SetBasicAuth("phone", "pw") }
	req, _ := http.NewRequest("PUT", srv.URL+"/regions", strings.NewReader(`{"name":"집","kind":"home","lat":37.5559,"lon":126.9104,"radiusM":120}`))
	req.Header.Set("Authorization", "Bearer api")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("영역: %v", err)
	}

	// Home at 12:00.
	if code := post("/owntracks", `{"_type":"location","lat":37.5560,"lon":126.9105,"acc":10,"tst":1760583600}`, phone); code != 200 {
		t.Fatalf("집: %d", code)
	}
	// A fix from 11:00, far away, arrives only now.
	if code := post("/owntracks", `{"_type":"location","lat":37.4979,"lon":127.0276,"acc":10,"tst":1760580000}`, phone); code != 200 {
		t.Fatalf("늦은 위치: %d", code)
	}
	// And a leave from 11:30 - before this visit began.
	if code := post("/owntracks", `{"_type":"transition","event":"leave","desc":"집","tst":1760581800}`, phone); code != 200 {
		t.Fatalf("늦은 나감: %d", code)
	}
	open, err := st.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].Region != "집" {
		t.Fatalf("집 체류가 열려 있어야 합니다: %+v", open)
	}
	// A leave after the visit began does close it.
	if code := post("/owntracks", `{"_type":"transition","event":"leave","desc":"집","tst":1760590800}`, phone); code != 200 {
		t.Fatalf("나감: %d", code)
	}
	if open, _ := st.Open(ctx); len(open) != 0 {
		t.Fatalf("제때 온 나감은 닫아야 합니다: %+v", open)
	}
}

// One day's way about comes back as the 위치 screen draws it: that day's
// fixes in order, with nothing else of the reports, the last good fix
// before it, its stays, and how far back the fixes go.
func TestTrack(t *testing.T) {
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
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	seoul, _ := time.LoadLocation("Asia/Seoul")
	server := New(st, Config{Token: "api", User: "phone", Pass: "pw", RawDays: 30}, nil, quiet)
	server.now = func() time.Time { return time.Date(2026, 10, 23, 9, 0, 0, 0, seoul) }
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()
	do := func(method, path, body string, set func(*http.Request)) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		set(req)
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
	at := func(s string) int64 {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, seoul)
		if err != nil {
			t.Fatal(err)
		}
		return tm.Unix()
	}
	if code, _ := do("PUT", "/regions", `{"name":"집","kind":"home","lat":37.5559,"lon":126.9104,"radiusM":120}`, api); code != 200 {
		t.Fatalf("영역: %d", code)
	}
	for _, b := range []string{
		// The evening before: home, then a fix from a cell tower.
		fmt.Sprintf(`{"_type":"location","lat":37.5560,"lon":126.9105,"acc":12,"tst":%d}`, at("2026-10-21 22:00")),
		fmt.Sprintf(`{"_type":"location","lat":37.60,"lon":126.95,"acc":2500,"tst":%d}`, at("2026-10-21 23:30")),
		// The day: at home on the Wi-Fi, on the road in a car, there.
		fmt.Sprintf(`{"_type":"location","lat":37.5561,"lon":126.9106,"acc":9,"tst":%d,"vel":0,"motionactivities":["stationary"],"ssid":"home-wifi","bssid":"aa:bb:cc"}`, at("2026-10-22 08:00")),
		fmt.Sprintf(`{"_type":"transition","event":"leave","desc":"집","tst":%d}`, at("2026-10-22 08:05")),
		fmt.Sprintf(`{"_type":"location","lat":37.53,"lon":126.97,"acc":15,"tst":%d,"vel":48,"motionactivities":["stationary","automotive"]}`, at("2026-10-22 08:20")),
		fmt.Sprintf(`{"_type":"location","lat":37.4979,"lon":127.0276,"acc":20,"tst":%d,"vel":-1}`, at("2026-10-22 08:45")),
		// Past midnight: the next day's.
		fmt.Sprintf(`{"_type":"location","lat":37.5560,"lon":126.9105,"acc":12,"tst":%d}`, at("2026-10-23 00:10")),
	} {
		if code, body := do("POST", "/owntracks", b, phone); code != 200 {
			t.Fatalf("보고: %d %s", code, body)
		}
	}
	if code, _ := do("GET", "/track?date=2026-10-22", "", func(*http.Request) {}); code != 401 {
		t.Fatalf("토큰 없이: %d", code)
	}
	if code, _ := do("GET", "/track?date=2026-13-01", "", api); code != 400 {
		t.Fatalf("없는 날: %d", code)
	}
	code, body := do("GET", "/track?date=2026-10-22", "", api)
	var tr struct {
		Date     string
		From, To time.Time
		Fixes    []store.Fix
		Before   *store.Fix
		Visits   []store.Visit
		KeepDays int
		Since    string
	}
	if err := json.Unmarshal([]byte(body), &tr); err != nil || code != 200 {
		t.Fatalf("하루: %d %s", code, body)
	}
	if tr.Date != "2026-10-22" || tr.From.Unix() != at("2026-10-22 00:00") || tr.To.Unix() != at("2026-10-23 00:00") {
		t.Fatalf("하루의 경계: %+v", tr)
	}
	if len(tr.Fixes) != 3 || tr.Fixes[0].At.Unix() != at("2026-10-22 08:00") || tr.Fixes[0].Act != "stationary" ||
		tr.Fixes[1].Act != "automotive" || tr.Fixes[1].Vel == nil || *tr.Fixes[1].Vel != 48 || tr.Fixes[2].Vel != nil {
		t.Fatalf("그날의 위치 셋, 차례대로: %s", body)
	}
	if strings.Contains(body, "home-wifi") || strings.Contains(body, "aa:bb") {
		t.Fatalf("와이파이는 나가지 않음: %s", body)
	}
	if tr.Before == nil || tr.Before.Acc != 12 || tr.Before.At.Unix() != at("2026-10-21 22:00") {
		t.Fatalf("시작은 기지국 위치가 아니라 그 전 집: %+v", tr.Before)
	}
	if len(tr.Visits) != 1 || tr.Visits[0].Region != "집" || tr.Visits[0].LeftAt == nil {
		t.Fatalf("집에서 나선 체류: %+v", tr.Visits)
	}
	if tr.KeepDays != 30 || tr.Since != "2026-10-21" {
		t.Fatalf("어디까지 거슬러 갈 수 있나: %d %s", tr.KeepDays, tr.Since)
	}
	// Without a date, today: the one fix past midnight, begun where the
	// day before ended.
	code, body = do("GET", "/track", "", api)
	tr.Fixes, tr.Before = nil, nil
	if err := json.Unmarshal([]byte(body), &tr); err != nil || code != 200 || tr.Date != "2026-10-23" || len(tr.Fixes) != 1 ||
		tr.Before == nil || tr.Before.At.Unix() != at("2026-10-22 08:45") {
		t.Fatalf("오늘: %d %s", code, body)
	}
	if code, body := do("GET", "/track?date=2026-09-01", "", api); code != 200 || !strings.Contains(body, `"fixes":[]`) || !strings.Contains(body, `"before":null`) {
		t.Fatalf("남은 기록이 없는 날: %d %s", code, body)
	}
}

// The guest's phone has credentials of its own. Its fixes are kept apart -
// the operator's visits, state and regions do not move with them - and the
// reply tells it how to watch: Significant inside a region (one kept here or
// its own) or after a while in one spot, Move once out and moving.
func TestGuestPhone(t *testing.T) {
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
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	seoul, _ := time.LoadLocation("Asia/Seoul")
	server := New(st, Config{Token: "api", User: "phone", Pass: "pw", GuestUser: "guest-phone", GuestPass: "gpw", Public: "https://gw.example/context"}, nil, quiet)
	server.now = func() time.Time { return time.Date(2026, 10, 23, 13, 0, 0, 0, seoul) }
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()
	do := func(method, path, body string, set func(*http.Request)) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		set(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	api := func(r *http.Request) { r.Header.Set("Authorization", "Bearer api") }
	guest := func(r *http.Request) { r.SetBasicAuth("guest-phone", "gpw") }
	at := func(hm string) int64 {
		tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-23 "+hm, seoul)
		return tm.Unix()
	}
	if code, _ := do("PUT", "/regions", `{"name":"집","kind":"home","lat":37.5559,"lon":126.9104,"radiusM":120}`, api); code != 200 {
		t.Fatalf("영역: %d", code)
	}
	if code, _ := do("POST", "/owntracks", `{"_type":"location","lat":37.5,"lon":127.0,"acc":10,"tst":1}`, func(r *http.Request) { r.SetBasicAuth("guest-phone", "nope") }); code != 401 {
		t.Fatalf("틀린 비밀번호: %d", code)
	}
	say := func(body string) string {
		code, reply := do("POST", "/owntracks", body, guest)
		if code != 200 {
			t.Fatalf("상대 폰: %d %s", code, reply)
		}
		return reply
	}
	// At home (a region kept here), already Significant: nothing to say but
	// the first ask for its regions.
	if r := say(fmt.Sprintf(`{"_type":"location","lat":37.5560,"lon":126.9105,"acc":10,"tst":%d,"m":1,"batt":80}`, at("08:00"))); r != `[{"_type":"cmd","action":"waypoints"}]`+"\n" {
		t.Fatalf("집, Significant 그대로: %s", r)
	}
	// Out on the road in a car: Move.
	if r := say(fmt.Sprintf(`{"_type":"location","lat":37.53,"lon":126.97,"acc":10,"tst":%d,"m":1,"batt":80,"motionactivities":["automotive"]}`, at("08:20"))); !strings.Contains(r, `"monitoring":2`) {
		t.Fatalf("나가서 움직임 → Move: %s", r)
	}
	// At a café nobody registered: still Move ten minutes in, Significant after fifteen.
	cafe := func(hm, extra string) string {
		return say(fmt.Sprintf(`{"_type":"location","lat":37.5216,"lon":126.9243,"acc":15,"tst":%d,"m":2,"batt":70,"motionactivities":["stationary"]%s}`, at(hm), extra))
	}
	cafe("09:00", "")
	if r := cafe("09:10", ""); r != "[]\n" {
		t.Fatalf("10분째는 그대로 Move: %s", r)
	}
	if r := cafe("09:16", ""); !strings.Contains(r, `"monitoring":1`) {
		t.Fatalf("한자리에 15분 → Significant: %s", r)
	}
	// A region of its own (the phone says it is in it): Significant at once.
	if r := say(fmt.Sprintf(`{"_type":"location","lat":37.48,"lon":127.05,"acc":10,"tst":%d,"m":2,"batt":70,"inregions":["상대 회사"]}`, at("10:00"))); !strings.Contains(r, `"monitoring":1`) {
		t.Fatalf("자기 영역 → Significant: %s", r)
	}
	// Its waypoints and transitions change nothing of the operator's.
	say(`{"_type":"waypoints","waypoints":[{"_type":"waypoint","desc":"집","lat":37.48,"lon":127.05,"rad":100,"tst":1}]}`)
	say(fmt.Sprintf(`{"_type":"transition","event":"enter","desc":"집","tst":%d}`, at("11:00")))
	regions, _ := st.Regions(ctx)
	if len(regions) != 1 || regions[0].Lat != 37.5559 {
		t.Fatalf("운영자의 영역은 그대로: %+v", regions)
	}
	if open, _ := st.Open(ctx); len(open) != 0 {
		t.Fatalf("체류는 열리지 않음: %+v", open)
	}
	code, body := do("GET", "/now", "", api)
	if code != 200 || strings.Contains(body, `"location"`) {
		t.Fatalf("운영자의 지금 위치는 없음 그대로: %d %s", code, body)
	}
	// The day, by phone.
	code, body = do("GET", "/track?date=2026-10-23&who=guest", "", api)
	var tr struct {
		Who    string
		Fixes  []store.Fix
		Visits []store.Visit
		Since  string
	}
	if err := json.Unmarshal([]byte(body), &tr); err != nil || code != 200 || tr.Who != "guest" || len(tr.Fixes) != 6 || len(tr.Visits) != 0 || tr.Since != "2026-10-23" {
		t.Fatalf("상대의 하루: %d %s", code, body)
	}
	if code, body := do("GET", "/track?date=2026-10-23", "", api); code != 200 || !strings.Contains(body, `"fixes":[]`) || !strings.Contains(body, `"who":"owner"`) {
		t.Fatalf("운영자의 하루에는 상대 위치가 없음: %d %s", code, body)
	}
	if code, _ := do("GET", "/track?who=someone", "", api); code != 400 {
		t.Fatalf("없는 사람: %d", code)
	}
	// The guest's setup: its own credentials, nothing of the operator's.
	if code, _ := do("GET", "/owntracks/setup/guest", "", func(*http.Request) {}); code != 401 {
		t.Fatalf("토큰 없는 설정: %d", code)
	}
	code, body = do("GET", "/owntracks/setup/guest", "", api)
	var out struct{ Link, URL string }
	_ = json.Unmarshal([]byte(body), &out)
	u, err := url.Parse(out.Link)
	if err != nil || code != 200 || strings.Contains(body, "signals") {
		t.Fatalf("상대 설정: %d %s", code, body)
	}
	raw, _ := base64.StdEncoding.DecodeString(u.Query().Get("inline"))
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	if cfg["username"] != "guest-phone" || cfg["password"] != "gpw" || cfg["deviceId"] != "guest-iphone" || cfg["cmd"] != true || cfg["remoteConfiguration"] != true || cfg["url"] != "https://gw.example/context/owntracks" {
		t.Fatalf("상대 설정 내용: %s", raw)
	}
}

// The guest's regions are its own - kept apart from the operator's, renamed
// by id, refreshed by the whole list once a day - and its arrivals and
// departures at them go to its own inbox, never as the operator's.
func TestGuestRegions(t *testing.T) {
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
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	seoul, _ := time.LoadLocation("Asia/Seoul")
	// What reaches events-svc.
	got := make(chan map[string]any, 32)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e map[string]any
		_ = json.NewDecoder(r.Body).Decode(&e)
		got <- e
	}))
	defer sink.Close()
	server := New(st, Config{Token: "api", User: "phone", Pass: "pw", GuestUser: "guest-phone", GuestPass: "gpw"}, notify.New(sink.URL, "", "", quiet), quiet)
	var today atomic.Value
	today.Store(time.Date(2026, 10, 23, 13, 0, 0, 0, seoul))
	server.now = func() time.Time { return today.Load().(time.Time) }
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()
	do := func(method, path, body string, set func(*http.Request)) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		set(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	api := func(r *http.Request) { r.Header.Set("Authorization", "Bearer api") }
	at := func(day, hm string) int64 {
		tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-"+day+" "+hm, seoul)
		return tm.Unix()
	}
	say := func(body string) string {
		code, reply := do("POST", "/owntracks", body, func(r *http.Request) { r.SetBasicAuth("guest-phone", "gpw") })
		if code != 200 {
			t.Fatalf("상대 폰: %d %s", code, reply)
		}
		return reply
	}
	// The guest's home and work, the road between; the operator's home is elsewhere.
	const home, work, road = `"lat":37.6000,"lon":127.0000`, `"lat":37.5001,"lon":127.1001`, `"lat":37.5500,"lon":127.0500`
	fix := func(day, hm, where, extra string) string {
		return say(fmt.Sprintf(`{"_type":"location",%s,"acc":20,"tst":%d,"m":1,"batt":80%s}`, where, at(day, hm), extra))
	}
	if code, _ := do("PUT", "/regions", `{"name":"집","kind":"home","lat":37.5559,"lon":126.9104,"radiusM":120}`, api); code != 200 {
		t.Fatalf("운영자 영역: %d", code)
	}

	// The first report asks for the phone's regions; the next ones, that day, do not.
	if r := fix("23", "07:00", home, ""); !strings.Contains(r, `{"_type":"cmd","action":"waypoints"}`) {
		t.Fatalf("첫 보고에 영역 목록 요청: %s", r)
	}
	// "+" on the phone: a point named Here-..., not a region yet. Named and
	// given a radius, the same id is a region - made where the phone is, so
	// no arrival.
	say(fmt.Sprintf(`{"_type":"waypoint","rid":"r1","desc":"Here-r1",%s,"rad":0,"tst":%d}`, home, at("23", "07:05")))
	say(fmt.Sprintf(`{"_type":"waypoint","rid":"r1","desc":"집",%s,"rad":100,"tst":%d}`, home, at("23", "07:06")))
	if r := fix("23", "07:10", home, `,"inregions":["집"]`); strings.Contains(r, `"waypoints"`) {
		t.Fatalf("같은 날 두 번 묻지 않음: %s", r)
	}
	say(fmt.Sprintf(`{"_type":"transition","event":"leave","desc":"집",%s,"tst":%d}`, home, at("23", "08:00")))
	fix("23", "08:10", road, "")
	// The whole list: one more, at work. Arriving there, before the phone
	// notices, the circle says so.
	say(fmt.Sprintf(`{"_type":"waypoints","waypoints":[{"_type":"waypoint","rid":"r1","desc":"집",%s,"rad":100},{"_type":"waypoint","rid":"r2","desc":"회사",%s,"rad":150}]}`, home, work))
	fix("23", "09:00", work, "")
	fix("23", "08:55", home, "") // late: changes nothing
	// Renamed at work: the same region, no departure nor arrival. A name the
	// phone says but that is not kept here is not one.
	say(fmt.Sprintf(`{"_type":"waypoint","rid":"r2","desc":"직장",%s,"rad":150,"tst":%d}`, work, at("23", "12:00")))
	fix("23", "12:30", work, `,"inregions":["직장","옛 영역"]`)
	fix("23", "18:00", road, "")
	say(fmt.Sprintf(`{"_type":"transition","event":"enter","desc":"집",%s,"tst":%d}`, home, at("23", "18:40")))

	var events []string
	for len(events) < 4 {
		select {
		case e := <-got:
			n, _ := e["notify"].(map[string]any)
			d, _ := e["data"].(map[string]any)
			events = append(events, fmt.Sprintf("%v %v %v %v %v %v %v", e["type"], d["region"], d["kind"], d["who"], n["title"], n["body"], n["audience"]))
		case <-time.After(3 * time.Second):
			t.Fatalf("도착·출발 알림이 모자람: %q", events)
		}
	}
	slices.Sort(events)
	want := []string{
		"location.guest.enter 집 home guest 집 도착 18:40 guest",
		"location.guest.enter 회사 work guest 회사 도착 09:00 guest",
		"location.guest.leave 직장 work guest 직장에서 출발 18:00 guest",
		"location.guest.leave 집 home guest 집에서 출발 08:00 guest",
	}
	slices.Sort(want)
	if !slices.Equal(events, want) {
		t.Fatalf("상대의 도착·출발:\n%s", strings.Join(events, "\n"))
	}

	// Nothing of the operator's moved.
	regions, _ := st.Regions(ctx)
	if len(regions) != 1 || regions[0].Lat != 37.5559 {
		t.Fatalf("운영자의 영역은 그대로: %+v", regions)
	}
	if open, _ := st.Open(ctx); len(open) != 0 {
		t.Fatalf("운영자 체류는 열리지 않음: %+v", open)
	}
	// The guest's day is named by its own regions first, the operator's after.
	var tr struct {
		Regions      []place.Region
		OtherRegions []place.Region
	}
	code, body := do("GET", "/track?date=2026-10-23&who=guest", "", api)
	if err := json.Unmarshal([]byte(body), &tr); err != nil || code != 200 || len(tr.Regions) != 2 || tr.Regions[0].Name != "직장" || tr.Regions[1].Name != "집" ||
		tr.Regions[1].Lat != 37.6 || tr.Regions[1].RadiusM != 100 || len(tr.OtherRegions) != 1 || tr.OtherRegions[0].Lat != 37.5559 {
		t.Fatalf("상대의 영역: %d %s", code, body)
	}
	if code, body := do("GET", "/track?date=2026-10-23", "", api); code != 200 || strings.Contains(body, "otherRegions") || !strings.Contains(body, `"lat":37.5559`) || strings.Contains(body, "직장") {
		t.Fatalf("운영자의 하루는 운영자의 영역으로: %d %s", code, body)
	}

	// A day on, it asks again; deleted on the phone, a region is gone with the
	// whole list - and with it, no departure.
	today.Store(time.Date(2026, 10, 24, 13, 1, 0, 0, seoul))
	if r := fix("24", "08:00", home, `,"inregions":["집"]`); !strings.Contains(r, `"waypoints"`) {
		t.Fatalf("하루 지나 다시 묻기: %s", r)
	}
	say(`{"_type":"waypoints","waypoints":[]}`)
	fix("24", "08:10", home, `,"inregions":["집"]`)
	select {
	case e := <-got:
		t.Fatalf("지운 영역에서 알림: %v", e)
	case <-time.After(300 * time.Millisecond):
	}
	if code, body := do("GET", "/track?date=2026-10-24&who=guest", "", api); code != 200 || !strings.Contains(body, `"regions":[]`) {
		t.Fatalf("지운 영역: %d %s", code, body)
	}
}
