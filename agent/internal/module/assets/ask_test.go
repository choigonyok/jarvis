package assets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// ledger stands in for assets-svc: the withdrawals and the answers written.
type ledger struct {
	mu       sync.Mutex
	outflows []Outflow
	answers  []OutflowAnswer
}

func (l *ledger) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/outflows":
			_ = json.NewEncoder(w).Encode(map[string]any{"since": "2026-05-01", "outflows": l.outflows})
		case r.Method == http.MethodPost && r.URL.Path == "/outflows/answer":
			var a OutflowAnswer
			_ = json.NewDecoder(r.Body).Decode(&a)
			l.answers = append(l.answers, a)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (l *ledger) set(outflows []Outflow) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.outflows = outflows
}

// wait returns the answers once there are n (decisions are heard on their
// own goroutines), or what there is after a second.
func (l *ledger) wait(n int) []OutflowAnswer {
	for i := 0; i < 100; i++ {
		l.mu.Lock()
		got := append([]OutflowAnswer(nil), l.answers...)
		l.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]OutflowAnswer(nil), l.answers...)
}

var categories = []string{"식비", "생활·통신", "기타"}

func askConfig() AskConfig {
	return AskConfig{MaxOpen: 12, Categories: func(context.Context) ([]string, error) { return categories, nil }}
}

func asking(t *testing.T, outflows []Outflow) (*Module, *proposal.Store, *ledger) {
	t.Helper()
	l := &ledger{outflows: outflows}
	m := New(NewStore(l.serve(t).URL, "tok"))
	props := proposal.NewStore(bus.New())
	m.Ask(props, nil)
	return m, props, l
}

func pendingCards(props *proposal.Store) []proposal.Proposal {
	return props.PendingWhere(func(p proposal.Proposal) bool { return p.Action.Kind == KindOutflow })
}

var history = []Outflow{
	{Key: "upbit:a", Date: "2026-03-04", Venue: "upbit", AmountKrw: 120000, Memo: "원화 출금", Status: "ask", AskKrw: 120000},
	{Key: "upbit:b", Date: "2026-03-11", Venue: "upbit", AmountKrw: 120000, Memo: "원화 출금", Status: "ask", AskKrw: 120000,
		Candidates: []FlowRef{{Key: "upbit:c", Date: "2026-03-18", Venue: "upbit", AmountKrw: 175000}}},
	{Key: "manual:10", Date: "2026-04-21", Venue: "kis", AmountKrw: 600000, Memo: "업비트로 이체", Status: "transfer",
		Parts: []OutflowPart{{Use: "transfer", AmountKrw: 600000, To: &FlowRef{Key: "upbit:d", Date: "2026-04-21", Venue: "upbit", AmountKrw: 600000}}}},
	{Key: "auto:kis:2026-04-22", Date: "2026-04-22", Venue: "kis", AmountKrw: 900000, Memo: "출금", Status: "ask", AskKrw: 250000,
		Parts: []OutflowPart{{Use: "transfer", AmountKrw: 650000, To: &FlowRef{Key: "upbit:e", Date: "2026-04-24", Venue: "upbit", AmountKrw: 650000}}}},
	{Key: "upbit:f", Date: "2026-04-27", Venue: "upbit", AmountKrw: 55000, Memo: "원화 출금", Status: "waiting", AskKrw: 55000},
}

// Every withdrawal nothing explains gets its card at once - the ones from
// before too - and none is asked twice while its card waits. A transfer and
// one too fresh to ask about get none.
func TestEveryUnexplainedWithdrawalGetsACard(t *testing.T) {
	m, props, _ := asking(t, history)
	if err := m.askOnce(context.Background(), askConfig()); err != nil {
		t.Fatal(err)
	}
	cards := pendingCards(props)
	if len(cards) != 3 {
		t.Fatalf("물을 출금마다 한 장: %d", len(cards))
	}
	first := cards[0]
	if first.Origin != proposal.OriginNotice || first.Card.Title != "3/4 업비트에서 뺀 ₩120,000, 어디로 갔나요?" ||
		!strings.Contains(first.Card.Body, "업비트 원화 출금 ₩120,000 · 3/4(수)") ||
		!strings.Contains(first.Card.Body, "다른 투자 계좌로 다시 들어온 돈이 없어요.") {
		t.Fatalf("카드: %+v", first.Card)
	}
	f := first.Card.Fields
	if len(f) != 4 || f[0].Key != "use" || !f[0].Required || len(f[0].Options) != 5 ||
		f[1].Key != "category" || strings.Join(f[1].Options, ",") != "식비,생활·통신,기타" ||
		f[2].Key != "memo" || f[3].Key != "amount" || f[3].Value != "120000" {
		t.Fatalf("칸: %+v", f)
	}
	if !strings.Contains(cards[1].Card.Body, "그 뒤 들어온 돈: 3/18 업비트 ₩175,000") {
		t.Fatalf("후보 입금: %s", cards[1].Card.Body)
	}
	part := cards[2].Card
	if part.Title != "4/22 한국투자증권에서 뺀 돈 중 ₩250,000은 어디로 갔나요?" ||
		!strings.Contains(part.Body, "4/24 업비트 입금으로 옮김 ₩650,000") || !strings.Contains(part.Body, "남은 ₩250,000을 물어요.") ||
		part.Fields[3].Value != "250000" {
		t.Fatalf("일부만 찾은 출금: %+v", part)
	}

	if err := m.askOnce(context.Background(), askConfig()); err != nil || len(pendingCards(props)) != 3 {
		t.Fatalf("기다리는 카드가 있으면 다시 묻지 않습니다: %d %v", len(pendingCards(props)), err)
	}
}

// The card's answer is what assets-svc is told: a spend with its category
// and what it was for; a card bill alone. Turned down, the question waits a
// week.
func TestAnsweringWritesWhereItWent(t *testing.T) {
	m, props, l := asking(t, history[:2])
	if err := m.askOnce(context.Background(), askConfig()); err != nil {
		t.Fatal(err)
	}
	cards := pendingCards(props)
	act, card, err := m.Revise(context.Background(), cards[0].Action,
		map[string]string{"use": useSpend, "category": "생활·통신", "memo": "관리비", "amount": "70,000"})
	if err != nil {
		t.Fatal(err)
	}
	if err := props.ReviseWith(cards[0].ID, act, card, nil); err != nil {
		t.Fatal(err)
	}
	if err := props.Decide(cards[0].ID, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	got := l.wait(1)
	want := OutflowAnswer{Key: "upbit:a", FlowKrw: 120000, Use: "spend", AmountKrw: 70000, Category: "생활·통신", Memo: "관리비"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("지출 답: %+v", got)
	}
	time.Sleep(30 * time.Millisecond)
	if p, _ := props.Get(cards[0].ID); p.State != proposal.Executed ||
		p.Note != "지출(관리비, 생활·통신) ₩70,000 - 가계부에 투자금으로 들어가요. 남은 ₩50,000은 다시 물어요." {
		t.Fatalf("결과: %s %q", p.State, p.Note)
	}

	if err := props.Decide(cards[1].ID, proposal.Reject); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if p, _ := props.Get(cards[1].ID); p.State != proposal.Rejected || p.Note != "일주일 뒤에 다시 물어요." {
		t.Fatalf("반려: %s %q", p.State, p.Note)
	}
	if err := m.askOnce(context.Background(), askConfig()); err != nil {
		t.Fatal(err)
	}
	for _, p := range pendingCards(props) {
		var in OutflowInput
		_ = json.Unmarshal(p.Action.Input, &in)
		if in.Outflow.Key == "upbit:b" {
			t.Fatal("반려한 출금은 일주일 동안 다시 묻지 않습니다")
		}
	}
}

func TestReviseTakesOnlyAnAnswerThatCanBeWritten(t *testing.T) {
	m, props, _ := asking(t, history[:1])
	if err := m.askOnce(context.Background(), askConfig()); err != nil {
		t.Fatal(err)
	}
	a := pendingCards(props)[0].Action
	for _, edits := range []map[string]string{
		{},                                // 어디로 갔는지 고르지 않음
		{"use": "몰라"},                     // 없는 선택지
		{"use": useSpend},                 // 지출인데 분류가 없음
		{"use": useSpend, "category": "없는분류"},
		{"use": useCard, "amount": "0"},
		{"use": useCard, "amount": "120001"}, // 나간 돈보다 많이
		{"use": useCard, "note": "x"},
	} {
		if _, _, err := m.Revise(context.Background(), a, edits); err == nil {
			t.Fatalf("받으면 안 됩니다: %v", edits)
		}
	}
	act, _, err := m.Revise(context.Background(), a, map[string]string{"use": useCard})
	if err != nil {
		t.Fatal(err)
	}
	var in OutflowInput
	_ = json.Unmarshal(act.Input, &in)
	if got, err := answerOf(in); err != nil || got != (OutflowAnswer{Key: "upbit:a", FlowKrw: 120000, Use: "card", AmountKrw: 120000}) {
		t.Fatalf("카드값은 금액 그대로, 분류 없이: %+v %v", got, err)
	}
}

// A card waiting on what is no longer a question - the money turned up in an
// account here - is put away rather than left to be answered.
func TestACardThatIsNoLongerAQuestionIsPutAway(t *testing.T) {
	m, props, l := asking(t, history[:1])
	if err := m.askOnce(context.Background(), askConfig()); err != nil {
		t.Fatal(err)
	}
	id := pendingCards(props)[0].ID
	moved := history[0]
	moved.Status, moved.AskKrw = "transfer", 0
	l.set([]Outflow{moved})
	if err := m.askOnce(context.Background(), askConfig()); err != nil {
		t.Fatal(err)
	}
	if p, _ := props.Get(id); p.State != proposal.Rejected || !strings.Contains(p.Note, "이제 물을 게 없어요") {
		t.Fatalf("내려야 합니다: %s %q", p.State, p.Note)
	}
}
