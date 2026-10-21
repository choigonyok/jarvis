package checklist

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func service(st Store) (*Service, *time.Time) {
	now := time.Date(2026, 10, 22, 19, 0, 0, 0, time.Local)
	return &Service{St: st, Now: func() time.Time { return now }}, &now
}

// A list keeps what was checked, whoever checked it; an item named that is
// not on it is an error, not a quiet nothing.
func TestListKeepsWhatIsDone(t *testing.T) {
	ctx := context.Background()
	s, now := service(NewMemory())
	l, err := s.Make(ctx, New{Title: " 캠핑  준비물 ", Items: []string{"장갑", "베개", " ", "랜턴", "장갑"}})
	if err != nil {
		t.Fatal(err)
	}
	if l.Title != "캠핑 준비물" || len(l.Items) != 3 || l.Items[2].ID != "i3" || l.Status != Active || l.Source != ByChat {
		t.Fatalf("만든 목록: %+v", l)
	}
	*now = now.Add(time.Minute)
	if l, err = s.Set(ctx, l.ID, "i2", true, ByCard); err != nil {
		t.Fatal(err)
	}
	if !l.Items[1].Done || l.Items[1].By != ByCard || l.Items[1].DoneAt == nil || l.Count() != "1/3" {
		t.Fatalf("체크: %+v", l.Items[1])
	}
	got, _ := s.Get(ctx, l.ID)
	if !got.Items[1].Done {
		t.Fatal("체크가 저장되지 않았습니다")
	}
	if l, err = s.Change(ctx, l.ID, Change{Check: []string{"장갑"}, Add: []string{"이소가스"}, Remove: []string{"i3"}, By: ByChat}); err != nil {
		t.Fatal(err)
	}
	if line := l.Line(); line != "["+l.ID+"] 캠핑 준비물 2/3 · 남은 것: 이소가스" {
		t.Fatalf("한 줄: %s", line)
	}
	if l.Items[2].ID != "i4" {
		t.Fatalf("지운 항목의 번호는 다시 쓰지 않습니다: %+v", l.Items)
	}
	var bad ErrInvalid
	if _, err := s.Change(ctx, l.ID, Change{Check: []string{"침낭"}}); !errors.As(err, &bad) || !strings.Contains(err.Error(), "침낭") {
		t.Fatalf("없는 항목: %v", err)
	}
	if _, err := s.Set(ctx, l.ID, "i9", true, ByCard); !errors.As(err, &bad) {
		t.Fatalf("없는 항목 id: %v", err)
	}
	if _, err := s.Make(ctx, New{Title: "빈 목록"}); !errors.As(err, &bad) {
		t.Fatalf("항목 없는 목록: %v", err)
	}
	if _, err := s.Get(ctx, "chk_none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 목록: %v", err)
	}
}

// Made again with its topic, a list is the same list, with what was new
// added and what was done still done; closed, it leaves the active ones.
func TestTopicMakesOneList(t *testing.T) {
	ctx := context.Background()
	s, now := service(NewMemory())
	first, err := s.Make(ctx, New{Title: "양평 캠핑", Topic: "camping-1024", Items: []string{"장갑", "베개"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, first.ID, "i1", true, ByCard); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	again, err := s.Make(ctx, New{Title: "양평 캠핑 준비물", Topic: "camping-1024", Items: []string{"베개", "장작"}})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.Title != "양평 캠핑 준비물" || len(again.Items) != 3 || !again.Items[0].Done {
		t.Fatalf("같은 주제는 한 목록: %+v", again)
	}
	other, _ := s.Make(ctx, New{Title: "집에 가서 할 일", Items: []string{"빨래"}})
	*now = now.Add(time.Hour)
	if _, err := s.Archive(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	active, _ := s.All(ctx, false)
	if len(active) != 1 || active[0].ID != other.ID {
		t.Fatalf("열린 목록: %+v", active)
	}
	all, _ := s.All(ctx, true)
	if len(all) != 2 || all[1].Status != Archived || !strings.HasSuffix(all[1].Line(), "(닫음)") {
		t.Fatalf("닫은 것까지: %+v", all)
	}
	// A closed list's topic is free: the next one is a list of its own.
	fresh, _ := s.Make(ctx, New{Title: "다음 캠핑", Topic: "camping-1024", Items: []string{"장갑"}})
	if fresh.ID == first.ID {
		t.Fatal("닫은 목록에 더했습니다")
	}
}

// Against a throwaway Postgres, as svc_mind (scripts/dbtest mind-svc).
func TestPostgresKeepsLists(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest mind-svc)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s, _ := service(NewPostgres(pool))
	l, err := s.Make(ctx, New{Title: "dbtest 목록", Topic: "dbtest-" + time.Now().Format("150405.000"), Items: []string{"하나", "둘"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, l.ID, "i2", true, ByCard); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, l.ID)
	if err != nil || len(got.Items) != 2 || !got.Items[1].Done || got.Next != 3 {
		t.Fatalf("읽은 목록: %+v %v", got, err)
	}
	ls, err := s.All(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range ls {
		found = found || x.ID == l.ID
	}
	if !found {
		t.Fatal("열린 목록에 없습니다")
	}
	if _, err := s.Archive(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "chk_none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 목록: %v", err)
	}
}
