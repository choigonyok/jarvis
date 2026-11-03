package proposal

import (
	"strings"
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

// A rejection keeps the operator's reason, and a form card every edit made
// before the decision.
func TestReasonAndEdits(t *testing.T) {
	s := NewStore(bus.New())
	p, _ := s.Open(Proposal{Origin: OriginSuggest, Action: action.Action{Kind: "x.y"}})
	if err := s.ReviseWith(p.ID, action.Action{Kind: "x.y"}, action.Card{Title: "t"}, map[string]string{"price": "10000"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideWhy(p.ID, Reject, "  지금은 필요 없어  "); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(p.ID)
	if got.Reason != "지금은 필요 없어" || len(got.Edits) != 1 || got.Edits[0].Fields["price"] != "10000" {
		t.Fatalf("이유와 수정 내역: %+v", got)
	}
}

// When a card was raised is read off its id; an id of another shape has none.
func TestOpened(t *testing.T) {
	s := NewStore(bus.New())
	before := time.Now().Add(-time.Second)
	p, _ := s.Open(Proposal{Action: action.Action{Kind: "x"}})
	if at := p.Opened(); at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Fatalf("열린 시각 %v", at)
	}
	if !(Proposal{ID: "local-1"}).Opened().IsZero() {
		t.Fatal("모양이 다른 id")
	}
}

// A card told what it asks now says so at once, and a decided one stays decided.
func TestRefresh(t *testing.T) {
	b := bus.New()
	s := NewStore(b)
	p, _ := s.Open(Proposal{Action: action.Action{Kind: "x.y"}, Card: action.Card{Title: "처음"}})
	events, stop := b.Subscribe()
	defer stop()
	if err := s.Refresh(p.ID, action.Action{Kind: "x.z"}, action.Card{Title: "고침"}, 0.7); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(p.ID)
	if got.Card.Title != "고침" || got.Action.Kind != "x.z" || got.Confidence != 0.7 || got.State != Pending {
		t.Fatalf("%+v", got)
	}
	select {
	case ev := <-events:
		if p, ok := ev.Proposal.(Proposal); !ok || p.Card.Title != "고침" {
			t.Fatalf("%+v", ev)
		}
	default:
		t.Fatal("화면에 알려야 합니다")
	}
	if err := s.Refresh("없음", action.Action{}, action.Card{}, 0); err != ErrNotFound {
		t.Fatal(err)
	}
	if err := s.Decide(p.ID, Reject); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(p.ID, action.Action{}, action.Card{}, 0); err != ErrAlreadyDecided {
		t.Fatal(err)
	}
}

// The cards the conversation is told are waiting: only those still waiting,
// oldest first, each with what it says, who raised it and when - a card of
// another day with its date.
func TestWaitingIsWhatIsOnTheScreen(t *testing.T) {
	s := NewStore(bus.New())
	pay, _ := s.Open(Proposal{Origin: OriginIntercept, Card: action.Card{Title: "결제를 진행합니다", Body: "pay.example.com\n\nhttps://pay.example.com/points/booking"}})
	done, _ := s.Open(Proposal{Origin: OriginChat, Card: action.Card{Title: "일정을 추가합니다"}})
	s.Open(Proposal{Origin: OriginSuggest, Card: action.Card{Title: "캠핑 일정 추가할까요?"}})
	if err := s.Decide(done.ID, Reject); err != nil {
		t.Fatal(err)
	}
	now := pay.Opened()
	got := s.Waiting(now)
	if len(got) != 2 {
		t.Fatalf("%q", got)
	}
	if want := "- 결제를 진행합니다: pay.example.com / https://pay.example.com/points/booking (브라우저가 보내려던 요청을 결제 감시가 멈춘 것, " + now.Format("15:04") + ")"; got[0] != want {
		t.Fatalf("\n%s\n%s", got[0], want)
	}
	if got[1] != "- 캠핑 일정 추가할까요? (비서가 먼저 묻는 것, "+now.Format("15:04")+")" {
		t.Fatalf("%q", got[1])
	}
	if l := pay.line(now.AddDate(0, 0, 1)); !strings.Contains(l, now.Format("1/2 15:04")) {
		t.Fatalf("다른 날의 카드는 날짜까지: %s", l)
	}
	long := Proposal{Card: action.Card{Title: "t", Body: strings.Repeat("가", 300)}, Origin: "kakao", At: "09:00"}
	if l := long.line(now); !strings.Contains(l, strings.Repeat("가", waitingRunes)+"… (kakao, 09:00)") {
		t.Fatalf("%s", l)
	}
}
