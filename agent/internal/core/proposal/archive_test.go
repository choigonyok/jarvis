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
	if got, err := s.Archived(); err != nil || !slices.Equal(idsOf(got), []string{old}) {
		t.Fatalf("보관한 카드의 목록: %v %v", idsOf(got), err)
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

// A running store looks for old cards once a day, on a change. A card is
// aged by its last change, not by when it was raised: one that waited three
// weeks and was decided just now stays live for two weeks after, and
// whatever settles it meanwhile finds it there.
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

	clock = clock.Add(archiveAfter - time.Hour)
	almost, _ := s.Open(Proposal{Card: action.Card{Title: "두 주가 되기 한 시간 전"}})
	if got := idsOf(s.List()); !slices.Equal(got, []string{long.ID, almost.ID}) {
		t.Fatalf("마지막으로 바뀐 지 두 주가 안 된 카드는 남습니다: %v", got)
	}
	clock = clock.Add(2 * time.Hour)
	after, _ := s.Open(Proposal{Card: action.Card{Title: "두 시간 뒤"}})
	if got := idsOf(s.List()); !slices.Equal(got, []string{long.ID, almost.ID, after.ID}) {
		t.Fatalf("하루에 한 번만 봅니다: %v", got)
	}
	clock = clock.Add(23 * time.Hour)
	later, _ := s.Open(Proposal{Card: action.Card{Title: "하루 뒤"}})
	if got := idsOf(s.List()); !slices.Equal(got, []string{almost.ID, after.ID, later.ID}) {
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
	if got := idsOf(restarted.List()); !slices.Equal(got, []string{almost.ID, after.ID, later.ID}) {
		t.Fatalf("다시 시작해도 같습니다: %v", got)
	}
	if p, ok := restarted.Get(long.ID); !ok || p.State != Executed {
		t.Fatalf("다시 시작한 뒤의 보관한 카드: %+v %v", p, ok)
	}
}

// waitedFile is a proposals file holding one card raised twenty days ago, in
// this state, carrying when it last changed (or not, changed zero).
func waitedFile(t *testing.T, state State, changed time.Time) (path, id string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "proposals.json")
	id = raisedID(time.Now().Add(-20*24*time.Hour), 1)
	p := &Proposal{ID: id, Origin: OriginChat, State: state, Card: action.Card{Title: "스무 날 기다린 카드"}, At: "10:00"}
	if !changed.IsZero() {
		p.changed(changed)
	}
	if err := jsonfile.Save(path, file{Seq: 1, Proposals: []*Proposal{p}}); err != nil {
		t.Fatal(err)
	}
	return path, id
}

// What acts on a decision settles its card however long after, a restart
// between: a card that waited twenty days and was approved just before the
// agent stopped is live after it, and the settling reaches it.
func TestASettleAfterARestartFindsItsCard(t *testing.T) {
	path, id := waitedFile(t, Pending, time.Time{})
	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(id, Approve); err != nil {
		t.Fatal(err)
	}

	restarted := NewStore(bus.New())
	if err := restarted.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	restarted.Settle(id, Executed, "했습니다")
	if p, ok := restarted.Get(id); !ok || p.State != Executed || p.Note != "했습니다" {
		t.Fatalf("다시 시작한 뒤의 정리가 카드에 닿아야 합니다: %+v %v", p, ok)
	}
	if got := idsOf(restarted.List()); !slices.Equal(got, []string{id}) {
		t.Fatalf("결정한 지 얼마 안 된 카드는 살아 있습니다: %v", got)
	}
}

// A card archived while what acts on it is still at work - one that carries
// no time of its last change is aged by when it was raised - comes back
// among the live ones when it is settled, and leaves the archive file. Only
// settling brings one back: what changes a waiting card refuses a decided one
// and leaves it where it is.
func TestSettlingAnArchivedCardBringsItBack(t *testing.T) {
	path, id := waitedFile(t, Approved, time.Time{})
	archived := filepath.Join(filepath.Dir(path), "proposals-archive.json")
	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(s.List()); len(got) != 0 {
		t.Fatalf("바뀐 시각이 없는 카드는 올린 시각으로 옮깁니다: %v", got)
	}
	if err := s.Decide(id, Reject); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("결정: %v", err)
	}
	if err := s.ReviseWith(id, action.Action{Kind: "x.y"}, action.Card{Title: "고침"}, map[string]string{"a": "b"}); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("고치기: %v", err)
	}
	if err := s.Refresh(id, action.Action{Kind: "x.y"}, action.Card{Title: "다시"}, 0.5); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("다시 그리기: %v", err)
	}
	s.Abandon(id, "버림")
	if p, _ := s.Get(id); p.State != Approved || p.Note != "" || len(s.List()) != 0 {
		t.Fatalf("거절된 변경은 보관한 카드를 건드리지 않습니다: %+v %v", p, idsOf(s.List()))
	}

	s.Settle(id, Executed, "했습니다")
	if p, ok := s.Get(id); !ok || p.State != Executed || p.Note != "했습니다" || p.Changed().IsZero() {
		t.Fatalf("보관한 카드도 정리됩니다: %+v %v", p, ok)
	}
	if got := idsOf(s.List()); !slices.Equal(got, []string{id}) {
		t.Fatalf("정리한 카드는 다시 살아 있습니다: %v", got)
	}
	if got, err := s.Archived(); err != nil || len(got) != 0 {
		t.Fatalf("보관 목록에서는 빠집니다: %v %v", idsOf(got), err)
	}
	var kept archive
	if err := jsonfile.Load(archived, &kept); err != nil || len(kept.Proposals) != 0 {
		t.Fatalf("보관 파일에서도 빠집니다: %+v %v", kept, err)
	}

	restarted := NewStore(bus.New())
	if err := restarted.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if p, ok := restarted.Get(id); !ok || p.State != Executed || !slices.Equal(idsOf(restarted.List()), []string{id}) {
		t.Fatalf("다시 시작해도 살아 있습니다: %+v %v %v", p, ok, idsOf(restarted.List()))
	}
}

// A decided card leaves the live file two weeks after it last changed; one
// that does not carry when, two weeks after it was raised. A waiting card
// stays however old.
func TestACardIsAgedByItsLastChange(t *testing.T) {
	now := time.Now()
	weeks := now.Add(-21 * 24 * time.Hour)
	card := func(seq int, state State, changed time.Time) *Proposal {
		p := &Proposal{ID: raisedID(weeks, seq), Origin: OriginChat, State: state, At: "10:00"}
		if !changed.IsZero() {
			p.changed(changed)
		}
		return p
	}
	unstamped := card(1, Rejected, time.Time{})
	yesterday := card(2, Executed, now.Add(-24*time.Hour))
	fortnight := card(3, Executed, now.Add(-15*24*time.Hour))
	waits := card(4, Pending, weeks)
	path := filepath.Join(t.TempDir(), "proposals.json")
	if err := jsonfile.Save(path, file{Seq: 4, Proposals: []*Proposal{unstamped, yesterday, fortnight, waits}}); err != nil {
		t.Fatal(err)
	}
	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(s.List()); !slices.Equal(got, []string{yesterday.ID, waits.ID}) {
		t.Fatalf("남는 카드: %v", got)
	}
	if got, err := s.Archived(); err != nil || !slices.Equal(idsOf(got), []string{unstamped.ID, fortnight.ID}) {
		t.Fatalf("보관한 카드: %v %v", idsOf(got), err)
	}
}

// A card in both files - a write cut short between the live file and the
// archive - is the live one: found as it is there, listed once, and gone
// from the archive at its next writing.
func TestTheLiveCopyIsTheCard(t *testing.T) {
	now := time.Now()
	weeks := now.Add(-21 * 24 * time.Hour)
	both, due := raisedID(weeks, 1), raisedID(weeks, 2)
	path := filepath.Join(t.TempDir(), "proposals.json")
	live := &Proposal{ID: both, Origin: OriginChat, State: Executed, Note: "했습니다", At: "10:00"}
	live.changed(now.Add(-time.Hour))
	if err := jsonfile.Save(path, file{Seq: 2, Proposals: []*Proposal{
		live,
		{ID: due, Origin: OriginChat, State: Rejected, At: "10:01"},
	}}); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(filepath.Dir(path), "proposals-archive.json")
	if err := jsonfile.Save(archived, archive{Proposals: []*Proposal{{ID: both, Origin: OriginChat, State: Approved, At: "10:00"}}}); err != nil {
		t.Fatal(err)
	}
	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(both); p.State != Executed || p.Note != "했습니다" {
		t.Fatalf("살아 있는 사본이 카드입니다: %+v", p)
	}
	if got := idsOf(s.ListWith([]string{both, due})); !slices.Equal(got, []string{due, both}) {
		t.Fatalf("한 번씩: %v", got)
	}
	if got, err := s.Archived(); err != nil || !slices.Equal(idsOf(got), []string{due}) {
		t.Fatalf("보관 목록: %v %v", idsOf(got), err)
	}
	var kept archive
	if err := jsonfile.Load(archived, &kept); err != nil || len(kept.Proposals) != 1 || kept.Proposals[0].ID != due {
		t.Fatalf("보관 파일은 다음에 쓸 때 옛 사본을 뺍니다: %+v %v", kept, err)
	}
}

// Every change to a card stamps when it was made; redrawing a card with what
// it asks already is not one.
func TestEveryChangeIsStamped(t *testing.T) {
	clock := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	s := NewStore(bus.New())
	s.now = func() time.Time { return clock }
	stamped := func(id string, want time.Time) {
		t.Helper()
		if p, _ := s.Get(id); !p.Changed().Equal(want) {
			t.Fatalf("%s: 바뀐 시각 %s, 기대 %s", id, p.ChangedAt, want.Format(time.RFC3339))
		}
	}
	act, card := action.Action{Kind: "x.y", Input: []byte(`{"a":1}`)}, action.Card{Title: "처음"}
	p, _ := s.Open(Proposal{Action: act, Card: card})
	stamped(p.ID, clock)
	opened := clock

	clock = clock.Add(time.Hour)
	if err := s.Refresh(p.ID, action.Action{Kind: "x.y", Input: []byte(`{ "a": 1 }`)}, card, 0); err != nil {
		t.Fatal(err)
	}
	stamped(p.ID, opened)
	if err := s.Refresh(p.ID, act, action.Card{Title: "다시"}, 0.4); err != nil {
		t.Fatal(err)
	}
	stamped(p.ID, clock)

	clock = clock.Add(time.Hour)
	if err := s.ReviseWith(p.ID, act, action.Card{Title: "고침"}, map[string]string{"a": "2"}); err != nil {
		t.Fatal(err)
	}
	stamped(p.ID, clock)
	clock = clock.Add(time.Hour)
	if err := s.Decide(p.ID, Approve); err != nil {
		t.Fatal(err)
	}
	stamped(p.ID, clock)
	clock = clock.Add(time.Hour)
	s.Settle(p.ID, Executed, "했습니다")
	stamped(p.ID, clock)

	other, _ := s.Open(Proposal{Card: action.Card{Title: "다른 카드"}})
	clock = clock.Add(time.Hour)
	s.Abandon(other.ID, "새 카드로 바꿨습니다")
	stamped(other.ID, clock)
}

// The cards a restart turns down are changed by it.
func TestARestartStampsTheCardsItTurnsDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proposals.json")
	id := raisedID(time.Now().Add(-30*24*time.Hour), 1)
	if err := jsonfile.Save(path, file{Seq: 1, Proposals: []*Proposal{{ID: id, Origin: "kakao", State: Pending, At: "10:00"}}}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	s := NewStore(bus.New())
	if err := s.Persist(path, quiet); err != nil {
		t.Fatal(err)
	}
	p, ok := s.Get(id)
	if !ok || p.State != Rejected || p.Changed().Before(before) || !slices.Contains(idsOf(s.List()), id) {
		t.Fatalf("재시작이 닫은 카드는 지금 바뀐 것입니다: %+v %v", p, ok)
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
	if got, err := s.Archived(); !errors.Is(err, ErrArchiveUnreadable) || got != nil {
		t.Fatalf("읽지 못한 보관 파일은 빈 목록이 아니라 실패입니다: %v %v", idsOf(got), err)
	}
	if got, err := NewStore(bus.New()).Archived(); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("파일이 없는 저장소의 보관 목록은 비어 있습니다: %v %v", got, err)
	}
}
