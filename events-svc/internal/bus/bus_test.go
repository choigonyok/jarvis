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
	"time"

	"github.com/choigonyok/jarvis/events-svc/internal/store"
	"github.com/choigonyok/jarvis/gocommon/health"
)

type fakeStore struct {
	mu     sync.Mutex
	events []store.Event
	sub    store.Subscription
	head   int64
	held   map[int64]*store.HeldEvent
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
	f.sub.Failures, f.sub.HeadTries, f.head = 0, 0, 0
	return nil
}
func (f *fakeStore) Failed(_ context.Context, _ string, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sub.Failures++
	f.sub.LastError = msg
	return nil
}
func (f *fakeStore) ItemFailed(_ context.Context, _ string, event int64, msg string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.head != event {
		f.head, f.sub.HeadTries = event, 0
	}
	f.sub.HeadTries++
	f.sub.Failures++
	f.sub.LastError = msg
	return f.sub.HeadTries, nil
}
func (f *fakeStore) Hold(_ context.Context, _ string, event int64, tries int, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[int64]*store.HeldEvent{}
	}
	for _, e := range f.events {
		if e.ID == event {
			f.held[event] = &store.HeldEvent{Event: e, Tries: tries, LastError: msg}
		}
	}
	f.sub.Cursor = max(f.sub.Cursor, event)
	f.sub.Failures, f.sub.HeadTries, f.head = 0, 0, 0
	return nil
}
func (f *fakeStore) ReleaseHeld(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for _, h := range f.held {
		if h.NextTry == nil {
			h.NextTry = &now
		}
	}
	return nil
}
func (f *fakeStore) DueHeld(context.Context, string, int) ([]store.HeldEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.HeldEvent
	for _, h := range f.held {
		if h.NextTry != nil {
			out = append(out, *h)
		}
	}
	return out, nil
}
func (f *fakeStore) HeldDelivered(_ context.Context, _ string, event int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, event)
	return nil
}
func (f *fakeStore) HeldFailed(_ context.Context, _ string, event int64, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := f.held[event]; h != nil {
		h.Tries++
		h.LastError, h.NextTry = msg, nil
	}
	return nil
}

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

	n, _, err := b.pass(ctx, "c")
	if err == nil || n != 2 || st.sub.Cursor != 3 {
		t.Fatalf("첫 번째: n=%d err=%v cursor=%d (assets.band 에서 멈춰야 함)", n, err, st.sub.Cursor)
	}
	n, _, err = b.pass(ctx, "c")
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

// consumer is a stand-in consumer: /health answers while up, and /consume
// fails on the types in failOn with 500.
type consumer struct {
	mu     sync.Mutex
	up     bool
	health string // its health check's path: "/health" unless set (the agent's is /healthz)
	status int    // when set, every /consume gets it (an outage: 503, 401 ...)
	failOn map[string]bool
	got    []string
}

func (c *consumer) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if r.URL.Path == c.healthPath() {
			if !c.up {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		if r.URL.Path == "/health" || r.URL.Path == "/healthz" {
			http.NotFound(w, r)
			return
		}
		var e store.Event
		_ = json.NewDecoder(r.Body).Decode(&e)
		switch {
		case c.status != 0:
			w.WriteHeader(c.status)
		case c.failOn[e.Type]:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			c.got = append(c.got, e.Type)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (c *consumer) healthPath() string {
	if c.health != "" {
		return c.health
	}
	return "/health"
}

func (c *consumer) taken() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.got...)
}

// One event the consumer cannot take no longer holds every event behind it:
// after health.MaxTries, while the consumer is up, it is held and the cursor
// moves past it. When the consumer takes it later (fixed and restarted), it
// goes once more.
func TestAnEventThatKeepsFailingIsHeldAndTheRestGoOn(t *testing.T) {
	t.Run("/health", func(t *testing.T) { heldAndOn(t, "") })
	t.Run("/healthz", func(t *testing.T) { heldAndOn(t, "/healthz") }) // the agent
}

func heldAndOn(t *testing.T, healthPath string) {
	c := &consumer{up: true, health: healthPath, failOn: map[string]bool{"assets.band": true}}
	srv := c.serve(t)
	st := &fakeStore{
		events: []store.Event{{ID: 1, Type: "assets.band"}, {ID: 2, Type: "spending.big"}},
		sub:    store.Subscription{Consumer: "c", URL: srv.URL + "/consume", Types: []string{"*"}},
	}
	b := New(st, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	for i := 1; i < health.MaxTries; i++ {
		n, wait, err := b.pass(ctx, "c")
		if err == nil || n != 0 || st.sub.Cursor != 0 || wait <= 0 {
			t.Fatalf("시도 %d: 그 자리에서 기다려야 합니다 n=%d wait=%v err=%v cursor=%d", i, n, wait, err, st.sub.Cursor)
		}
	}
	n, _, err := b.pass(ctx, "c")
	if err != nil || n != 1 || st.sub.Cursor != 2 || len(st.held) != 1 {
		t.Fatalf("다섯 번째: 격리하고 넘어가야 합니다 n=%d err=%v cursor=%d held=%d", n, err, st.sub.Cursor, len(st.held))
	}
	if got := c.taken(); len(got) != 1 || got[0] != "spending.big" {
		t.Fatalf("뒤의 이벤트는 갔어야 합니다: %v", got)
	}

	// Fixed and back: it registers again, and what was held goes.
	c.mu.Lock()
	c.failOn = nil
	c.mu.Unlock()
	_ = st.ReleaseHeld(ctx, "c")
	if _, _, err := b.pass(ctx, "c"); err != nil || len(st.held) != 0 {
		t.Fatalf("다시 보내야 합니다: err=%v held=%d", err, len(st.held))
	}
	if got := c.taken(); len(got) != 2 || got[1] != "assets.band" {
		t.Fatalf("격리했던 이벤트가 가야 합니다: %v", got)
	}
}

// A consumer that is down, or refuses the token, costs its events nothing:
// no try is counted, nothing is held or skipped, and the cursor waits - a
// 401 used to be read as "never" and every event went past unseen.
func TestAnOutageWaitsWithoutCountingOrSkipping(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusBadGateway} {
		c := &consumer{up: true, status: status}
		srv := c.serve(t)
		st := &fakeStore{
			events: []store.Event{{ID: 1, Type: "x.y"}, {ID: 2, Type: "x.z"}},
			sub:    store.Subscription{Consumer: "c", URL: srv.URL + "/consume", Types: []string{"*"}},
		}
		b := New(st, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
		for i := 0; i < health.MaxTries+2; i++ {
			n, wait, err := b.pass(context.Background(), "c")
			if err == nil || n != 0 || wait != 0 {
				t.Fatalf("%d: 장애는 이벤트 탓이 아닙니다 n=%d wait=%v err=%v", status, n, wait, err)
			}
		}
		if st.sub.Cursor != 0 || len(st.held) != 0 || st.sub.HeadTries != 0 || st.sub.Failures != health.MaxTries+2 {
			t.Fatalf("%d: cursor=%d held=%d tries=%d failures=%d", status, st.sub.Cursor, len(st.held), st.sub.HeadTries, st.sub.Failures)
		}
	}

	// A 500 from a consumer whose /health is down is the consumer, not the
	// event: it is not held however often it comes.
	c := &consumer{up: false, failOn: map[string]bool{"x.y": true}}
	srv := c.serve(t)
	st := &fakeStore{
		events: []store.Event{{ID: 1, Type: "x.y"}},
		sub:    store.Subscription{Consumer: "c", URL: srv.URL + "/consume", Types: []string{"*"}},
	}
	b := New(st, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < health.MaxTries+2; i++ {
		_, _, _ = b.pass(context.Background(), "c")
	}
	if st.sub.Cursor != 0 || len(st.held) != 0 {
		t.Fatalf("살아 있지 않은 소비자의 500 은 격리하지 않습니다: cursor=%d held=%d", st.sub.Cursor, len(st.held))
	}
}

// Taking something after failing is the consumer working again: what was
// held for it goes once, and if it fails again it waits for the next time.
func TestHeldEventsGoAgainWhenTheConsumerRecovers(t *testing.T) {
	c := &consumer{up: true, status: http.StatusServiceUnavailable}
	srv := c.serve(t)
	st := &fakeStore{
		events: []store.Event{{ID: 1, Type: "a.old"}, {ID: 2, Type: "a.new"}},
		sub:    store.Subscription{Consumer: "c", URL: srv.URL + "/consume", Types: []string{"*"}, Cursor: 1},
		held:   map[int64]*store.HeldEvent{1: {Event: store.Event{ID: 1, Type: "a.old"}, Tries: health.MaxTries}},
	}
	b := New(st, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	_, _, _ = b.pass(ctx, "c") // down: a failure on record
	c.mu.Lock()
	c.status = 0
	c.failOn = map[string]bool{"a.old": true}
	c.mu.Unlock()
	if _, _, err := b.pass(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if got := c.taken(); len(got) != 1 || got[0] != "a.new" {
		t.Fatalf("%v", got)
	}
	h := st.held[1]
	if h == nil || h.Tries != health.MaxTries+1 || h.NextTry != nil {
		t.Fatalf("한 번 더 보내고, 실패하면 다음 계기를 기다립니다: %+v", h)
	}
	if _, _, err := b.pass(ctx, "c"); err != nil || st.held[1].Tries != health.MaxTries+1 {
		t.Fatalf("계기 없이 다시 보내지 않습니다: %+v", st.held[1])
	}
}
