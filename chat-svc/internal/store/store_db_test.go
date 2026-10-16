package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/chat-svc/internal/turn"
)

// B-11 and B-1 against a throwaway Postgres, as svc_chat (scripts/dbtest):
// since= returns only the newer turns, and the timestamp is RFC 3339 with a
// colon in the offset - what mind parses.
func TestTurnsSince(t *testing.T) {
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
	_, err = conn.Exec(ctx, `delete from turns where thread = 'dbtest'`)
	conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	var got []turn.Turn
	for _, text := range []string{"하나", "둘", "셋"} {
		tt, err := s.Append(ctx, turn.Turn{Role: turn.RoleUser, Text: text, Thread: "dbtest"}, "Asia/Seoul")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, tt)
		time.Sleep(10 * time.Millisecond)
	}
	all, err := s.All(ctx, "Asia/Seoul", "dbtest", time.Time{})
	if err != nil || len(all) != 3 {
		t.Fatalf("전체: %d %v", len(all), err)
	}
	second, err := time.Parse(time.RFC3339Nano, all[1].AtISO)
	if err != nil {
		t.Fatalf("시각은 RFC3339 여야 합니다(mind 가 그대로 읽는다): %q %v", all[1].AtISO, err)
	}
	newer, err := s.All(ctx, "Asia/Seoul", "dbtest", second)
	if err != nil || len(newer) != 1 || newer[0].Text != "셋" {
		t.Fatalf("since 뒤의 것만: %+v %v", newer, err)
	}
}
