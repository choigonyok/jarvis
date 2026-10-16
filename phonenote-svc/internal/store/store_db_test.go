package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
)

// Migration 056 as svc_phonenote (scripts/dbtest): a notification handed in
// twice is one, what is open is read and applied, and only what was worth
// keeping comes back as meaningful.
func TestNotes(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	admin := os.Getenv("JARVIS_TEST_ADMIN_DSN")
	if admin == "" {
		admin = dsn
	}
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `delete from phone_notes where device = 'dbtest'`)
	conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool)

	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	in := []note.In{
		{Key: "a", App: "com.jawebs.baedal", Title: "주문 접수", Body: "교촌치킨 허니콤보", At: at},
		{Key: "b", App: "com.some.game", Title: "보상을 받으세요", At: at},
	}
	if n, err := s.Add(ctx, "dbtest", in); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if n, _ := s.Add(ctx, "dbtest", in); n != 0 {
		t.Fatalf("두 번 받은 알림이 두 개가 됐습니다: %d", n)
	}
	if err := s.Seen(ctx, "dbtest", "권한 없음", "42"); err != nil {
		t.Fatal(err)
	}
	open, err := s.Open(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range open {
		if n.Device != "dbtest" {
			continue
		}
		r := note.Reading{Kind: note.DeliveryOrder, Meaningful: n.App == "com.jawebs.baedal", Items: []note.Item{{Name: "허니콤보"}}}
		n.Reading, n.Status = &r, "done"
		if err := s.Save(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := s.Meaningful(ctx, at.Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Reading == nil || kept[0].Reading.Items[0].Name != "허니콤보" {
		t.Fatalf("의미 있는 것만: %+v", kept)
	}
	usual, err := s.Usual(ctx, "com.some.game", 8)
	if err != nil || len(usual) != 1 || usual[0] != "noise" {
		t.Fatalf("앱의 지난 알림: %v %v", usual, err)
	}
	cs, err := s.Collectors(ctx)
	if err != nil || len(cs) == 0 || cs[0].Problem != "권한 없음" {
		t.Fatalf("수집기: %+v %v", cs, err)
	}
}
