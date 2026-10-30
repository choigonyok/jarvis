package calls

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
)

func TestTheListIsWhatMayBeWritten(t *testing.T) {
	for _, c := range []struct {
		service, method, path string
		want                  bool
	}{
		{"calendar", "PUT", "/events", true},
		{"calendar", "DELETE", "/events/abc", true},
		{"calendar", "POST", "/events", false},
		{"spending", "PATCH", "/transactions/12", true},
		{"spending", "DELETE", "/transactions/12", false},
		{"assets", "POST", "/orders", true},
		{"assets", "POST", "/orders/rebalance", true},
		{"assets", "DELETE", "/orders", false},
		{"jobs", "POST", "/jobs/4/cancel", true},
		{"immich", "POST", "/api/albums", true},
		{"vault", "POST", "/secrets", false},
		{"browser", "POST", "/anything", false},
	} {
		if got := Allowed(c.service, c.method, c.path); got != c.want {
			t.Errorf("%s %s %s: %v", c.service, c.method, c.path, got)
		}
	}
}

var seq atomic.Int64

func setup(t *testing.T, h http.Handler) (*Hand, *record.Memory, *atomic.Int64) {
	t.Helper()
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없습니다 (scripts/dbtest output-svc)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, mem := record.NewMemory(log)
	go w.Run(ctx)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	return &Hand{Services: map[string]Service{"assets": {URL: srv.URL, Auth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }}},
		Pool: pool, Records: w, HTTP: srv.Client(), Log: log}, mem, &hits
}

func message(path string, deadline time.Time) queue.Message {
	raw, _ := json.Marshal(map[string]any{"service": "assets", "method": "POST", "path": path, "body": json.RawMessage(`{"a":1}`), "deadline": deadline})
	return queue.Message{Kind: KindCall, SourceKey: fmt.Sprintf("call-%d-%d", time.Now().UnixNano(), seq.Add(1)), Payload: raw}
}

func result(t *testing.T, mem *record.Memory, key string) (int, string) {
	t.Helper()
	r, ok := mem.Find(func(r record.Record) bool { return r.SourceKey == "result:"+key })
	if !ok {
		t.Fatalf("결과가 없습니다: %+v", mem.All())
	}
	raw, _ := json.Marshal(r.Payload)
	var p struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p.Status, string(p.Body)
}

// A write goes to the adapter with the hand's own credentials, and what came
// of it goes back as a record.
func TestACallIsMade(t *testing.T) {
	h, mem, hits := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer tok" || string(body) != `{"a":1}` {
			t.Errorf("%s %s", r.Header.Get("Authorization"), body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	m := message("/orders", time.Now().Add(time.Minute))
	if err := h.Take(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	status, body := result(t, mem, m.SourceKey)
	if status != http.StatusCreated || !strings.Contains(body, "ok") || hits.Load() != 1 {
		t.Fatalf("%d %s %d", status, body, hits.Load())
	}
}

// Delivered twice it is made once: an order is not placed twice.
func TestACallIsMadeOnce(t *testing.T) {
	h, mem, hits := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	m := message("/orders", time.Now().Add(time.Minute))
	for i := 0; i < 2; i++ {
		if err := h.Take(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("%d 번", hits.Load())
	}
	result(t, mem, m.SourceKey)
}

// One under way when the process died is answered as not known: it may have
// gone, so it is not tried again.
func TestACallCutOffIsNotTriedAgain(t *testing.T) {
	h, mem, hits := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	ctx := context.Background()
	m := message("/orders", time.Now().Add(time.Minute))
	if _, err := h.Pool.Exec(ctx, `insert into output.calls (key, service, method, path) values ($1, 'assets', 'POST', '/orders')`, m.SourceKey); err != nil {
		t.Fatal(err)
	}
	if err := h.Take(ctx, m); err != nil {
		t.Fatal(err)
	}
	status, body := result(t, mem, m.SourceKey)
	if status != http.StatusConflict || hits.Load() != 0 || !strings.Contains(body, "모릅니다") {
		t.Fatalf("%d %s %d", status, body, hits.Load())
	}
}

// Past its deadline it is reported failed already: it must not happen after all.
func TestALateCallIsNotMade(t *testing.T) {
	h, mem, hits := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	m := message("/orders", time.Now().Add(-time.Second))
	if err := h.Take(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if status, _ := result(t, mem, m.SourceKey); status != http.StatusGone || hits.Load() != 0 {
		t.Fatalf("%d %d", status, hits.Load())
	}
}

// What is not on the list is not made.
func TestAWriteOffTheListIsRefused(t *testing.T) {
	h, mem, hits := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	m := message("/secrets", time.Now().Add(time.Minute))
	if err := h.Take(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if status, _ := result(t, mem, m.SourceKey); status != http.StatusForbidden || hits.Load() != 0 {
		t.Fatalf("%d %d", status, hits.Load())
	}
}

// An adapter that cannot be reached is an answer, for the one who waits.
func TestAnAdapterAwayIsAnAnswer(t *testing.T) {
	h, mem, _ := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.Services["assets"] = Service{URL: "http://127.0.0.1:1"}
	m := message("/orders", time.Now().Add(time.Minute))
	if err := h.Take(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if status, _ := result(t, mem, m.SourceKey); status != http.StatusBadGateway {
		t.Fatalf("%d", status)
	}
}
