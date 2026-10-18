package spending

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

// book stands in for spending-svc: what to ask about, and the answers written.
type book struct {
	mu      sync.Mutex
	entries []Unclear
	patches []string
}

func (b *book) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/unclear":
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": b.entries,
				"categories": []string{"식비", "카페·간식", "문화·여가", "쇼핑", "기타"}})
		case r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			raw, _ := json.Marshal(body)
			b.patches = append(b.patches, r.URL.Path+" "+string(raw))
			_ = json.NewEncoder(w).Encode(map[string]int{"updated": 3})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wait returns the patches once there are n (decisions are heard on their own
// goroutines), or what there is after a second.
func (b *book) wait(n int) []string {
	for i := 0; i < 100; i++ {
		b.mu.Lock()
		got := append([]string(nil), b.patches...)
		b.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.patches...)
}

func asking(t *testing.T, entries []Unclear) (*Module, *proposal.Store, *book) {
	t.Helper()
	b := &book{entries: entries}
	m := New(NewStore(b.serve(t).URL, "tok"))
	props := proposal.NewStore(bus.New())
	m.Ask(props, nil)
	return m, props, b
}

var at = time.Date(2026, 10, 18, 12, 34, 0, 0, time.Local)

// Each charge the rules left in 기타 gets a card, a few at a time, and the
// same one is not asked twice while its card waits.
func TestAskRaisesACardPerUnclearChargeAFewAtATime(t *testing.T) {
	m, props, _ := asking(t, []Unclear{
		{Kind: "tx", ID: 51, TxID: 51, Merchant: "어느극장(주)", AmountKrw: 30000, ApprovedAt: at, Issuer: "하나카드", CardTail: "1234"},
		{Kind: "item", ID: 7, TxID: 50, Merchant: "쿠팡", Name: "알 수 없는 물건", AmountKrw: 12000, ApprovedAt: at},
		{Kind: "tx", ID: 52, TxID: 52, Merchant: "어느가게", AmountKrw: 7000, ApprovedAt: at},
		{Kind: "tx", ID: 53, TxID: 53, Merchant: "또다른곳", AmountKrw: 9000, ApprovedAt: at},
	})
	if err := m.askOnce(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	cards := props.List()
	if len(cards) != 3 {
		t.Fatalf("한 번에 세 장까지: %d", len(cards))
	}
	tx := cards[0]
	if tx.Origin != proposal.OriginNotice || tx.Action.Kind != KindCategory || tx.Card.Title != "이 결제는 어떤 분류인가요?" ||
		!strings.Contains(tx.Card.Body, "어느극장(주) · ₩30,000") || !strings.Contains(tx.Card.Body, "10/18(일) 12:34 · 하나카드 1234") {
		t.Fatalf("결제 카드: %+v", tx.Card)
	}
	if f := tx.Card.Fields; len(f) != 2 || f[0].Key != "category" || !f[0].Required || contains(f[0].Options, "기타") ||
		f[1].Key != "remember" || f[1].Value != rememberYes {
		t.Fatalf("결제 카드의 칸: %+v", f)
	}
	if item := cards[1]; item.Card.Title != "이 상품은 어떤 분류인가요?" || len(item.Card.Fields) != 1 {
		t.Fatalf("상품 카드: %+v", item.Card)
	}
	if err := m.askOnce(context.Background(), 3); err != nil || len(props.List()) != 3 {
		t.Fatalf("기다리는 카드가 있으면 더 열지 않고, 같은 것을 다시 묻지 않습니다: %d %v", len(props.List()), err)
	}
}

// Picked, the charge takes the category and teaches its merchant; turned
// down, it is 기타 for good; a product changes alone.
func TestAnsweringWritesTheCategory(t *testing.T) {
	m, props, b := asking(t, []Unclear{
		{Kind: "tx", ID: 51, TxID: 51, Merchant: "어느극장(주)", AmountKrw: 30000, ApprovedAt: at},
		{Kind: "tx", ID: 52, TxID: 52, Merchant: "어느가게", AmountKrw: 7000, ApprovedAt: at},
		{Kind: "item", ID: 7, TxID: 50, Merchant: "쿠팡", Name: "알 수 없는 물건", AmountKrw: 12000, ApprovedAt: at},
	})
	if err := m.askOnce(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	cards := props.List()
	approve := func(p proposal.Proposal, edits map[string]string) {
		t.Helper()
		act, card, err := m.Revise(context.Background(), p.Action, edits)
		if err != nil {
			t.Fatal(err)
		}
		if err := props.ReviseWith(p.ID, act, card, edits); err != nil {
			t.Fatal(err)
		}
		if err := props.Decide(p.ID, proposal.Approve); err != nil {
			t.Fatal(err)
		}
	}
	approve(cards[0], map[string]string{"category": "문화·여가"})
	if got := b.wait(1); len(got) != 1 || got[0] != `/transactions/51 {"category":"문화·여가","remember":true}` {
		t.Fatalf("고른 분류로, 가맹점까지: %v", got)
	}
	if err := props.Decide(cards[1].ID, proposal.Reject); err != nil {
		t.Fatal(err)
	}
	if got := b.wait(2); len(got) != 2 || got[1] != `/transactions/52 {"category":"기타","remember":false}` {
		t.Fatalf("반려는 기타로 확정: %v", got)
	}
	approve(cards[2], map[string]string{"category": "쇼핑"})
	if got := b.wait(3); len(got) != 3 || got[2] != `/items/7 {"category":"쇼핑"}` {
		t.Fatalf("상품은 상품만: %v", got)
	}
	time.Sleep(30 * time.Millisecond)
	if p, _ := props.Get(cards[0].ID); p.State != proposal.Executed || !strings.Contains(p.Note, "같은 가맹점 2건") {
		t.Fatalf("결과: %s %q", p.State, p.Note)
	}
	if p, _ := props.Get(cards[1].ID); p.State != proposal.Rejected || !strings.Contains(p.Note, "다시 묻지 않습니다") {
		t.Fatalf("반려 결과: %s %q", p.State, p.Note)
	}
}

func TestRevisePicksOnlyWhatTheCardOffers(t *testing.T) {
	m, props, _ := asking(t, []Unclear{{Kind: "tx", ID: 51, TxID: 51, Merchant: "어느극장(주)", AmountKrw: 30000, ApprovedAt: at}})
	if err := m.askOnce(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	a := props.List()[0].Action
	for _, edits := range []map[string]string{
		{},                 // 분류를 고르지 않음
		{"category": "기타"}, // 반려가 기타다
		{"category": "없는분류"},
		{"category": "식비", "remember": "가끔"},
		{"category": "식비", "memo": "x"},
	} {
		if _, _, err := m.Revise(context.Background(), a, edits); err == nil {
			t.Fatalf("받으면 안 됩니다: %v", edits)
		}
	}
	if _, card, err := m.Revise(context.Background(), a, map[string]string{"category": "식비", "remember": rememberNo}); err != nil || card.Fields[0].Value != "식비" {
		t.Fatalf("고른 값: %v %+v", err, card.Fields)
	}
}
