package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
)

// A run stopped at a payment waits in wait_payment while the request is held
// at the guard: approved in that time, the request went on and the run is
// told to carry on without taking the step again - and the decision is only
// written down for it, not queued for another run that would pay twice.
func TestWaitPaymentHearsAnApprovalWhileTheRequestIsHeld(t *testing.T) {
	svc := &jobsSvc{}
	m := New(NewStore(svc.serve(t).URL, ""), nil, proposal.NewStore(bus.New()), nil, nil)
	m.current.add(5, Job{ID: 16, Title: "장범준 LP"}, true)
	s := runSession(t, m, "5")

	m.PaymentRaised(16, "p-2", time.Now().Add(3*time.Second))
	done := make(chan string, 1)
	go func() {
		text, _ := callRun(t, s, "wait_payment", nil)
		done <- text
	}()
	time.Sleep(200 * time.Millisecond)
	if err := m.PaymentDecided(16, "p-2", true, true, "[결제 승인됨] 승인과 함께 그대로 나갔습니다."); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-done:
		if !strings.Contains(text, "그대로 나갔습니다") || !strings.Contains(text, "다시 누르지 말고") {
			t.Fatalf("멈춘 요청이 나갔다고, 다시 누르지 말라고 해야 합니다: %q", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("결정이 나면 wait_payment 가 바로 돌아와야 합니다")
	}
	got := svc.got()
	if contains(got, "/jobs/16/answer p-2") || contains(got, "/runs/5/wait p-2") {
		t.Fatalf("같은 실행이 이어 가므로 다음 실행을 걸지 않아야 합니다: %v", got)
	}
	if !contains(got, "/runs/5/step") {
		t.Fatalf("결정은 이 실행의 기록에 남아야 합니다: %v", got)
	}
}

type fakeVault struct {
	s  vault.Shipping
	ok bool
}

func (f fakeVault) Shipping(context.Context) (vault.Shipping, bool, error) { return f.s, f.ok, nil }
func (f fakeVault) Preview(_ context.Context, a action.Action) (action.Card, error) {
	return action.Card{Title: "카드 " + a.Kind, Fields: []action.Field{{Key: "x", Label: "x", Kind: "text", Private: true}}}, nil
}

type fakeCharges struct{ c Charges }

func (f fakeCharges) Charges(context.Context, time.Time) (Charges, error) { return f.c, nil }

// An order sheet is filled from the saved shipping profile; without one the
// run raises the vault's card itself and waits on it, and the decision comes
// back as a card answer that wakes the job.
func TestOrderToolsReadTheProfileAndRaiseTheVaultCards(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, nil, nil)
	m.current.add(5, Job{ID: 16, Title: "장범준 LP"}, true)
	s := runSession(t, m, "5")

	if text, isErr := callRun(t, s, "shipping_address", nil); !isErr || !strings.Contains(text, "보관함이 없습니다") {
		t.Fatalf("보관함이 없으면 그렇게 말해야 합니다: %q", text)
	}
	m.SetVault(fakeVault{s: vault.Shipping{Name: "기본", Recipient: "홍길동", Phone: "01012345678", Zip: "04524",
		Address: "서울 중구 세종대로 110", Detail: "101동 1001호"}, ok: true})
	text, _ := callRun(t, s, "shipping_address", nil)
	for _, want := range []string{"홍길동", "010-1234-5678", "04524", "세종대로 110", "101동 1001호"} {
		if !strings.Contains(text, want) {
			t.Fatalf("배송지에 %q 가 없습니다: %q", want, text)
		}
	}

	m.SetVault(fakeVault{})
	if text, _ := callRun(t, s, "shipping_address", nil); !strings.Contains(text, "request_shipping_address") {
		t.Fatalf("저장된 배송지가 없으면 카드를 올리라고 해야 합니다: %q", text)
	}
	if text, isErr := callRun(t, s, "request_payment_pin", map[string]any{"pay": "네이버페이"}); isErr || !strings.Contains(text, "실행을 마치세요") {
		t.Fatalf("결제 비밀번호 카드: %q", text)
	}
	cards := props.List()
	if len(cards) != 1 || cards[0].Action.Kind != vault.KindSavePin || vaultJob(cards[0]) != 16 || !strings.Contains(cards[0].Card.Title, "장범준 LP") {
		t.Fatalf("작업 16 의 결제 비밀번호 카드여야 합니다: %+v", cards)
	}
	var in vaultCard
	_ = json.Unmarshal(cards[0].Action.Input, &in)
	if in.Pay != "네이버페이" {
		t.Fatalf("카드는 어느 결제의 것인지 가져야 합니다: %+v", in)
	}
	if got := svc.got(); !contains(got, "/runs/5/wait "+cards[0].ID) {
		t.Fatalf("작업을 그 카드에 걸어야 합니다: %v", got)
	}
	if w := m.waitingOn(16); len(w) != 1 || w[0].ID != cards[0].ID {
		t.Fatalf("작업이 기다리는 카드로 보여야 합니다: %v", w)
	}
	if err := props.Decide(cards[0].ID, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && !contains(svc.got(), "/jobs/16/answer "+cards[0].ID); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := svc.got(); !contains(got, "/jobs/16/answer "+cards[0].ID) {
		t.Fatalf("결정은 카드 답으로 작업에 가야 합니다: %v", got)
	}
	if p, _ := props.Get(cards[0].ID); p.State != proposal.Executed {
		t.Fatalf("승인된 저장 카드는 실행됨이어야 합니다: %v", p.State)
	}

	// A new run: the shipping card, the same way.
	m.current.add(6, Job{ID: 16, Title: "장범준 LP"}, true)
	s6 := runSession(t, m, "6")
	if _, isErr := callRun(t, s6, "request_shipping_address", map[string]any{"reason": "주문서 칸"}); isErr {
		t.Fatal("배송지 카드를 올려야 합니다")
	}
	var ship proposal.Proposal
	for _, p := range props.List() {
		if p.Action.Kind == vault.KindSaveShipping {
			ship = p
		}
	}
	if ship.ID == "" || vaultJob(ship) != 16 {
		t.Fatalf("작업 16 의 배송지 카드가 있어야 합니다: %+v", props.List())
	}
}

// Whether a payment went through is read from the card alerts first; when
// they have stopped coming in, the run is told not to read their silence as
// "not paid" (KakaoTalk logged out 10/28 17:56 and they stopped for 33 hours).
func TestPaymentAlertsSayWhenTheyMayBeLate(t *testing.T) {
	svc := &jobsSvc{}
	m := New(NewStore(svc.serve(t).URL, ""), nil, proposal.NewStore(bus.New()), nil, nil)
	m.current.add(5, Job{ID: 16, Title: "장범준 LP"}, true)
	s := runSession(t, m, "5")
	at := time.Date(2026, 10, 30, 2, 7, 0, 0, time.Local)
	m.SetCharges(fakeCharges{Charges{List: []Charge{{At: at, Issuer: "하나카드", Merchant: "샴푸뮤직엔터테인", Amount: 113000}}, LastAlertAt: &at}})
	text, _ := callRun(t, s, "payment_alerts", map[string]any{"hours": 3})
	if !strings.Contains(text, "10/30 02:07 하나카드 113,000원 · 샴푸뮤직엔터테인") || strings.Contains(text, "주의") {
		t.Fatalf("카드 알림: %q", text)
	}
	m.SetCharges(fakeCharges{Charges{Stale: true}})
	if text, _ := callRun(t, s, "payment_alerts", nil); !strings.Contains(text, "알림이 없습니다") || !strings.Contains(text, "결제가 안 된 것은 아닙니다") {
		t.Fatalf("수집이 멈췄으면 그렇게 말해야 합니다: %q", text)
	}
}

// A list of every job came to 55,000 characters (2026-10-29) and the
// conversation read it through a file: the jobs under way are listed in
// full, the finished ones counted, and a waiting job names the card it
// waits on - "승인 대기" read as "the payment waits" when the card was a
// question (2026-10-30 01:12).
func TestListJobsIsShortAndNamesTheCard(t *testing.T) {
	props := proposal.NewStore(bus.New())
	m := New(NewStore("http://unused", ""), nil, props, nil, nil)
	card, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindAsk, Input: json.RawMessage(`{}`)},
		Card: action.Card{Title: "네이버페이 결제는 성공했는데 샵 주문 반영이 확인되지 않아요"}})
	long := strings.Repeat("가", 400)
	js := []Job{
		{ID: 1, Title: "장범준 LP", State: "waiting", WaitingCard: card.ID, Summary: long},
		{ID: 2, Title: "김수영 콘서트", State: "active"},
	}
	for i := 0; i < 30; i++ {
		js = append(js, Job{ID: int64(100 + i), Title: fmt.Sprintf("끝난 일 %d", i), State: "done", Summary: long})
	}
	out := m.renderList(js, false)
	if !strings.Contains(out, "[1 · 카드 대기] 장범준 LP") || !strings.Contains(out, "기다리는 카드: '네이버페이 결제는 성공했는데") {
		t.Fatalf("기다리는 카드의 제목이 보여야 합니다: %s", out)
	}
	if strings.Contains(out, "끝난 일 5") || !strings.Contains(out, "끝난 작업 30개") || len([]rune(out)) > 1200 {
		t.Fatalf("끝난 작업은 세고 몇 개만: %d자 %s", len([]rune(out)), out)
	}
	if all := m.renderList(js, true); !strings.Contains(all, "[129 · 끝남] 끝난 일 29") || strings.Contains(all, long) {
		t.Fatalf("all 이면 끝난 작업도 짧게: %s", all)
	}
	d := m.renderDetail(Detail{Job: js[0], Memory: map[string]string{"order_form": "010-1234-5678 세종대로"},
		Events: []Event{{Kind: "step", Text: "누르기"}, {Kind: "card", Text: "결제 승인 대기"}, {Kind: "answer", Text: "[결제 승인됨]"}}})
	if !strings.Contains(d, "기억(키만): order_form") || strings.Contains(d, "010-1234-5678") || strings.Contains(d, "누르기") || !strings.Contains(d, "answer: [결제 승인됨]") {
		t.Fatalf("자세히: 기억은 키만, 누르기는 빼고, 카드·답은 보여야 합니다: %s", d)
	}
}
