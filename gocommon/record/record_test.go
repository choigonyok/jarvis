package record

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
)

type fakeSink struct {
	mu   sync.Mutex
	got  []string
	fail int // how many appends fail first
}

func (f *fakeSink) append(_ context.Context, r Record, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("데이터베이스가 없습니다")
	}
	f.got = append(f.got, r.SourceKey+" "+string(payload))
	return nil
}

func (f *fakeSink) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func valid(key string) Record {
	return Record{Kind: "guard.query", Source: "agent", SourceKey: key, Actor: "guard", Tier: Durable, Payload: map[string]string{"a": "b"}}
}

func start(t *testing.T, w *Writer) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("기다린 일이 일어나지 않았습니다")
}

func TestCheckNamesWhatIsMissing(t *testing.T) {
	r := Record{Kind: "x", Source: "agent", Tier: Raw}
	err := r.Check()
	if err == nil || !strings.Contains(err.Error(), "source_key") || !strings.Contains(err.Error(), "actor") || !strings.Contains(err.Error(), "occurred_at") {
		t.Fatalf("빠진 칸을 말해야 합니다: %v", err)
	}
	r = valid("k")
	r.OccurredAt = time.Now()
	r.Tier = "forever"
	if r.Check() == nil {
		t.Fatal("층은 둘뿐입니다")
	}
}

// A nil Writer is what a process without a database has.
func TestANilWriterDoesNothing(t *testing.T) {
	var w *Writer
	w.Write(valid("k"))
	w.Run(context.Background())
	if w.Dropped() != 0 {
		t.Fatal("없는 Writer 는 셀 것도 없습니다")
	}
}

func TestRecordsGoInOrder(t *testing.T) {
	to := &fakeSink{}
	w := newWriter(to, health.NewLoops(), quietLog())
	stop := start(t, w)
	defer stop()
	for _, k := range []string{"a", "b", "c"} {
		w.Write(valid(k))
	}
	waitFor(t, func() bool { return len(to.seen()) == 3 })
	got := to.seen()
	if !strings.HasPrefix(got[0], "a ") || !strings.HasPrefix(got[2], "c ") {
		t.Fatalf("순서가 바뀌었습니다: %v", got)
	}
	if !strings.Contains(got[0], `{"a":"b"}`) {
		t.Fatalf("payload: %v", got)
	}
}

// A database that is away is waited for, in order, and /health says so.
func TestAnOutageWaitsInOrderAndShows(t *testing.T) {
	to := &fakeSink{fail: 2}
	loops := health.NewLoops()
	w := newWriter(to, loops, quietLog())
	w.wait = func(context.Context, time.Duration) {}
	stop := start(t, w)
	defer stop()
	w.Write(valid("first"))
	w.Write(valid("second"))
	waitFor(t, func() bool { return len(to.seen()) == 2 })
	if got := to.seen(); !strings.HasPrefix(got[0], "first ") {
		t.Fatalf("순서: %v", got)
	}
	l := loops.List()[0]
	if l.Name != LoopName || l.LastOK == nil || !strings.Contains(l.LastError, "데이터베이스가 없습니다") || l.Failing != 0 || l.Behind != nil {
		t.Fatalf("상태 탭이 볼 것: %+v", l)
	}
}

// The payment path never waits on a record: past the queue they are counted.
func TestAFullQueueDropsRatherThanBlocks(t *testing.T) {
	w := newWriter(&fakeSink{}, health.NewLoops(), quietLog())
	w.queue = make(chan queued, 1) // no Run: nothing drains it
	done := make(chan struct{})
	go func() {
		w.Write(valid("a"))
		w.Write(valid("b"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Write 가 기다렸습니다")
	}
	if w.Dropped() != 1 {
		t.Fatalf("빠진 수 %d", w.Dropped())
	}
}

func TestStoppingWritesWhatIsQueued(t *testing.T) {
	to := &fakeSink{}
	w := newWriter(to, health.NewLoops(), quietLog())
	w.Write(valid("a"))
	w.Write(valid("b"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx)
	if len(to.seen()) != 2 {
		t.Fatalf("끝나면서 남은 것을 써야 합니다: %v", to.seen())
	}
}

func TestCommandKind(t *testing.T) {
	for _, c := range []struct{ method, path, want string }{
		{"POST", "/api/reminders", "reminder.set"},
		{"PATCH", "/api/reminders/rem_1", "reminder.change"},
		{"DELETE", "/api/reminders/rem_1", "reminder.cancel"},
		{"POST", "/api/checklists", "checklist.make"},
		{"PATCH", "/api/checklists/c1", "checklist.change"},
		{"DELETE", "/api/checklists/c1", "checklist.close"},
		{"POST", "/api/checklists/c1/items/i1", "checklist.check"},
		{"POST", "/api/checklists/c1/show", "checklist.show"},
		{"POST", "/api/checklists?x=1", "checklist.make"},
		{"POST", "/api/intents/int_1/approve", "intent.answer"},
		{"POST", "/api/notes/n1", "note.decided"},
		{"POST", "/api/links", "link.decided"},
		{"POST", "/somewhere", "command"},
	} {
		if got := CommandKind(c.method, c.path); got != c.want {
			t.Errorf("%s %s: %s", c.method, c.path, got)
		}
	}
}

// Over HTTP a refusal is not tried again - it would hold up everything behind
// it - and an outage is.
func TestHTTPWriter(t *testing.T) {
	var mu sync.Mutex
	var got []string
	statuses := []int{http.StatusServiceUnavailable, http.StatusCreated, http.StatusForbidden, http.StatusOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		var e struct {
			Key string `json:"source_key"`
		}
		_ = json.Unmarshal(raw, &e)
		got = append(got, r.URL.Path+" "+r.Header.Get("Authorization")+" "+e.Key)
		w.WriteHeader(statuses[0])
		statuses = statuses[1:]
	}))
	defer srv.Close()
	w := NewHTTP(srv.URL+"/", "tok", health.NewLoops(), quietLog())
	w.wait = func(context.Context, time.Duration) {}
	stop := start(t, w)
	w.Write(valid("a"))
	w.Write(valid("b"))
	w.Write(valid("c"))
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 4
	})
	stop()
	want := []string{"/records Bearer tok a", "/records Bearer tok a", "/records Bearer tok b", "/records Bearer tok c"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", got)
	}
	if NewHTTP("", "", health.NewLoops(), quietLog()) != nil {
		t.Fatal("주소가 없으면 없습니다")
	}
}
