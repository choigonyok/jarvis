package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/person"
	"github.com/choigonyok/jarvis/input-svc/internal/workout/book"
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
	if _, err := conn.Exec(ctx, `truncate input.workout_sets, input.workout_exercises, input.workout_sessions, input.routines restart identity cascade;
		delete from settings where scope = 'workout'`); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

func session(id, name string) book.Session {
	start := time.Date(2026, 10, 16, 19, 0, 0, 0, time.UTC).UnixMilli()
	end := start + 3600_000
	return book.Session{ID: id, Date: "2026-10-16", StartedAt: start, EndedAt: &end,
		Exercises: []book.Exercise{{ID: id + "-e", Name: name, Sets: []book.Set{{Weight: 100, Reps: 5, Done: true}}}}}
}

// B-2: two writers on one book: changes by session id never collide, and a
// deletion by id removes only that session.
func TestTwoWritersDoNotWipeEachOther(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	b, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := s.Apply(ctx, person.Owner, book.Changes{Sessions: []book.Session{session("w-op", "스쿼트")}}, nil)
	if err != nil || mine != b.Revision+1 {
		t.Fatalf("세션 추가: rev=%d err=%v", mine, err)
	}
	theirs, err := s.Apply(ctx, person.Guest, book.Changes{Sessions: []book.Session{session("w-guest", "벤치프레스")}}, nil)
	if err != nil {
		t.Fatal(err)
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
	if _, err := s.Apply(ctx, person.Owner, book.Changes{Deleted: []string{"w-guest"}}, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Load(ctx)
	if len(b.Sessions) != 1 || b.Sessions[0].ID != "w-op" {
		t.Fatalf("지운 세션만 빠져야 합니다: %+v", b.Sessions)
	}
}

// A session is whoever's save first wrote it: not what the save says, not
// whoever saves it next. One saved before makers were kept stays without one,
// whoever saves it.
func TestASessionIsWhoeverFirstSavedIt(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	byID := func() map[string]string {
		t.Helper()
		b, err := s.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, x := range b.Sessions {
			out[x.ID] = x.By
		}
		return out
	}

	// The guest's first save, claiming the operator's name: the guest's, and
	// the claim is not what the record carries either.
	claimed := session("w-guest", "벤치프레스")
	claimed.By = person.Owner
	var recorded book.Changes
	if _, err := s.Apply(ctx, person.Guest, book.Changes{Sessions: []book.Session{claimed}},
		func(_ context.Context, _ pgx.Tx, _ int64, c book.Changes) error { recorded = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(recorded.Sessions) != 1 || recorded.Sessions[0].By != "" {
		t.Fatalf("보낸 by 는 기록에도 남지 않아야 합니다: %+v", recorded.Sessions)
	}
	if _, err := s.Apply(ctx, person.Owner, book.Changes{Sessions: []book.Session{session("w-op", "스쿼트")}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := byID(); got["w-guest"] != person.Guest || got["w-op"] != person.Owner {
		t.Fatalf("처음 저장한 토큰의 것이어야 합니다: %v", got)
	}

	// The operator puts a set of the guest's right (and says it is theirs):
	// the set changes, the session is still the guest's.
	fixed := session("w-guest", "벤치프레스")
	fixed.Exercises[0].Sets[0].Reps = 6
	fixed.By = person.Owner
	if _, err := s.Apply(ctx, person.Owner, book.Changes{Sessions: []book.Session{fixed}}, nil); err != nil {
		t.Fatal(err)
	}
	b, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range b.Sessions {
		if x.ID == "w-guest" && (x.By != person.Guest || x.Exercises[0].Sets[0].Reps != 6) {
			t.Fatalf("고친 것은 남고 주인은 그대로여야 합니다: %+v", x)
		}
	}

	// From before: nobody's, and saving it - by either - does not make it anybody's.
	if _, err := s.pool.Exec(ctx, `insert into input.workout_sessions (id, on_date, started_at) values ('w-old', '2026-10-01', now())`); err != nil {
		t.Fatal(err)
	}
	if got := byID(); got["w-old"] != "" {
		t.Fatalf("예전 세션에는 주인이 없습니다: %q", got["w-old"])
	}
	old := session("w-old", "데드리프트")
	if _, err := s.Apply(ctx, person.Guest, book.Changes{Sessions: []book.Session{old}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := byID(); got["w-old"] != "" || len(got) != 3 {
		t.Fatalf("저장해도 예전 세션은 주인이 없어야 합니다: %v", got)
	}
}

// A save whose record could not be left is not saved.
func TestASaveLandsWithItsRecord(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	refuse := func(context.Context, pgx.Tx, int64, book.Changes) error { return errors.New("안 됩니다") }
	if _, err := s.Apply(ctx, person.Owner, book.Changes{Sessions: []book.Session{session("w-1", "스쿼트")}}, refuse); err == nil {
		t.Fatal("기록이 실패하면 저장도 실패해야 합니다")
	}
	b, err := s.Load(ctx)
	if err != nil || len(b.Sessions) != 0 || b.Revision != 0 {
		t.Fatalf("%+v %v", b, err)
	}
	var seen book.Changes
	var at int64
	rev, err := s.Apply(ctx, person.Owner, book.Changes{Sessions: []book.Session{session("w-1", "스쿼트")}}, func(_ context.Context, _ pgx.Tx, r int64, c book.Changes) error {
		seen, at = c, r
		return nil
	})
	if err != nil || at != rev || len(seen.Sessions) != 1 {
		t.Fatalf("%d %d %+v %v", rev, at, seen, err)
	}
}
