package mind

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/goals"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"io"
	"log/slog"
)

func goalMind(t *testing.T, at time.Time) (*Mind, *act.MemoryNotifier, *clock.Clock) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.New()
	clk.Pin(at)
	note := &act.MemoryNotifier{}
	m := New(Options{Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: answerLLM{}, Calendar: act.NewMemoryCalendar(), Notifier: note,
		Loc: at.Location(), Now: clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"}, Policy: gate.DefaultPolicy(), Mode: "eval",
		Goals: goals.NewMemory()})
	return m, note, clk
}

func makeGoal(t *testing.T, m *Mind, n goals.New) goals.Goal {
	t.Helper()
	g, err := m.Goals.Make(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func said(note *act.MemoryNotifier) []string {
	var out []string
	for _, n := range note.All() {
		out = append(out, n.Title+" | "+n.Body)
	}
	return out
}

// The operator's example: three a week, nothing by Thursday night. The evening
// it can only just be made it says which days it takes - once - and the week
// that has a day in hand says nothing.
func TestTheEveningAWeekCanOnlyJustBeMade(t *testing.T) {
	ctx := context.Background()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	wed := time.Date(2026, 11, 4, 21, 30, 0, 0, seoul)
	m, note, clk := goalMind(t, wed)
	g := makeGoal(t, m, goals.New{Title: "운동", Cadence: goals.Weekly, Times: 3, Start: "2026-10-05"})
	if _, _, err := m.Goals.MarkDay(ctx, g.ID, "2026-10-30", goals.Done, "", goals.ByCard); err != nil {
		t.Fatal(err)
	}
	if err := m.goalNight(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(note); len(got) != 0 {
		t.Fatalf("수요일 밤에는 아직 하루 여유가 있습니다: %v", got)
	}
	clk.Pin(wed.AddDate(0, 0, 1))
	if err := m.goalNight(ctx); err != nil {
		t.Fatal(err)
	}
	got := said(note)
	if len(got) != 1 || !strings.Contains(got[0], "'운동' 남은 날 다 해야 해요") || !strings.Contains(got[0], "이번 주 0/3, 금·토·일 모두 해야 3번을 채워요") {
		t.Fatalf("%v", got)
	}
	last := note.All()[0]
	if last.Kind != "remind.goal" || !last.Card || last.URL != "/goals" || last.Data["goals"] == nil {
		t.Fatalf("%+v", last)
	}
	// Once a week: one done and still tight on Friday says nothing more.
	if _, _, err := m.Goals.MarkDay(ctx, g.ID, "2026-11-05", goals.Done, "", goals.ByCard); err != nil {
		t.Fatal(err)
	}
	clk.Pin(wed.AddDate(0, 0, 2))
	if err := m.goalNight(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(note); len(got) != 1 {
		t.Fatalf("%v", got)
	}
}

// A day a goal asked of with no word on it is asked about at noon, each person
// their own - a goal let go of for days, or a day already said, not at all.
func TestYesterdayIsAskedAboutAtNoon(t *testing.T) {
	ctx := context.Background()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	noon := time.Date(2026, 11, 3, 12, 30, 0, 0, seoul) // Tuesday
	m, note, _ := goalMind(t, noon)
	water := makeGoal(t, m, goals.New{Title: "물 2L", Cadence: goals.Daily, Start: "2026-11-02"})
	makeGoal(t, m, goals.New{Title: "독서", Cadence: goals.Days, Weekdays: []int{3}, Start: "2026-10-01"})
	makeGoal(t, m, goals.New{Title: "스트레칭", Cadence: goals.Daily, Start: "2026-11-02", Who: goals.Guest})
	marked := makeGoal(t, m, goals.New{Title: "일기", Cadence: goals.Daily, Start: "2026-11-02"})
	if _, _, err := m.Goals.MarkDay(ctx, marked.ID, "2026-11-02", goals.Missed, "", goals.ByCard); err != nil {
		t.Fatal(err)
	}
	makeGoal(t, m, goals.New{Title: "버린 목표", Cadence: goals.Daily, Start: "2026-10-01"})
	if err := m.goalNoon(ctx); err != nil {
		t.Fatal(err)
	}
	got := note.All()
	if len(got) != 2 {
		t.Fatalf("운영자 하나, 손님 하나: %v", said(note))
	}
	for _, n := range got {
		list, _ := n.Data["goals"].([]goalAsk)
		switch n.Audience {
		case "":
			if n.Title != "어제 '물 2L' 했어요?" || len(list) != 1 || list[0].Goal != water.ID || list[0].Day != "2026-11-02" {
				t.Fatalf("%+v", n)
			}
		case goals.Guest:
			if !strings.Contains(n.Title, "스트레칭") || n.Data["who"] != goals.Guest {
				t.Fatalf("%+v", n)
			}
		default:
			t.Fatalf("%+v", n)
		}
	}
}

// A run worth keeping gets a word before the day ends; the goal's own hour is
// said once, and not once today has an answer. Sunday says how the week went.
func TestEveningsWords(t *testing.T) {
	ctx := context.Background()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	sunday := time.Date(2026, 11, 8, 21, 30, 0, 0, seoul)
	m, note, _ := goalMind(t, sunday)
	g := makeGoal(t, m, goals.New{Title: "물 2L", Cadence: goals.Daily, Start: "2026-10-30"})
	for d := time.Date(2026, 10, 30, 0, 0, 0, 0, seoul); d.Before(sunday.Truncate(24 * time.Hour)); d = d.AddDate(0, 0, 1) {
		if day := d.Format("2006-01-02"); day < "2026-11-08" {
			if _, _, err := m.Goals.MarkDay(ctx, g.ID, day, goals.Done, "", goals.ByCard); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := m.goalNight(ctx); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(said(note), "\n")
	if !strings.Contains(got, "오늘 '물 2L' 기록이 아직 없어요") || !strings.Contains(got, "이번 주 목표 | 물 2L 6/7") {
		t.Fatalf("%s", got)
	}

	seven := time.Date(2026, 11, 3, 20, 0, 0, 0, seoul)
	m, note, clk := goalMind(t, seven)
	h := makeGoal(t, m, goals.New{Title: "독서", Cadence: goals.Daily, Start: "2026-11-01", Remind: "20:00"})
	if err := m.goalHour(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Pin(seven.Add(time.Minute))
	if err := m.goalHour(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(note); len(got) != 1 || !strings.Contains(got[0], "오늘 '독서' 했어요?") {
		t.Fatalf("%v", got)
	}
	clk.Pin(seven.AddDate(0, 0, 1))
	if _, _, err := m.Goals.MarkDay(ctx, h.ID, "2026-11-04", goals.Done, "", goals.ByCard); err != nil {
		t.Fatal(err)
	}
	if err := m.goalHour(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(note); len(got) != 1 {
		t.Fatalf("이미 답한 날은 묻지 않습니다: %v", got)
	}
	// Made after its hour had passed, it waits for tomorrow's.
	late, lateNote, lateClock := goalMind(t, seven.Add(time.Hour))
	makeGoal(t, late, goals.New{Title: "늦게 만든 목표", Cadence: goals.Daily, Remind: "20:00"})
	if err := late.goalHour(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(lateNote); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	lateClock.Pin(seven.AddDate(0, 0, 1))
	if err := late.goalHour(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(lateNote); len(got) != 1 {
		t.Fatalf("%v", got)
	}
}

// Pushes of one kind take each other's place on a phone: goals due at one
// moment go out as one notice, each person's own, every goal answerable there.
func TestGoalsOfOneMomentGoOutTogether(t *testing.T) {
	ctx := context.Background()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	nine := time.Date(2026, 11, 5, 21, 0, 0, 0, seoul)
	m, note, _ := goalMind(t, nine.Add(-time.Hour))
	makeGoal(t, m, goals.New{Title: "독서", Cadence: goals.Daily, Start: "2026-11-05", Remind: "21:00"})
	makeGoal(t, m, goals.New{Title: "스트레칭", Cadence: goals.Daily, Start: "2026-11-05", Remind: "21:00"})
	makeGoal(t, m, goals.New{Title: "손님 독서", Cadence: goals.Daily, Start: "2026-11-05", Remind: "21:00", Who: goals.Guest})
	m.Goals.Now = func() time.Time { return nine }
	m.Opts.Now = m.Goals.Now
	if err := m.goalHour(ctx); err != nil {
		t.Fatal(err)
	}
	all := note.All()
	if len(all) != 2 {
		t.Fatalf("사람마다 하나씩이어야 합니다: %v", said(note))
	}
	for _, n := range all {
		asks, _ := n.Data["goals"].([]goalAsk)
		switch n.Audience {
		case goals.Guest:
			if len(asks) != 1 || !strings.Contains(n.Title, "손님 독서") {
				t.Fatalf("%+v", n)
			}
		default:
			if len(asks) != 2 || n.Title != "오늘 목표 2개, 했어요?" {
				t.Fatalf("%+v", n)
			}
		}
	}

	thursday := time.Date(2026, 11, 5, 21, 30, 0, 0, seoul)
	m, note, _ = goalMind(t, thursday)
	run := makeGoal(t, m, goals.New{Title: "달리기", Cadence: goals.Weekly, Times: 3, Start: "2026-11-02"})
	read := makeGoal(t, m, goals.New{Title: "책", Cadence: goals.Weekly, Times: 3, Start: "2026-11-02"})
	if _, _, err := m.Goals.MarkDay(ctx, read.ID, "2026-11-05", goals.Missed, "", goals.ByCard); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := m.goalNight(ctx); err != nil {
			t.Fatal(err)
		}
	}
	all = note.All()
	if len(all) != 1 || all[0].Title != "목표 2개가 남은 날 다 해야 해요" {
		t.Fatalf("%v", said(note))
	}
	if b := all[0].Body; !strings.Contains(b, "- 달리기: 이번 주 0/3, 금·토·일 모두 해야 3번을 채워요") || !strings.Contains(b, "- 책: ") {
		t.Fatal(b)
	}
	// 책 was answered today: only 달리기 is left to ask about.
	if asks, _ := all[0].Data["goals"].([]goalAsk); len(asks) != 1 || asks[0].Goal != run.ID {
		t.Fatalf("%+v", asks)
	}
}

// A workout saved marks the goal tied to the log, unless the day was said.
func TestAWorkoutMarksItsGoal(t *testing.T) {
	ctx := context.Background()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	now := time.Date(2026, 11, 3, 20, 0, 0, 0, seoul)
	m, _, _ := goalMind(t, now)
	tied := makeGoal(t, m, goals.New{Title: "운동", Cadence: goals.Weekly, Times: 3, Start: "2026-10-01", Auto: goals.AutoWorkout})
	free := makeGoal(t, m, goals.New{Title: "독서", Cadence: goals.Weekly, Times: 3, Start: "2026-10-01"})
	if err := m.goalWorkout(ctx, model.Occurrence{Kind: "workout", At: now}); err != nil {
		t.Fatal(err)
	}
	if ms, _ := m.Goals.Marks(ctx, tied.ID); ms["2026-11-03"].Mark != goals.Done || ms["2026-11-03"].By != goals.ByWorkout {
		t.Fatalf("%+v", ms)
	}
	if ms, _ := m.Goals.Marks(ctx, free.ID); len(ms) != 0 {
		t.Fatalf("%+v", ms)
	}
}

// What the conversation is told, each person their own.
func TestGoalsInTheSituation(t *testing.T) {
	ctx := context.Background()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	m, _, _ := goalMind(t, time.Date(2026, 11, 3, 9, 0, 0, 0, seoul))
	makeGoal(t, m, goals.New{Title: "운동", Cadence: goals.Weekly, Times: 3})
	makeGoal(t, m, goals.New{Title: "손님 산책", Cadence: goals.Daily, Who: goals.Guest})
	s, err := m.Situation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Text, "운동 (주 3회)") || strings.Contains(s.Text, "손님 산책") {
		t.Fatal(s.Text)
	}
	g, err := m.GuestSituation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Text, "손님 산책 (매일)") || strings.Contains(g.Text, "운동") {
		t.Fatal(g.Text)
	}
}

// A count over days stops asking too once it has gone quiet for three weeks.
func TestAGoalLetGoOfIsNotAskedAbout(t *testing.T) {
	ctx := context.Background()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	thursday := time.Date(2026, 11, 5, 21, 30, 0, 0, seoul)
	m, note, _ := goalMind(t, thursday)
	makeGoal(t, m, goals.New{Title: "운동", Cadence: goals.Weekly, Times: 3, Start: "2026-09-01"})
	if err := m.goalNight(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(note); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	kept := makeGoal(t, m, goals.New{Title: "독서", Cadence: goals.Weekly, Times: 3, Start: "2026-09-01"})
	if _, _, err := m.Goals.MarkDay(ctx, kept.ID, "2026-10-30", goals.Done, "", goals.ByCard); err != nil {
		t.Fatal(err)
	}
	if err := m.goalNight(ctx); err != nil {
		t.Fatal(err)
	}
	if got := said(note); len(got) != 1 || !strings.Contains(got[0], "독서") {
		t.Fatalf("%v", got)
	}
}
