package mind

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/registry"
)

// The registry is read at the start, and a row someone wrote stays theirs
// through a new release's seed.
func TestARowWrittenStays(t *testing.T) {
	m, st, _ := chargeMind(t, "")
	ctx := context.Background()
	if err := m.LoadRegistry(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Registry().Single("OWNS") {
		t.Fatal("씨앗에 없는 관계입니다")
	}
	if err := m.WriteRegistry(ctx, registry.SectionFact, "OWNS", json.RawMessage(`{"single":true}`)); err != nil {
		t.Fatal(err)
	}
	if !m.Registry().Single("OWNS") {
		t.Fatal("쓴 뒤에는 새 표를 읽습니다")
	}
	if err := m.WriteRegistry(ctx, registry.SectionFact, "LIVES_IN", json.RawMessage(`{"single":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.LoadRegistry(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.Registry().Single("OWNS") || m.Registry().Single("LIVES_IN") {
		t.Fatal("사람이 고친 줄은 씨앗이 덮지 않습니다")
	}
	rows, err := st.RegistryRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]string{}
	for _, r := range rows {
		by[r.Section+"/"+r.Key] = r.By
	}
	if by["fact/LIVES_IN"] != registry.ByOperator || by["fact/WORKS_AT"] != registry.BySeed {
		t.Fatalf("%v", by)
	}
}

// A row that makes no sense is turned down and the table stands.
func TestABadRowIsTurnedDown(t *testing.T) {
	m, _, _ := chargeMind(t, "")
	ctx := context.Background()
	err := m.WriteRegistry(ctx, registry.SectionKind, "timer.fired", json.RawMessage(`{"lane":"interpret"}`))
	if !BadRow(err) {
		t.Fatalf("%v", err)
	}
	if m.Registry().LaneOf("timer.fired") != registry.Deterministic {
		t.Fatal("결정적 레인 그대로입니다")
	}
}

// The gate's numbers are the registry's, and so is what a lane subscribes to.
func TestWhatReadsTheRegistry(t *testing.T) {
	m, _, _ := chargeMind(t, "")
	ctx := context.Background()
	var heard *registry.Snapshot
	m.OnRegistry = func(_ context.Context, r *registry.Snapshot) { heard = r }
	if err := m.LoadRegistry(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteRegistry(ctx, registry.SectionPolicy, "gate", json.RawMessage(`{"dailyCards":1,"minConfidence":0.7}`)); err != nil {
		t.Fatal(err)
	}
	if heard == nil || heard.Gate().DailyCards != 1 {
		t.Fatalf("%+v", heard)
	}
}

// A kind nobody registered is let in and said once.
func TestAnUnknownKindIsNoted(t *testing.T) {
	m, _, _ := chargeMind(t, "")
	m.NoteKind("처음.보는.종류")
	m.NoteKind("처음.보는.종류")
	m.NoteKind("job.step")
	if got := m.UnknownKinds(); len(got) != 1 || got[0] != "처음.보는.종류" {
		t.Fatalf("%v", got)
	}
}
