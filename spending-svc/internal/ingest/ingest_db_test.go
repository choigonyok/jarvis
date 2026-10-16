package ingest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/spending-svc/internal/store"
)

// G-D4, end to end: a card company's message is kept as it came even when it
// is not read as a payment, and an approval in a format not seen before goes
// to the queue for a person instead of being dropped. Needs JARVIS_TEST_DSN
// (scripts/dbtest).
func TestCardMessagesAreKept(t *testing.T) {
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
	in := New("http://127.0.0.1:1", "", nil, st, time.Minute, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))

	run := time.Now().UnixNano()
	at := time.Date(2026, 10, 16, 12, 34, 0, 0, time.Local)
	msgs := map[string]string{
		"notice":   "[테스트카드] 이번 달 결제예정 금액 안내입니다. 앱에서 확인하세요.",
		"unknown":  "[테스트카드] 홍*동님 12,300원 승인 테스트상점",
		"approval": "[테스트카드] 승인\n홍*동님\n카드 : 테스트카드(1234)\n금액 : 12,000원 일시불\n일시 : 10/16 12:34\n가맹점 : 테스트상점 본점",
	}
	ids := map[string]string{}
	for k, text := range msgs {
		ids[k] = fmt.Sprintf("gd4-%s-%d", k, run)
		if err := in.handle(ctx, message{MessageID: ids[k], ChatName: "테스트카드", ChatType: "direct", Text: text, SentAt: at.Unix()}); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}

	for k, id := range ids {
		var n int
		if err := db.QueryRow(ctx, `select count(*) from raw_events where source = 'kakaotalk' and ext_id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s: 원본이 남아야 합니다 (raw_events %d건)", k, n)
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
	if err := db.QueryRow(ctx, `select count(*) from card_transactions where source_ref = $1`, ids["approval"]).Scan(&charged); err != nil {
		t.Fatal(err)
	}
	if charged != 1 {
		t.Fatalf("읽히는 승인은 장부에 들어가야 합니다 (%d건)", charged)
	}
}
