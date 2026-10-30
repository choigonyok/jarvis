package mind

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

func streamed(t *testing.T, now *time.Time) (*Mind, *[]model.Timer, *act.MemoryNotifier) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	seoul, _ := time.LoadLocation("Asia/Seoul")
	var sent []model.Timer
	notes := &act.MemoryNotifier{}
	m := New(Options{
		Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: notes, Loc: seoul, Now: func() time.Time { return *now }, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
		Reminders: moments.NewMemory(), Checklists: checklist.NewMemory(),
		EmitTimer: func(_ context.Context, tm model.Timer) error { sent = append(sent, tm); return nil },
	})
	return m, &sent, notes
}

// A reminder sets its timer; its time sends a timer.fired rather than saying
// it there; the record coming back says it, once.
func TestAReminderGoesByTheStream(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	m, sent, _ := streamed(t, &now)
	ctx := context.Background()
	at := now.Add(time.Hour)
	r, err := m.Moments.Set(ctx, moments.Reminder{Text: "약", Source: moments.Asked, At: &at})
	if err != nil {
		t.Fatal(err)
	}
	timers, err := m.Opts.Store.Timers(ctx)
	if err != nil || len(timers) != 1 || timers[0].Skill != moments.TimerSkill || timers[0].EntityID != r.ID || !timers[0].FireAt.Equal(at) {
		t.Fatalf("%+v %v", timers, err)
	}
	now = at.Add(time.Second)
	m.Attn.Settle(ctx)
	if len(*sent) != 1 {
		t.Fatalf("보낸 타이머 %d", len(*sent))
	}
	got, _ := m.Moments.St.Reminder(ctx, r.ID)
	if got.Status != moments.Waiting {
		t.Fatalf("레인이 말하기 전에 말했습니다: %s", got.Status)
	}
	raw, _ := json.Marshal((*sent)[0])
	if err := m.TimerFired(ctx, queue.Message{Kind: obs.TimerFired, Payload: raw}); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Moments.St.Reminder(ctx, r.ID)
	if got.Status != moments.Sent || got.SentBy != moments.ByTime {
		t.Fatalf("%+v", got)
	}
	// Delivered again it is not said again.
	if err := m.TimerFired(ctx, queue.Message{Kind: obs.TimerFired, Payload: raw}); err != nil {
		t.Fatal(err)
	}
}

// Changing it moves the timer, cancelling it takes the timer away, and one
// waiting from before gets its timer at the start.
func TestTimersFollowTheReminder(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	m, _, _ := streamed(t, &now)
	ctx := context.Background()
	at := now.Add(time.Hour)
	r, err := m.Moments.Set(ctx, moments.Reminder{Text: "약", Source: moments.Asked, At: &at})
	if err != nil {
		t.Fatal(err)
	}
	later := at.Add(time.Hour)
	if _, err := m.Moments.Change(ctx, r.ID, moments.Change{At: &later}); err != nil {
		t.Fatal(err)
	}
	timers, _ := m.Opts.Store.Timers(ctx)
	if len(timers) != 1 || !timers[0].FireAt.Equal(later) {
		t.Fatalf("%+v", timers)
	}
	if _, err := m.Moments.Cancel(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if timers, _ := m.Opts.Store.Timers(ctx); len(timers) != 0 {
		t.Fatalf("%+v", timers)
	}
	// One stored before there were timers.
	old := moments.Reminder{ID: "rem_old", Text: "옛것", Source: moments.Asked, At: &at, Status: moments.Waiting, CreatedAt: now, UpdatedAt: now}
	if err := m.Moments.St.PutReminder(ctx, old); err != nil {
		t.Fatal(err)
	}
	m.scheduleReminders(ctx)
	if timers, _ := m.Opts.Store.Timers(ctx); len(timers) != 1 || timers[0].EntityID != "rem_old" {
		t.Fatalf("%+v", timers)
	}
}

// A place reminder has a timer for its latest and for its end, and one that
// cannot tell where the person is looks again.
func TestAPlaceReminderLooksAgain(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	m, _, _ := streamed(t, &now)
	ctx := context.Background()
	after := now.Add(time.Hour)
	r, err := m.Moments.Set(ctx, moments.Reminder{Text: "집에서", Source: moments.Asked, Place: "home", Move: moments.Arrive, After: &after})
	if err != nil {
		t.Fatal(err)
	}
	timers, _ := m.Opts.Store.Timers(ctx)
	if len(timers) != 1 || timers[0].Name != "after" {
		t.Fatalf("%+v", timers)
	}
	now = after.Add(time.Second)
	if err := m.Moments.Fire(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	found := false
	timers, _ = m.Opts.Store.Timers(ctx)
	for _, tm := range timers {
		if tm.Name == "there" && tm.FireAt.After(now) {
			found = true
		}
	}
	if !found {
		t.Fatalf("다시 보지 않습니다: %+v", timers)
	}
}

// A card the agent holds is in the ledger, and nowhere the gate counts.
func TestAHeldCardIsOnlyLedger(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	m, _, _ := streamed(t, &now)
	ctx := context.Background()
	raised, _ := json.Marshal(map[string]any{"card": "p-1", "title": "LP 결제"})
	if err := m.PaymentRaised(ctx, queue.Message{Payload: raised, TraceID: "job:12", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	in, err := m.Opts.Store.Intent(ctx, "card:p-1")
	if err != nil || in.Holder != "job:12" || in.Status != model.Pending || in.Skill != model.HeldSkill {
		t.Fatalf("%+v %v", in, err)
	}
	answer, _ := json.Marshal(map[string]any{"card": "p-1", "action": "payment.approve", "decision": "approve"})
	if err := m.HeldCard(ctx, queue.Message{Payload: answer, TraceID: "job:12", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if in, _ = m.Opts.Store.Intent(ctx, "card:p-1"); in.Status != model.Executed {
		t.Fatalf("%+v", in)
	}
	list, _ := m.Opts.Store.Intents(ctx, 100)
	if len(list) != 0 {
		t.Fatalf("관문이 세는 곳에 들어갔습니다: %+v", list)
	}
	// One raised without a request before it: the answer is its first word.
	other, _ := json.Marshal(map[string]any{"card": "p-2", "action": "calendar.add", "decision": "reject", "title": "일정"})
	if err := m.HeldCard(ctx, queue.Message{Payload: other, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if in, err := m.Opts.Store.Intent(ctx, "card:p-2"); err != nil || in.Status != model.Rejected || in.Holder != "chat" || in.Kind != "calendar.add" {
		t.Fatalf("%+v %v", in, err)
	}
	// mind's own cards are commands, not the agent's to hold.
	own, _ := json.Marshal(map[string]any{"card": "p-3", "action": "mind.approve", "decision": "approve"})
	if err := m.HeldCard(ctx, queue.Message{Payload: own, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Opts.Store.Intent(ctx, "card:p-3"); err == nil {
		t.Fatal("mind 의 카드를 장부에 또 넣었습니다")
	}
}
