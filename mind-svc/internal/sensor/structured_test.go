package sensor

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/book"
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
		Items: []book.Item{{ID: 1, Name: "참나무 장작 10kg", Quantity: 1, AmountKrw: 20000, Category: "생활"}}})
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

// An entry put on the calendar another way (the assistant, in a conversation)
// under another title is tied to the conversation's plan it is - no second
// plan of the calendar's own to be "missing from the calendar" or to collide.
func TestSyncCalendarTiesAConversationPlanToItsEntry(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 10, 17, 11, 0, 0, 0, seoul)
	w := world.New(st, func() time.Time { return now })
	s := &Structured{Store: st, Observer: observer{st}, World: w, Loc: seoul, Now: func() time.Time { return now }}
	talked, err := w.Create(ctx, model.KindPlan, "kakao:7", model.State{Title: "은주와 합주", Status: model.Confirmed, Date: "2026-10-17", Start: "15:00", People: []string{"은주"}}, Meta())
	if err != nil {
		t.Fatal(err)
	}
	evs := []entry{{ID: "c9", Title: "기타 합주 (은주)", Date: "2026-10-17", Start: "15:00", End: "16:00"}}
	if err := s.SyncCalendar(ctx, evs, "2026-10-10", "2026-12-17"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Entity(ctx, talked.ID)
	if got.State.CalendarID != "c9" {
		t.Fatalf("대화의 일정이 캘린더 일정에 이어져야 합니다: %+v", got.State)
	}
	if own, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, true); len(own) != 0 {
		t.Fatalf("캘린더 쪽 일정을 따로 만들면 안 됩니다: %+v", own)
	}
	// A different thing at the same time is not tied.
	_ = s.SyncCalendar(ctx, append(evs, entry{ID: "c10", Title: "치과", Date: "2026-10-17", Start: "15:00"}), "2026-10-10", "2026-12-17")
	if own, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, true); len(own) != 1 || own[0].State.CalendarID != "c10" {
		t.Fatalf("같은 때의 다른 일은 따로: %+v", own)
	}
}

// 2026-10-26: the conversation with the assistant was read as an all-day
// plan, and the assistant's card put the same title on the calendar at
// 20:00. Read from the calendar, that entry is the plan - tied, with the
// entry's time - and no plan of the calendar's own is made beside it. A
// plan tied before it had a time (linked when its card found the entry
// there) takes the entry's time the same way.
func TestSyncCalendarTiesAPlanToldWithoutATime(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 10, 26, 23, 40, 0, 0, seoul)
	w := world.New(st, func() time.Time { return now })
	s := &Structured{Store: st, Observer: observer{st}, World: w, Loc: seoul, Now: func() time.Time { return now }}
	talked, _ := w.Create(ctx, model.KindPlan, model.OwnerRoom, model.State{Title: "Netflix 플랜 변경·해지", Status: model.Confirmed, Date: "2026-11-15"}, Meta())
	linked, _ := w.Create(ctx, model.KindPlan, model.OwnerRoom, model.State{Title: "치과", Status: model.Confirmed, Date: "2026-11-16", CalendarID: "c8"}, Meta())
	evs := []entry{
		{ID: "c7", Title: "Netflix 플랜 변경·해지", Date: "2026-11-15", Start: "20:00", End: "20:30"},
		{ID: "c8", Title: "치과 진료", Date: "2026-11-16", Start: "10:00", End: "11:00"},
	}
	if err := s.SyncCalendar(ctx, evs, "2026-10-19", "2026-12-26"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Entity(ctx, talked.ID)
	if got.State.CalendarID != "c7" || got.State.Start != "20:00" || got.State.End != "20:30" {
		t.Fatalf("캘린더 일정에 이어지고 시각을 받아야 합니다: %+v", got.State)
	}
	if own, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, true); len(own) != 0 {
		t.Fatalf("캘린더 쪽 일정을 따로 만들면 안 됩니다: %+v", own)
	}
	got, _ = st.Entity(ctx, linked.ID)
	if got.State.Start != "10:00" || got.State.End != "11:00" || got.State.Title != "치과" {
		t.Fatalf("이미 이어진 일정도 시각을 받아야 합니다(제목은 그대로): %+v", got.State)
	}
	// Read again, nothing more changes.
	before, _ := st.Changes(ctx, talked.ID, 100)
	_ = s.SyncCalendar(ctx, evs, "2026-10-19", "2026-12-26")
	after, _ := st.Changes(ctx, talked.ID, 100)
	if len(after) != len(before) {
		t.Fatalf("다시 읽어도 그대로: %d → %d", len(before), len(after))
	}
}

// A pair split before the fix - the entry read first as a plan of its own,
// the conversation's plan moved onto its time after - is mended on the next
// pass: the conversation's plan takes the entry, the calendar's copy goes.
func TestSyncCalendarMendsASplitPair(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 10, 17, 11, 0, 0, 0, seoul)
	w := world.New(st, func() time.Time { return now })
	s := &Structured{Store: st, Observer: observer{st}, World: w, Loc: seoul, Now: func() time.Time { return now }}
	talked, _ := w.Create(ctx, model.KindPlan, "kakao:7", model.State{Title: "은주와 합주", Status: model.Confirmed, Date: "2026-10-17", Start: "16:00", People: []string{"은주"}}, Meta())
	evs := []entry{{ID: "c9", Title: "기타 합주 (은주)", Date: "2026-10-17", Start: "15:00", End: "16:00"}}
	_ = s.SyncCalendar(ctx, evs, "2026-10-10", "2026-12-17")
	if own, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, true); len(own) != 1 {
		t.Fatalf("한 시간 다르면 아직 다른 일정: %+v", own)
	}
	_, _, _ = w.Update(ctx, talked.ID, func(s *model.State) bool { s.Start = "15:00"; return true }, Meta())
	_ = s.SyncCalendar(ctx, evs, "2026-10-10", "2026-12-17")
	got, _ := st.Entity(ctx, talked.ID)
	if got.State.CalendarID != "c9" {
		t.Fatalf("대화의 일정이 캘린더 일정을 맡아야 합니다: %+v", got.State)
	}
	if own, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, true); len(own) != 0 {
		t.Fatalf("캘린더 쪽 복사본은 없어져야 합니다: %+v", own)
	}
}

// A past entry's plan the night marked done is still that entry: read
// again, it is not made anew (every night made one more, 2026-10-20) - and
// the copies left from before are called off, the first one kept.
func TestSyncCalendarKeepsADonePlan(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	now := time.Date(2026, 10, 19, 3, 0, 0, 0, seoul)
	w := world.New(st, func() time.Time { return now })
	s := &Structured{Store: st, Observer: observer{st}, World: w, Loc: seoul, Now: func() time.Time { return now }}
	evs := []entry{{ID: "c1", Title: "데이트", Date: "2026-10-18", Start: "15:30", Place: "수원역"}}
	if err := s.SyncCalendar(ctx, evs, "2026-10-12", "2026-12-19"); err != nil {
		t.Fatal(err)
	}
	plans, _ := st.Entities(ctx, model.KindPlan, CalendarRoom, false)
	if len(plans) != 1 {
		t.Fatalf("캘린더 일정 하나: %+v", plans)
	}
	first := plans[0].ID
	_, _, _ = w.Update(ctx, first, func(s *model.State) bool { s.Status = model.Done; return true }, world.Meta{Actor: "observer", Why: "날짜가 지남"})
	for range 3 {
		now = now.Add(24 * time.Hour)
		if err := s.SyncCalendar(ctx, evs, "2026-10-12", "2026-12-19"); err != nil {
			t.Fatal(err)
		}
	}
	plans, _ = st.Entities(ctx, model.KindPlan, CalendarRoom, false)
	if len(plans) != 1 || plans[0].State.Status != model.Done {
		t.Fatalf("끝난 일정은 그대로 하나여야 합니다: %+v", plans)
	}

	// Copies the old reading left behind.
	for range 2 {
		now = now.Add(time.Hour)
		if _, err := w.Create(ctx, model.KindPlan, CalendarRoom, fromEntry(evs[0]), world.Meta{Actor: "sensor:calendar"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.dedupeCalendar(ctx, "2026-07-22", "2026-12-22"); err != nil {
		t.Fatal(err)
	}
	plans, _ = st.Entities(ctx, model.KindPlan, CalendarRoom, false)
	kept := 0
	for _, p := range plans {
		if p.State.Status != model.Cancelled {
			kept++
			if p.ID != first {
				t.Fatalf("처음 읽은 일정이 남아야 합니다: %+v", p)
			}
		}
	}
	if kept != 1 || len(plans) != 3 {
		t.Fatalf("중복은 취소: %+v", plans)
	}
	// Called off by the calendar's reader: nothing to take off the calendar.
	for _, p := range plans {
		if p.ID == first {
			continue
		}
		cs, _ := st.Changes(ctx, p.ID, 1)
		if len(cs) == 0 || cs[0].Actor != "sensor:calendar" || cs[0].Why != "같은 캘린더 항목의 중복" {
			t.Fatalf("중복 취소의 근거: %+v", cs)
		}
	}
}

// Everyday titles that were 기타 before (2026-10-20).
func TestClassifyMoreTitles(t *testing.T) {
	for in, want := range map[string]string{"호캉스": "여행", "민지와 삼겹살": "식사", "아빠 생신": "기념일", "서연이랑 야구 보기": "공연",
		"레코드 페어": "공연", "독감 접종": "병원", "지수와 치킨 저녁": "식사"} {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %q, want %q", in, got, want)
		}
	}
}
