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
	resetTables(t, dsn, `truncate events, event_subscriptions, event_outbox, event_held restart identity`)
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
	if _, err := s.Subscribe(ctx, "slow", "http://x", []string{"*"}, true, false); err != nil {
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
	if _, err := s.Subscribe(ctx, "live", "http://x", []string{"*"}, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe(ctx, "dead", "http://y", []string{"*"}, true, false); err != nil {
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

// The held box (066): an event failing on its own is counted against it,
// held and stepped past; it survives pruning, goes again when the consumer
// registers again, and leaves the box once delivered or dropped.
func TestHeldEventsAreSetAsideAndKept(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, _, err := s.Append(ctx, Event{Source: "a", Type: "x.y"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Subscribe(ctx, "c", "http://c/consume", []string{"*"}, true, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Failed(ctx, "c", "닿지 못함"); err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		if tries, err := s.ItemFailed(ctx, "c", 1, "500"); err != nil || tries != want {
			t.Fatalf("tries %d (%v), want %d", tries, err, want)
		}
	}
	if tries, _ := s.ItemFailed(ctx, "c", 2, "500"); tries != 1 {
		t.Fatalf("다른 이벤트는 처음부터 셉니다: %d", tries)
	}
	subs, _ := s.Subscriptions(ctx)
	if subs[0].FailingSince == nil || subs[0].Failures != 5 {
		t.Fatalf("%+v", subs[0])
	}
	waiting, err := s.Waiting(ctx)
	if err != nil || waiting["c"].IsZero() {
		t.Fatalf("커서 뒤에서 기다리는 이벤트: %v %v", waiting, err)
	}

	if err := s.Hold(ctx, "c", 1, 5, "500"); err != nil {
		t.Fatal(err)
	}
	subs, _ = s.Subscriptions(ctx)
	if subs[0].Cursor != 1 || subs[0].Failures != 0 || subs[0].FailingSince != nil || subs[0].Held != 1 {
		t.Fatalf("격리하면 넘어가고 실패가 지워집니다: %+v", subs[0])
	}
	_ = s.Advance(ctx, "c", 3, true)
	_, _ = pool.Exec(ctx, `update events set received_at = now() - interval '100 days'`)
	if n, err := s.Prune(ctx, 90*24*time.Hour); err != nil || n != 2 {
		t.Fatalf("격리된 이벤트는 지우지 않습니다: %d %v", n, err)
	}
	if due, _ := s.DueHeld(ctx, "c", 10); len(due) != 0 {
		t.Fatalf("계기 전에는 다시 보내지 않습니다: %+v", due)
	}
	if _, err := s.Subscribe(ctx, "c", "http://c/consume", []string{"*"}, false, false); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueHeld(ctx, "c", 10)
	if err != nil || len(due) != 1 || due[0].Event.ID != 1 || due[0].Tries != 5 {
		t.Fatalf("다시 구독하면 한 번 더: %+v %v", due, err)
	}
	if err := s.HeldFailed(ctx, "c", 1, "또 500"); err != nil {
		t.Fatal(err)
	}
	held, _ := s.Held(ctx, 10)
	if len(held) != 1 || held[0].Tries != 6 || held[0].NextTry != nil || held[0].LastError != "또 500" {
		t.Fatalf("%+v", held)
	}
	if ok, _ := s.RetryHeld(ctx, "c", 1); !ok {
		t.Fatal("사람이 다시 보내기")
	}
	if err := s.HeldDelivered(ctx, "c", 1); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.DropHeld(ctx, "c", 1); ok {
		t.Fatal("전해진 것은 격리함에 없습니다")
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

// A guest's subscription (071) is registered as the guest's and stays so:
// registering it again with the other side's token changes nothing and
// says so, and the same goes for a services' subscription.
func TestASubscriptionStaysOnItsSide(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.Subscribe(ctx, "agent", "http://agent:8080/consume", []string{"*"}, false, false); err != nil {
		t.Fatal(err)
	}
	sub, err := s.Subscribe(ctx, "agent-guest", "http://agent-guest:8081/consume", []string{"remind.*"}, false, true)
	if err != nil || !sub.Guest || sub.URL != "http://agent-guest:8081/consume" {
		t.Fatalf("손님 구독: %+v %v", sub, err)
	}
	if sub, err := s.Subscribe(ctx, "agent-guest", "http://agent-guest:8081/consume", []string{"remind.*", "checklist.shown"}, false, true); err != nil || !sub.Guest || len(sub.Types) != 2 {
		t.Fatalf("손님이 다시 등록: %+v %v", sub, err)
	}
	if _, err := s.Subscribe(ctx, "agent", "http://evil/consume", []string{"*"}, false, true); !errors.Is(err, ErrOtherSide) {
		t.Fatalf("손님 쪽으로 서비스 구독을 바꾸면: %v", err)
	}
	if _, err := s.Subscribe(ctx, "agent-guest", "http://agent:8080/consume", []string{"*"}, false, false); !errors.Is(err, ErrOtherSide) {
		t.Fatalf("서비스 쪽으로 손님 구독을 바꾸면: %v", err)
	}
	subs, err := s.Subscriptions(ctx)
	if err != nil || len(subs) != 2 {
		t.Fatalf("%+v %v", subs, err)
	}
	for _, sub := range subs {
		switch sub.Consumer {
		case "agent":
			if sub.Guest || sub.URL != "http://agent:8080/consume" {
				t.Fatalf("서비스 구독이 바뀌었습니다: %+v", sub)
			}
		case "agent-guest":
			if !sub.Guest || sub.URL != "http://agent-guest:8081/consume" || len(sub.Types) != 2 {
				t.Fatalf("손님 구독이 바뀌었습니다: %+v", sub)
			}
		}
	}
}
