package mind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/link"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// A card to add a plan reads the calendar first, and an entry there that the
// rules leave open is settled by the gate's one answer - the small model that
// settles two plans: the all-day "지수 생일" it calls another thing does not
// swallow the cake pickup at 10:30 on it, and "치과 진료" at 15:00 it calls the
// same is the "치과" told without a time.
func TestAnEntryTheRulesLeaveOpenIsJudged(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.New()
	clk.Pin(time.Date(2026, 11, 16, 12, 0, 0, 0, seoul))
	var asked []string
	judge := &llm.Script{Answer: func(system, user string) (string, error) {
		if system != link.SameSystem {
			return "", errors.New("같은 일정인지만 묻습니다")
		}
		asked = append(asked, user)
		return fmt.Sprintf(`{"same":%v}`, strings.Contains(user, "치과")), nil
	}}
	cal := act.NewMemoryCalendar()
	m := New(Options{Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: judge, Calendar: cal, Notifier: &act.MemoryNotifier{},
		Loc: seoul, Now: clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"}, Policy: gate.DefaultPolicy(), Mode: "eval"})
	ctx := context.Background()
	if _, err := cal.Put(ctx, model.CalendarEvent{ID: "5d0c1e2a-7b3f-4c1d-9e8a-2f6b4a1c0d9e#2026-11-21", Title: "지수 생일", Date: "2026-11-21", Owner: "shared"}); err != nil {
		t.Fatal(err)
	}
	dentist, err := cal.Put(ctx, model.CalendarEvent{Title: "치과 진료", Date: "2026-11-18", Start: "15:00"})
	if err != nil {
		t.Fatal(err)
	}
	add := func(plan string, e model.CalendarEvent) model.Intent {
		t.Helper()
		payload, _ := json.Marshal(model.CalendarAction{Event: e})
		in, err := m.Gate.Submit(ctx, model.Intent{Skill: "schedule", Kind: model.CalendarCreate, EntityID: plan, Payload: payload,
			Title: "캘린더에 추가: " + e.Title, Confidence: 0.9, Reversible: true})
		if err != nil {
			t.Fatal(err)
		}
		return in
	}

	pickup := add("plan_pickup", model.CalendarEvent{Title: "지수 생일 케이크 픽업", Date: "2026-11-21", Start: "10:30", Place: "케이크가게"})
	if pickup.Status != model.Pending || !pickup.Card || len(asked) != 1 || !strings.Contains(asked[0], "지수 생일 케이크 픽업") {
		t.Fatalf("생일과 다른 일이라 했으니 카드로 묻습니다: %+v %v", pickup, asked)
	}
	vague := add("plan_dentist", model.CalendarEvent{Title: "치과", Date: "2026-11-18"})
	if vague.Status != model.Executed || vague.Card || !strings.Contains(string(vague.Result), dentist.ID) || len(asked) != 2 {
		t.Fatalf("같은 일이라 했으니 이미 있는 항목에 잇습니다: %+v %v", vague, asked)
	}
	if n := len(cal.All()); n != 2 {
		t.Fatalf("캘린더에는 아무것도 더해지지 않습니다: %d", n)
	}
}
