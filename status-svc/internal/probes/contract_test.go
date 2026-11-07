package probes

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

func serveHealth(t *testing.T, rep func() health.Report) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		health.Write(w, http.StatusOK, rep())
	}))
	t.Cleanup(srv.Close)
	return srv
}

func freshEnv() *env {
	contracts.mu.Lock()
	contracts.byID = map[string]*cached{}
	contracts.mu.Unlock()
	return &env{client: &http.Client{Timeout: 5 * time.Second}}
}

// A service that answers but whose loop has not got through in its time is
// broken - the silent stop the contract is for; one failing a few times in a
// row is a warning; a plain {"status":"ok"} is as before.
func TestAServiceIsJudgedByItsLoops(t *testing.T) {
	now := time.Now()
	old, recent := now.Add(-20*time.Minute), now.Add(-time.Minute)
	rep := health.Report{Loops: []health.Loop{
		{Name: "ingest", Label: "카드 알림 수집", StaleSec: 600, StartedAt: old, LastOK: &old, LastError: "메시지 a1: 끊김", ErrorAt: &recent, Failing: 40},
	}}
	srv := serveHealth(t, func() health.Report { return rep })
	e := freshEnv()
	v := e.service("svc.spending", "가계부", srv.URL, nil).Run(context.Background())
	if v.State != check.Fail || !strings.Contains(v.Summary, "카드 알림 수집") || !strings.Contains(v.Summary, "20분째") {
		t.Fatalf("멈춘 루프는 이상: %+v", v)
	}

	rep.Loops[0].LastOK, rep.Loops[0].Failing = &recent, 3
	v = freshEnv().service("svc.spending", "가계부", srv.URL, nil).Run(context.Background())
	if v.State != check.Warn || !strings.Contains(v.Summary, "잘 되지 않고") || !strings.Contains(strings.Join(v.Facts, ""), "연달아 3번") {
		t.Fatalf("연달아 실패하면 주의: %+v", v)
	}

	rep.Loops[0].Failing = 0
	v = freshEnv().service("svc.spending", "가계부", srv.URL, nil).Run(context.Background())
	if v.State != check.OK || len(v.Facts) != 1 {
		t.Fatalf("돌고 있으면 정상, 루프는 한 줄씩: %+v", v)
	}

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer plain.Close()
	if v := freshEnv().service("svc.x", "x", plain.URL, nil).Run(context.Background()); v.State != check.OK {
		t.Fatalf("계약 이전의 /health 도 정상: %+v", v)
	}
}

// Every service's row reads its /health in the contract: what it says of
// itself rides along unjudged, and a 503 with its reason is broken - the
// same verdicts a body without the contract's fields (a bare key, an "ok" flag)
// get.
func TestEveryServiceRowReadsTheContract(t *testing.T) {
	code, body := http.StatusOK, ""
	stack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/health") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	defer stack.Close()
	u := stack.URL
	cfg := Config{KakaoURL: u, SpendingURL: u + "/api/spending", AssetsURL: u, CalendarURL: u, AgentURL: u, MindURL: u,
		MemoryURL: u, ContextURL: u, VaultURL: u, InputURL: u, NotifyURL: u, PhotosURL: u, JobsURL: u + "/api/jobs",
		IMessageURL: u, SearchURL: u}
	var rows []check.Probe
	for _, p := range All(cfg) {
		// Immich is not ours: it is asked its own ping.
		if strings.HasPrefix(p.ID, "svc.") && p.ID != "svc.immich" {
			rows = append(rows, p)
		}
	}
	if len(rows) < 15 {
		t.Fatalf("서비스 줄이 %d개뿐입니다", len(rows))
	}
	for _, c := range []struct {
		name  string
		code  int
		body  string
		state check.State
	}{
		{"계약", http.StatusOK, `{"status":"ok"}`, check.OK},
		{"보관함의 키", http.StatusOK, `{"status":"ok","about":{"key":"257335f4"}}`, check.OK},
		{"웹 찾기의 설정", http.StatusOK, `{"status":"ok","about":{"configured":true,"naver":true,"ocr":false}}`, check.OK},
		{"키만 있는 보관함", http.StatusOK, `{"status":"ok","key":"257335f4"}`, check.OK},
		{"ok 만 있는 웹 찾기", http.StatusOK, `{"ok":true,"configured":false,"naver":false,"ocr":false}`, check.OK},
		{"데이터베이스 없음", http.StatusServiceUnavailable, `{"status":"down","error":"데이터베이스에 연결할 수 없습니다."}`, check.Fail},
	} {
		code, body = c.code, c.body
		for _, p := range rows {
			contracts.mu.Lock()
			contracts.byID = map[string]*cached{}
			contracts.mu.Unlock()
			v := p.Run(context.Background())
			if v.State != c.state {
				t.Errorf("%s · %s: %+v", c.name, p.ID, v)
			}
			if c.code != http.StatusOK && !slices.Contains(v.Facts, "서비스가 밝힌 이유: 데이터베이스에 연결할 수 없습니다.") {
				t.Errorf("%s · %s: 503 의 이유가 펼친 줄에 없습니다: %+v", c.name, p.ID, v)
			}
		}
	}
}

// A 503 says why in the row's opened lines, on every row that reads the
// contract, and the verdict is what it was: a service row is broken, a held
// box unread, a delivery broken.
func TestA503SaysWhyInTheOpenedRow(t *testing.T) {
	why := "데이터베이스에 연결할 수 없습니다."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		health.Write(w, http.StatusServiceUnavailable, health.Report{Error: why})
	}))
	defer srv.Close()
	e := freshEnv()
	e.InputURL = srv.URL
	for _, c := range []struct {
		probe   check.Probe
		state   check.State
		summary string
	}{
		{e.service("svc.vault", "로그인 정보 보관함", srv.URL, nil), check.Fail, "응답 코드 503."},
		{e.loop("kakao.read", "수집", "카카오톡", srv.URL, "read", nil, "", ""), check.Fail, "응답 코드 503."},
		{e.heldBox("spending.unparsed", "가계부", srv.URL, "", "", nil), check.Unknown, "격리함을 읽지 못했어요 (응답 코드 503)."},
		{e.delivery("notify", "기록 → 알림", nil), check.Fail, "입구의 상태를 읽지 못했어요."},
	} {
		v := c.probe.Run(context.Background())
		if v.State != c.state || v.Summary != c.summary || !slices.Contains(v.Facts, "서비스가 밝힌 이유: "+why) {
			t.Errorf("%s: %+v", c.probe.ID, v)
		}
	}

	quiet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer quiet.Close()
	if v := freshEnv().service("svc.x", "x", quiet.URL, nil).Run(context.Background()); v.State != check.Fail || len(v.Facts) != 0 {
		t.Fatalf("이유를 말하지 않은 503 에는 덧붙일 것이 없습니다: %+v", v)
	}
}

// What a service set aside is a warning row of its own, with what and why,
// and where a person deals with it.
func TestAHeldBoxIsAWarning(t *testing.T) {
	at := time.Date(2026, 10, 27, 9, 30, 0, 0, time.Local)
	rep := health.Report{}
	srv := serveHealth(t, func() health.Report { return rep })
	probe := freshEnv().heldBox("spending.unparsed", "가계부", srv.URL, "", "", nil)
	if v := probe.Run(context.Background()); v.State != check.OK {
		t.Fatalf("빈 격리함은 정상: %+v", v)
	}
	rep.Held = &health.Held{Count: 7, Oldest: &at, Where: "가계부 탭",
		Items: []health.Item{{What: "테스트카드", Why: "기록하지 못했습니다: NUL", At: at}}}
	v := freshEnv().heldBox("spending.unparsed", "가계부", srv.URL, "", "", nil).Run(context.Background())
	if v.State != check.Warn || !strings.Contains(v.Summary, "7건") || v.ObservedAt == nil {
		t.Fatalf("%+v", v)
	}
	joined := strings.Join(v.Facts, "\n")
	for _, want := range []string{"테스트카드", "NUL", "그 밖에 6건", "처리: 가계부 탭"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%q 가 없습니다: %v", want, v.Facts)
		}
	}
}

// Each consumer of the event log is its own row, so that a stuck delivery
// to output-svc points at output-svc.
func TestADeliveryRowReadsItsConsumer(t *testing.T) {
	now := time.Now()
	since := now.Add(-30 * time.Minute)
	srv := serveHealth(t, func() health.Report {
		return health.Report{Loops: []health.Loop{
			{Name: "drain", Label: "outbox", StaleSec: 300, StartedAt: now, LastOK: &now},
			{Name: "deliver:notify", Label: "notify 에게 전달", StaleSec: 900, StartedAt: now, LastOK: &since, Behind: &since,
				Failing: 12, LastError: "notify 가 401 을 돌려줬습니다"},
			{Name: "deliver:agent", Label: "agent 에게 전달", StaleSec: 900, StartedAt: now, LastOK: &now},
		}}
	})
	e := freshEnv()
	e.InputURL = srv.URL
	notify := e.delivery("notify", "기록 → 알림", []string{"svc.output"})
	if v := notify.Run(context.Background()); v.State != check.Fail || !strings.Contains(v.Summary, "401") || v.ObservedAt == nil {
		t.Fatalf("15분 넘게 막힌 전달은 이상: %+v", v)
	}
	if v := e.delivery("agent", "기록 → 에이전트", nil).Run(context.Background()); v.State != check.OK {
		t.Fatalf("%+v", v)
	}
	if v := e.delivery("mind", "기록 → mind", nil).Run(context.Background()); v.State != check.Unknown {
		t.Fatalf("구독 전: %+v", v)
	}
	if v := e.service("svc.input", "입구(기록)", srv.URL, nil, "drain").Run(context.Background()); v.State != check.OK {
		t.Fatalf("입구 자신은 outbox 만 봅니다: %+v", v)
	}
	if notify.Deps[0] != "svc.input" || notify.Deps[1] != "svc.output" {
		t.Fatalf("원인 연결: %v", notify.Deps)
	}
}

func TestRedisPing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 64)
			_, _ = c.Read(buf)
			_, _ = c.Write([]byte("+PONG\r\n"))
			c.Close()
		}
	}()
	if v := freshEnv().redisPing(ln.Addr().String())(context.Background()); v.State != check.OK {
		t.Fatalf("%+v", v)
	}
	if v := freshEnv().redisPing("127.0.0.1:1")(context.Background()); v.State != check.Fail {
		t.Fatalf("닫힌 포트: %+v", v)
	}
}
