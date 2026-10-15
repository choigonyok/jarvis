package sensor

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

func TestRules(t *testing.T) {
	for in, want := range map[string]string{"GS25 망원점": "망원", "스타벅스 강남역점": "강남", "쿠팡": "", "육전식당 강남점": "강남"} {
		if got := AreaOf(in); got != want {
			t.Errorf("AreaOf(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"민수·지훈과 저녁": "식사", "가평 캠핑": "캠핑", "치과": "병원", "팀 회의": "업무", "엄마 생일": "기념일", "100일": "기념일", "1주년": "기념일", "뭔가": "기타"} {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := People("민수·지훈과 저녁"); !slices.Equal(got, []string{"민수", "지훈"}) {
		t.Errorf("People: %v", got)
	}
	if got := People("치과 예약"); len(got) != 0 {
		t.Errorf("People: %v", got)
	}
	o, ok := Transfer("kakao-pay:1", time.Now(), "김민수님이 32,000원을 보냈어요.")
	if !ok || o.Amount != 32000 || o.People[0] != "김민수" || o.Kind != "transfer" {
		t.Errorf("Transfer: %+v %v", o, ok)
	}
	if _, ok := Transfer("x", time.Now(), "32,000원 결제 완료"); ok {
		t.Error("결제 알림을 송금으로 읽었습니다")
	}
	ps := Purchase(tx{ID: 7, Kind: "approval", AmountKrw: 23000, Merchant: "쿠팡", Category: "쇼핑", Status: "ok",
		Items: []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Quantity  int    `json:"quantity"`
			AmountKrw int64  `json:"amountKrw"`
			Category  string `json:"category"`
		}{{ID: 1, Name: "참나무 장작 10kg", Quantity: 1, AmountKrw: 20000, Category: "생활"}}})
	if len(ps) != 2 || ps[1].ID != "spending:7#1" || ps[1].Kind != "purchase.item" || !slices.Contains(ps[0].Things, "참나무 장작 10kg") {
		t.Errorf("Purchase: %+v", ps)
	}
}

type observer struct{ st store.Store }

func (o observer) Observe(ctx context.Context, oc model.Occurrence) error {
	return o.st.PutOccurrence(ctx, oc)
}

// The calendar's entries become plans and stay in step; a conversation's
// plan on the calendar speaks for its entry; an entry gone cancels its plan.
func TestSyncCalendar(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, seoul)
	w := world.New(st, func() time.Time { return now })
	s := &Structured{Store: st, Observer: observer{st}, World: w, Loc: seoul, Now: func() time.Time { return now }}
	evs := []entry{{ID: "c1", Title: "민수·지훈과 저녁", Date: "2026-10-16", Start: "19:00"}, {ID: "c2", Title: "치과", Date: "2026-10-17", Start: "10:00"}}
	if err := s.SyncCalendar(ctx, evs, "2026-10-08", "2026-12-15"); err != nil {
		t.Fatal(err)
	}
	plans, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, true)
	if len(plans) != 2 {
		t.Fatalf("일정 2개: %+v", plans)
	}
	var dinner model.Entity
	for _, p := range plans {
		if p.State.CalendarID == "c1" {
			dinner = p
		}
	}
	if dinner.State.Type != "식사" || !slices.Equal(dinner.State.People, []string{"민수", "지훈"}) {
		t.Fatalf("저녁: %+v", dinner.State)
	}

	evs[0].Start = "19:30"
	_, _ = w.Create(ctx, model.KindPlan, "kakao:9", model.State{Title: "치과", Status: model.Confirmed, Date: "2026-10-17", CalendarID: "c2"}, Meta())
	if err := s.SyncCalendar(ctx, evs[:1], "2026-10-08", "2026-12-15"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Entity(ctx, dinner.ID)
	if got.State.Start != "19:30" {
		t.Fatalf("옮긴 시각을 따라가야 합니다: %+v", got.State)
	}
	live, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, true)
	if len(live) != 1 {
		t.Fatalf("지워진 일정은 취소: %+v", live)
	}
}

func Meta() world.Meta { return world.Meta{Actor: "test"} }
