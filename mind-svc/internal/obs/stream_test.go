package obs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
)

func TestWhatGoesOutAndWhatComesIn(t *testing.T) {
	for _, c := range []struct {
		o    Observation
		want bool
	}{
		{Observation{Source: "mind", Type: "mind.card"}, true},
		{Observation{Source: "mind", Type: "mind.card.closed"}, true},
		{Observation{Source: "mind", Type: "mind.notice"}, true},
		{Observation{Source: "mind", Type: "remind.asked"}, true},
		{Observation{Source: "mind", Type: "checklist.shown"}, true},
		{Observation{Source: "mind", Type: "action.executed"}, false},
		{Observation{Source: "mind", Type: "action.undone"}, false},
		{Observation{Source: "kakao", Type: "message.observed"}, false},
		{Observation{Source: "spending", Type: "occurrence.observed"}, false},
	} {
		if got := IsEffect(c.o); got != c.want {
			t.Errorf("%s %s: %v", c.o.Source, c.o.Type, got)
		}
	}
}

func TestLikes(t *testing.T) {
	got := likes([]string{"message.observed", "action.*", "*"})
	if strings.Join(got, " ") != "message.observed action.% %" {
		t.Fatalf("%v", got)
	}
}

var seq atomic.Int64

func key(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq.Add(1))
}

// setup is the stream under mind's role, the owner beside it to look, and a
// consumer on each effects topic to see what is delivered.
func setup(t *testing.T) (*Stream, *pgxpool.Pool, string) {
	t.Helper()
	dsn, admin := os.Getenv("JARVIS_TEST_DSN"), os.Getenv("JARVIS_TEST_ADMIN_DSN")
	if dsn == "" || admin == "" {
		t.Skip("JARVIS_TEST_DSN 이 없습니다 (scripts/dbtest mind-svc)")
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
	name := key("c")
	for _, table := range []string{"mind.effect_consumers", "mind.guest_effect_consumers"} {
		if _, err := owner.Exec(ctx, `insert into `+table+` (name, kinds) values ($1, array['*'])`, name); err != nil {
			t.Fatal(err)
		}
	}
	return NewStream("", "", pool, []string{"message.observed", "action.*"}), owner, name
}

func one(t *testing.T, db *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// What mind decided goes straight out, with a delivery, and not through the
// outbox; what happened goes in through the outbox.
func TestAppendSortsWhatItIsGiven(t *testing.T) {
	s, owner, name := setup(t)
	ctx := context.Background()
	card := key("card")
	data, _ := json.Marshal(map[string]string{"intentId": "int_1"})
	if err := s.Append(ctx, Observation{Source: "mind", Type: "mind.card", Subject: "int_1", Data: data, Key: card, OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var id int64
	var payload string
	if err := owner.QueryRow(ctx, `select id, payload::text from mind.effects where source = 'mind' and source_key = $1`, card).Scan(&id, &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, "int_1") {
		t.Fatalf("%s", payload)
	}
	if n := one(t, owner, `select count(*) from mind.effect_deliveries where consumer = $1 and id = $2`, name, id); n != 1 {
		t.Fatalf("배달 %d", n)
	}
	if n := one(t, owner, `select count(*) from input.outbox where payload->>'key' = $1`, card); n != 0 {
		t.Fatal("효과가 받은편지함으로 들어갔습니다")
	}

	msg := key("msg")
	if err := s.Append(ctx, Observation{Source: "kakao", Type: "message.observed", Subject: "room", Key: msg, OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if n := one(t, owner, `select count(*) from input.outbox where payload->>'key' = $1`, msg); n != 1 {
		t.Fatal("일어난 일은 받은편지함으로 갑니다")
	}
	if n := one(t, owner, `select count(*) from mind.effects where source_key = $1`, msg); n != 0 {
		t.Fatal("일어난 일이 효과로 나갔습니다")
	}
}

// What is said to the guest goes to the guest's side only.
func TestAGuestsNoticeGoesToTheGuest(t *testing.T) {
	s, owner, name := setup(t)
	ctx := context.Background()
	k := key("remind")
	if err := s.Append(ctx, Observation{Source: "mind", Type: "remind.asked", Key: k, OccurredAt: time.Now(),
		Notify: json.RawMessage(`{"tier":"now","title":"약","audience":"guest"}`)}); err != nil {
		t.Fatal(err)
	}
	if n := one(t, owner, `select count(*) from mind.guest_effects where source_key = $1 and payload->'notify'->>'title' = '약'`, k); n != 1 {
		t.Fatal("손님 효과에 없습니다")
	}
	if n := one(t, owner, `select count(*) from mind.effects where source_key = $1`, k); n != 0 {
		t.Fatal("운영자 효과에 들어갔습니다")
	}
	if n := one(t, owner, `select count(*) from mind.guest_effect_deliveries d join mind.guest_effects e on e.id = d.id where d.consumer = $1 and e.source_key = $2`, name, k); n != 1 {
		t.Fatal("손님 배달이 없습니다")
	}
}

func relayMessage(kind, k string, payload string) queue.Message {
	return queue.Message{ID: 1, Kind: kind, Source: "assets", SourceKey: k, Actor: "assets", OccurredAt: time.Now(), Payload: json.RawMessage(payload)}
}

// The relay passes on what has wording and the three kinds output listens to,
// once, and leaves the rest - and what output reads for itself.
func TestTheRelay(t *testing.T) {
	s, owner, _ := setup(t)
	ctx := context.Background()
	relay := s.Relay(queue.Effects)
	cases := []struct {
		kind, payload string
		want          int
	}{
		{"assets.daily", `{"a":1,"notify":{"tier":"digest","title":"오늘 자산"}}`, 1},
		{"proposal.decided", `{"notifyKey":"k"}`, 1},
		{"suggestion.raised", `{"supersedes":["k"]}`, 1},
		{"context.state", `{"state":{"driving":true}}`, 1},
		{"job.step", `{"text":"열기"}`, 0},
		{"status.fail", `{"notify":{"tier":"now","title":"멈춤"}}`, 0},
		{"approval.pending", `{"notify":{"tier":"now","title":"결재 대기"}}`, 0},
	}
	for _, c := range cases {
		k := key("relay")
		m := relayMessage(c.kind, k, c.payload)
		if err := relay(ctx, m); err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		if err := relay(ctx, m); err != nil {
			t.Fatalf("%s 다시: %v", c.kind, err)
		}
		if n := one(t, owner, `select count(*) from mind.effects where source = 'assets' and source_key = $1`, k); n != c.want {
			t.Fatalf("%s: %d", c.kind, n)
		}
	}
	if err := relay(ctx, relayMessage("a.b", key("x"), `[1,2`)); !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("읽지 못하는 것은 거절: %v", err)
	}
}

// On the guest's side there is nothing output reads for itself.
func TestTheGuestsRelayPassesApprovals(t *testing.T) {
	s, owner, _ := setup(t)
	k := key("relay")
	m := relayMessage("approval.pending", k, `{"notify":{"tier":"now","title":"결재 대기","audience":"guest"}}`)
	if err := s.Relay(queue.GuestEffects)(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if n := one(t, owner, `select count(*) from mind.guest_effects where source_key = $1`, k); n != 1 {
		t.Fatalf("%d", n)
	}
}

// A replay reads both sides in one order, and only what the loop reads.
func TestReadForAReplay(t *testing.T) {
	s, owner, _ := setup(t)
	ctx := context.Background()
	var start int64
	if err := owner.QueryRow(ctx, `select last_value from input.records_id_seq`).Scan(&start); err != nil {
		t.Fatal(err)
	}
	put := func(guest bool, kind, k string) {
		t.Helper()
		if _, err := owner.Exec(ctx, `select input.append($1, $2, 'kakao', $3, 'kakao', '', 'room', now(), 'raw', 1, '', '', '{"ref":"r","notify":{"tier":"log","title":"t"}}'::jsonb)`, guest, kind, k); err != nil {
			t.Fatal(err)
		}
	}
	a, b, c := key("a"), key("b"), key("c")
	put(false, "message.observed", a)
	put(true, "action.executed", b)
	put(false, "job.step", c)
	got, err := s.Read(ctx, start, 10)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, o := range got {
		keys = append(keys, o.Key)
	}
	if len(got) != 2 || got[0].Key != a || got[1].Key != b || got[0].Type != "message.observed" || got[0].Subject != "room" {
		t.Fatalf("%v", keys)
	}
	var d struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(got[0].Data, &d); err != nil || d.Ref != "r" || len(got[0].Notify) == 0 {
		t.Fatalf("%s %s %v", got[0].Data, got[0].Notify, err)
	}
}

func TestPruneKeepsWhatIsStillOnItsWay(t *testing.T) {
	s, owner, name := setup(t)
	ctx := context.Background()
	old, waiting := key("old"), key("waiting")
	back := time.Now().Add(-40 * 24 * time.Hour)
	var id int64
	for _, k := range []string{old, waiting} {
		if err := owner.QueryRow(ctx, `insert into mind.effects (kind, source, source_key, actor, occurred_at, received_at) values ('a', 'mind', $1, 'mind', $2, $2) returning id`, k, back).Scan(&id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owner.Exec(ctx, `insert into mind.effect_deliveries (consumer, id, at) values ($1, $2, $3)`, name, id, back); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneEffects(ctx, 30*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if n := one(t, owner, `select count(*) from mind.effects where source_key = $1`, old); n != 0 {
		t.Fatal("지난 효과가 남았습니다")
	}
	if n := one(t, owner, `select count(*) from mind.effects where source_key = $1`, waiting); n != 1 {
		t.Fatal("아직 가는 중인 효과가 지워졌습니다")
	}
}
