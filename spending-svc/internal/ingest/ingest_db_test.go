package ingest

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
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/gocommon/health"
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

// One card message that cannot be recorded (a NUL byte Postgres will not
// store) no longer stops collection: after health.MaxTries it is set aside
// as an unread alert with why, the cursor moves past it, and the charge
// behind it is booked. A database that does not answer costs no try.
func TestAMessageThatCannotBeRecordedIsHeldAndCollectionGoesOn(t *testing.T) {
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
	if err := st.SetCursor(ctx, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.SetCursor(ctx, 0) })

	run := time.Now().UnixNano()
	at := time.Date(2026, 10, 27, 9, 30, 0, 0, time.Local)
	poison := fmt.Sprintf("held-poison-%d", run)
	good := fmt.Sprintf("held-good-%d", run)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after := r.URL.Query().Get("rowid")
		msgs := []message{
			{RowID: 1, MessageID: poison, ChatName: "테스트카드", ChatType: "direct", Text: "[테스트카드] 승인\x00 12,000원", SentAt: at.Unix()},
			{RowID: 2, MessageID: good, ChatName: "테스트카드", ChatType: "direct",
				Text: "[테스트카드] 승인\n홍*동님\n카드 : 테스트카드(1234)\n금액 : 12,000원 일시불\n일시 : 10/27 09:31\n가맹점 : 테스트상점 본점", SentAt: at.Unix() + 60},
		}
		var out []message
		for _, m := range msgs {
			if fmt.Sprint(m.RowID) > after {
				out = append(out, m)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": out})
	}))
	t.Cleanup(srv.Close)

	in := New(srv.URL, "", nil, st, time.Second, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	in.Loops = health.NewLoops()
	clock := time.Now()
	in.now = func() time.Time { return clock }
	for i := 1; i < health.MaxTries; i++ {
		if err := in.pollOnce(ctx); err == nil {
			t.Fatalf("시도 %d: 그 메시지 앞에서 멈춰야 합니다", i)
		}
		if c, _ := st.Cursor(ctx); c != 0 {
			t.Fatalf("시도 %d: 커서가 넘어갔습니다 (%d)", i, c)
		}
		if err := in.pollOnce(ctx); err == nil || !strings.Contains(err.Error(), "잠시 뒤") {
			t.Fatalf("간격 전에는 다시 시도하지 않습니다: %v", err)
		}
		clock = clock.Add(time.Hour)
	}
	if err := in.pollOnce(ctx); err != nil {
		t.Fatalf("다섯 번째에는 따로 두고 넘어가야 합니다: %v", err)
	}
	if c, _ := st.Cursor(ctx); c != 2 {
		t.Fatalf("커서 %d, 2 여야 합니다", c)
	}
	var reason string
	if err := db.QueryRow(ctx, `select reason from spending_unparsed where message_id = $1 and not resolved`, poison).Scan(&reason); err != nil || !strings.HasPrefix(reason, "기록하지 못했습니다") {
		t.Fatalf("읽지 못한 알림에 이유와 함께 있어야 합니다: %q %v", reason, err)
	}
	var booked int
	_ = db.QueryRow(ctx, `select count(*) from card_transactions where source_ref = $1`, good).Scan(&booked)
	if booked != 1 {
		t.Fatalf("뒤의 결제가 장부에 들어가야 합니다 (%d건)", booked)
	}
	if l := in.Loops.List()[0]; l.LastOK == nil || l.Behind != nil {
		t.Fatalf("넘어간 뒤의 수집 루프: %+v", l)
	}

	// The database gone: no message is counted against.
	down, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	down.Close()
	in2 := New(srv.URL, "", nil, down, time.Second, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < health.MaxTries+2; i++ {
		if err := in2.take(ctx, message{MessageID: "x", ChatName: "테스트카드", Text: "t", SentAt: at.Unix()}); err == nil {
			t.Fatal("데이터베이스 없이 기록될 수 없습니다")
		}
	}
	if in2.stuck.tries != 0 {
		t.Fatalf("데이터베이스 장애는 메시지 탓이 아닙니다: %d번 셌습니다", in2.stuck.tries)
	}
}
