package api

import (
	"context"
	"encoding/base64"
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
	"github.com/choigonyok/jarvis/gocommon/seal"
	chat "github.com/choigonyok/jarvis/input-svc/internal/chat/store"
	"github.com/choigonyok/jarvis/input-svc/internal/chat/turn"
	"github.com/choigonyok/jarvis/input-svc/internal/store"
	workout "github.com/choigonyok/jarvis/input-svc/internal/workout/store"
)

const (
	token      = "services"
	guestToken = "guest"
)

var testSeal, _ = seal.New(make([]byte, 32))

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
	views := Views{Chat: chat.New(pool), Workout: workout.New(pool), Seal: testSeal, TZ: "Asia/Seoul"}
	srv := httptest.NewServer(New(store.New(pool), token, guestToken, views, loops, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
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
	body := fmt.Sprintf(`{"kind":"a.b","source":"t","source_key":%q,"actor":"t","occurred_at":"2026-10-31T02:00:00Z","tier":"raw","payload":{"x":1}}`, key)
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
	body := fmt.Sprintf(`{"kind":"approval.pending","source":"agent","source_key":%q,"actor":"agent","occurred_at":"2026-10-31T02:00:00Z","tier":"raw","payload":{"notify":{"tier":"now","title":"카드","audience":"owner"}}}`, key)
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
	body = fmt.Sprintf(`{"kind":"mind.card","source":"agent","source_key":%q,"actor":"agent","occurred_at":"2026-10-31T02:00:00Z","tier":"raw","payload":{"a":1}}`, unique("k"))
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
	body := fmt.Sprintf(`{"kind":"a.b","source":"t","source_key":%q,"actor":"t","occurred_at":"2026-10-31T02:00:00Z","tier":"raw"}`, key)
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
		fmt.Sprintf(`{"kind":"whole.way","source":"t","source_key":%q,"actor":"t","occurred_at":"2026-10-31T02:00:00Z","tier":"raw","subject":"s","payload":{"n":7,"notify":{"tier":"log","title":"t"}}}`, key)); err != nil {
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

// A turn said is a record, with its words sealed, and a turn the console reads.
func TestATurnIsARecord(t *testing.T) {
	srv, owner := setup(t)
	ctx := context.Background()
	thread := fmt.Sprintf("t%d", time.Now().UnixNano()%1_000_000_000)
	code, out := call(t, http.MethodPost, srv.URL+"/turns", token, fmt.Sprintf(`{"thread":%q,"role":"user","text":"치과 예약했어"}`, thread))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	var saved turn.Turn
	if err := json.Unmarshal(out, &saved); err != nil || saved.ID == "" {
		t.Fatalf("%s %v", out, err)
	}
	var kind, actor, subject, tier string
	var raw []byte
	if err := owner.QueryRow(ctx, `select kind, actor, subject, tier, payload from input.records where source = 'chat' and subject = $1`, "chat:"+thread).
		Scan(&kind, &actor, &subject, &tier, &raw); err != nil {
		t.Fatal(err)
	}
	if kind != "chat.turn" || actor != "operator" || tier != "durable" {
		t.Fatalf("%s %s %s", kind, actor, tier)
	}
	if strings.Contains(string(raw), "치과") {
		t.Fatalf("글은 봉인돼야 합니다: %s", raw)
	}
	var p struct {
		Thread string `json:"thread"`
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	sealed, err := base64.StdEncoding.DecodeString(p.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := testSeal.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	var back turn.Turn
	if err := json.Unmarshal(plain, &back); err != nil || back.Text != "치과 예약했어" || back.ID != saved.ID || back.AtISO == "" {
		t.Fatalf("%+v %v", back, err)
	}
	if code, out := call(t, http.MethodGet, srv.URL+"/turns?limit=5&thread="+thread, token, ""); code != http.StatusOK || !strings.Contains(string(out), "치과") {
		t.Fatalf("%d %s", code, out)
	}

	// A card given its newest word is a record too.
	code, out = call(t, http.MethodPost, srv.URL+"/turns", token, fmt.Sprintf(`{"thread":%q,"role":"agent","paragraphs":["지켜봅니다"],"origin":{"kind":"job","job":1,"run":2}}`, thread))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	var card turn.Turn
	_ = json.Unmarshal(out, &card)
	time.Sleep(5 * time.Millisecond)
	if code, out := call(t, http.MethodPut, srv.URL+"/turns/"+card.ID, token, fmt.Sprintf(`{"thread":%q,"paragraphs":["끝났습니다"]}`, thread)); code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	var n int
	if err := owner.QueryRow(ctx, `select count(*) from input.records where subject = $1`, "chat:"+thread).Scan(&n); err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
}

// What the guest says is the guest's: nothing of it on the operator's stream.
func TestTheGuestsTurnIsTheGuests(t *testing.T) {
	srv, owner := setup(t)
	ctx := context.Background()
	marker := unique("손님")
	code, out := call(t, http.MethodPost, srv.URL+"/turns", guestToken, fmt.Sprintf(`{"thread":"guest","role":"user","text":%q}`, marker))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	var saved turn.Turn
	_ = json.Unmarshal(out, &saved)
	var actor string
	if err := owner.QueryRow(ctx, `select actor from input.guest_records where source_key like $1`, "turn:"+saved.ID+":%").Scan(&actor); err != nil || actor != "guest" {
		t.Fatalf("%q %v", actor, err)
	}
	var n int
	if err := owner.QueryRow(ctx, `select count(*) from input.records where source_key like $1`, "turn:"+saved.ID+":%").Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}

// A save is a record of what changed, and the log reads it back at once. What
// the guest saves is on the same book, on the guest's stream.
func TestAWorkoutSaveIsARecord(t *testing.T) {
	srv, owner := setup(t)
	ctx := context.Background()
	session := func(id string) string {
		return fmt.Sprintf(`{"sessions":[{"id":%q,"date":"2026-10-31","startedAt":1793383465000,"endedAt":1793387065000,
			"exercises":[{"id":"e1","name":"스쿼트","sets":[{"weight":100,"reps":5,"done":true}]}]}]}`, id)
	}
	id := unique("w")
	code, out := call(t, http.MethodPost, srv.URL+"/workout/changes", token, session(id))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	var saved struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(out, &saved); err != nil || saved.Revision == 0 {
		t.Fatalf("%s %v", out, err)
	}
	var kind, actor string
	var raw []byte
	if err := owner.QueryRow(ctx, `select kind, actor, payload from input.records where source = 'workout' and source_key = $1`, fmt.Sprintf("workout:%d", saved.Revision)).
		Scan(&kind, &actor, &raw); err != nil {
		t.Fatal(err)
	}
	if kind != "workout.changes" || actor != "operator" || !strings.Contains(string(raw), id) {
		t.Fatalf("%s %s %s", kind, actor, raw)
	}
	code, out = call(t, http.MethodGet, srv.URL+"/workout", guestToken, "")
	if code != http.StatusOK || !strings.Contains(string(out), id) {
		t.Fatalf("%d %s", code, out)
	}

	other := unique("w")
	code, out = call(t, http.MethodPost, srv.URL+"/workout/changes", guestToken, session(other))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	if err := json.Unmarshal(out, &saved); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(ctx, `select actor from input.guest_records where source = 'workout' and source_key = $1`, fmt.Sprintf("workout:%d", saved.Revision)).Scan(&actor); err != nil || actor != "guest" {
		t.Fatalf("%q %v", actor, err)
	}
}

// mind's deterministic lane as far as the way in sees it: it finds the record
// and leaves what came of it.
func answers(t *testing.T, owner *pgxpool.Pool, table, subject string, status int, body string) <-chan string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	done := make(chan string, 1)
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			var key, kind string
			err := owner.QueryRow(ctx, `select source_key, kind from input.`+table+` where subject = $1`, subject).Scan(&key, &kind)
			if err != nil {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if _, err := owner.Exec(ctx, `insert into mind.commands (key, kind, status, body) values ($1, $2, $3, $4)`, key, kind, status, body); err != nil {
				t.Error(err)
			}
			done <- kind
			return
		}
	}()
	return done
}

// What a screen changes goes in as a record, and the answer is what mind made
// of it, as mind said it.
func TestACommandIsARecord(t *testing.T) {
	srv, owner := setup(t)
	path := "/api/checklists/" + unique("chk") + "/items/i1"
	kind := answers(t, owner, "records", path, http.StatusOK, `{"checklist":{"id":"chk_1"}}`)
	code, out := call(t, http.MethodPost, srv.URL+"/mind"+path, token, `{"done":true}`)
	if code != http.StatusOK || !strings.Contains(string(out), "chk_1") {
		t.Fatalf("%d %s", code, out)
	}
	if got := <-kind; got != "checklist.check" {
		t.Fatalf("종류: %q", got)
	}
	var payload, actor string
	if err := owner.QueryRow(context.Background(), `select payload::text, actor from input.records where subject = $1`, path).Scan(&payload, &actor); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Method, Path string
		Body         json.RawMessage
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	if p.Method != http.MethodPost || p.Path != path || !strings.Contains(string(p.Body), "true") || actor != "operator" {
		t.Fatalf("mind 가 읽을 것: %+v %s", p, actor)
	}

	// Refused by mind is refused here, and nothing said is said as nothing.
	path = "/api/reminders/" + unique("rem")
	answers(t, owner, "records", path, http.StatusNoContent, `{"text":""}`)
	if code, out := call(t, http.MethodDelete, srv.URL+"/mind"+path, token, ""); code != http.StatusNoContent || len(out) != 0 {
		t.Fatalf("%d %s", code, out)
	}

	// An id keeps what it held: mind reads the path as it came.
	path = "/api/calendar/" + unique("s") + "%232026-10-31"
	kind = answers(t, owner, "records", path, http.StatusOK, `{"id":"e1"}`)
	if code, out := call(t, http.MethodPatch, srv.URL+"/mind"+path, token, `{"title":"옮김"}`); code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	if got := <-kind; got != "manual.edit" {
		t.Fatalf("종류: %q", got)
	}

	// A path no command has is not a record, nor a read one.
	if code, _ := call(t, http.MethodPost, srv.URL+"/mind/api/replay", token, "{}"); code != http.StatusNotFound {
		t.Fatalf("없는 명령: %d", code)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/mind/api/notes", token, ""); code != http.StatusMethodNotAllowed && code != http.StatusNotFound {
		t.Fatalf("읽기는 이 문이 아닙니다: %d", code)
	}
	if code, _ := call(t, http.MethodPost, srv.URL+"/mind"+path, "", "{}"); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %d", code)
	}
}

// The guest's goes to the guest's, and the guest changes only what is theirs.
func TestAGuestsCommandIsTheGuests(t *testing.T) {
	srv, owner := setup(t)
	path := "/api/checklists/" + unique("chk")
	answers(t, owner, "guest_records", path, http.StatusOK, `{"checklist":{"id":"chk_g"}}`)
	if code, out := call(t, http.MethodDelete, srv.URL+"/mind"+path, guestToken, ""); code != http.StatusOK || !strings.Contains(string(out), "chk_g") {
		t.Fatalf("%d %s", code, out)
	}
	var mine int
	if err := owner.QueryRow(context.Background(), `select count(*) from input.records where subject = $1`, path).Scan(&mine); err != nil || mine != 0 {
		t.Fatalf("운영자 기록에 %d건 %v", mine, err)
	}
	if code, _ := call(t, http.MethodPost, srv.URL+"/mind/api/spending/transactions", guestToken, "{}"); code != http.StatusForbidden {
		t.Fatalf("가계부: %d", code)
	}
}

// What the status tab reads: when a source last had something new to say, and
// the latest of each check. Saying the same again is no news.
func TestTheLastOfEachSource(t *testing.T) {
	srv, _ := setup(t)
	source := unique("src")
	post := func(kind, subject, payload string) {
		t.Helper()
		body := fmt.Sprintf(`{"kind":%q,"source":%q,"source_key":%q,"actor":"t","subject":%q,"occurred_at":"2026-10-31T02:00:00Z","tier":"raw","payload":%s}`,
			kind, source, unique("k"), subject, payload)
		if code, out := call(t, http.MethodPost, srv.URL+"/records", token, body); code != http.StatusCreated {
			t.Fatalf("%d %s", code, out)
		}
	}
	type last struct {
		Sources []struct {
			Kind, Source string
			At           time.Time
		}
		Checks []struct {
			ID   string
			Body json.RawMessage
		}
	}
	read := func() last {
		t.Helper()
		code, out := call(t, http.MethodGet, srv.URL+"/last", token, "")
		if code != http.StatusOK {
			t.Fatalf("%d %s", code, out)
		}
		var l last
		if err := json.Unmarshal(out, &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	at := func(kind string) time.Time {
		t.Helper()
		for _, s := range read().Sources {
			if s.Kind == kind && s.Source == source {
				return s.At
			}
		}
		t.Fatalf("%s 가 없습니다", kind)
		return time.Time{}
	}
	post("a.b", "x", `{"n":1}`)
	first := at("a.b")
	post("a.b", "x", `{"n":1}`)
	if got := at("a.b"); !got.Equal(first) {
		t.Fatalf("같은 말은 소식이 아닙니다: %v %v", first, got)
	}
	post("a.b", "x", `{"n":2}`)
	if got := at("a.b"); !got.After(first) {
		t.Fatalf("새 말: %v %v", first, got)
	}

	id := unique("check")
	post("check.result", id, `{"id":"`+id+`","state":"warn","summary":"처음"}`)
	post("check.result", id, `{"id":"`+id+`","state":"ok","summary":"나중"}`)
	found := 0
	for _, c := range read().Checks {
		if c.ID == id {
			found++
			if !strings.Contains(string(c.Body), "나중") {
				t.Fatalf("%s", c.Body)
			}
		}
	}
	if found != 1 {
		t.Fatalf("점검 하나에 한 줄: %d", found)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/last", guestToken, ""); code != http.StatusUnauthorized {
		t.Fatalf("손님 토큰으로: %d", code)
	}
}

// What is said is a record before anything reads it, with its words sealed, and
// the agent reads them back by the record's key - only the one who said it.
func TestWhatIsSaidWaitsAtTheWayIn(t *testing.T) {
	srv, owner := setup(t)
	code, out := call(t, http.MethodPost, srv.URL+"/chat/messages", token, `{"text":"  비밀 이야기 ","images":["a.jpg"],"ref":{"id":"u-1-1"}}`)
	if code != http.StatusAccepted {
		t.Fatalf("%d %s", code, out)
	}
	var made struct{ Key string }
	if err := json.Unmarshal(out, &made); err != nil || made.Key == "" {
		t.Fatalf("%s %v", out, err)
	}
	var payload, subject, actor string
	if err := owner.QueryRow(context.Background(), `select payload::text, subject, actor from input.records where kind = 'chat.message' and source_key = $1`, made.Key).Scan(&payload, &subject, &actor); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "비밀") || subject != "chat:" || actor != "operator" {
		t.Fatalf("봉인: %s %s %s", payload, subject, actor)
	}
	code, out = call(t, http.MethodGet, srv.URL+"/chat/messages/"+made.Key, token, "")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, out)
	}
	var said struct {
		Text   string
		Images []string
		Ref    struct{ ID string }
	}
	if err := json.Unmarshal(out, &said); err != nil {
		t.Fatal(err)
	}
	if said.Text != "비밀 이야기" || len(said.Images) != 1 || said.Ref.ID != "u-1-1" {
		t.Fatalf("%+v", said)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/chat/messages/"+made.Key, guestToken, ""); code != http.StatusNotFound {
		t.Fatalf("손님은 운영자의 말을 못 읽습니다: %d", code)
	}

	code, out = call(t, http.MethodPost, srv.URL+"/chat/messages", guestToken, `{"text":"손님 말"}`)
	if code != http.StatusAccepted {
		t.Fatalf("%d %s", code, out)
	}
	if err := json.Unmarshal(out, &made); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(context.Background(), `select subject, actor from input.guest_records where source_key = $1`, made.Key).Scan(&subject, &actor); err != nil {
		t.Fatal(err)
	}
	if subject != "chat:guest" || actor != "guest" {
		t.Fatalf("%s %s", subject, actor)
	}
	if code, _ := call(t, http.MethodPost, srv.URL+"/chat/messages", token, `{"text":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("빈 말: %d", code)
	}
}

// A press on a card is a record, sealed, and it hears how it went from whoever
// took it: the agent, with mind nowhere in it.
func TestAnAnswerIsARecordAndHearsHowItWent(t *testing.T) {
	srv, owner := setup(t)
	id := unique("p")
	type out struct {
		code int
		body []byte
	}
	done := make(chan out, 1)
	go func() {
		code, body := call(t, http.MethodPost, srv.URL+"/chat/cards/"+id+"/decision", token, `{"decision":"approved","edits":{"memo":"비밀 메모"}}`)
		done <- out{code, body}
	}()
	var key, payload string
	for i := 0; i < 500; i++ {
		err := owner.QueryRow(context.Background(), `select source_key, payload::text from input.records where kind = 'card.answer' and subject = $1`, "card:"+id).Scan(&key, &payload)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if key == "" || strings.Contains(payload, "비밀") {
		t.Fatalf("봉인: %q %q", key, payload)
	}
	code, body := call(t, http.MethodGet, srv.URL+"/chat/answers/"+key, token, "")
	if code != http.StatusOK || !strings.Contains(string(body), "비밀 메모") {
		t.Fatalf("%d %s", code, body)
	}
	if code, _ := call(t, http.MethodGet, srv.URL+"/chat/answers/"+key, guestToken, ""); code != http.StatusNotFound {
		t.Fatalf("손님은 운영자의 답을 못 읽습니다: %d", code)
	}
	if code, _ := call(t, http.MethodPost, srv.URL+"/chat/answers/"+key+"/reply", token, `{"status":409,"error":"이미 결재된 요청입니다."}`); code != http.StatusNoContent {
		t.Fatalf("%d", code)
	}
	select {
	case got := <-done:
		if got.code != http.StatusConflict || !strings.Contains(string(got.body), "이미") {
			t.Fatalf("%d %s", got.code, got.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("누른 쪽이 듣지 못했습니다")
	}
	// Nobody waiting any more is fine.
	if code, _ := call(t, http.MethodPost, srv.URL+"/chat/answers/none/reply", token, `{"status":204}`); code != http.StatusNoContent {
		t.Fatalf("%d", code)
	}
}
