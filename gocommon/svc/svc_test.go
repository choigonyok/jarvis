package svc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSettingsFromTheEnvironment(t *testing.T) {
	t.Setenv("SVC_TEST_S", "")
	if got := Env("SVC_TEST_S", "기본"); got != "기본" {
		t.Fatalf("비면 기본값: %q", got)
	}
	t.Setenv("SVC_TEST_S", "값")
	if got := Env("SVC_TEST_S", "기본"); got != "값" {
		t.Fatalf("%q", got)
	}
	for v, want := range map[string]int{"": 90, "30": 30, "0": 0, "-1": 90, "삼십": 90, "1.5": 90} {
		t.Setenv("SVC_TEST_N", v)
		if got := Int("SVC_TEST_N", 90); got != want {
			t.Errorf("Int(%q) = %d, want %d", v, got, want)
		}
	}
	for v, want := range map[string]time.Duration{"": time.Second, "2m": 2 * time.Minute, "0s": 0, "-1s": time.Second, "곧": time.Second} {
		t.Setenv("SVC_TEST_D", v)
		if got := Duration("SVC_TEST_D", time.Second); got != want {
			t.Errorf("Duration(%q) = %v, want %v", v, got, want)
		}
	}
	for v, want := range map[string]float64{"": 1, "2.5": 2.5, "0": 0, "x": 1} {
		t.Setenv("SVC_TEST_F", v)
		if got := Float("SVC_TEST_F", 1); got != want {
			t.Errorf("Float(%q) = %v, want %v", v, got, want)
		}
	}
}

// exited is what code a call stopped the process with, -1 when it did not.
func exited(t *testing.T, call func()) int {
	t.Helper()
	code := -1
	exit = func(c int) { code = c }
	t.Cleanup(func() { exit = os.Exit })
	call()
	return code
}

// What a service cannot run without stops it at the start; the token may be
// left out only when a local trial says so.
func TestWhatAServiceNeedsStopsItWhenMissing(t *testing.T) {
	t.Setenv("SVC_TEST_NEED", "")
	if code := exited(t, func() { Need(quiet, "SVC_TEST_NEED") }); code != 1 {
		t.Fatalf("비어 있으면 멈춥니다: %d", code)
	}
	t.Setenv("SVC_TEST_NEED", "postgres://x")
	var got string
	if code := exited(t, func() { got = Need(quiet, "SVC_TEST_NEED") }); code != -1 || got != "postgres://x" {
		t.Fatalf("%d %q", code, got)
	}

	t.Setenv("API_TOKEN", "")
	t.Setenv("API_TOKEN_OPTIONAL", "")
	if code := exited(t, func() { Token(quiet) }); code != 1 {
		t.Fatalf("토큰 없이는 뜨지 않습니다: %d", code)
	}
	t.Setenv("API_TOKEN_OPTIONAL", "true")
	if code := exited(t, func() { got = Token(quiet) }); code != -1 || got != "" {
		t.Fatalf("로컬 시험: %d %q", code, got)
	}
	t.Setenv("API_TOKEN", "tok")
	if code := exited(t, func() { got = Token(quiet) }); code != -1 || got != "tok" {
		t.Fatalf("%d %q", code, got)
	}
}

func TestTheTokenGuardsARoute(t *testing.T) {
	ask := func(h http.HandlerFunc, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	ok := func(w http.ResponseWriter, r *http.Request) { WriteJSON(w, http.StatusOK, map[string]bool{"ok": true}) }
	guarded := Guard("tok", ok)
	if rec := ask(guarded, "Bearer tok"); rec.Code != http.StatusOK {
		t.Fatalf("맞는 토큰: %d", rec.Code)
	}
	for _, auth := range []string{"", "tok", "Bearer to", "Bearer tok2", "bearer tok"} {
		rec := ask(guarded, auth)
		var body map[string]string
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if rec.Code != http.StatusUnauthorized || body["error"] != "인증이 필요합니다." {
			t.Errorf("%q: %d %v", auth, rec.Code, body)
		}
	}
	if rec := ask(Guard("", ok), ""); rec.Code != http.StatusOK {
		t.Fatalf("토큰이 없는 로컬 시험은 엽니다: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if Bearer(req, "") {
		t.Fatal("빈 토큰은 아무것과도 맞지 않습니다")
	}
	req.Header.Set("Authorization", "Bearer ")
	if Bearer(req, "") {
		t.Fatal("빈 토큰은 아무것과도 맞지 않습니다")
	}
}

func TestAnswersAreJSONAndNeverCached(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		rec.Header().Get("Cache-Control") != "no-store" || strings.TrimSpace(rec.Body.String()) != `{"error":"본문을 읽을 수 없습니다."}` {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

// serve runs s on a port of its own and is its address and what Serve
// returned, once it has.
func serve(t *testing.T, ctx context.Context, s Server) (string, <-chan error) {
	t.Helper()
	got := make(chan string, 1)
	listen = func(network, _ string) (net.Listener, error) {
		ln, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			got <- ln.Addr().String()
		}
		return ln, err
	}
	t.Cleanup(func() { listen = net.Listen })
	s.Log = quiet
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, s) }()
	select {
	case addr := <-got:
		return "http://" + addr, done
	case err := <-done:
		t.Fatalf("듣지 못했습니다: %v", err)
	}
	return "", done
}

// Told to stop, it takes nothing new and lets what is in flight finish.
func TestServeLetsWhatIsInFlightFinish(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	started := make(chan struct{})
	addr, done := serve(t, ctx, Server{Name: "test-svc", Read: time.Second, Write: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			time.Sleep(300 * time.Millisecond)
			_, _ = io.WriteString(w, "끝")
		})})
	answered := make(chan string, 1)
	go func() {
		res, err := http.Get(addr + "/slow")
		if err != nil {
			answered <- err.Error()
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		answered <- string(b)
	}()
	<-started
	stop()
	if got := <-answered; got != "끝" {
		t.Fatalf("진행 중이던 요청: %q", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("내려간 서버는 nil: %v", err)
	}
	if _, err := http.Get(addr + "/again"); err == nil {
		t.Fatal("내린 뒤에도 받았습니다")
	}
}

// An address it cannot listen on is an error, not a service that carries on
// unreachable.
func TestServeSaysWhenItCannotListen(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	err = Serve(context.Background(), Server{Name: "test-svc", Addr: taken.Addr().String(), Handler: http.NotFoundHandler(), Log: quiet})
	if err == nil {
		t.Fatal("쓰이고 있는 주소에서 듣기 시작했습니다")
	}
}

// A handler that waits as long as the write limit loses its answer; one that
// gives itself more time gets it out.
func TestALongWaitExtendsItsOwnDeadline(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	wait := func(extend bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if extend {
				Extend(w, time.Second)
			}
			time.Sleep(400 * time.Millisecond)
			WriteJSON(w, http.StatusAccepted, map[string]string{"error": "아직 처리하지 못했습니다."})
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/plain", wait(false))
	mux.HandleFunc("/extended", wait(true))
	addr, _ := serve(t, ctx, Server{Name: "test-svc", Read: time.Second, Write: 400 * time.Millisecond, Handler: mux})
	if res, err := http.Get(addr + "/plain"); err == nil {
		res.Body.Close()
		t.Fatalf("쓰기 한도를 넘긴 답이 나갔습니다: %d", res.StatusCode)
	}
	res, err := http.Get(addr + "/extended")
	if err != nil {
		t.Fatalf("늘린 답: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("%d", res.StatusCode)
	}
}

// A stream outlives both limits: it lifts the write limit, and net/http lifts
// the read limit for a request without a body.
func TestAStreamOutlivesTheServersLimits(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ended := make(chan time.Duration, 1)
	addr, _ := serve(t, ctx, Server{Name: "test-svc", Read: 200 * time.Millisecond, Write: 300 * time.Millisecond,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rc := Stream(w)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			beat := time.NewTicker(50 * time.Millisecond)
			defer beat.Stop()
			for {
				select {
				case <-r.Context().Done():
					ended <- time.Since(start)
					return
				case <-beat.C:
				}
				if _, err := io.WriteString(w, ": beat\n\n"); err != nil || rc.Flush() != nil {
					ended <- time.Since(start)
					return
				}
			}
		})})
	cctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, addr+"/stream", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	beats := 0
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if sc.Text() == ": beat" {
			beats++
		}
	}
	res.Body.Close()
	select {
	case d := <-ended:
		if d < time.Second {
			t.Fatalf("스트림이 %v 만에 끊겼습니다 (읽기 200ms·쓰기 300ms)", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("손님이 떠난 것을 알아채지 못했습니다")
	}
	if beats < 10 {
		t.Fatalf("심장 박동 %d번", beats)
	}
}
