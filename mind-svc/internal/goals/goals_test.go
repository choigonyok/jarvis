package goals

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

func marks(done ...string) map[string]Mark {
	out := map[string]Mark{}
	for _, d := range done {
		out[d] = Mark{Day: d, Mark: Done}
	}
	return out
}

func miss(m map[string]Mark, days ...string) map[string]Mark {
	for _, d := range days {
		m[d] = Mark{Day: d, Mark: Missed}
	}
	return m
}

// 2026-11-02 is a Monday.
func weekly(times int) Goal {
	return Goal{ID: "g", Title: "운동", Cadence: Weekly, Times: times, Start: "2026-10-05", Status: Active}
}

// The operator's example: three a week, nothing by Thursday night. With Friday,
// Saturday and Sunday left every one of them is needed; during Thursday there is
// still a day in hand, and once it is marked there is not.
func TestAWeekThatCanStillJustBeMade(t *testing.T) {
	g := weekly(3)
	thursday := "2026-11-05"
	p := g.Of(marks(), thursday)
	if p.Need != 3 || p.Room != 4 || p.Tight || p.Lost {
		t.Fatalf("%+v", p)
	}
	if !strings.Contains(p.Says, "이번 주 0/3") || !strings.Contains(p.Says, "남은 4일 중 3번") {
		t.Fatal(p.Says)
	}
	p = g.Of(miss(marks(), thursday), thursday)
	if p.Room != 3 || !p.Tight || p.Lost || !strings.Contains(p.Says, "남은 날 모두") {
		t.Fatalf("%+v", p)
	}
	left := g.LeftDays(thursday)
	if len(left) != 3 || Name(left[0]) != "금" || Name(left[2]) != "일" {
		t.Fatal(left)
	}
	// One more day gone and it cannot be made.
	p = g.Of(miss(marks(), thursday, "2026-11-06"), "2026-11-06")
	if !p.Lost || p.Tight || !strings.Contains(p.Says, "남은 날로는 2번까지") {
		t.Fatalf("%+v", p)
	}
	// Made, nothing more is asked.
	p = g.Of(marks("2026-11-02", "2026-11-03", "2026-11-04"), thursday)
	if p.Need != 0 || p.Tight || !strings.Contains(p.Says, "다 채웠어요") {
		t.Fatalf("%+v", p)
	}
}

// A week the goal runs for only part of asks its share, rounded up.
func TestAPartWeekAsksItsShare(t *testing.T) {
	g := weekly(3)
	g.Start = "2026-11-05" // a Thursday: four days of seven
	p := g.Of(marks(), "2026-11-05")
	if p.Period.Target != 2 {
		t.Fatalf("%+v", p.Period)
	}
	g.Start = "2026-11-08" // the Sunday: one day
	if p := g.Of(marks(), "2026-11-08"); p.Period.Target != 1 {
		t.Fatalf("%+v", p.Period)
	}
	m := Goal{Cadence: Monthly, Times: 8, Start: "2026-11-16", Status: Active}
	if p := m.Of(marks(), "2026-11-16"); p.Period.Target != 4 || p.Period.From != "2026-11-01" || p.Period.To != "2026-11-30" {
		t.Fatalf("%+v", p.Period)
	}
}

// Today not marked yet breaks nothing; a day gone by unmarked does.
func TestDailyStreak(t *testing.T) {
	g := Goal{Cadence: Daily, Start: "2026-10-28", Status: Active}
	done := marks("2026-10-29", "2026-10-30", "2026-10-31", "2026-11-01")
	p := g.Of(done, "2026-11-02")
	if p.Streak != 4 || p.Best != 4 || p.Unit != "일" || !strings.Contains(p.Says, "4일째") {
		t.Fatalf("%+v", p)
	}
	// 10/28 was not marked: four of five.
	if p.Rate == nil || *p.Rate != 0.8 {
		t.Fatalf("%v", p.Rate)
	}
	done["2026-11-02"] = Mark{Mark: Done}
	if p := g.Of(done, "2026-11-02"); p.Streak != 5 || *p.Rate != 5.0/6 {
		t.Fatalf("%+v", p)
	}
	done["2026-11-02"] = Mark{Mark: Missed}
	if p := g.Of(done, "2026-11-02"); p.Streak != 0 || p.Best != 4 {
		t.Fatalf("안 했다고 하면 오늘 끊깁니다: %+v", p)
	}
	delete(done, "2026-11-01")
	delete(done, "2026-11-02")
	if p := g.Of(done, "2026-11-02"); p.Streak != 0 || p.Best != 3 {
		t.Fatalf("어제 기록이 없으면 끊깁니다: %+v", p)
	}
}

// Chosen days are the only ones that count, and a day between them does not
// break a run.
func TestChosenDays(t *testing.T) {
	g := Goal{Cadence: Days, Weekdays: []int{1, 3, 5}, Start: "2026-10-26", Status: Active} // 월·수·금
	if g.Rule() != "월·수·금" {
		t.Fatal(g.Rule())
	}
	done := marks("2026-10-26", "2026-10-28", "2026-10-30", "2026-11-02")
	p := g.Of(done, "2026-11-03") // Tuesday
	if p.Asks || p.Streak != 4 || p.Unit != "번" || !strings.Contains(p.Says, "오늘은 쉬는 날") {
		t.Fatalf("%+v", p)
	}
	if !p.Week[0].Asks || p.Week[1].Asks || p.Week[0].Mark != Done || !p.Week[2].Asks || p.Week[2].Open {
		t.Fatalf("%+v", p.Week)
	}
}

// A rest asks nothing and breaks nothing, in a day goal or a week goal.
func TestARestAsksNothing(t *testing.T) {
	g := Goal{Cadence: Daily, Start: "2026-10-26", Status: Active, Rests: []Rest{{From: "2026-10-29", To: "2026-10-31"}}}
	done := marks("2026-10-26", "2026-10-27", "2026-10-28", "2026-11-01")
	p := g.Of(done, "2026-11-02")
	if p.Streak != 4 || *p.Rate != 1 {
		t.Fatalf("%+v %v", p, *p.Rate)
	}
	w := weekly(3)
	w.Rests = []Rest{{From: "2026-11-02", To: "2026-11-05"}}
	if p := w.Of(marks(), "2026-11-06"); p.Period.Target != 2 {
		t.Fatalf("%+v", p.Period)
	}
}

// A week still going counts for the rate only once made: a Monday morning is
// not a bad week. A week gone by short is, whatever it got.
func TestRateCountsWhatHasClosed(t *testing.T) {
	g := weekly(3)
	g.Start = "2026-10-26"
	p := g.Of(marks("2026-10-26", "2026-10-27"), "2026-11-02")
	if p.Rate == nil || *p.Rate != 2.0/3 || p.Streak != 0 {
		t.Fatalf("%v %+v", p.Rate, p)
	}
	p = g.Of(marks("2026-10-26", "2026-10-27", "2026-10-28", "2026-11-02", "2026-11-03", "2026-11-04"), "2026-11-04")
	if p.Streak != 2 || p.Unit != "주" || *p.Rate != 1 {
		t.Fatalf("%+v", p)
	}
	fresh := weekly(3)
	fresh.Start = "2026-11-02"
	if p := fresh.Of(marks(), "2026-11-02"); p.Rate != nil {
		t.Fatalf("아직 물은 것이 없으면 비어 있어야 합니다: %v", *p.Rate)
	}
}

// The graph: twelve weeks back at most, none before the goal began, what each
// asked; and how the days of the week have gone, Monday first.
func TestStats(t *testing.T) {
	g := Goal{Cadence: Days, Weekdays: []int{1, 5}, Start: "2026-10-19", Status: Active}
	st := g.StatsOf(miss(marks("2026-10-19", "2026-10-26", "2026-10-30"), "2026-10-23"), "2026-11-02")
	if len(st.Bars) != 3 || st.Bars[0].From != "2026-10-19" || st.Bars[0].Done != 1 || st.Bars[0].Target != 2 || !st.Bars[2].Open {
		t.Fatalf("%+v", st.Bars)
	}
	if st.Weekdays[0].Day != 1 || st.Weekdays[0].Done != 2 || st.Weekdays[0].Asks != 2 || st.Weekdays[4].Day != 5 || st.Weekdays[4].Asks != 2 || st.Weekdays[4].Done != 1 {
		t.Fatalf("%+v", st.Weekdays)
	}
	if st.Marks["2026-10-23"] != Missed {
		t.Fatalf("%+v", st.Marks)
	}
	month := Goal{Cadence: Monthly, Times: 4, Start: "2026-03-01", Status: Active}
	st = month.StatsOf(marks("2026-11-01"), "2026-11-02")
	if len(st.Bars) != 9 || st.Bars[len(st.Bars)-1].From != "2026-11-01" || st.Bars[len(st.Bars)-1].Done != 1 || st.Bars[0].From != "2026-03-01" {
		t.Fatalf("%+v", st.Bars)
	}
}

func service(now *time.Time) *Service {
	return &Service{St: NewMemory(), Now: func() time.Time { return *now }, Loc: seoul}
}

func TestMakeAndChange(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 21, 0, 0, 0, seoul)
	s := service(&now)
	for name, n := range map[string]New{
		"이름 없음": {Cadence: Daily},
		"주기 없음": {Title: "운동"},
		"요일 없음": {Title: "운동", Cadence: Days},
		"횟수 없음": {Title: "운동", Cadence: Weekly},
		"시각":    {Title: "운동", Cadence: Daily, Remind: "25:00"},
		"끝이 먼저": {Title: "운동", Cadence: Daily, Start: "2026-11-02", End: "2026-11-01"},
	} {
		var bad ErrInvalid
		if _, err := s.Make(ctx, n); !errors.As(err, &bad) {
			t.Errorf("%s: %v", name, err)
		}
	}
	g, err := s.Make(ctx, New{Title: "  물  2L ", Cadence: Weekly, Times: 7, Who: Guest})
	if err != nil || g.Title != "물 2L" || g.Cadence != Daily || g.Start != "2026-11-02" || g.Person() != Guest {
		t.Fatalf("%+v %v", g, err)
	}
	if mine, _ := s.AllOf(ctx, Owner, false); len(mine) != 0 {
		t.Fatalf("손님 것이 보이면 안 됩니다: %+v", mine)
	}
	cadence := Weekly
	three := 3
	g, err = s.Change(ctx, g.ID, Change{Cadence: &cadence, Times: &three})
	if err != nil || g.Rule() != "주 3회" {
		t.Fatalf("%+v %v", g, err)
	}

	// Rested and picked up: only the days between are off.
	on, off := true, false
	if g, _ = s.Change(ctx, g.ID, Change{Pause: &on}); g.Status != Paused || g.Rests[0].From != "2026-11-02" {
		t.Fatalf("%+v", g)
	}
	if p := g.Of(marks(), "2026-11-02"); p.Says != "쉬는 중" {
		t.Fatal(p.Says)
	}
	now = now.AddDate(0, 0, 3)
	if g, _ = s.Change(ctx, g.ID, Change{Pause: &off}); g.Status != Active || g.Rests[0].To != "2026-11-04" {
		t.Fatalf("%+v", g)
	}

	// Ended, it keeps what it got and leaves the list; taken up again it is back.
	if g, _ = s.Change(ctx, g.ID, Change{Finish: &on}); g.Status != Ended || g.End != "2026-11-05" {
		t.Fatalf("%+v", g)
	}
	if open, _ := s.AllOf(ctx, Guest, false); len(open) != 0 {
		t.Fatalf("%+v", open)
	}
	if all, _ := s.AllOf(ctx, Guest, true); len(all) != 1 {
		t.Fatalf("%+v", all)
	}
	if g, _ = s.Change(ctx, g.ID, Change{Finish: &off}); g.Status != Active || g.End != "" {
		t.Fatalf("%+v", g)
	}
	if err := s.Remove(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestMarkDay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 3, 0, 30, 0, 0, seoul) // past midnight here, the day before in UTC
	s := service(&now)
	g, err := s.Make(ctx, New{Title: "독서", Cadence: Daily, Start: "2026-11-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, m, err := s.MarkDay(ctx, g.ID, "", Done, "", ByCard); err != nil || m["2026-11-03"].Mark != Done {
		t.Fatalf("이 땅의 오늘로 적혀야 합니다: %+v %v", m, err)
	}
	if _, m, err := s.MarkDay(ctx, g.ID, "2026-11-02", Missed, "약속", ByChat); err != nil || m["2026-11-02"].Mark != Missed || m["2026-11-02"].Note != "약속" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, m, err := s.MarkDay(ctx, g.ID, "2026-11-02", "", "", ByCard); err != nil || len(m) != 1 {
		t.Fatalf("지우면 기록이 없어져야 합니다: %+v %v", m, err)
	}
	for _, day := range []string{"2026-11-04", "2026-10-31", "어제"} {
		var bad ErrInvalid
		if _, _, err := s.MarkDay(ctx, g.ID, day, Done, "", ByCard); !errors.As(err, &bad) {
			t.Errorf("%s: %v", day, err)
		}
	}
	// A tied source fills in; it does not overrule what was said.
	if _, _, err := s.MarkDay(ctx, g.ID, "2026-11-02", Missed, "", ByCard); err != nil {
		t.Fatal(err)
	}
	_ = s.Unmarked(ctx, g.ID, "2026-11-02", ByWorkout)
	_ = s.Unmarked(ctx, g.ID, "2026-11-01", ByWorkout)
	m, _ := s.Marks(ctx, g.ID)
	if m["2026-11-02"].Mark != Missed || m["2026-11-01"].By != ByWorkout {
		t.Fatalf("%+v", m)
	}
}

func TestLine(t *testing.T) {
	g := weekly(3)
	g.ID, g.Title = "goal_1", "운동"
	p := g.Of(marks("2026-11-02"), "2026-11-03")
	want := "[goal_1] 운동 (주 3회) 이번 주 1/3 · 남은 6일 중 2번"
	if got := g.Line(p); got != want {
		t.Fatalf("%q", got)
	}
}

// The real tables, under mind's role: a goal and its marks, as the service keeps
// them; a day said twice is one row, and removing a goal takes its marks.
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없습니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 11, 3, 0, 30, 0, 0, seoul)
	s := &Service{St: NewPostgres(db), Now: func() time.Time { return now }, Loc: seoul}
	g, err := s.Make(ctx, New{Title: "독서", Cadence: Days, Weekdays: []int{1, 3}, Start: "2026-11-01", Remind: "21:00", Who: Guest})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Remove(ctx, g.ID) }()
	got, err := s.Get(ctx, g.ID)
	if err != nil || got.Rule() != "월·수" || got.Person() != Guest || got.Remind != "21:00" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, _, err := s.MarkDay(ctx, g.ID, "2026-11-02", Missed, "약속", ByChat); err != nil {
		t.Fatal(err)
	}
	if _, m, err := s.MarkDay(ctx, g.ID, "2026-11-02", Done, "", ByCard); err != nil || len(m) != 1 || m["2026-11-02"].Mark != Done || m["2026-11-02"].By != ByCard {
		t.Fatalf("%+v %v", m, err)
	}
	if _, m, err := s.MarkDay(ctx, g.ID, "2026-11-03", Missed, "", ByCard); err != nil || len(m) != 2 {
		t.Fatalf("%+v %v", m, err)
	}
	if _, m, err := s.MarkDay(ctx, g.ID, "2026-11-03", "", "", ByCard); err != nil || len(m) != 1 {
		t.Fatalf("%+v %v", m, err)
	}
	on := true
	if g, err = s.Change(ctx, g.ID, Change{Finish: &on}); err != nil || g.Status != Ended {
		t.Fatalf("%+v %v", g, err)
	}
	if open, err := s.AllOf(ctx, Guest, false); err != nil || slices.ContainsFunc(open, func(x Goal) bool { return x.ID == g.ID }) {
		t.Fatalf("%+v %v", open, err)
	}
	if all, err := s.AllOf(ctx, Guest, true); err != nil || !slices.ContainsFunc(all, func(x Goal) bool { return x.ID == g.ID }) {
		t.Fatalf("%+v %v", all, err)
	}
	if err := s.Remove(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if m, err := s.Marks(ctx, g.ID); err != nil || len(m) != 0 {
		t.Fatalf("지우면 기록도 없어져야 합니다: %+v %v", m, err)
	}
	if _, err := s.Get(ctx, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
