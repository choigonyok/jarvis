package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/health"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("JARVIS_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_ADMIN_DSN 이 없습니다 (scripts/dbtest gocommon)")
	}
	return dsn
}

func open(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// as is a pool under a role of the stack.
func as(t *testing.T, role string) *pgxpool.Pool {
	t.Helper()
	return open(t, strings.Replace(adminDSN(t), "jarvis:test@", role+":test@", 1))
}

var seq atomic.Int64

func unique(t *testing.T) string {
	return fmt.Sprintf("q-%d-%d", time.Now().UnixNano(), seq.Add(1))
}

// world is a consumer under a fresh name and a way to publish to it as
// input-svc does.
type world struct {
	t     *testing.T
	admin *pgxpool.Pool
	topic Topic
	name  string
}

func newWorld(t *testing.T, topic Topic) *world {
	t.Helper()
	w := &world{t: t, admin: open(t, adminDSN(t)), topic: topic, name: unique(t)}
	if _, err := w.admin.Exec(context.Background(), `insert into `+topic.Consumers+` (name, kinds) values ($1, $2)`, w.name, []string{"*"}); err != nil {
		t.Fatal(err)
	}
	return w
}

// publish stores a record and leaves it for the world's consumer.
func (w *world) publish(kind, subject string) int64 {
	w.t.Helper()
	ctx := context.Background()
	tx, err := w.admin.Begin(ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var id int64
	var at time.Time
	err = tx.QueryRow(ctx, `select r_id, r_at from input.append($1, $2, 'test', $3, 'test', '', $4, now(), 'raw', 1, '', '', '{"a":1}'::jsonb)`,
		w.topic == Guest, kind, unique(w.t), subject).Scan(&id, &at)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := Deliver(ctx, tx, w.topic, []Sub{{Name: w.name, Kinds: []string{"*"}}}, id, at, kind, subject); err != nil {
		w.t.Fatal(err)
	}
	if err := Wake(ctx, tx, w.topic); err != nil {
		w.t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) consumer(pool *pgxpool.Pool, h func(context.Context, Message) error) *Consumer {
	return &Consumer{Pool: pool, Topic: w.topic, Name: w.name, Kinds: []string{"*"}, Handle: h, Log: quiet(),
		Poll: 20 * time.Millisecond, Backoff: func(int) time.Duration { return time.Millisecond }}
}

// start runs a consumer until the test ends or the returned stop is called.
func start(t *testing.T, c *Consumer) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("끝내 %s 되지 않았습니다", what)
}

func (w *world) state() State {
	w.t.Helper()
	states, err := States(context.Background(), w.admin, w.topic)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, s := range states {
		if s.Name == w.name {
			return s
		}
	}
	w.t.Fatalf("소비자 %s 가 없습니다", w.name)
	return State{}
}

func (w *world) left() int {
	w.t.Helper()
	var n int
	if err := w.admin.QueryRow(context.Background(), `select count(*) from `+w.topic.Deliveries+` where consumer = $1`, w.name).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

type log struct {
	mu   sync.Mutex
	seen []int64
}

func (l *log) add(id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, id)
}

func (l *log) list() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int64(nil), l.seen...)
}

func TestDeliversInOrderAndCleansUp(t *testing.T) {
	w := newWorld(t, Records)
	var ids []int64
	for i := 0; i < 5; i++ {
		ids = append(ids, w.publish("a.b", ""))
	}
	seen := &log{}
	start(t, w.consumer(w.admin, func(_ context.Context, m Message) error {
		seen.add(m.ID)
		if m.Kind != "a.b" || m.Source != "test" || string(m.Payload) != `{"a": 1}` || m.Tier != "raw" {
			return fmt.Errorf("%w: %+v", ErrRefused, m)
		}
		return nil
	}))
	eventually(t, "다 받게", func() bool { return len(seen.list()) == 5 })
	eventually(t, "배달 행이 지워지게", func() bool { return w.left() == 0 })
	for i, id := range seen.list() {
		if id != ids[i] {
			t.Fatalf("순서: %v, 낸 것 %v", seen.list(), ids)
		}
	}
	eventually(t, "커서가 끝까지 오르게", func() bool { return w.state().Cursor == ids[4] })
	if s := w.state(); s.LastOK == nil || s.Failures != 0 || s.Oldest != nil {
		t.Fatalf("%+v", s)
	}
}

// A notification wakes it long before the poll comes round.
func TestWakesOnNotify(t *testing.T) {
	w := newWorld(t, Records)
	got := make(chan struct{}, 1)
	c := w.consumer(w.admin, func(context.Context, Message) error { got <- struct{}{}; return nil })
	c.Poll = time.Minute
	start(t, c)
	eventually(t, "등록되게", func() bool {
		var n int
		_ = w.admin.QueryRow(context.Background(), `select count(*) from pg_stat_activity where query like 'listen %'`).Scan(&n)
		return n > 0
	})
	w.publish("a.b", "")
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("깨우지 못했습니다")
	}
}

// One message failing on its own goes again, waits behind nothing else of
// another subject, and after five tries is held with why; what follows gets
// through, and the cursor passes it.
func TestAMessageThatKeepsFailingIsHeld(t *testing.T) {
	w := newWorld(t, Records)
	bad := w.publish("a.bad", "x")
	good := w.publish("a.good", "y")
	var tries atomic.Int64
	seen := &log{}
	start(t, w.consumer(w.admin, func(_ context.Context, m Message) error {
		if m.ID == bad {
			tries.Add(1)
			return errors.New("망가진 메시지")
		}
		seen.add(m.ID)
		return nil
	}))
	eventually(t, "다음 것이 지나가게", func() bool { return len(seen.list()) == 1 && seen.list()[0] == good })
	eventually(t, "격리되게", func() bool { return w.state().Held == 1 })
	if n := tries.Load(); n != health.MaxTries {
		t.Fatalf("시도 %d 번", n)
	}
	held, err := Held(context.Background(), w.admin, w.topic, 200)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, h := range held {
		if h.Consumer == w.name && h.ID == bad {
			found = true
			if h.Kind != "a.bad" || h.Tries != health.MaxTries || !strings.Contains(h.LastError, "망가진") {
				t.Fatalf("%+v", h)
			}
		}
	}
	if !found {
		t.Fatalf("격리함: %+v", held)
	}
	eventually(t, "실패 표시가 지워지게", func() bool { s := w.state(); return s.Failures == 0 && s.FailingSince == nil })
	eventually(t, "커서가 넘어가게", func() bool { return w.state().Cursor == good })
}

// An earlier message of a subject that is not done holds up its subject and
// no other, however many workers there are.
func TestOrderIsKeptPerSubject(t *testing.T) {
	w := newWorld(t, Records)
	first := w.publish("a", "s")
	second := w.publish("a", "s")
	other := w.publish("a", "t")
	release := make(chan struct{})
	seen := &log{}
	c := w.consumer(w.admin, func(_ context.Context, m Message) error {
		if m.ID == first {
			<-release
		}
		seen.add(m.ID)
		return nil
	})
	c.Workers = 3
	start(t, c)
	eventually(t, "다른 주어가 먼저 지나가게", func() bool { return len(seen.list()) == 1 && seen.list()[0] == other })
	time.Sleep(100 * time.Millisecond)
	if got := seen.list(); len(got) != 1 {
		t.Fatalf("같은 주어의 뒤 행이 앞질렀습니다: %v", got)
	}
	close(release)
	eventually(t, "나머지가 지나가게", func() bool { return len(seen.list()) == 3 })
	if got := seen.list(); got[1] != first || got[2] != second {
		t.Fatalf("순서: %v", got)
	}
}

// With a row still running the cursor stays below it: the lower bound of what
// is done, not the highest thing done.
func TestTheCursorIsTheLowerBound(t *testing.T) {
	w := newWorld(t, Records)
	slow := w.publish("a", "s")
	fast := w.publish("a", "t")
	release := make(chan struct{})
	seen := &log{}
	c := w.consumer(w.admin, func(_ context.Context, m Message) error {
		if m.ID == slow {
			<-release
		}
		seen.add(m.ID)
		return nil
	})
	c.Workers = 2
	start(t, c)
	eventually(t, "빠른 것이 끝나게", func() bool { return len(seen.list()) == 1 })
	eventually(t, "커서가 멈춰 있게", func() bool { s := w.state(); return s.LastOK != nil })
	if cur := w.state().Cursor; cur >= slow {
		t.Fatalf("앞 행이 도는데 커서가 %d (앞 행 %d)", cur, slow)
	}
	close(release)
	eventually(t, "커서가 끝까지 오르게", func() bool { return w.state().Cursor == fast })
}

// An outage costs a message no try, and it is the same message that goes
// again; when it clears, what was held goes once more.
func TestAnOutageCostsNoTry(t *testing.T) {
	w := newWorld(t, Records)
	ctx := context.Background()
	heldID := w.publish("a.held", "h")
	if _, err := w.admin.Exec(ctx, `update `+w.topic.Deliveries+` set held_at = now(), not_before = null, tries = 5 where consumer = $1 and id = $2`, w.name, heldID); err != nil {
		t.Fatal(err)
	}
	w.publish("a", "s")
	var calls, heldCalls atomic.Int64
	c := w.consumer(w.admin, func(_ context.Context, m Message) error {
		if m.ID == heldID {
			heldCalls.Add(1)
			return nil
		}
		if calls.Add(1) < 3 {
			return fmt.Errorf("%w: 메모리가 꺼졌습니다", ErrOutage)
		}
		return nil
	})
	// Register cleans the held box's waiting; put it back to see the recovery
	// do it instead.
	if err := func() error { c.defaults(); return c.register(ctx) }(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.admin.Exec(ctx, `update `+w.topic.Deliveries+` set not_before = null where consumer = $1 and id = $2`, w.name, heldID); err != nil {
		t.Fatal(err)
	}
	stop := c.workOnly()
	defer stop()
	eventually(t, "세 번 불리게", func() bool { return calls.Load() == 3 })
	eventually(t, "격리된 것이 다시 가게", func() bool { return heldCalls.Load() == 1 })
	eventually(t, "비워지게", func() bool { return w.left() == 0 })
}

// workOnly runs a worker without Run's register, which would release what the
// test put in the held box itself.
func (c *Consumer) workOnly() func() {
	ctx, cancel := context.WithCancel(context.Background())
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() { c.work(ctx, wake); close(done) }()
	return func() { cancel(); <-done }
}

// A refused message is skipped, not retried.
func TestARefusedMessageIsSkipped(t *testing.T) {
	w := newWorld(t, Records)
	w.publish("a", "")
	var calls atomic.Int64
	start(t, w.consumer(w.admin, func(context.Context, Message) error {
		calls.Add(1)
		return fmt.Errorf("%w: 안 받습니다", ErrRefused)
	}))
	eventually(t, "지워지게", func() bool { return w.left() == 0 })
	if calls.Load() != 1 {
		t.Fatalf("%d 번", calls.Load())
	}
}

// A panic is the message's failure: the worker goes on to the next.
func TestAPanicIsAFailureOfTheMessage(t *testing.T) {
	w := newWorld(t, Records)
	bad := w.publish("a", "x")
	good := w.publish("a", "y")
	seen := &log{}
	start(t, w.consumer(w.admin, func(_ context.Context, m Message) error {
		if m.ID == bad {
			panic("터짐")
		}
		seen.add(m.ID)
		return nil
	}))
	eventually(t, "다음 것이 지나가게", func() bool { return len(seen.list()) == 1 && seen.list()[0] == good })
	eventually(t, "격리되게", func() bool { return w.state().Held == 1 })
}

// A restart gives back what the last process held and sends the held box
// again: a restart is usually the fix.
func TestARestartStartsClean(t *testing.T) {
	w := newWorld(t, Records)
	ctx := context.Background()
	leased := w.publish("a", "s")
	held := w.publish("a", "t")
	if _, err := w.admin.Exec(ctx, `update `+w.topic.Deliveries+` set lease_until = now() + interval '1 hour' where consumer = $1 and id = $2`, w.name, leased); err != nil {
		t.Fatal(err)
	}
	if _, err := w.admin.Exec(ctx, `update `+w.topic.Deliveries+` set held_at = now(), not_before = null, tries = 5 where consumer = $1 and id = $2`, w.name, held); err != nil {
		t.Fatal(err)
	}
	seen := &log{}
	start(t, w.consumer(w.admin, func(_ context.Context, m Message) error { seen.add(m.ID); return nil }))
	eventually(t, "둘 다 가게", func() bool { return len(seen.list()) == 2 })
	eventually(t, "비워지게", func() bool { return w.left() == 0 })
}

// A dead worker's message goes again once its lease is up, without a restart.
func TestALeaseThatRunsOutGoesAgain(t *testing.T) {
	w := newWorld(t, Records)
	ctx := context.Background()
	c := w.consumer(w.admin, func(context.Context, Message) error { return nil })
	c.defaults()
	c.Lease = 50 * time.Millisecond
	id := w.publish("a", "")
	d, ok, err := c.claim(ctx)
	if err != nil || !ok || d.ID != id {
		t.Fatalf("%v %v %v", d, ok, err)
	}
	if _, ok, _ := c.claim(ctx); ok {
		t.Fatal("쥐고 있는 동안은 다시 집히지 않습니다")
	}
	time.Sleep(80 * time.Millisecond)
	if d, ok, err := c.claim(ctx); err != nil || !ok || d.ID != id {
		t.Fatalf("기한이 지나면 다시 집힙니다: %v %v %v", d, ok, err)
	}
}

// Stopping mid-message gives it back at no cost.
func TestStoppingGivesTheMessageBack(t *testing.T) {
	w := newWorld(t, Records)
	w.publish("a", "")
	started := make(chan struct{})
	stop := start(t, w.consumer(w.admin, func(ctx context.Context, _ Message) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}))
	<-started
	stop()
	var tries int
	var lease *time.Time
	if err := w.admin.QueryRow(context.Background(), `select tries, lease_until from `+w.topic.Deliveries+` where consumer = $1`, w.name).Scan(&tries, &lease); err != nil {
		t.Fatal(err)
	}
	if tries != 0 || lease != nil {
		t.Fatalf("tries=%d lease=%v", tries, lease)
	}
}

func TestRetryAndDrop(t *testing.T) {
	w := newWorld(t, Records)
	ctx := context.Background()
	one, two := w.publish("a", "s"), w.publish("a", "t")
	if _, err := w.admin.Exec(ctx, `update `+w.topic.Deliveries+` set held_at = now(), not_before = null, tries = 5 where consumer = $1`, w.name); err != nil {
		t.Fatal(err)
	}
	if ok, err := Retry(ctx, w.admin, w.topic, w.name, one); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, err := Drop(ctx, w.admin, w.topic, w.name, two); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, _ := Drop(ctx, w.admin, w.topic, w.name, two); ok {
		t.Fatal("이미 버린 것")
	}
	seen := &log{}
	c := w.consumer(w.admin, func(_ context.Context, m Message) error { seen.add(m.ID); return nil })
	c.defaults()
	stop := c.workOnly()
	defer stop()
	eventually(t, "다시 가게", func() bool { return len(seen.list()) == 1 && seen.list()[0] == one })
}

// Each role of the stack reads what it is there to read, with the rights it
// has - and the guest's sees the guest's tables and only its own rows there.
func TestRolesCanDoTheirPart(t *testing.T) {
	for _, role := range []string{"svc_agent", "svc_mind", "svc_notify"} {
		t.Run(role, func(t *testing.T) {
			w := newWorld(t, Records)
			id := w.publish("a", "")
			seen := &log{}
			start(t, w.consumer(as(t, role), func(_ context.Context, m Message) error { seen.add(m.ID); return nil }))
			eventually(t, "받게", func() bool { return len(seen.list()) == 1 && seen.list()[0] == id })
			eventually(t, "지워지게", func() bool { return w.left() == 0 })
		})
	}
	for _, role := range []string{"svc_mind", "svc_notify"} {
		t.Run(role+"-guest", func(t *testing.T) {
			w := newWorld(t, Guest)
			id := w.publish("a", "")
			seen := &log{}
			start(t, w.consumer(as(t, role), func(_ context.Context, m Message) error { seen.add(m.ID); return nil }))
			eventually(t, "받게", func() bool { return len(seen.list()) == 1 && seen.list()[0] == id })
			eventually(t, "지워지게", func() bool { return w.left() == 0 })
		})
	}
}

func TestTheGuestsRoleSeesOnlyItsOwn(t *testing.T) {
	ctx := context.Background()
	guest := as(t, "svc_guest")
	w := newWorld(t, Guest)
	w.publish("remind.asked", "")
	admin := w.admin
	if _, err := admin.Exec(ctx, `insert into input.guest_consumers (name, kinds) values ('agent-guest', array['remind.*']) on conflict do nothing`); err != nil {
		t.Fatal(err)
	}
	w2 := &world{t: t, admin: admin, topic: Guest, name: "agent-guest"}
	id := w2.publish("remind.asked", "")
	seen := &log{}
	start(t, w2.consumer(guest, func(_ context.Context, m Message) error { seen.add(m.ID); return nil }))
	eventually(t, "받게", func() bool { return len(seen.list()) == 1 && seen.list()[0] == id })

	var n int
	if err := guest.QueryRow(ctx, `select count(*) from input.guest_deliveries where consumer <> 'agent-guest'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("남의 배달이 보입니다: %d %v", n, err)
	}
	if tag, err := guest.Exec(ctx, `delete from input.guest_deliveries where consumer = $1`, w.name); err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("남의 배달을 지웠습니다: %v %v", tag, err)
	}
	if _, err := guest.Exec(ctx, `insert into input.guest_consumers (name, kinds) values ('notify-guest', array['*'])
		on conflict (name) do update set kinds = excluded.kinds`); err == nil {
		t.Fatal("남의 이름으로 등록할 수 없어야 합니다")
	}
	for _, q := range []string{
		`select count(*) from input.records`, `select count(*) from input.deliveries`, `select count(*) from input.consumers`,
		`select count(*) from input.outbox`, `select count(*) from mind_intents`,
	} {
		if err := guest.QueryRow(ctx, q).Scan(new(int)); err == nil {
			t.Fatalf("%s 를 읽을 수 없어야 합니다", q)
		}
	}
	if _, err := guest.Exec(ctx, `insert into input.outbox (payload) values ('{}')`); err == nil {
		t.Fatal("손님은 outbox 에 쓸 수 없습니다")
	}
	if _, err := guest.Exec(ctx, `select input.append(false, 'x', 'x', 'x', 'x', '', '', now(), 'raw', 1, '', '', '{}'::jsonb)`); err == nil {
		t.Fatal("손님은 기록을 남길 수 없습니다")
	}
}

// Only input-svc stores a record: a producer can leave one in the outbox and
// that is all.
func TestProducersOnlyLeaveItInTheOutbox(t *testing.T) {
	ctx := context.Background()
	for _, role := range []string{"svc_agent", "svc_assets", "svc_calendar", "svc_context", "svc_mind", "svc_spending", "svc_status"} {
		t.Run(role, func(t *testing.T) {
			pool := as(t, role)
			if _, err := pool.Exec(ctx, `insert into input.outbox (payload) values ('{"test":true}')`); err != nil {
				t.Fatalf("outbox 에 쓸 수 있어야 합니다: %v", err)
			}
			if err := pool.QueryRow(ctx, `select count(*) from input.outbox`).Scan(new(int)); err == nil {
				t.Fatal("outbox 를 읽을 수 없어야 합니다")
			}
			if _, err := pool.Exec(ctx, `select input.append(false, 'x', 'x', 'x', 'x', '', '', now(), 'raw', 1, '', '', '{}'::jsonb)`); err == nil {
				t.Fatal("기록을 직접 남길 수 없어야 합니다")
			}
			if _, err := pool.Exec(ctx, `insert into input.records (kind, source, source_key, actor, occurred_at, tier) values ('x', 'x', 'x', 'x', now(), 'raw')`); err == nil {
				t.Fatal("표에 직접 쓸 수 없어야 합니다")
			}
		})
	}
	admin := open(t, adminDSN(t))
	if _, err := admin.Exec(ctx, `delete from input.outbox where payload @> '{"test":true}'`); err != nil {
		t.Fatal(err)
	}
}
