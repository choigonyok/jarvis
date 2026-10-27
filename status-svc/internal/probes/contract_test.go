package probes

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
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
// to notify-svc points at notify-svc.
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
	e.EventsURL = srv.URL
	notify := e.delivery("notify", "이벤트 → 알림", []string{"svc.notify"})
	if v := notify.Run(context.Background()); v.State != check.Fail || !strings.Contains(v.Summary, "401") || v.ObservedAt == nil {
		t.Fatalf("15분 넘게 막힌 전달은 이상: %+v", v)
	}
	if v := e.delivery("agent", "이벤트 → 에이전트", nil).Run(context.Background()); v.State != check.OK {
		t.Fatalf("%+v", v)
	}
	if v := e.delivery("mind", "이벤트 → mind", nil).Run(context.Background()); v.State != check.Unknown {
		t.Fatalf("구독 전: %+v", v)
	}
	if v := e.service("svc.events", "이벤트 로그", srv.URL, nil, "drain").Run(context.Background()); v.State != check.OK {
		t.Fatalf("이벤트 로그 자신은 outbox 만 봅니다: %+v", v)
	}
	if notify.Deps[0] != "svc.events" || notify.Deps[1] != "svc.notify" {
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

func TestIMessageCollector(t *testing.T) {
	st := map[string]any{"last_poll_at": time.Now().Unix(), "last_error": "", "total": 12}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(st)
	}))
	defer srv.Close()
	e := freshEnv()
	e.IMessageURL = srv.URL
	if v := e.imessageCollector(context.Background()); v.State != check.OK {
		t.Fatalf("%+v", v)
	}
	st["last_poll_at"] = time.Now().Add(-time.Hour).Unix()
	if v := e.imessageCollector(context.Background()); v.State != check.Fail {
		t.Fatalf("한 시간 동안 읽지 않음: %+v", v)
	}
}
