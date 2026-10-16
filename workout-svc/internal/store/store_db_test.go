package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/workout-svc/internal/book"
)

// Against a throwaway Postgres with every migration applied, as the service's
// own role (scripts/dbtest). Without JARVIS_TEST_DSN it skips.
func testStore(t *testing.T) *Store {
	t.Helper()
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
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `truncate workout_sets, workout_exercises, workout_sessions, routines restart identity cascade;
		delete from settings where scope = 'workout'`); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func session(id, name string) book.Session {
	start := time.Date(2026, 10, 16, 19, 0, 0, 0, time.UTC).UnixMilli()
	end := start + 3600_000
	return book.Session{ID: id, Date: "2026-10-16", StartedAt: start, EndedAt: &end,
		Exercises: []book.Exercise{{ID: id + "-e", Name: name, Sets: []book.Set{{Weight: 100, Reps: 5, Done: true}}}}}
}

// B-2: two writers on one book. A whole-book replace built on an old
// revision is refused instead of deleting what the other added; changes by
// session id never collide.
func TestTwoWritersDoNotWipeEachOther(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	b, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := s.Apply(ctx, book.Changes{Sessions: []book.Session{session("w-op", "스쿼트")}})
	if err != nil || mine != b.Revision+1 {
		t.Fatalf("세션 추가: rev=%d err=%v", mine, err)
	}
	theirs, err := s.Apply(ctx, book.Changes{Sessions: []book.Session{session("w-guest", "벤치프레스")}})
	if err != nil {
		t.Fatal(err)
	}

	// The operator's screen still holds the book as of `mine`.
	if _, err := s.Save(ctx, book.Book{Sessions: []book.Session{session("w-op", "스쿼트")}}, mine); !errors.Is(err, ErrConflict) {
		t.Fatalf("낡은 리비전의 전체 교체는 거절돼야 합니다: %v", err)
	}
	b, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Sessions) != 2 || b.Revision != theirs {
		t.Fatalf("두 사람의 세션이 다 남아야 합니다: %d세션 rev=%d", len(b.Sessions), b.Revision)
	}
	if b.Groups["스쿼트"] != "하체" || b.Groups["벤치프레스"] != "가슴" {
		t.Fatalf("부위는 서비스가 판정해 내려 준다: %v", b.Groups)
	}

	// A replace built on the latest revision goes through; a deletion by id
	// removes only that session.
	if _, err := s.Save(ctx, b, b.Revision); err != nil {
		t.Fatalf("최신 리비전의 전체 교체: %v", err)
	}
	if _, err := s.Apply(ctx, book.Changes{Deleted: []string{"w-guest"}}); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Load(ctx)
	if len(b.Sessions) != 1 || b.Sessions[0].ID != "w-op" {
		t.Fatalf("지운 세션만 빠져야 합니다: %+v", b.Sessions)
	}

	// The rule gate's numbers come from SQL functions and views, which run
	// with this role's rights.
	if _, err := s.Stats(ctx); err != nil {
		t.Fatalf("통계: %v", err)
	}
}
