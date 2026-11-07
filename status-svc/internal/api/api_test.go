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
