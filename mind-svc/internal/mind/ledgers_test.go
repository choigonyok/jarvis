package mind

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

func testMind(t *testing.T, at time.Time) (*Mind, *store.Memory, *clock.Clock) {
	t.Helper()
	seoul, _ := time.LoadLocation("Asia/Seoul")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.New()
	clk.Pin(at)
	st := store.NewMemory()
	m := New(Options{Store: st, Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(), Notifier: &act.MemoryNotifier{},
		Loc: seoul, Now: clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"}, Policy: gate.DefaultPolicy(), Mode: "eval"})
	return m, st, clk
}

func pay(id, merchant, class string, amount int64, at time.Time) model.Occurrence {
	return model.Occurrence{ID: id, Kind: "purchase", At: at, Title: merchant, Class: class, Amount: amount, Ref: id}
}

// Monthly charges: the same merchant, about the same day and amount, three
// months running - and the month it changed. Shopping is not a bill.
func TestFixedCosts(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 9, 0, 0, 0, seoul) }
	m, st, _ := testMind(t, day(10, 16))
	ctx := context.Background()
	for i, mo := range []time.Month{6, 7, 8, 9, 10} {
		amount := int64(13500)
		if mo == 10 {
			amount = 17000
		}
		_ = st.PutOccurrence(ctx, pay("s"+string(rune('a'+i)), "넷플릭스", "구독", amount, day(mo, 14+i%2)))
		_ = st.PutOccurrence(ctx, pay("t"+string(rune('a'+i)), "SKT 통신요금", "통신", 55000, day(mo, 25)))
		_ = st.PutOccurrence(ctx, pay("c"+string(rune('a'+i)), "쿠팡", "쇼핑", 30000, day(mo, 3)))
		_ = st.PutOccurrence(ctx, pay("d"+string(rune('a'+i)), "쿠팡", "쇼핑", 12000, day(mo, 20)))
	}
	fixed, err := m.FixedCosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Fixed{}
	for _, f := range fixed {
		byName[f.Merchant] = f
	}
	if len(fixed) != 2 || byName["넷플릭스"].Before != 13500 || byName["넷플릭스"].Amount != 17000 || byName["SKT 통신요금"].Before != 0 || byName["SKT 통신요금"].Months != 4 {
		t.Fatalf("고정비: %+v", fixed)
	}
	if err := m.fixedChanges(ctx); err != nil {
		t.Fatal(err)
	}
	intents, _ := st.Intents(ctx, 100)
	if len(intents) != 1 || !strings.Contains(intents[0].Title, "넷플릭스 1만 3,500원 → 1만 7,000원") {
		t.Fatalf("바뀐 고정비 한 줄: %+v", intents)
	}
}

func TestSizes(t *testing.T) {
	for name, want := range map[string][]string{
		"나이키 에어포스 1 화이트 270mm": {"270mm"},
		"유니클로 히트텍 크루넥 T (XL)":  {"XL"},
		"남성 등산바지 사이즈 32":       {"32"},
		"삼다수 2L 24개":           nil,
	} {
		if got := Sizes(name); !slices.Equal(got, want) {
			t.Errorf("Sizes(%q) = %v, want %v", name, got, want)
		}
	}
}

// The operator's map: reviewed places, best first, with visits and spend;
// meals with delivery and home; a warranty's year about to end.
func TestPlacesMealsWarranty(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, seoul)
	m, st, _ := testMind(t, now)
	ctx := context.Background()
	_ = st.AddObservations(ctx, []model.Observation{
		{SubjectType: "식당", Subject: "망원 국숫집", Score: 5, Source: "r1", At: now.AddDate(0, 0, -20)},
		{SubjectType: "식당", Subject: "망원 국숫집", Dimension: "가격 대비", Polarity: 1, Score: 5, Source: "r1", At: now.AddDate(0, 0, -20)},
		{SubjectType: "캠핑장", Subject: "강변캠핑장", Score: 2, Source: "r2", At: now.AddDate(0, 0, -10)},
		{SubjectType: "캠핑장", Subject: "강변캠핑장", Dimension: "소음", Polarity: -1, Score: 2, Source: "r2", At: now.AddDate(0, 0, -10)},
		{SubjectType: "물건", Subject: "캠핑 의자", Score: 1, Source: "r3", At: now},
	})
	_ = st.PutOccurrence(ctx, pay("p1", "망원 국숫집", "식비", 18000, now.AddDate(0, 0, -20).Add(3*time.Hour)))
	_ = st.PutOccurrence(ctx, pay("p2", "망원 국숫집", "식비", 22000, now.AddDate(0, 0, -5).Add(3*time.Hour)))
	places, err := m.Places(ctx)
	if err != nil || len(places) != 2 || places[0].Name != "망원 국숫집" || places[0].Visits != 2 || places[0].Spent != 40000 ||
		!slices.Contains(places[0].Good, "가격 대비") || !slices.Contains(places[1].Bad, "소음") {
		t.Fatalf("나만의 지도: %+v %v", places, err)
	}

	home := now.Add(-26 * time.Hour)
	left := home.Add(14 * time.Hour)
	_ = st.PutOccurrence(ctx, model.Occurrence{ID: "visit:1", Kind: "visit", At: home, Until: &left, Title: "집", Class: "home", Ref: "visit:1"})
	_ = st.PutOccurrence(ctx, pay("p3", "배달의민족", "식비", 27000, home.Add(2*time.Hour)))
	meals, err := m.Meals(ctx, 30)
	if err != nil || len(meals) != 3 {
		t.Fatalf("식사: %+v %v", meals, err)
	}
	var delivered Meal
	for _, x := range meals {
		if x.Delivery {
			delivered = x
		}
	}
	if !delivered.Home || delivered.Merchant != "배달의민족" {
		t.Fatalf("집에서 배달: %+v", delivered)
	}

	bought := now.Add(-Warranty + 10*24*time.Hour)
	_ = st.PutOccurrence(ctx, model.Occurrence{ID: "spending:9#1", Kind: "purchase.item", At: bought, Title: "로봇 청소기 X1", Class: "가전디지털", Amount: 459000, Ref: "spending:9"})
	_ = st.PutOccurrence(ctx, model.Occurrence{ID: "spending:9#2", Kind: "purchase.item", At: bought, Title: "물티슈", Class: "생활용품", Amount: 12000, Ref: "spending:9"})
	if err := m.warranties(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.warranties(ctx); err != nil { // once
		t.Fatal(err)
	}
	intents, _ := st.Intents(ctx, 100)
	n := 0
	for _, in := range intents {
		if strings.Contains(in.Title, "보증") {
			n++
			if !strings.Contains(in.Title, "로봇 청소기") {
				t.Fatalf("보증: %+v", in)
			}
		}
	}
	if n != 1 {
		t.Fatalf("보증 알림은 한 번: %d", n)
	}
}
