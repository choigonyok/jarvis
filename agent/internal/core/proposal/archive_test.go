package proposal

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// raisedID is a card's id as Open mints it, for a card raised at t.
func raisedID(t time.Time, seq int) string { return fmt.Sprintf("p-%d-%d", t.UnixMilli(), seq) }

func idsOf(ps []Proposal) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// liveFile is a proposals file as an agent of before left it: a card
// decided three weeks ago, one still waiting from then, one turned down an
// hour ago.
func liveFile(t *testing.T, now time.Time) (path string, old, waiting, recent string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "proposals.json")
	weeks := now.Add(-21 * 24 * time.Hour)
	old, waiting, recent = raisedID(weeks, 1), raisedID(weeks, 2), raisedID(now.Add(-time.Hour), 3)
	err := jsonfile.Save(path, file{Seq: 3, Proposals: []*Proposal{
		{ID: old, Origin: OriginChat, State: Executed, Card: action.Card{Title: "지난 일정"}, At: "10:00", DecidedAt: "10:01"},
		{ID: waiting, Origin: OriginChat, State: Pending, Card: action.Card{Title: "아직 기다리는 카드"}, At: "10:02"},
		{ID: recent, Origin: OriginSuggest, State: Rejected, Card: action.Card{Title: "방금 반려한 제안"}, At: "09:00", DecidedAt: "09:05"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return path, old, waiting, recent
}

// A decided card raised long ago leaves the live file for the archive beside
// it; what is still waiting and what was decided lately stay. It is still
// found by its id, and still decided.
func TestOldDecidedCardsMoveToTheArchive(t *testing.T) {
	path, old, waiting, recent := liveFile(t, time.Now())
	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(s.List()); !slices.Equal(got, []string{waiting, recent}) {
		t.Fatalf("남는 카드: %v", got)
	}
	var live file
	if err := jsonfile.Load(path, &live); err != nil || live.Seq != 3 || len(live.Proposals) != 2 {
		t.Fatalf("살아 있는 파일: %+v %v", live, err)
	}
	var kept archive
	if err := jsonfile.Load(filepath.Join(filepath.Dir(path), "proposals-archive.json"), &kept); err != nil || len(kept.Proposals) != 1 || kept.Proposals[0].ID != old {
		t.Fatalf("보관 파일: %+v %v", kept, err)
	}
	if p, ok := s.Get(old); !ok || p.Card.Title != "지난 일정" || p.State != Executed {
		t.Fatalf("보관한 카드도 id 로 찾아야 합니다: %+v %v", p, ok)
	}
	if err := s.Decide(old, Approve); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("보관한 카드는 이미 결정된 카드입니다: %v", err)
	}
	if err := s.Decide("p-1-404", Approve); !errors.Is(err, ErrNotFound) {
		t.Fatalf("없는 카드: %v", err)
	}
}

// After a restart the archive is not read until a card only it holds is
// asked for; a page's turns bring theirs along, before the live ones.
func TestTheArchiveIsReadWhenACardInItIsAskedFor(t *testing.T) {
	path, old, waiting, recent := liveFile(t, time.Now())
	if err := NewStore(bus.New()).Persist(path, quiet); err != nil {
		t.Fatal(err)
	}

	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(waiting); !ok || s.archived != nil {
		t.Fatalf("살아 있는 카드에는 보관 파일을 읽지 않습니다: %v", s.archived)
	}
	if got := idsOf(s.List()); !slices.Equal(got, []string{waiting, recent}) {
		t.Fatalf("다시 시작한 뒤 남는 카드: %v", got)
	}
	if got := idsOf(s.ListWith([]string{old, recent, old, "", "p-1-404"})); !slices.Equal(got, []string{old, waiting, recent}) {
		t.Fatalf("페이지의 카드: %v", got)
	}
	if s.archived == nil {
		t.Fatal("보관한 카드를 찾으려면 보관 파일을 읽어야 합니다")
	}
	if p, ok := s.Find(func(p Proposal) bool { return p.Card.Title == "지난 일정" }); !ok || p.ID != old {
		t.Fatalf("Find 는 보관한 카드까지 봅니다: %+v %v", p, ok)
	}
	if _, ok := s.Find(func(p Proposal) bool { return p.Card.Title == "없는 카드" }); ok {
		t.Fatal("없는 카드")
	}
}

// A running store looks for old cards once a day, on a change. A card raised
// long ago and decided just now stays until what acts on the decision has
// settled it.
func TestARunningStoreArchivesOnceADay(t *testing.T) {
	clock := time.Now()
	s := NewStore(bus.New())
	s.now = func() time.Time { return clock }
	path := filepath.Join(t.TempDir(), "proposals.json")
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	early, _ := s.Open(Proposal{Card: action.Card{Title: "먼저 결정한 카드"}})
	if err := s.Decide(early.ID, Approve); err != nil {
		t.Fatal(err)
	}
	long, _ := s.Open(Proposal{Card: action.Card{Title: "오래 기다린 카드"}})

	clock = clock.Add(20 * 24 * time.Hour)
	if err := s.Decide(long.ID, Approve); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(s.List()); !slices.Equal(got, []string{long.ID}) {
		t.Fatalf("방금 결정한 카드는 남고 오래된 것만 옮깁니다: %v", got)
	}
	s.Settle(long.ID, Executed, "했습니다")
	if p, _ := s.Get(long.ID); p.State != Executed || p.Note != "했습니다" {
		t.Fatalf("결정 뒤의 정리가 카드에 닿아야 합니다: %+v", p)
	}

	clock = clock.Add(2 * time.Hour)
	s.Open(Proposal{Card: action.Card{Title: "두 시간 뒤"}})
	if got := idsOf(s.List()); len(got) != 2 || got[0] != long.ID {
		t.Fatalf("하루에 한 번만 봅니다: %v", got)
	}
	clock = clock.Add(23 * time.Hour)
	later, _ := s.Open(Proposal{Card: action.Card{Title: "하루 뒤"}})
	if got := idsOf(s.List()); len(got) != 2 || slices.Contains(got, long.ID) || got[1] != later.ID {
		t.Fatalf("하루가 지나면 옮깁니다: %v", got)
	}
	for _, id := range []string{early.ID, long.ID} {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("%s 를 보관 파일에서 찾아야 합니다", id)
		}
	}

	restarted := NewStore(bus.New())
	if err := restarted.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(restarted.List()); len(got) != 2 || slices.Contains(got, long.ID) {
		t.Fatalf("다시 시작해도 같습니다: %v", got)
	}
	if p, ok := restarted.Get(long.ID); !ok || p.State != Executed {
		t.Fatalf("다시 시작한 뒤의 보관한 카드: %+v %v", p, ok)
	}
}

// An archive that cannot be read is not written over: what it holds would
// be gone. The cards stay in the live file meanwhile.
func TestAnUnreadableArchiveIsLeftAlone(t *testing.T) {
	path, old, _, _ := liveFile(t, time.Now())
	archived := filepath.Join(filepath.Dir(path), "proposals-archive.json")
	if err := os.WriteFile(archived, []byte("{깨진"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(old); !ok || len(s.List()) != 3 {
		t.Fatalf("옮기지 못한 카드는 그대로 남습니다: %v", idsOf(s.List()))
	}
	if b, _ := os.ReadFile(archived); string(b) != "{깨진" {
		t.Fatalf("읽지 못한 보관 파일을 덮어썼습니다: %q", b)
	}
	if _, ok := s.Get("p-1-404"); ok {
		t.Fatal("없는 카드")
	}
}
