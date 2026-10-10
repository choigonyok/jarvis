package bus

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/choigonyok/jarvis/events-svc/internal/store"
)

type fakeStore struct {
	mu     sync.Mutex
	events []store.Event
	sub    store.Subscription
}

func (f *fakeStore) After(_ context.Context, cursor int64, limit int) ([]store.Event, error) {
	var out []store.Event
	for _, e := range f.events {
		if e.ID > cursor && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}
func (f *fakeStore) Subscriptions(context.Context) ([]store.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []store.Subscription{f.sub}, nil
}
func (f *fakeStore) Advance(_ context.Context, _ string, cursor int64, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cursor > f.sub.Cursor {
		f.sub.Cursor = cursor
	}
	return nil
}
func (f *fakeStore) Failed(context.Context, string, string) error { return nil }

func TestMatches(t *testing.T) {
	cases := []struct {
		pats []string
		typ  string
		want bool
	}{
		{[]string{"*"}, "spending.big", true},
		{[]string{"assets.*"}, "assets.band", true},
		{[]string{"assets.*"}, "assetsx.band", false},
		{[]string{"memory.plan"}, "memory.plan", true},
		{[]string{"memory.plan"}, "memory.planx", false},
	}
	for _, c := range cases {
		if got := Matches(c.pats, c.typ); got != c.want {
			t.Errorf("Matches(%v, %q) = %v", c.pats, c.typ, got)
		}
	}
}

func TestPassDeliversInOrderSkipsRefusedStopsOnFailure(t *testing.T) {
	var got []string
	fail := map[string]int{"assets.band": 500} // first try fails
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e store.Event
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &e)
		if code, ok := fail[e.Type]; ok {
			delete(fail, e.Type)
			w.WriteHeader(code)
			return
		}
		if e.Type == "spending.bad" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		got = append(got, e.Type)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := &fakeStore{
		events: []store.Event{
			{ID: 1, Type: "spending.big"},
			{ID: 2, Type: "calendar.partner"}, // not subscribed
			{ID: 3, Type: "spending.bad"},     // refused for good
			{ID: 4, Type: "assets.band"},      // fails once
			{ID: 5, Type: "assets.flow"},
		},
		sub: store.Subscription{Consumer: "c", URL: srv.URL, Types: []string{"spending.*", "assets.*"}},
	}
	b := New(st, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	n, err := b.pass(ctx, "c")
	if err == nil || n != 2 || st.sub.Cursor != 3 {
		t.Fatalf("첫 번째: n=%d err=%v cursor=%d (assets.band 에서 멈춰야 함)", n, err, st.sub.Cursor)
	}
	n, err = b.pass(ctx, "c")
	if err != nil || n != 2 || st.sub.Cursor != 5 {
		t.Fatalf("두 번째: n=%d err=%v cursor=%d", n, err, st.sub.Cursor)
	}
	want := []string{"spending.big", "assets.band", "assets.flow"}
	if len(got) != len(want) {
		t.Fatalf("전달: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("순서: %v", got)
		}
	}
}
