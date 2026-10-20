package moments

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var seoul = time.FixedZone("KST", 9*60*60)

type fixture struct {
	s    *Service
	now  time.Time
	said []Reminder
	here []Visit
	fail error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Date(2026, 10, 23, 9, 0, 0, 0, seoul)}
	f.s = &Service{St: NewMemory(), Loc: seoul, Now: func() time.Time { return f.now },
		Here: func(context.Context) ([]Visit, error) { return f.here, nil },
		Say: func(_ context.Context, r Reminder) (string, error) {
			if f.fail != nil {
				return "", f.fail
			}
			f.said = append(f.said, r)
			return "푸시", nil
		}}
	return f
}

func (f *fixture) at(h, m int) *time.Time {
	t := time.Date(f.now.Year(), f.now.Month(), f.now.Day(), h, m, 0, 0, seoul)
	return &t
}

func (f *fixture) tick(t *testing.T) {
	t.Helper()
	if err := f.s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTimeReminderIsSaidOnceAtItsTime(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r, err := f.s.Set(ctx, Reminder{Text: "베개 챙기기", At: f.at(10, 0)})
	if err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	if len(f.said) != 0 {
		t.Fatal("시각 전에 말했습니다")
	}
	f.now = *f.at(10, 0)
	f.tick(t)
	f.tick(t)
	if len(f.said) != 1 || f.said[0].SentBy != ByTime {
		t.Fatalf("시각에 한 번 말해야 합니다: %+v", f.said)
	}
	got, _ := f.s.St.Reminder(ctx, r.ID)
	if got.Status != Sent || got.SentAt == nil || got.Result != "푸시" {
		t.Fatalf("보낸 것으로 남아야 합니다: %+v", got)
	}
}

func TestArrivalAtHomeSaysIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.Set(ctx, Reminder{Text: "장갑 챙기기", Place: "집"}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	for _, m := range [][3]string{{Leave, "집", "home"}, {Arrive, "회사", "work"}} {
		if err := f.s.Moved(ctx, m[0], m[1], m[2], f.now); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.said) != 0 {
		t.Fatalf("집 도착이 아닌데 말했습니다: %+v", f.said)
	}
	// Delivered late: not the moment it was.
	_ = f.s.Moved(ctx, Arrive, "집", "home", f.now.Add(-time.Hour))
	if len(f.said) != 0 {
		t.Fatal("늦게 온 도착으로 말했습니다")
	}
	_ = f.s.Moved(ctx, Arrive, "집", "home", f.now)
	if len(f.said) != 1 || f.said[0].SentBy != ByArrive || f.said[0].Why(seoul) != "집에 도착하셔서 알려 드려요" {
		t.Fatalf("집 도착에 말해야 합니다: %+v", f.said)
	}
}

func TestAfterCountsBeingThereAndTheLatestRingsAnyway(t *testing.T) {
	ctx := context.Background()

	// Already home when it starts counting: said then.
	f := newFixture(t)
	if _, err := f.s.Set(ctx, Reminder{Text: "장갑", Place: "home", After: f.at(19, 0), At: f.at(23, 0)}); err != nil {
		t.Fatal(err)
	}
	f.here = []Visit{{Region: "집", Kind: "home"}}
	f.now = *f.at(18, 30)
	_ = f.s.Moved(ctx, Arrive, "집", "home", f.now) // before it counts
	f.tick(t)
	if len(f.said) != 0 {
		t.Fatal("세기 전의 도착으로 말했습니다")
	}
	f.now = *f.at(19, 0)
	f.tick(t)
	if len(f.said) != 1 || f.said[0].SentBy != ByThere {
		t.Fatalf("19시에 집에 있으니 말해야 합니다: %+v", f.said)
	}

	// Out when it starts: the arrival says it.
	f = newFixture(t)
	_, _ = f.s.Set(ctx, Reminder{Text: "장갑", Place: "home", After: f.at(19, 0), At: f.at(23, 0)})
	f.now = *f.at(19, 0)
	f.tick(t)
	f.now = *f.at(20, 10)
	f.tick(t)
	if len(f.said) != 0 {
		t.Fatal("밖에 있는데 말했습니다")
	}
	_ = f.s.Moved(ctx, Arrive, "집", "home", f.now)
	if len(f.said) != 1 || f.said[0].SentBy != ByArrive {
		t.Fatalf("도착에 말해야 합니다: %+v", f.said)
	}

	// Never home (or the phone never said): the latest.
	f = newFixture(t)
	_, _ = f.s.Set(ctx, Reminder{Text: "장갑", Place: "home", After: f.at(19, 0), At: f.at(23, 0)})
	f.now = *f.at(23, 0)
	f.tick(t)
	if len(f.said) != 1 || f.said[0].SentBy != ByLatest || !strings.Contains(f.said[0].Why(seoul), "정해 둔 시각") {
		t.Fatalf("늦어도 23시에는 말해야 합니다: %+v", f.said)
	}
}

func TestWhereUnknownIsLookedAtAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	down := true
	f.s.Here = func(context.Context) ([]Visit, error) {
		if down {
			return nil, errors.New("context-svc down")
		}
		return []Visit{{Region: "집", Kind: "home"}}, nil
	}
	_, _ = f.s.Set(ctx, Reminder{Text: "리밸런싱", Place: "home", After: f.at(17, 0)})
	f.now = *f.at(17, 0)
	f.tick(t)
	down = false
	f.tick(t)
	if len(f.said) != 1 || f.said[0].SentBy != ByThere {
		t.Fatalf("위치를 다시 읽고 말해야 합니다: %+v", f.said)
	}
}

func TestLeavingAndUntil(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.s.Set(ctx, Reminder{Text: "우산", Place: "home", Move: Leave, Until: f.at(12, 0)})
	_, _ = f.s.Set(ctx, Reminder{Text: "택배 받기", Place: "home", Until: f.at(12, 0)})
	f.now = *f.at(10, 0)
	_ = f.s.Moved(ctx, Leave, "집", "home", f.now)
	if len(f.said) != 1 || f.said[0].Text != "우산" || f.said[0].Why(seoul) != "집에서 나가셔서 알려 드려요" {
		t.Fatalf("집을 나설 때 말해야 합니다: %+v", f.said)
	}
	f.now = *f.at(12, 0)
	f.tick(t)
	rs, _ := f.s.List(ctx, true)
	var expired int
	for _, r := range rs {
		if r.Status == Expired {
			expired++
		}
	}
	if expired != 1 || len(f.said) != 1 {
		t.Fatalf("기한이 지난 것은 말하지 않고 끝나야 합니다: %+v", rs)
	}
}

func TestTopicKeepsOneWaiting(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, _ := f.s.Set(ctx, Reminder{Text: "주식 비중", Source: Rebalance, Topic: "rebalance", Place: "home", After: f.at(17, 0)})
	b, _ := f.s.Set(ctx, Reminder{Text: "채권 비중", Source: Rebalance, Topic: "rebalance", Place: "home", After: f.at(17, 0)})
	rs, _ := f.s.List(ctx, false)
	if a.ID != b.ID || len(rs) != 1 || rs[0].Text != "채권 비중" {
		t.Fatalf("같은 주제는 하나만 기다려야 합니다: %+v", rs)
	}
}

func TestChangeAndCancel(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r, _ := f.s.Set(ctx, Reminder{Text: "베개", At: f.at(10, 0)})
	f.now = *f.at(10, 0)
	f.tick(t)

	// Said already: a new time makes it wait again.
	place := "집"
	c, err := f.s.Change(ctx, r.ID, Change{Place: &place, After: f.at(19, 0), At: f.at(23, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != Waiting || c.Place != "home" || c.Move != Arrive || c.When(seoul) != "10/23(금) 19:00부터 집에 있거나 도착할 때 · 늦어도 23:00" {
		t.Fatalf("바뀐 때로 다시 기다려야 합니다: %+v %q", c, c.When(seoul))
	}
	c, err = f.s.Change(ctx, r.ID, Change{Clear: []string{"place", "after"}, At: f.at(21, 0)})
	if err != nil || c.Place != "" || c.When(seoul) != "10/23(금) 21:00" {
		t.Fatalf("장소를 비우면 시각만 남아야 합니다: %+v %v", c, err)
	}
	if _, err := f.s.Cancel(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Cancel(ctx, r.ID); err == nil {
		t.Fatal("취소한 것을 또 취소할 수는 없습니다")
	}
	f.now = *f.at(21, 0)
	f.tick(t)
	if len(f.said) != 1 {
		t.Fatalf("취소한 것은 말하지 않아야 합니다: %+v", f.said)
	}
	if _, err := f.s.Change(ctx, "rem_none", Change{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 알림: %v", err)
	}
}

func TestInvalid(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	past := f.now.Add(-time.Hour)
	for name, r := range map[string]Reminder{
		"말 없음":      {At: f.at(10, 0)},
		"때 없음":      {Text: "베개"},
		"지난 시각":     {Text: "베개", At: &past},
		"이상한 move":  {Text: "베개", Place: "집", Move: "stay"},
		"늦어도가 시작 전": {Text: "베개", Place: "집", After: f.at(19, 0), At: f.at(18, 0)},
	} {
		var bad ErrInvalid
		if _, err := f.s.Set(ctx, r); !errors.As(err, &bad) {
			t.Errorf("%s: 고칠 것을 말해야 합니다 (%v)", name, err)
		}
	}
	rs, _ := f.s.List(ctx, true)
	if len(rs) != 0 {
		t.Fatalf("잘못된 것은 저장하지 않아야 합니다: %+v", rs)
	}
}

func TestFailureLeavesItWaiting(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.s.Set(ctx, Reminder{Text: "베개", At: f.at(10, 0)})
	f.now = *f.at(10, 0)
	f.fail = errors.New("outbox down")
	f.tick(t)
	f.fail = nil
	f.tick(t)
	if len(f.said) != 1 {
		t.Fatalf("실패하면 다음에 다시 말해야 합니다: %+v", f.said)
	}
}

func TestParseTime(t *testing.T) {
	for _, v := range []string{"2026-10-24T08:00", "2026-10-24 08:00", "2026-10-24T08:00:00+09:00"} {
		got, err := ParseTime(v, seoul)
		if err != nil || !got.Equal(time.Date(2026, 10, 24, 8, 0, 0, 0, seoul)) {
			t.Errorf("%s: %v %v", v, got, err)
		}
	}
	if _, err := ParseTime("내일 아침", seoul); err == nil {
		t.Error("읽을 수 없는 시각")
	}
}

// Against a throwaway Postgres with the migrations applied (scripts/dbtest).
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("MIND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIND_TEST_DATABASE_URL 이 없어 건너뜁니다")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := NewPostgres(pool)
	at := time.Date(2026, 10, 24, 8, 0, 0, 0, seoul)
	now := time.Now().Truncate(time.Microsecond)
	r := Reminder{ID: "rem_test" + now.Format("150405.000000"), Text: "베개", Source: Asked, At: &at, Status: Waiting, CreatedAt: now, UpdatedAt: now}
	if err := st.PutReminder(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := st.Reminder(ctx, r.ID)
	if err != nil || got.Text != "베개" || !got.At.Equal(at) {
		t.Fatalf("%+v %v", got, err)
	}
	r.Status, r.UpdatedAt = Sent, now.Add(time.Second)
	if err := st.PutReminder(ctx, r); err != nil {
		t.Fatal(err)
	}
	waiting, _ := st.Reminders(ctx, time.Time{})
	recent, _ := st.Reminders(ctx, now)
	has := func(rs []Reminder) bool {
		for _, x := range rs {
			if x.ID == r.ID {
				return true
			}
		}
		return false
	}
	if has(waiting) || !has(recent) {
		t.Fatalf("보낸 것은 기다리는 목록에서 빠지고 최근 목록에는 있어야 합니다")
	}
	if _, err := st.Reminder(ctx, "rem_none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 알림: %v", err)
	}
}
