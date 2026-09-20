package proposal

import (
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// The gate is the whole product: a tool call must block until a person
// decides, and exactly one decision may land.
func TestOpenBlocksUntilDecided(t *testing.T) {
	s := NewStore(bus.New())
	p, decisions := s.Open(Proposal{Card: action.Card{Title: "캐시를 지웁니다"}})

	select {
	case d := <-decisions:
		t.Fatalf("결정 전에 풀렸습니다: %v", d)
	case <-time.After(20 * time.Millisecond):
	}

	if err := s.Decide(p.ID, Approve); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	select {
	case d := <-decisions:
		if d != Approve {
			t.Fatalf("got %v, want %v", d, Approve)
		}
	case <-time.After(time.Second):
		t.Fatal("결정이 전달되지 않았습니다")
	}

	if err := s.Decide(p.ID, Reject); err != ErrAlreadyDecided {
		t.Fatalf("두 번째 결재: got %v, want %v", err, ErrAlreadyDecided)
	}
}

// A proposal nobody is waiting on is the whole point of this package: a
// detector must be able to raise one and walk away without deadlocking.
func TestOpenDoesNotRequireAWaiter(t *testing.T) {
	s := NewStore(bus.New())
	p, _ := s.Open(Proposal{Origin: "kakao", Card: action.Card{Title: "캠핑 일정"}})

	done := make(chan error, 1)
	go func() { done <- s.Decide(p.ID, Approve) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("듣는 사람이 없는 제안에서 Decide가 막혔습니다")
	}
}

func TestDecideUnknown(t *testing.T) {
	if err := NewStore(bus.New()).Decide("missing", Approve); err != ErrNotFound {
		t.Fatalf("got %v, want %v", err, ErrNotFound)
	}
}

// A run that dies with a card still up must not leave the operator staring at
// a control that no longer does anything.
func TestAbandonClosesTheCard(t *testing.T) {
	s := NewStore(bus.New())
	p, _ := s.Open(Proposal{Card: action.Card{Title: "무언가"}})
	s.Abandon(p.ID, "시간 초과")

	got, _ := s.Get(p.ID)
	if got.State != Rejected {
		t.Fatalf("카드가 닫히지 않았습니다: %+v", got)
	}
	if err := s.Decide(p.ID, Approve); err != ErrAlreadyDecided {
		t.Fatalf("버려진 제안이 다시 결재됐습니다: %v", err)
	}
}

// Confidence may never buy its way past a change that cannot be undone.
func TestIrreversibleNeverRunsItself(t *testing.T) {
	g := DefaultGate()
	if got := g.Route(0.99, action.Spec{Reversible: false}); got != RouteAsk {
		t.Fatalf("되돌릴 수 없는 동작이 자동 실행됐습니다: %v", got)
	}
	if got := g.Route(0.99, action.Spec{Reversible: true}); got != RouteAuto {
		t.Fatalf("got %v, want %v", got, RouteAuto)
	}
	if got := g.Route(0.1, action.Spec{Reversible: true}); got != RouteDrop {
		t.Fatalf("got %v, want %v", got, RouteDrop)
	}
}
