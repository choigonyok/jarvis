package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/input-svc/internal/chat/turn"
)

func pool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// A turn whose record could not be left is not stored: what the screen reads
// and what the stream holds never disagree.
func TestATurnLandsWithItsRecord(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s := New(pool(t, dsn))
	const th = "dbtest-after"
	refuse := func(context.Context, pgx.Tx, turn.Turn) error { return errors.New("안 됩니다") }
	if _, err := s.Append(ctx, turn.Turn{Role: turn.RoleUser, Text: "하나", Thread: th}, "Asia/Seoul", refuse); err == nil {
		t.Fatal("기록이 실패하면 턴도 실패해야 합니다")
	}
	all, err := s.All(ctx, "Asia/Seoul", th, time.Time{})
	if err != nil || len(all) != 0 {
		t.Fatalf("%+v %v", all, err)
	}
	var seen turn.Turn
	saved, err := s.Append(ctx, turn.Turn{Role: turn.RoleUser, Text: "둘", Thread: th}, "Asia/Seoul",
		func(_ context.Context, _ pgx.Tx, got turn.Turn) error { seen = got; return nil })
	if err != nil || seen.ID != saved.ID || seen.Thread != th || seen.AtISO == "" {
		t.Fatalf("%+v %+v %v", seen, saved, err)
	}
}

// B-11 and B-1 against a throwaway Postgres, as svc_input (scripts/dbtest):
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
	_, err = conn.Exec(ctx, `delete from input.turns where thread = 'dbtest'`)
	conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := New(pool(t, dsn))

	var got []turn.Turn
	for _, text := range []string{"하나", "둘", "셋"} {
		tt, err := s.Append(ctx, turn.Turn{Role: turn.RoleUser, Text: text, Thread: "dbtest"}, "Asia/Seoul", nil)
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
	s := New(pool(t, dsn))
	first, err := s.Append(ctx, turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"알람시계 T872 는 16,900원입니다."}, Thread: "dbtest-ref"}, "Asia/Seoul", nil)
	if err != nil {
		t.Fatal(err)
	}
	ref := &turn.Ref{ID: first.ID, Role: turn.RoleAgent, At: first.At, Text: "알람시계 T872 는 16,900원입니다."}
	saved, err := s.Append(ctx, turn.Turn{Role: turn.RoleUser, Text: "이 제품 구매해줘", Ref: ref, Thread: "dbtest-ref"}, "Asia/Seoul", nil)
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
	s := New(pool(t, dsn))
	const th = "dbtest-job"
	origin := &turn.Origin{Kind: "job", Job: 7, Run: 42, Title: "가격 지켜보기"}
	card, err := s.Append(ctx, turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"지켜보는 중입니다."}, Origin: origin, Thread: th}, "Asia/Seoul", nil)
	if err != nil {
		t.Fatal(err)
	}
	if card.Origin == nil || card.Origin.Run != 42 || card.Origin.Title != "가격 지켜보기" {
		t.Fatalf("저장한 턴이 origin 을 돌려줘야 합니다: %+v", card.Origin)
	}
	time.Sleep(10 * time.Millisecond)
	later, err := s.Append(ctx, turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"그 사이의 대화 답"}, Thread: th}, "Asia/Seoul", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	updated, err := s.Update(ctx, th, card.ID, []string{"가격이 내렸습니다: 16,900원"}, "Asia/Seoul", nil)
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
	if _, err := s.Update(ctx, th, later.ID, []string{"고쳐 쓰기"}, "Asia/Seoul", nil); err != ErrNoTurn {
		t.Fatalf("대화의 말은 못 바꿔야 합니다: %v", err)
	}
	if _, err := s.Update(ctx, "other", card.ID, []string{"x"}, "Asia/Seoul", nil); err != ErrNoTurn {
		t.Fatalf("다른 스레드에서는 못 바꿔야 합니다: %v", err)
	}

	// A notice with the list it shows keeps both through the store.
	notice, err := s.Append(ctx, turn.Turn{Role: turn.RoleAgent, Paragraphs: []string{"집에 가서 할 일"}, Thread: th,
		Origin: &turn.Origin{Kind: "notice", Title: "집에 도착하셔서 알려 드려요", Ref: "remind:rem_1:1", Checklist: "chk_1"}}, "Asia/Seoul", nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.All(ctx, "Asia/Seoul", th, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	last := again[len(again)-1]
	if last.ID != notice.ID || last.Origin == nil || last.Origin.Ref != "remind:rem_1:1" || last.Origin.Checklist != "chk_1" {
		t.Fatalf("안내 카드의 ref 와 체크리스트가 남아야 합니다: %+v", last.Origin)
	}
}

// The console's page: the newest turns, then the ones before the oldest on
// screen, by its id - every turn once, oldest first within a page.
func TestWindowPagesByTurnID(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s := New(pool(t, dsn))
	const th = "dbtest-page"
	var ids []string
	for i := 0; i < 7; i++ {
		tt, err := s.Append(ctx, turn.Turn{Role: turn.RoleUser, Text: fmt.Sprint("m", i), Thread: th}, "Asia/Seoul", nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tt.ID)
	}
	page, more, err := s.Window(ctx, "Asia/Seoul", th, time.Time{}, "", 3)
	if err != nil || !more || len(page) != 3 || page[0].ID != ids[4] || page[2].ID != ids[6] {
		t.Fatalf("최신 3개: %+v more=%v err=%v", page, more, err)
	}
	page, more, err = s.Window(ctx, "Asia/Seoul", th, time.Time{}, page[0].ID, 3)
	if err != nil || !more || len(page) != 3 || page[0].ID != ids[1] || page[2].ID != ids[3] {
		t.Fatalf("그 앞 3개: %+v more=%v err=%v", page, more, err)
	}
	page, more, err = s.Window(ctx, "Asia/Seoul", th, time.Time{}, page[0].ID, 3)
	if err != nil || more || len(page) != 1 || page[0].ID != ids[0] {
		t.Fatalf("마지막 한 개, 더 없음: %+v more=%v err=%v", page, more, err)
	}
	if _, _, err := s.Window(ctx, "Asia/Seoul", th, time.Time{}, "a-0-0", 3); err != nil {
		t.Fatalf("없는 id 는 빈 페이지지 오류가 아닙니다: %v", err)
	}
}
