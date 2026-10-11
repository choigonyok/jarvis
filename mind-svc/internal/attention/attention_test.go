package attention

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

// recorder is a skill that remembers what it was handed and, once, edits
// the plan itself.
type recorder struct {
	batches [][]model.Change
	edit    bool
}

func (r *recorder) Name() string              { return "rec" }
func (r *recorder) Wants(e model.Entity) bool { return e.Kind == model.KindPlan }
func (r *recorder) OnChange(_ context.Context, _ skill.View, e model.Entity, cs []model.Change) skill.Result {
	r.batches = append(r.batches, cs)
	if r.edit {
		r.edit = false
		return skill.Result{Updates: []skill.Update{{ID: e.ID, Set: func(s *model.State) bool { s.Note = "스킬이 고침"; return true }}}}
	}
	return skill.Result{}
}
func (r *recorder) OnTimer(context.Context, skill.View, model.Entity, model.Timer) skill.Result {
	return skill.Result{}
}
func (r *recorder) OnTick(context.Context, skill.View, string) skill.Result { return skill.Result{} }

func setup(settle time.Duration, now *time.Time) (*Attention, *world.World, *recorder) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	clock := func() time.Time { return *now }
	w := world.New(st, clock)
	log := obs.NewMemoryLog(quiet)
	go log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	g := gate.New(st, act.NewRegistry(), &act.MemoryNotifier{}, log, gate.DefaultPolicy(), seoul, clock, "", quiet)
	rec := &recorder{}
	return New([]skill.Skill{rec}, w, st, g, settle, seoul, clock, quiet), w, rec
}

// Changes to one entity within the settle time are handed on as one batch.
func TestChangesSettleIntoOneBatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, seoul)
	a, w, rec := setup(time.Minute, &now)
	e, _ := w.Create(ctx, model.KindPlan, "r", model.State{Title: "강릉", Status: model.Proposed}, world.Meta{Actor: "interpreter"})
	_, _, _ = w.Update(ctx, e.ID, func(s *model.State) bool { s.Status = model.Confirmed; return true }, world.Meta{Actor: "interpreter"})
	a.step(ctx, false)
	if len(rec.batches) != 0 {
		t.Fatal("아직 가라앉는 중인데 넘겼습니다")
	}
	now = now.Add(61 * time.Second)
	a.step(ctx, false)
	if len(rec.batches) != 1 || len(rec.batches[0]) != 2 {
		t.Fatalf("두 변화가 한 묶음으로 가야 합니다: %v", rec.batches)
	}
}

// A skill is not handed back a change it made itself.
func TestOwnChangesAreNotHandedBack(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, seoul)
	a, w, rec := setup(0, &now)
	rec.edit = true
	_, _ = w.Create(ctx, model.KindPlan, "r", model.State{Title: "강릉", Status: model.Proposed}, world.Meta{Actor: "interpreter"})
	a.Settle(ctx)
	if len(rec.batches) != 1 {
		t.Fatalf("스킬 자신의 변경은 다시 오면 안 됩니다: %d번 받음", len(rec.batches))
	}
}
