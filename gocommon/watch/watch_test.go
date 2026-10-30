package watch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/record"
)

func setup(t *testing.T) (*Watcher, *record.Memory) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, m := record.NewMemory(log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go w.Run(ctx)
	return &Watcher{Service: "calendar", Records: w, Loops: health.NewLoops(), Log: log}, m
}

func count(m *record.Memory, kind string) int {
	n := 0
	for _, r := range m.All() {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// Saying the same says nothing, and a change is a record, whole.
func TestOnlyAChangeIsARecord(t *testing.T) {
	w, m := setup(t)
	ctx := context.Background()
	v := map[string]int{"a": 1}
	s := Source{Kind: "calendar.near", Read: func(context.Context) (any, error) { return v, nil }}
	last, err := w.round(ctx, s, "")
	if err != nil {
		t.Fatal(err)
	}
	if last2, err := w.round(ctx, s, last); err != nil || last2 != last {
		t.Fatalf("%v %v", last2, err)
	}
	v = map[string]int{"a": 2}
	if _, err := w.round(ctx, s, last); err != nil {
		t.Fatal(err)
	}
	r, ok := m.Find(func(r record.Record) bool { return strings.Contains(string(mustJSON(r.Payload)), `"a":2`) })
	if !ok {
		t.Fatalf("%+v", m.All())
	}
	if r.Kind != "calendar.near" || r.Source != "calendar" || r.Actor != "calendar" || r.Tier != record.Raw || r.Subject != "calendar.near" {
		t.Fatalf("%+v", r)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && count(m, "calendar.near") < 2; time.Sleep(time.Millisecond) {
	}
	if n := count(m, "calendar.near"); n != 2 {
		t.Fatalf("%d", n)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// A read that fails keeps what was last said, so the same answer after it is
// not news.
func TestAFailedReadSaysNothing(t *testing.T) {
	w, m := setup(t)
	s := Source{Kind: "k", Read: func(context.Context) (any, error) { return nil, errors.New("없음") }}
	if last, err := w.round(context.Background(), s, "before"); err == nil || last != "before" {
		t.Fatalf("%v %v", last, err)
	}
	if n := len(m.All()); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestLocalReadsTheServicesOwnRoute(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/now" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"visits":[1],"at":"지금"}`))
	})
	got, err := Local(h, "tok", "/now", "visits")(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b := string(mustJSON(got)); b != `{"visits":[1]}` {
		t.Fatalf("%s", b)
	}
	whole, err := Local(h, "tok", "/now")(context.Background())
	if err != nil || !strings.Contains(string(mustJSON(whole)), "지금") {
		t.Fatalf("%s %v", mustJSON(whole), err)
	}
	if _, err := Local(h, "bad", "/now")(context.Background()); err == nil {
		t.Fatal("실패는 오류입니다")
	}
}
