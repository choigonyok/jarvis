package api

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

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/input-svc/internal/store"
)

const (
	token      = "services"
	guestToken = "guest"
)

var seq atomic.Int64

func unique(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq.Add(1))
}

func setup(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
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
	loops := health.NewLoops()
	loops.Add("drain", "outbox 를 기록으로", time.Minute)
	loops.OK("drain")
	srv := httptest.NewServer(New(store.New(pool), token, guestToken, loops, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return srv, owner
}

func call(t *testing.T, method, url, bearer, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestTheDoorIsLocked(t *testing.T) {
	srv, _ := setup(t)
	if code, _ := call(t, http.MethodPost, srv.URL+"/records", "", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %d", code)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/held", guestToken, ""); code != http.StatusUnauthorized {
		t.Fatalf("손님 토큰으로 격리함: %d", code)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/health", "", ""); code != http.StatusOK {
		t.Fatalf("/health 는 열려 있습니다: %d", code)
	}
}

func TestPostStoresARecord(t *testing.T) {
	srv, owner := setup(t)
	key := unique("k")
	body := fmt.Sprintf(`{"source":"t","type":"a.b","key":%q,"data":{"x":1}}`, key)
	code, out := call(t, http.MethodPost, srv.URL+"/records", token, body)
	if code != http.StatusCreated {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := call(t, http.MethodPost, srv.URL+"/records", token, body); code != http.StatusOK || !strings.Contains(string(out), "duplicate") {
		t.Fatalf("같은 것을 다시: %d %s", code, out)
	}
	var kind string
	if err := owner.QueryRow(context.Background(), `select kind from input.records where source_key = $1`, key).Scan(&kind); err != nil || kind != "a.b" {
		t.Fatalf("%q %v", kind, err)
	}
	if code, _ := call(t, http.MethodPost, srv.URL+"/records", token, `{"type":"a"}`); code != http.StatusBadRequest {
		t.Fatalf("봉투가 틀리면 400: %d", code)
	}
}

// The guest's token tells the guest something, whatever it asks, and does
// nothing else.
func TestTheGuestsTokenOnlyNotifiesTheGuest(t *testing.T) {
	srv, owner := setup(t)
	key := unique("k")
	body := fmt.Sprintf(`{"source":"agent","type":"approval.pending","key":%q,"notify":{"tier":"now","title":"카드","audience":"owner"}}`, key)
	if code, out := call(t, http.MethodPost, srv.URL+"/records", guestToken, body); code != http.StatusCreated {
		t.Fatalf("%d %s", code, out)
	}
	ctx := context.Background()
	var n int
	if err := owner.QueryRow(ctx, `select count(*) from input.records where source_key = $1`, key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("운영자 기록에 들어갔습니다: %d %v", n, err)
	}
	var actor, audience string
	if err := owner.QueryRow(ctx, `select actor, payload->'notify'->>'audience' from input.guest_records where source_key = $1`, key).Scan(&actor, &audience); err != nil {
		t.Fatal(err)
	}
	if actor != "guest" || audience != "guest" {
		t.Fatalf("%s %s", actor, audience)
	}
	body = fmt.Sprintf(`{"source":"agent","type":"mind.card","key":%q,"data":{"a":1}}`, unique("k"))
	if code, _ := call(t, http.MethodPost, srv.URL+"/records", guestToken, body); code != http.StatusForbidden {
		t.Fatalf("알림이 아닌 것: %d", code)
	}
}

// /health speaks the contract: a loop per consumer of both streams, and what
// is held - by the consumer's name and the kind, and nothing from inside it.
func TestHealthShowsDeliveryAndTheHeldBox(t *testing.T) {
	srv, owner := setup(t)
	ctx := context.Background()
	name := unique("c")
	guestName := unique("c") + "-guest"
	if _, err := owner.Exec(ctx, `insert into input.consumers (name, kinds) values ($1, array['*'])`, name); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `insert into input.guest_consumers (name, kinds) values ($1, array['*'])`, guestName); err != nil {
		t.Fatal(err)
	}
	key := unique("k")
	body := fmt.Sprintf(`{"source":"t","type":"a.b","key":%q}`, key)
	var id struct {
		ID int64 `json:"id"`
	}
	_, out := call(t, http.MethodPost, srv.URL+"/records", token, body)
	if err := json.Unmarshal(out, &id); err != nil || id.ID == 0 {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := owner.Exec(ctx, `update input.deliveries set held_at = now(), not_before = null, tries = 5, last_error = '안 됨'
		where consumer = $1 and id = $2`, name, id.ID); err != nil {
		t.Fatal(err)
	}
	code, out := call(t, http.MethodGet, srv.URL+"/health", "", "")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	var rep health.Report
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, l := range rep.Loops {
		seen[l.Name] = true
	}
	for _, want := range []string{"drain", "deliver:" + name, "deliver:" + guestName} {
		if !seen[want] {
			t.Fatalf("%s 가 없습니다: %v", want, seen)
		}
	}
	if rep.Held == nil || rep.Held.Count < 1 {
		t.Fatalf("격리함: %+v", rep.Held)
	}

	url := fmt.Sprintf("%s/held/%s/%d", srv.URL, name, id.ID)
	if code, _ := call(t, http.MethodPost, url+"/retry", token, ""); code != http.StatusAccepted {
		t.Fatalf("다시 보내기: %d", code)
	}
	var due *time.Time
	if err := owner.QueryRow(ctx, `select not_before from input.deliveries where consumer = $1 and id = $2`, name, id.ID).Scan(&due); err != nil || due == nil {
		t.Fatalf("%v %v", due, err)
	}
	if code, _ := call(t, http.MethodDelete, url, token, ""); code != http.StatusNoContent {
		t.Fatalf("버리기: %d", code)
	}
	if code, _ := call(t, http.MethodDelete, url, token, ""); code != http.StatusNotFound {
		t.Fatalf("없는 것: %d", code)
	}
	if code, _ := call(t, http.MethodDelete, srv.URL+"/held/"+name+"/x", token, ""); code != http.StatusBadRequest {
		t.Fatalf("숫자가 아닌 id: %d", code)
	}
}

// The whole way: left in the outbox, stored by input-svc, read by a consumer
// with the library, gone from the deliveries.
func TestTheWholeWay(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없습니다 (scripts/dbtest input-svc)")
	}
	_, owner := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := store.New(pool)
	key, name := unique("k"), unique("c")
	got := make(chan queue.Message, 1)
	c := &queue.Consumer{Pool: owner, Topic: queue.Records, Name: name, Kinds: []string{"whole.*"},
		Handle: func(_ context.Context, m queue.Message) error { got <- m; return nil }, Poll: 20 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	go c.Run(ctx)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var n int
		if err := owner.QueryRow(ctx, `select count(*) from input.consumers where name = $1`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("등록되지 않았습니다")
		}
	}
	if _, err := owner.Exec(ctx, `insert into input.outbox (payload) values ($1)`,
		fmt.Sprintf(`{"source":"t","type":"whole.way","key":%q,"subject":"s","data":{"n":7},"notify":{"tier":"log","title":"t"}}`, key)); err != nil {
		t.Fatal(err)
	}
	for {
		n, err := st.Drain(ctx, 200)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	select {
	case m := <-got:
		raw, err := m.Event()
		if err != nil {
			t.Fatal(err)
		}
		var ev struct {
			Type, Subject, Key string
			Data               map[string]int
			Notify             map[string]string
		}
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type != "whole.way" || ev.Subject != "s" || ev.Key != key || ev.Data["n"] != 7 || ev.Notify["title"] != "t" {
			t.Fatalf("%s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("끝까지 오지 않았습니다")
	}
}
