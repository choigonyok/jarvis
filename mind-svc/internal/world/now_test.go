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
