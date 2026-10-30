package mindcmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/record"
)

func writer(t *testing.T) (*record.Writer, *record.Memory) {
	t.Helper()
	w, m := record.NewMemory(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go w.Run(ctx)
	return w, m
}

// fakeLane is mind's window: nothing until the lane got to it, then the answer.
func fakeLane(t *testing.T, misses int64, status int, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var seen atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/commands/cmd-") || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if seen.Add(1) <= misses {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "body": json.RawMessage(body)})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// What is asked goes out as a record, and what mind stored comes back.
func TestDoWritesARecordAndReadsTheAnswer(t *testing.T) {
	w, mem := writer(t)
	srv, seen := fakeLane(t, 2, 200, `{"reminder":{"id":"rem_1","line":"내일 8시"}}`)
	c := &Client{URL: srv.URL, Token: "tok", Records: w}
	var out struct {
		Reminder struct{ ID, Line string } `json:"reminder"`
	}
	if err := c.Do(context.Background(), http.MethodPost, "/api/reminders", map[string]string{"text": "약"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Reminder.Line != "내일 8시" || seen.Load() != 3 {
		t.Fatalf("%+v %d", out, seen.Load())
	}
	r, ok := mem.Find(func(r record.Record) bool { return r.Kind == "reminder.set" })
	if !ok {
		t.Fatalf("기록이 없습니다: %+v", mem.All())
	}
	if r.Source != "agent" || r.Actor != "operator" || r.Channel != "tool" || r.Subject != "/api/reminders" || r.Tier != record.Raw || !strings.HasPrefix(r.SourceKey, "cmd-") {
		t.Fatalf("%+v", r)
	}
	raw, _ := json.Marshal(r.Payload)
	var p struct {
		Method, Path string
		Body         map[string]string
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.Method != "POST" || p.Path != "/api/reminders" || p.Body["text"] != "약" {
		t.Fatalf("%s %v", raw, err)
	}
}

// What mind refused comes back in mind's words.
func TestARefusalIsAnError(t *testing.T) {
	w, _ := writer(t)
	srv, _ := fakeLane(t, 0, 400, `{"error":"이미 지난 시각입니다"}`)
	err := (&Client{URL: srv.URL, Token: "tok", Records: w}).Do(context.Background(), http.MethodPost, "/api/reminders", nil, nil)
	if err == nil || err.Error() != "이미 지난 시각입니다" {
		t.Fatalf("%v", err)
	}
}

// mind down is what the record is there to outlast: the tool says so, and the
// record stands.
func TestMindDownLeavesTheRecord(t *testing.T) {
	w, mem := writer(t)
	c := &Client{URL: "http://127.0.0.1:1", Token: "tok", Records: w, Wait: 120 * time.Millisecond}
	err := c.Do(context.Background(), http.MethodDelete, "/api/reminders/rem_1", nil, nil)
	if !errors.Is(err, ErrWaiting) {
		t.Fatalf("%v", err)
	}
	if _, ok := mem.Find(func(r record.Record) bool { return r.Kind == "reminder.cancel" }); !ok {
		t.Fatal("기록이 남아야 합니다")
	}
}

// The guest's are the guest's.
func TestTheActorIsWhoAsked(t *testing.T) {
	w, mem := writer(t)
	srv, _ := fakeLane(t, 0, 200, `{}`)
	c := &Client{URL: srv.URL, Token: "tok", Records: w, Actor: "guest"}
	if err := c.Do(context.Background(), http.MethodPost, "/api/checklists", nil, nil); err != nil {
		t.Fatal(err)
	}
	if r, ok := mem.Find(func(r record.Record) bool { return r.Kind == "checklist.make" }); !ok || r.Actor != "guest" {
		t.Fatalf("%+v", r)
	}
}

func TestNoWriterIsAnError(t *testing.T) {
	if err := (&Client{}).Do(context.Background(), http.MethodPost, "/api/reminders", nil, nil); err == nil {
		t.Fatal("기록할 곳이 없으면 오류입니다")
	}
}
