package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

func handler() http.Handler {
	r := check.NewRunner(nil, []check.Probe{{ID: "x", Name: "x", Every: time.Hour,
		Run: func(context.Context) check.Verdict { return check.Verdict{State: check.OK} }}},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return New(r, "tok").Handler()
}

// The status service answers /health in the stack's contract, without the
// token, like every service it reads.
func TestHealthIsTheContract(t *testing.T) {
	rec := httptest.NewRecorder()
	handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var rep health.Report
	if err := json.NewDecoder(rec.Body).Decode(&rep); err != nil || rec.Code != http.StatusOK || rep.Status != "ok" {
		t.Fatalf("%d %+v %v", rec.Code, rep, err)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%v", rec.Header())
	}
}

func TestTheVerdictsNeedTheToken(t *testing.T) {
	h := handler()
	for _, c := range []struct {
		method, path, auth string
		want               int
	}{
		{http.MethodGet, "/status", "", http.StatusUnauthorized},
		{http.MethodGet, "/status", "Bearer nope", http.StatusUnauthorized},
		{http.MethodGet, "/status", "Bearer tok", http.StatusOK},
		{http.MethodPost, "/refresh", "", http.StatusUnauthorized},
		{http.MethodPost, "/refresh", "Bearer tok", http.StatusOK},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s %q: %d, want %d", c.method, c.path, c.auth, rec.Code, c.want)
		}
	}
}

// A refresh whose caller leaves still records what its probes found: they
// run apart from the request, not cut short with it.
func TestARefreshOutlivesItsCaller(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	runner := check.NewRunner(nil, []check.Probe{{ID: "slow", Name: "slow", Every: time.Hour,
		Run: func(ctx context.Context) check.Verdict {
			close(started)
			<-release
			if ctx.Err() != nil {
				return check.Verdict{State: check.Fail, Summary: "응답이 없어요."}
			}
			return check.Verdict{State: check.OK, Summary: "응답해요."}
		}}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := New(runner, "tok").Handler()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer tok")
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-started
	cancel()
	close(release)
	<-done
	if got := runner.State("slow"); got != check.OK {
		t.Fatalf("떠난 요청이 확인을 끊었습니다: %s", got)
	}
}
