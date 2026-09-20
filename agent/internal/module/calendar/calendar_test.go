package calendar

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "calendar.json"), bus.New())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return New(store)
}

func act(t *testing.T, kind string, in any) action.Action {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return action.Action{Kind: kind, Input: raw}
}

// The model is told to send absolute dates; this is what makes that
// instruction enforceable rather than hopeful.
func TestRelativeDatesAreRejected(t *testing.T) {
	m := newTestModule(t)
	_, err := m.Execute(context.Background(),
		act(t, KindCreate, CreateInput{Date: "다음 주 화요일", Title: "합주"}))
	if err == nil {
		t.Fatal("상대 날짜가 저장됐습니다")
	}
}

// An edit card that showed only the new value would be asking the operator to
// approve blind, so the card carries both sides.
func TestUpdatePreviewShowsBothSides(t *testing.T) {
	m := newTestModule(t)
	if _, err := m.Execute(context.Background(),
		act(t, KindCreate, CreateInput{Date: "2026-09-25", Start: "19:00", Title: "합주"})); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := m.store.Range("", "")[0].ID

	card, err := m.Preview(context.Background(),
		act(t, KindUpdate, UpdateInput{ID: id, Start: "20:00"}))
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !strings.Contains(card.Body, "- ") || !strings.Contains(card.Body, "+ ") {
		t.Fatalf("이전/이후가 모두 보이지 않습니다:\n%s", card.Body)
	}
	if !strings.Contains(card.Body, "19:00") || !strings.Contains(card.Body, "20:00") {
		t.Fatalf("바뀌는 값이 카드에 없습니다:\n%s", card.Body)
	}
}

// A patch leaves untouched fields alone - the update tool's schema promises
// the model it only has to send what changes.
func TestUpdateIsAPatch(t *testing.T) {
	m := newTestModule(t)
	if _, err := m.Execute(context.Background(), act(t, KindCreate,
		CreateInput{Date: "2026-09-25", Start: "19:00", Title: "합주", Place: "연습실"})); err != nil {
		t.Fatalf("create: %v", err)
	}
	id := m.store.Range("", "")[0].ID

	if _, err := m.Execute(context.Background(),
		act(t, KindUpdate, UpdateInput{ID: id, Start: "20:00"})); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := m.store.Get(id)
	if got.Start != "20:00" || got.Title != "합주" || got.Place != "연습실" {
		t.Fatalf("패치가 다른 필드를 지웠습니다: %+v", got)
	}
}

// The calendar must survive a restart: it is the one part of this process
// whose loss the operator would actually notice.
func TestSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calendar.json")
	first := New(NewStore(path, bus.New()))
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := first.Execute(context.Background(),
		act(t, KindCreate, CreateInput{Date: "2026-09-25", Title: "합주"})); err != nil {
		t.Fatalf("create: %v", err)
	}

	second := New(NewStore(path, bus.New()))
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := second.store.Range("", ""); len(got) != 1 || got[0].Title != "합주" {
		t.Fatalf("다시 읽지 못했습니다: %+v", got)
	}
}

func TestRangeIsInclusiveAndSorted(t *testing.T) {
	m := newTestModule(t)
	for _, in := range []CreateInput{
		{Date: "2026-09-26", Start: "10:00", Title: "둘째날"},
		{Date: "2026-09-25", Start: "19:00", Title: "저녁"},
		{Date: "2026-09-25", Title: "종일"},
		{Date: "2026-10-01", Title: "범위 밖"},
	} {
		if _, err := m.Execute(context.Background(), act(t, KindCreate, in)); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	got := m.store.Range("2026-09-25", "2026-09-26")
	titles := make([]string, 0, len(got))
	for _, e := range got {
		titles = append(titles, e.Title)
	}
	want := "종일,저녁,둘째날"
	if strings.Join(titles, ",") != want {
		t.Fatalf("got %v, want %s", titles, want)
	}
}

// The calendar answers "what is already booked", which is how a future
// reasoning step will know not to double-book. Read through the interface the
// registry will call it by, not the concrete type.
func TestFactsReadsThroughTheInterface(t *testing.T) {
	m := newTestModule(t)
	if _, err := m.Execute(context.Background(), act(t, KindCreate,
		CreateInput{Date: isoToday(), Start: "19:00", Title: "합주"})); err != nil {
		t.Fatalf("create: %v", err)
	}

	var source module.ContextSource = m
	facts, err := source.Facts(context.Background(), "이번 주")
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if len(facts) != 1 || facts[0].Source != Name || !strings.Contains(facts[0].Text, "합주") {
		t.Fatalf("사실이 비어 있거나 모양이 다릅니다: %+v", facts)
	}
}

func isoToday() string { return time.Now().Format("2006-01-02") }
