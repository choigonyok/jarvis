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

// 2: a turn that quotes an earlier one keeps the quote, read back as written.
func TestTurnKeepsItsReference(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	first, err := s.Append(ctx, turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"알람시계 T872 는 16,900원입니다."}, Thread: "dbtest-ref"}, "Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	ref := &turn.Ref{ID: first.ID, Role: turn.RoleAgent, At: first.At, Text: "알람시계 T872 는 16,900원입니다."}
	saved, err := s.Append(ctx, turn.Turn{Role: turn.RoleUser, Text: "이 제품 구매해줘", Ref: ref, Thread: "dbtest-ref"}, "Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Ref == nil || saved.Ref.ID != first.ID {
		t.Fatalf("저장한 턴이 참조를 돌려줘야 합니다: %+v", saved.Ref)
	}
	all, err := s.All(ctx, "Asia/Seoul", "dbtest-ref", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := all[len(all)-1].Ref; got == nil || got.Text != ref.Text || got.Role != turn.RoleAgent {
		t.Fatalf("다시 읽어도 참조가 남아야 합니다: %+v", got)
	}
}

// A job's card (060): appended with its origin, read back with it, and its
// later word replaces the earlier one and brings the card to the present -
// while the conversation's own words cannot be rewritten.
func TestJobCardIsOneTurnPerRun(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	const th = "dbtest-job"
	origin := &turn.Origin{Kind: "job", Job: 7, Run: 42, Title: "가격 지켜보기"}
	card, err := s.Append(ctx, turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"지켜보는 중입니다."}, Origin: origin, Thread: th}, "Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	if card.Origin == nil || card.Origin.Run != 42 || card.Origin.Title != "가격 지켜보기" {
		t.Fatalf("저장한 턴이 origin 을 돌려줘야 합니다: %+v", card.Origin)
	}
	time.Sleep(10 * time.Millisecond)
	later, err := s.Append(ctx, turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"그 사이의 대화 답"}, Thread: th}, "Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	updated, err := s.Update(ctx, th, card.ID, []string{"가격이 내렸습니다: 16,900원"}, "Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != card.ID || updated.Origin == nil || updated.Paragraphs[0] != "가격이 내렸습니다: 16,900원" {
		t.Fatalf("같은 턴이 새 말을 담아야 합니다: %+v", updated)
	}
	if updated.AtISO <= later.AtISO {
		t.Fatalf("갱신된 카드는 맨 아래로 와야 합니다: %s <= %s", updated.AtISO, later.AtISO)
	}
	all, err := s.All(ctx, "Asia/Seoul", th, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(all); n < 2 || all[n-1].ID != card.ID || all[n-1].Origin == nil {
		t.Fatalf("다시 읽어도 카드가 마지막이고 origin 이 남아야 합니다: %+v", all)
	}
	if _, err := s.Update(ctx, th, later.ID, []string{"고쳐 쓰기"}, "Asia/Seoul"); err != ErrNoTurn {
		t.Fatalf("대화의 말은 못 바꿔야 합니다: %v", err)
	}
	if _, err := s.Update(ctx, "other", card.ID, []string{"x"}, "Asia/Seoul"); err != ErrNoTurn {
		t.Fatalf("다른 스레드에서는 못 바꿔야 합니다: %v", err)
	}
}
