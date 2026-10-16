package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These run against a real, throwaway Postgres with migrations 033 and 034
// applied: EVENTS_TEST_DATABASE_URL=postgres://... go test ./internal/store/
func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	dsn := os.Getenv("EVENTS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("EVENTS_TEST_DATABASE_URL 이 없어 건너뜁니다")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	resetTables(t, dsn, `truncate events, event_subscriptions, event_outbox restart identity`)
	return New(pool), pool
}

func parse(raw []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(raw, &e); err != nil || e.Type == "" {
		return e, errors.New("type 이 없습니다")
	}
	return e, nil
}

func TestDrainMovesOutboxIntoLogOnce(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	for _, p := range []string{
		`{"source":"a","type":"x.one","key":"k1"}`,
		`{"source":"a","type":""}`,
		`{"source":"a","type":"x.one","key":"k1"}`,
		`{"source":"a","type":"x.two"}`,
	} {
		if _, err := pool.Exec(ctx, `insert into event_outbox (payload) values ($1)`, p); err != nil {
			t.Fatal(err)
		}
	}
	var rejected []int64
	n, err := s.Drain(ctx, 100, parse, func(id int64, _ error) { rejected = append(rejected, id) })
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(rejected) != 1 || rejected[0] != 2 {
		t.Fatalf("moved %d, rejected %v; want 2 moved and row 2 rejected", n, rejected)
	}
	var left int
	_ = pool.QueryRow(ctx, `select count(*) from event_outbox`).Scan(&left)
	events, _ := s.After(ctx, 0, 10)
	if left != 0 || len(events) != 2 || events[0].Type != "x.one" || events[1].Type != "x.two" {
		t.Fatalf("outbox left %d, log %+v", left, events)
	}
}

func TestPruneKeepsWhatAConsumerHasNotTaken(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if _, _, err := s.Append(ctx, Event{Source: "a", Type: "x.y"}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = pool.Exec(ctx, `update events set received_at = now() - interval '100 days'`)
	if _, err := s.Subscribe(ctx, "slow", "http://x", []string{"*"}, true); err != nil {
		t.Fatal(err)
	}
	_ = s.Advance(ctx, "slow", 2, true)
	n, err := s.Prune(ctx, 90*24*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d (%v); want the 2 the consumer is past", n, err)
	}
	if events, _ := s.After(ctx, 0, 10); len(events) != 2 || events[0].ID != 3 {
		t.Fatalf("left %+v", events)
	}
}

// B-8: a consumer that has taken nothing for longer than the log is kept
// does not hold it forever - only a live one does - and it can be removed.
func TestStalledConsumerDoesNotHoldTheLog(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if _, _, err := s.Append(ctx, Event{Source: "a", Type: "x.y"}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = pool.Exec(ctx, `update events set received_at = now() - interval '100 days'`)
	if _, err := s.Subscribe(ctx, "live", "http://x", []string{"*"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe(ctx, "dead", "http://y", []string{"*"}, true); err != nil {
		t.Fatal(err)
	}
	_ = s.Advance(ctx, "live", 3, true)
	_, _ = pool.Exec(ctx, `update event_subscriptions set updated_at = now() - interval '100 days', last_ok_at = null where consumer = 'dead'`)

	n, err := s.Prune(ctx, 90*24*time.Hour)
	if err != nil || n != 3 {
		t.Fatalf("pruned %d (%v); want the 3 the live consumer is past, whatever the dead one holds", n, err)
	}
	if ok, err := s.Unsubscribe(ctx, "dead"); err != nil || !ok {
		t.Fatalf("unsubscribe: %v %v", ok, err)
	}
	if ok, _ := s.Unsubscribe(ctx, "dead"); ok {
		t.Fatal("a second unsubscribe finds nothing")
	}
	subs, _ := s.Subscriptions(ctx)
	if len(subs) != 1 || subs[0].Consumer != "live" {
		t.Fatalf("left %+v", subs)
	}
}

// Many appends at once still commit in id order: a reader that saw id n has
// already seen everything below it.
func TestConcurrentAppendsCommitInOrder(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var bad []string
	var mu sync.Mutex
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			events, _ := s.After(ctx, 0, 1000)
			for i, e := range events {
				if e.ID != int64(i+1) {
					mu.Lock()
					bad = append(bad, "gap before "+strings.TrimSpace(e.Type))
					mu.Unlock()
					break
				}
			}
		}
	}()
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.Append(ctx, Event{Source: "a", Type: "x.y"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(stop)
	if len(bad) > 0 {
		t.Fatalf("a reader saw a gap %d times", len(bad))
	}
}

// resetTables empties tables as the owner when scripts/dbtest gives one
// (JARVIS_TEST_ADMIN_DSN): the service's own role may not TRUNCATE, and the
// code under test runs as that role.
func resetTables(t *testing.T, dsn, sql string) {
	t.Helper()
	if admin := os.Getenv("JARVIS_TEST_ADMIN_DSN"); admin != "" {
		dsn = admin
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}
