package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
)

var seq atomic.Int64

func unique(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq.Add(1))
}

// setup is the store under input-svc's role, and the owner beside it to look.
func setup(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn, admin := os.Getenv("JARVIS_TEST_DSN"), os.Getenv("JARVIS_TEST_ADMIN_DSN")
	if dsn == "" || admin == "" {
		t.Skip("JARVIS_TEST_DSN 이 없습니다 (scripts/dbtest input-svc)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	owner, err := pgxpool.New(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	return New(pool), owner
}

func put(t *testing.T, db *pgxpool.Pool, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `insert into input.outbox (payload) values ($1)`, raw); err != nil {
		t.Fatal(err)
	}
}

func consumer(t *testing.T, db *pgxpool.Pool, table string, kinds ...string) string {
	t.Helper()
	name := unique("c")
	if table == "input.guest_consumers" {
		name += "-guest"
	}
	if _, err := db.Exec(context.Background(), `insert into `+table+` (name, kinds) values ($1, $2)`, name, kinds); err != nil {
		t.Fatal(err)
	}
	return name
}

func drainAll(t *testing.T, s *Store) {
	t.Helper()
	for {
		n, err := s.Drain(context.Background(), 200)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

func count(t *testing.T, db *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// One row in, one record out, with a delivery only for those who want it, in
// one go - and the outbox is empty after.
func TestDrainStoresAndPublishes(t *testing.T) {
	s, owner := setup(t)
	wants := consumer(t, owner, "input.consumers", "a.*")
	other := consumer(t, owner, "input.consumers", "b.*")
	key := unique("k")
	put(t, owner, map[string]any{"kind": "a.b", "source": "t", "source_key": key, "actor": "t", "occurred_at": time.Now(), "tier": "raw", "subject": "s"})
	drainAll(t, s)
	if n := count(t, owner, `select count(*) from input.outbox where payload->>'source_key' = $1`, key); n != 0 {
		t.Fatalf("outbox 에 %d 행 남음", n)
	}
	var id int64
	var at time.Time
	if err := owner.QueryRow(context.Background(), `select id, received_at from input.records where source = 't' and source_key = $1`, key).Scan(&id, &at); err != nil {
		t.Fatal(err)
	}
	var subject string
	var dAt time.Time
	if err := owner.QueryRow(context.Background(), `select subject, at from input.deliveries where consumer = $1 and id = $2`, wants, id).Scan(&subject, &dAt); err != nil {
		t.Fatal(err)
	}
	if subject != "s" || !dAt.Equal(at) {
		t.Fatalf("배달: %q %v (받은 시각 %v)", subject, dAt, at)
	}
	if n := count(t, owner, `select count(*) from input.deliveries where consumer = $1`, other); n != 0 {
		t.Fatalf("원하지 않은 소비자에게 %d 건", n)
	}
}

// A sender may say the same thing twice; it is stored once and delivered once.
func TestSayingItTwiceIsOnce(t *testing.T) {
	s, owner := setup(t)
	name := consumer(t, owner, "input.consumers", "*")
	key := unique("k")
	e := map[string]any{"source": "t", "type": "a", "key": key, "notify": map[string]any{"tier": "log", "title": "x"}}
	put(t, owner, e)
	put(t, owner, e)
	drainAll(t, s)
	put(t, owner, e)
	drainAll(t, s)
	if n := count(t, owner, `select count(*) from input.records where source = 't' and source_key = $1`, key); n != 1 {
		t.Fatalf("기록 %d 건", n)
	}
	if n := count(t, owner, `select count(*) from input.deliveries where consumer = $1`, name); n != 1 {
		t.Fatalf("배달 %d 건", n)
	}
}

// What is for the guest goes to the guest's tables, to those who read them,
// and nowhere near the operator's.
func TestTheGuestsGoToTheGuestsTables(t *testing.T) {
	s, owner := setup(t)
	reads := consumer(t, owner, "input.guest_consumers", "*")
	operators := consumer(t, owner, "input.consumers", "*")
	key := unique("k")
	put(t, owner, map[string]any{"source": "context", "type": "location.guest.enter", "key": key,
		"data": map[string]any{"region": "집"}, "notify": map[string]any{"tier": "log", "title": "도착", "audience": "guest"}})
	drainAll(t, s)
	if n := count(t, owner, `select count(*) from input.records where source_key = $1`, key); n != 0 {
		t.Fatal("운영자 기록에 들어갔습니다")
	}
	var id int64
	var actor, title string
	if err := owner.QueryRow(context.Background(), `select id, actor, payload->'notify'->>'title' from input.guest_records where source_key = $1`, key).Scan(&id, &actor, &title); err != nil {
		t.Fatal(err)
	}
	if actor != "guest" || title != "도착" {
		t.Fatalf("%s %s", actor, title)
	}
	if n := count(t, owner, `select count(*) from input.guest_deliveries where consumer = $1 and id = $2`, reads, id); n != 1 {
		t.Fatalf("손님 배달 %d 건", n)
	}
	if n := count(t, owner, `select count(*) from input.deliveries where consumer = $1`, operators); n != 0 {
		t.Fatalf("운영자 쪽 배달 %d 건", n)
	}
}

// A row that will never be a record stays in sight, and does not hold up the
// next.
func TestABadRowIsSetAside(t *testing.T) {
	s, owner := setup(t)
	key := unique("k")
	put(t, owner, map[string]any{"nonsense": true, "marker": key})
	put(t, owner, map[string]any{"source": "t", "type": "a", "key": key})
	drainAll(t, s)
	if n := count(t, owner, `select count(*) from input.records where source_key = $1`, key); n != 1 {
		t.Fatalf("다음 행이 막혔습니다: %d", n)
	}
	if n := count(t, owner, `select count(*) from input.rejected where payload->>'marker' = $1`, key); n != 1 {
		t.Fatalf("거절 %d 건", n)
	}
	list, total, err := s.Rejections(context.Background(), 5)
	if err != nil || total < 1 || len(list) == 0 || list[0].Why == "" {
		t.Fatalf("%+v %d %v", list, total, err)
	}
}

// Over HTTP is the same door: one record, delivered, repeated safely.
func TestPutIsTheSameDoor(t *testing.T) {
	s, owner := setup(t)
	name := consumer(t, owner, "input.consumers", "*")
	e := envelopeFor(unique("k"))
	id, dup, err := s.Put(context.Background(), e)
	if err != nil || dup || id == 0 {
		t.Fatalf("%d %v %v", id, dup, err)
	}
	if _, dup, err := s.Put(context.Background(), e); err != nil || !dup {
		t.Fatalf("%v %v", dup, err)
	}
	if n := count(t, owner, `select count(*) from input.deliveries where consumer = $1 and id = $2`, name, id); n != 1 {
		t.Fatalf("배달 %d 건", n)
	}
}

// Past its retention the raw tier goes, its key with it; what was decided
// stays, and so does what a consumer has yet to take.
func TestPruneTakesOnlyWhatIsDue(t *testing.T) {
	s, owner := setup(t)
	ctx := context.Background()
	old, durable, waiting, fresh := unique("old"), unique("durable"), unique("waiting"), unique("fresh")
	name := consumer(t, owner, "input.consumers", "*")
	back := time.Now().Add(-200 * 24 * time.Hour)
	insert := func(key, tier string, at time.Time) int64 {
		t.Helper()
		var id int64
		if err := owner.QueryRow(ctx, `insert into input.records (kind, source, source_key, actor, occurred_at, received_at, tier)
			values ('a', 'prune', $1, 'x', $2, $2, $3) returning id`, key, at, tier).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, `insert into input.record_keys (source, source_key) values ('prune', $1)`, key); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert(old, "raw", back)
	insert(durable, "durable", back)
	id := insert(waiting, "raw", back)
	insert(fresh, "raw", time.Now())
	if _, err := owner.Exec(ctx, `insert into input.deliveries (consumer, id, at) values ($1, $2, $3)`, name, id, back); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Prune(ctx, 90*24*time.Hour); err != nil || n < 1 {
		t.Fatalf("%d %v", n, err)
	}
	for key, want := range map[string]int{old: 0, durable: 1, waiting: 1, fresh: 1} {
		if n := count(t, owner, `select count(*) from input.records where source = 'prune' and source_key = $1`, key); n != want {
			t.Fatalf("%s: %d", key, n)
		}
	}
	if n := count(t, owner, `select count(*) from input.record_keys where source = 'prune' and source_key = $1`, old); n != 0 {
		t.Fatal("지운 기록의 열쇠가 남았습니다")
	}
}

func TestPartitionsAreMadeAhead(t *testing.T) {
	s, owner := setup(t)
	ctx := context.Background()
	if _, err := s.Partitions(ctx, 40); err != nil {
		t.Fatal(err)
	}
	var name *string
	far := time.Now().AddDate(3, 0, 0).Format("2006_01")
	if err := owner.QueryRow(ctx, `select to_regclass('input.records_' || $1)::text`, far).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name == nil {
		t.Fatalf("records_%s 파티션이 없습니다", far)
	}
	if n, err := s.Partitions(ctx, 40); err != nil || n != 0 {
		t.Fatalf("두 번째는 할 일이 없습니다: %d %v", n, err)
	}
}

// The states /health reads, under input-svc's role.
func TestItCanReadTheQueue(t *testing.T) {
	s, owner := setup(t)
	name := consumer(t, owner, "input.consumers", "*")
	key := unique("k")
	put(t, owner, map[string]any{"source": "t", "type": "a", "key": key})
	drainAll(t, s)
	for _, topic := range []queue.Topic{queue.Records, queue.Guest} {
		if _, err := queue.States(context.Background(), s.Pool(), topic); err != nil {
			t.Fatal(err)
		}
		if _, err := queue.Held(context.Background(), s.Pool(), topic, 10); err != nil {
			t.Fatal(err)
		}
	}
	states, _ := queue.States(context.Background(), s.Pool(), queue.Records)
	for _, c := range states {
		if c.Name == name && c.Oldest == nil {
			t.Fatal("기다리는 것이 보여야 합니다")
		}
	}
	if d, err := s.Oldest(context.Background()); err != nil || d < 0 {
		t.Fatalf("%v %v", d, err)
	}
}

func envelopeFor(key string) envelope.Envelope {
	return envelope.Envelope{Kind: "a", Source: "t", SourceKey: key, Actor: "t", OccurredAt: time.Now(), Tier: "raw", SchemaVersion: 1, Payload: json.RawMessage("{}")}
}
