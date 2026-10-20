package mind

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// The engine end to end: a rule found at night predicts in silence while it
// is a candidate; once it is shown, what it expects of today is said to the
// conversation - until it happens.
func TestEngineSaysOnlyWhatCameTrue(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	day := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, seoul) }
	m, st, clk := testMind(t, day(11, 22, 3, 0)) // a Sunday
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := m.Opts.Log.(*obs.MemoryLog)
	go log.Run(ctx, m.Handle)
	n := 0
	for d := day(10, 5, 0, 0); d.Before(day(11, 22, 0, 0)); d = d.AddDate(0, 0, 1) {
		n++
		switch d.Weekday() {
		case time.Sunday:
			_ = st.PutOccurrence(ctx, pay(fmt.Sprintf("s%d", n), "주식회사 우아한형제", "식비", 21000, d.Add(12*time.Hour+30*time.Minute)))
		case time.Saturday:
		default:
			_ = st.PutOccurrence(ctx, pay(fmt.Sprintf("b%d", n), "버스", "교통", 1500, d.Add(8*time.Hour+30*time.Minute)))
		}
	}
	if err := m.engineNight(ctx); err != nil {
		t.Fatal(err)
	}
	id := self.ID(predict.Daily, predict.Delivery)
	note, err := st.Note(ctx, id)
	if err != nil || note.Status != model.Candidate {
		t.Fatalf("일요일 배달은 검증 중인 규칙으로: %+v %v", note, err)
	}
	if all, _ := m.Notes(ctx, false); slices.ContainsFunc(all, func(n model.Note) bool { return n.ID == id }) {
		t.Fatal("검증 중인 규칙은 화면에 묻지 않습니다")
	}
	clk.Pin(day(11, 22, 7, 0))
	if err := m.engineMorning(ctx); err != nil {
		t.Fatal(err)
	}
	pr, err := st.Prediction(ctx, predict.ID("day:"+predict.Delivery+":2026-11-22"))
	if err != nil || pr.Prob < 0.7 || pr.Base <= 0 || pr.Base >= pr.Prob {
		t.Fatalf("오늘 배달 예측: %+v %v", pr, err)
	}
	s, _ := m.Situation(ctx)
	if strings.Contains(s.Text, "배달 주문") {
		t.Fatalf("검증 중인 규칙의 기대는 말하지 않습니다:\n%s", s.Text)
	}
	// Its predictions came true: shown.
	note.Status = model.Hypothesis
	_ = st.PutNote(ctx, note)
	s, _ = m.Situation(ctx)
	if !strings.Contains(s.Text, "평소대로라면 오늘 있을 일") || !strings.Contains(s.Text, fmt.Sprintf("배달 주문(보통 12:30) %.0f%%", pr.Prob*100)) {
		t.Fatalf("맞아 온 규칙의 기대:\n%s", s.Text)
	}
	t.Log(s.Text)
	// It happened: not expected any more, and the prediction came true.
	clk.Pin(day(11, 22, 12, 40))
	if err := m.Observe(ctx, pay("s-today", "주식회사 우아한형제", "식비", 19000, day(11, 22, 12, 35))); err != nil {
		t.Fatal(err)
	}
	log.Drain()
	s, _ = m.Situation(ctx)
	if strings.Contains(s.Text, "배달 주문") {
		t.Fatalf("이미 있었던 일은 기대가 아닙니다:\n%s", s.Text)
	}
	clk.Pin(day(11, 23, 0, 5))
	m.Attn.Settle(ctx)
	pr, _ = st.Prediction(ctx, pr.ID)
	if pr.Outcome == nil || !*pr.Outcome {
		t.Fatalf("맞은 예측으로 채점: %+v", pr)
	}
}

// A monthly payment a trusted cycle expected and that did not come is a
// line for the evening; one a candidate expected says nothing.
func TestMissedMonthlyPayment(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	day := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, seoul) }
	m, st, clk := testMind(t, day(11, 1, 3))
	ctx := context.Background()
	for i, mo := range []time.Month{6, 7, 8, 9, 10} {
		_ = st.PutOccurrence(ctx, pay(fmt.Sprintf("n%d", i), "넷플릭스", "구독", 17000, day(mo, 15, 9)))
		_ = st.PutOccurrence(ctx, pay(fmt.Sprintf("t%d", i), "SKT", "통신", 55000, day(mo, 15, 10)))
	}
	if err := m.engineNight(ctx); err != nil {
		t.Fatal(err)
	}
	netflix := self.ID(predict.Cycle, "purchase/구독@넷플릭스")
	n, err := st.Note(ctx, netflix)
	if err != nil || n.Rule.Gap != 30 || !strings.HasPrefix(n.Text, "매달 15일쯤 넷플릭스 결제") {
		t.Fatalf("매달 넷플릭스: %+v %v", n, err)
	}
	if _, err := st.Note(ctx, self.ID(predict.Cycle, "purchase/통신@SKT")); err != nil {
		t.Fatalf("매달 SKT: %v", err)
	}
	n.Status = model.Hypothesis // its predictions came true before
	_ = st.PutNote(ctx, n)
	clk.Pin(day(11, 13, 7))
	if err := m.engineMorning(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Pin(day(11, 16, 0).Add(10 * time.Minute))
	m.Attn.Settle(ctx)
	intents, _ := st.Intents(ctx, 50)
	var lines []string
	for _, in := range intents {
		lines = append(lines, in.Title)
	}
	if !slices.Contains(lines, "이번엔 넷플릭스 결제가 없었어요") {
		t.Fatalf("빗나간 넷플릭스 결제를 저녁에: %v", lines)
	}
	if slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "SKT") }) {
		t.Fatalf("검증 중인 규칙은 말하지 않습니다: %v", lines)
	}
	for _, in := range intents {
		if strings.Contains(in.Title, "넷플릭스") && in.Tier != "digest" {
			t.Fatalf("저녁 요약으로: %+v", in)
		}
	}
}

// A thing bought before several plans of a kind counts for the nearest
// only, and a plan told twice counts once.
func TestPrepCountsTheNearestPlanOnly(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	m, st, _ := testMind(t, time.Date(2026, 11, 20, 3, 0, 0, 0, seoul))
	ctx := context.Background()
	for _, d := range []string{"2026-11-02", "2026-11-06", "2026-11-06", "2026-11-09", "2026-11-12"} {
		if _, err := m.World.Create(ctx, model.KindPlan, "calendar", model.State{Title: "데이트", Status: model.Done, Date: d, Start: "15:00", Type: "데이트"},
			world.Meta{Actor: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.PutThingClass(ctx, "향초 세트", "향초")
	_ = st.PutOccurrence(ctx, model.Occurrence{ID: "s2#4", Kind: "purchase.item", At: time.Date(2026, 10, 30, 22, 51, 0, 0, seoul), Title: "향초 세트", Ref: "s2"})
	if _, err := m.RebuildPatterns(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := st.Patterns(ctx, "데이트")
	total, support := 0, 0
	for _, p := range ps {
		if p.KindB == "" {
			total += p.Total
		} else if p.KindB == "향초" {
			support += p.Support
		}
	}
	if total != 4 || support != 1 {
		t.Fatalf("데이트 4번(같은 날 두 번 적힌 것은 하나), 향초는 가장 가까운 한 번에만: total %d support %d %+v", total, support, ps)
	}
}
