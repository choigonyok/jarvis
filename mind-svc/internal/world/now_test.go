package world

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

// The guest's situation is the guest's: their own entries and the shared
// ones, where their own phone is - none of the operator's own plans,
// payments, photos or stays, and no names a plan took from the operator's
// conversations.
func TestGuestSituation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 21, 30, 0, 0, seoul)
	st := store.NewMemory()
	w := New(st, func() time.Time { return now })
	plan := func(room string, s model.State) {
		s.Status, s.Date = model.Confirmed, "2026-10-26"
		if _, err := w.Create(ctx, model.KindPlan, room, s, Meta{Actor: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	plan("calendar", model.State{Title: "운영자 회식", Start: "19:00", End: "23:00", Place: "성수"})
	plan("calendar", model.State{Title: "상대 합주", Start: "20:00", End: "23:00", Place: "합정 연습실", Owner: "partner"})
	plan("calendar", model.State{Title: "같이 야식", Start: "23:00", Shared: true, People: []string{"지수"}})
	plan("kakao:room", model.State{Title: "주말 캠핑 준비", Shared: true, People: []string{"민지"}})
	for _, o := range []model.Occurrence{
		{ID: "spending:1", Kind: "purchase", At: now.Add(-time.Hour), Title: "GS25 성수점", Area: "성수동"},
		{ID: "photo:1", Kind: "photo", At: now.Add(-30 * time.Minute), Title: "사진", Area: "성수동"},
		{ID: "visit:1", Kind: "visit", At: now.Add(-3 * time.Hour), Title: "회사", Place: "회사"},
	} {
		if err := st.PutOccurrence(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	here := &Clue{At: time.Date(2026, 10, 26, 21, 3, 0, 0, seoul), Kind: "visit", Title: "집", Place: "집"}
	s, err := w.GuestSituation(ctx, now, seoul, here)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Plans) != 1 || s.Plans[0].Title != "상대 합주" || s.Next == nil || s.Next.Title != "같이 야식" ||
		len(s.Next.People) != 1 || len(s.Today) != 1 || s.Today[0].Title != "주말 캠핑 준비" || len(s.Today[0].People) != 0 || len(s.Clues) != 0 {
		t.Fatalf("손님의 일정만, 남의 대화에서 온 이름은 없이: %+v", s)
	}
	for _, not := range []string{"운영자 회식", "성수", "GS25", "회사", "민지"} {
		if strings.Contains(s.Text, not) {
			t.Fatalf("운영자의 것(%s)이 손님 상황에: %s", not, s.Text)
		}
	}
	for _, want := range []string{"진행 중일 수 있는 일정: 상대 합주 20:00~23:00 · 장소 합정 연습실", "오늘 다음 일정: 23:00 같이 야식", "- 위치: 21:03부터 집", "추정: 집 (0.85) · 근거: 21:03부터 집(위치)"} {
		if !strings.Contains(s.Text, want) {
			t.Fatalf("손님 상황에 %q 없음:\n%s", want, s.Text)
		}
	}

	// The operator's is as it was: their own and the shared ones, their stay.
	o, err := w.Situation(ctx, now, seoul)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Plans) != 1 || o.Plans[0].Title != "운영자 회식" || strings.Contains(o.Text, "상대 합주") || o.Here == nil || o.Here.Place != "회사" || len(o.Clues) != 2 {
		t.Fatalf("운영자 상황: %+v", o)
	}

	// A phone in a region from before its time was kept: where, not since when.
	s, _ = w.GuestSituation(ctx, now, seoul, &Clue{Kind: "visit", Title: "집", Place: "집"})
	if !strings.Contains(s.Text, "- 위치: 집\n") || !strings.Contains(s.Text, "근거: 집(위치)") {
		t.Fatalf("들어온 시각 모름: %s", s.Text)
	}
	// Since yesterday: the date is said.
	s, _ = w.GuestSituation(ctx, now, seoul, &Clue{At: now.Add(-25 * time.Hour), Kind: "visit", Title: "집", Place: "집"})
	if !strings.Contains(s.Text, "- 위치: 10/25 20:30부터 집") {
		t.Fatalf("어제부터: %s", s.Text)
	}
}

// A plan with no time is today's, not going on at 3 a.m.; a plan with
// nowhere and nobody gives no guess.
func TestUntimedIsTodayNotOngoing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 3, 0, 0, 0, seoul)
	w := New(store.NewMemory(), func() time.Time { return now })
	_, _ = w.Create(ctx, model.KindPlan, "r", model.State{Title: "미용실 예약", Status: model.Confirmed, Date: "2026-10-15"}, Meta{Actor: "t"})
	_, _ = w.Create(ctx, model.KindPlan, "r", model.State{Title: "회의", Status: model.Confirmed, Date: "2026-10-15", Start: "02:30", Type: "업무"}, Meta{Actor: "t"})
	s, err := w.Situation(ctx, now, seoul)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Today) != 1 || len(s.Plans) != 1 || s.Guess != nil {
		t.Fatalf("시간 없는 일정은 오늘, 장소 없는 일정은 추정 없음: %+v", s)
	}
	if !strings.Contains(s.Text, "오늘(시간 미정): 미용실 예약") || strings.Contains(s.Text, "알 수 없는 곳") {
		t.Fatalf("요약: %s", s.Text)
	}
}
