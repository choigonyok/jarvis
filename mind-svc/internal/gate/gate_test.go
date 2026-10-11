package gate

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

type fixture struct {
	g     *Gate
	st    *store.Memory
	cal   *act.MemoryCalendar
	note  *act.MemoryNotifier
	log   *obs.MemoryLog
	clock time.Time
}

func newFixture(p Policy) *fixture {
	f := &fixture{st: store.NewMemory(), cal: act.NewMemoryCalendar(), note: &act.MemoryNotifier{}, clock: time.Date(2026, 10, 12, 10, 0, 0, 0, seoul)}
	f.log = obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go f.log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	f.g = New(f.st, act.NewRegistry(act.CalendarActuator{Cal: f.cal}), f.note, f.log, p, seoul,
		func() time.Time { f.clock = f.clock.Add(time.Second); return f.clock }, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func create(entity string) model.Intent {
	payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "저녁", Date: "2026-10-17"}})
	return model.Intent{Skill: "schedule", Kind: "calendar.create", EntityID: entity, Payload: payload, Title: "캘린더에 추가", Confidence: 0.9, Reversible: true}
}

// Asked first; after enough approvals in a row, done and told; one undo and
// it is asked again.
func TestTrustIsLearned(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	for i := range 3 {
		in, err := f.g.Submit(ctx, create("plan_"+string(rune('a'+i))))
		if err != nil || in.Decision != model.Ask || in.Status != model.Pending {
			t.Fatalf("%d번째는 물어야 합니다: %+v %v", i+1, in, err)
		}
		if _, err := f.g.Approve(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
	}
	in, _ := f.g.Submit(ctx, create("plan_d"))
	if in.Decision != model.Notify || in.Status != model.Executed {
		t.Fatalf("세 번 승인한 뒤에는 바로 하고 알려야 합니다: %+v", in)
	}
	if len(f.cal.All()) != 4 {
		t.Fatalf("캘린더에 4개: %v", f.cal.All())
	}
	if _, err := f.g.Undo(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.cal.All()) != 3 {
		t.Fatalf("되돌리면 빠져야 합니다: %v", f.cal.All())
	}
	again, _ := f.g.Submit(ctx, create("plan_e"))
	if again.Decision != model.Ask {
		t.Fatalf("되돌린 뒤에는 다시 물어야 합니다: %+v", again)
	}
}

// Deleting is asked however often it was approved; an unsure skill is
// asked quietly.
func TestFixedAndUnsure(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	for i := range 4 {
		saved, _ := f.cal.Put(ctx, model.CalendarEvent{Title: "저녁", Date: "2026-10-17"})
		payload, _ := json.Marshal(model.CalendarAction{EventID: saved.ID, Event: saved})
		in, _ := f.g.Submit(ctx, model.Intent{Skill: "schedule", Kind: "calendar.delete", EntityID: "p" + string(rune('a'+i)), Payload: payload, Confidence: 0.95, Reversible: true})
		if in.Decision != model.Ask {
			t.Fatalf("%d번째 삭제도 물어야 합니다: %+v", i+1, in)
		}
		if _, err := f.g.Approve(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
	}
	unsure := create("q")
	unsure.Confidence = 0.55
	got, _ := f.g.Submit(ctx, unsure)
	if got.Decision != model.Ask || got.Tier != "digest" {
		t.Fatalf("확신이 낮으면 조용히 물어야 합니다: %+v", got)
	}
}

// Past the day's budget, pushes become lines in the evening summary.
func TestInterruptionBudget(t *testing.T) {
	ctx := context.Background()
	p := DefaultPolicy()
	p.DailyInterrupts = 2
	f := newFixture(p)
	var tiers []string
	for range 3 {
		in, _ := f.g.Submit(ctx, model.Intent{Skill: "loops", Kind: "info", Title: "알림", Urgent: true, Confidence: 1})
		tiers = append(tiers, in.Tier)
	}
	if tiers[0] != "now" || tiers[1] != "now" || tiers[2] != "digest" {
		t.Fatalf("한도 뒤에는 요약으로: %v", tiers)
	}
	if n := len(f.note.All()); n != 3 {
		t.Fatalf("알림 3개(2개는 바로, 1개는 요약): %d", n)
	}
}

// A newer intent about the same entity replaces an unanswered one.
func TestNewerReplacesUnanswered(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	first, _ := f.g.Submit(ctx, create("plan_x"))
	second, _ := f.g.Submit(ctx, create("plan_x"))
	old, _ := f.st.Intent(ctx, first.ID)
	if old.Status != model.Superseded || second.Status != model.Pending {
		t.Fatalf("앞의 질문은 대체돼야 합니다: %s / %s", old.Status, second.Status)
	}
	if _, err := f.g.Approve(ctx, first.ID); err == nil {
		t.Fatal("대체된 제안은 승인할 수 없어야 합니다")
	}
}

// A malformed action never reaches anyone.
func TestPreviewRejectsBadPayload(t *testing.T) {
	f := newFixture(DefaultPolicy())
	in, _ := f.g.Submit(context.Background(), model.Intent{Skill: "schedule", Kind: "calendar.create", Payload: json.RawMessage(`{"event":{"title":"x","date":"내일"}}`), Confidence: 1, Reversible: true})
	if in.Status != model.Failed || len(f.note.All()) != 0 {
		t.Fatalf("미리보기에서 걸러져야 합니다: %+v", in)
	}
}
