package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/store"
)

func setup(t *testing.T) (*Ingester, *store.Store, *pgx.Conn) {
	t.Helper()
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	db, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(ctx) })
	return New(nil, st, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil))), st, db
}

const approval = "[테스트카드] 승인\n홍*동님\n카드 : 테스트카드(1234)\n금액 : 12,000원 일시불\n일시 : 10/16 12:34\n가맹점 : 테스트상점 본점"

// G-D4, end to end: a charge goes in the book, an approval in a format not
// seen before goes to the queue for a person instead of being dropped, and a
// notice from the card company goes nowhere. Saying it twice is once.
func TestCardMessages(t *testing.T) {
	in, st, db := setup(t)
	ctx := context.Background()
	run := time.Now().UnixNano()
	at := time.Date(2026, 10, 16, 12, 34, 0, 0, time.Local)
	msgs := map[string]string{
		"notice":   "[테스트카드] 이번 달 결제예정 금액 안내입니다. 앱에서 확인하세요.",
		"unknown":  "[테스트카드] 홍*동님 12,300원 승인 테스트상점",
		"approval": approval,
	}
	ids := map[string]string{}
	for k, text := range msgs {
		ids[k] = fmt.Sprintf("gd4-%s-%d", k, run)
		for range 2 {
			if err := in.Take(ctx, Message{MessageID: ids[k], ChatName: "테스트카드", ChatType: "direct", Text: text, SentAt: at.Unix()}); err != nil {
				t.Fatalf("%s: %v", k, err)
			}
		}
	}
	queued, err := st.Unparsed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range queued {
		found = found || u.MessageID == ids["unknown"]
	}
	if !found {
		t.Fatal("형식을 모르는 승인은 확인 대기열에 있어야 합니다")
	}
	var charged int
	if err := db.QueryRow(ctx, `select count(*) from mind.card_transactions where source_ref = $1`, ids["approval"]).Scan(&charged); err != nil {
		t.Fatal(err)
	}
	if charged != 1 {
		t.Fatalf("읽히는 승인은 장부에 한 번 들어가야 합니다 (%d건)", charged)
	}
	if err := db.QueryRow(ctx, `select count(*) from mind.card_transactions where source_ref = $1`, ids["notice"]).Scan(&charged); err != nil || charged != 0 {
		t.Fatalf("안내는 결제가 아닙니다: %d %v", charged, err)
	}
}

// A friend's message and one of my own are messages seen, not alerts, and the
// screen hears of a silence.
func TestWhatIsSeen(t *testing.T) {
	in, _, _ := setup(t)
	ctx := context.Background()
	future := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	if err := in.Take(ctx, Message{MessageID: "seen-1", ChatName: "민수", ChatType: "direct", Text: "저녁 먹자", SentAt: future.Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := in.Take(ctx, Message{MessageID: "seen-2", ChatName: "카드 모임", ChatType: "group", Text: "승인 났어 12,000원", SentAt: future.Unix()}); err != nil {
		t.Fatal(err)
	}
	s, err := in.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.LatestMessageAt == nil || !s.LatestMessageAt.Equal(future) {
		t.Fatalf("%+v", s)
	}
	if s.LastAlertAt != nil && s.LastAlertAt.Equal(future) {
		t.Fatalf("카드사 방이 아닙니다: %+v", s)
	}
	if s.Stale {
		t.Fatalf("방금 왔습니다: %+v", s)
	}
	in.now = func() time.Time { return future.Add(2 * time.Hour) }
	if s, _ = in.Status(ctx); !s.Stale {
		t.Fatalf("한동안 조용합니다: %+v", s)
	}
	// An older message does not take the time back.
	if err := in.Take(ctx, Message{MessageID: "seen-3", ChatName: "민수", ChatType: "direct", Text: "늦게 온 말", SentAt: future.Add(-time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if s, _ = in.Status(ctx); !s.LatestMessageAt.Equal(future) {
		t.Fatalf("%+v", s)
	}
}

// A dollar charge is estimated with the rate assets-svc last said; without
// one a person writes it in.
func TestADollarCharge(t *testing.T) {
	in, st, db := setup(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `update mind.latest set at = now() - interval '2 days' where kind = 'assets.fx'`); err != nil {
		t.Fatal(err)
	}
	run := time.Now().UnixNano()
	at := time.Date(2026, 10, 16, 12, 34, 0, 0, time.Local)
	text := "[테스트카드] 해외승인\n홍*동님\n카드 : 테스트카드(1234)\n금액 : USD 10.00\n일시 : 10/16 12:34\n가맹점 : STEAM"
	first := fmt.Sprintf("usd-%d-a", run)
	if err := in.Take(ctx, Message{MessageID: first, ChatName: "테스트카드", ChatType: "direct", Text: text, SentAt: at.Unix()}); err != nil {
		t.Fatal(err)
	}
	queued, err := st.Unparsed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range queued {
		found = found || u.MessageID == first
	}
	if !found {
		t.Fatal("환율 없이는 사람이 적습니다")
	}
	if _, err := db.Exec(ctx, `insert into mind.latest (kind, payload, at) values ('assets.fx', '{"usdKrw":1400}', now())
		on conflict (kind) do update set payload = excluded.payload, at = excluded.at`); err != nil {
		t.Fatal(err)
	}
	second := fmt.Sprintf("usd-%d-b", run)
	if err := in.Take(ctx, Message{MessageID: second, ChatName: "테스트카드", ChatType: "direct", Text: text, SentAt: at.Unix()}); err != nil {
		t.Fatal(err)
	}
	var amount int64
	var estimated bool
	if err := db.QueryRow(ctx, `select amount_krw, estimated from mind.card_transactions where source_ref = $1`, second).Scan(&amount, &estimated); err != nil {
		t.Fatal(err)
	}
	if amount != 14000 || !estimated {
		t.Fatalf("%d %v", amount, estimated)
	}
}

// The database gone is an outage, and costs the message no try.
func TestADatabaseGoneIsAnOutage(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	down, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	down.Close()
	in := New(nil, down, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err = in.Take(ctx, Message{MessageID: "x", ChatName: "테스트카드", Text: approval, SentAt: time.Now().Unix()})
	if !errors.Is(err, queue.ErrOutage) {
		t.Fatalf("%v", err)
	}
}
