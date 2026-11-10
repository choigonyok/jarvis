package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/holder"
)

// driverRunner is a runner whose background state and conversation's hold on
// the browser a test changes.
type driverRunner struct {
	mu       sync.Mutex
	bg, chat bool
}

func (r *driverRunner) Send(string, []string) error { return nil }
func (r *driverRunner) InBackground() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bg
}
func (r *driverRunner) ChatOnBrowser() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chat
}
func (r *driverRunner) set(bg, chat bool) {
	r.mu.Lock()
	r.bg, r.chat = bg, chat
	r.mu.Unlock()
}

func postPayment(s *Server, body string) map[string]string {
	req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.postIntercept(rec, req)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

const (
	musinsaSession = `{"url":"https://order.musinsa.com/api2/order/v1/orders/payment-session","method":"POST","host":"order.musinsa.com"}`
	tossWindow     = `{"url":"https://apigw.tosspayments.com/payment-gateway-window/open/pg-window/v1/px-payment-parameters","method":"POST","host":"apigw.tosspayments.com","window":true}`
	hanaWindow     = `{"url":"https://acs.hanacard.co.kr/payapp/C000000000VPQC.web","method":"POST","host":"acs.hanacard.co.kr","window":true}`
	tossLog        = `{"url":"https://log.tosspayments.com/v1/log","method":"POST","host":"log.tosspayments.com","window":true}`
)

func carryServer(t *testing.T, runner *driverRunner, jobs *fakeJobs) (*Server, *proposal.Store, *followUps) {
	t.Helper()
	props := proposal.NewStore(bus.New())
	fu := &followUps{}
	s := New(Deps{Proposals: props, Runner: runner, InterceptToken: "tok", FollowUp: fu, Jobs: jobs,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.holdDur = 0
	return s, props, fu
}

// One payment, one card: the musinsa card approved, its 결제하기 hands the
// order to the PG, and the window - and the card company's inside it - opens
// on that approval. A window for another amount is another payment.
func TestTheMallsCardOpensItsPaymentWindow(t *testing.T) {
	runner := &driverRunner{bg: true}
	jobs := &fakeJobs{id: 137, title: "무신사 방한화 구매", summary: "결제 직전 · 방한화 베이지/240 · 수량 1 · 결제 금액 22470원 · 하나카드"}
	s, props, _ := carryServer(t, runner, jobs)

	first := postPayment(s, musinsaSession)
	if first["pending"] == "" {
		t.Fatalf("무신사 결제는 카드를 올립니다: %v", first)
	}
	if err := props.Decide(first["pending"], proposal.Approve); err != nil {
		t.Fatal(err)
	}
	jobs.wait(1)
	if again := postPayment(s, musinsaSession); again["decision"] != string(proposal.Approve) || again["why"] != "granted" {
		t.Fatalf("승인된 결제하기: %v", again)
	}
	if w := postPayment(s, tossWindow); w["decision"] != string(proposal.Approve) || w["why"] != "carried" {
		t.Fatalf("그 결제의 결제창은 같은 승인으로 열립니다: %v", w)
	}
	if c := postPayment(s, hanaWindow); c["decision"] != string(proposal.Approve) || c["why"] != "granted" {
		t.Fatalf("결제창 안의 카드사 창도 같은 결제입니다: %v", c)
	}
	if n := len(props.List()); n != 1 {
		t.Fatalf("결제 한 건에 카드 %d 장", n)
	}
	other := postPayment(s, `{"url":"https://stdpay.inicis.com/payMain/pay","method":"POST","host":"stdpay.inicis.com","window":true,"amount":"50000"}`)
	if other["pending"] == "" {
		t.Fatalf("금액이 다른 결제창은 따로 묻습니다: %v", other)
	}
}

// A window that opens long after the mall's request went through is not that
// payment handed on: it has a card of its own.
func TestALateWindowHasItsOwnCard(t *testing.T) {
	runner := &driverRunner{bg: true}
	jobs := &fakeJobs{id: 137, title: "무신사 구매"}
	s, props, _ := carryServer(t, runner, jobs)
	now := time.Now()
	s.payments.Now = func() time.Time { return now }

	first := postPayment(s, musinsaSession)
	_ = props.Decide(first["pending"], proposal.Approve)
	jobs.wait(1)
	postPayment(s, musinsaSession)
	now = now.Add(holder.CarryGap + time.Second)
	if w := postPayment(s, tossWindow); w["pending"] == "" || w["why"] != "waiting" {
		t.Fatalf("한참 뒤의 결제창은 카드로 묻습니다: %v", w)
	}
}

// A job's run that ended on wait_payment leaves its payment window open, and
// the window goes on writing. Those writes wait on the job's card - no card
// of the conversation's, which would ask about nothing it did and, approved,
// tell it to pay. Once the conversation drives the browser, what it sends is
// its own again.
func TestALeftPageWaitsOnItsJobsCard(t *testing.T) {
	runner := &driverRunner{bg: true}
	jobs := &fakeJobs{id: 137, title: "무신사 구매", summary: "결제 직전 · 결제 금액 22470원"}
	s, props, fu := carryServer(t, runner, jobs)

	card := postPayment(s, tossWindow)["pending"]
	if card == "" {
		t.Fatal("결제창 카드가 올라가야 합니다")
	}
	runner.set(false, false)
	jobs.running(0)
	left := postPayment(s, hanaWindow)
	if left["decision"] != string(proposal.Reject) || left["why"] != "left" || left["pending"] != card {
		t.Fatalf("작업이 남긴 페이지는 그 작업의 카드를 기다립니다: %v", left)
	}
	if n := len(props.List()); n != 1 {
		t.Fatalf("대화 카드가 %d 장 더 떴습니다", n-1)
	}

	// Approved later: the job hears it, the conversation is told nothing,
	// and the page still writing waits for the job's next run.
	if err := props.Decide(card, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	jobs.wait(1)
	if got := fu.wait(0); len(got) != 0 {
		t.Fatalf("대화에 결제하라고 전했습니다: %v", got)
	}
	if after := postPayment(s, hanaWindow); after["why"] != "left" || after["pending"] != "" {
		t.Fatalf("승인을 기다리는 작업의 페이지: %v", after)
	}

	// The conversation drives the browser: its request is its own.
	runner.set(false, true)
	if own := postPayment(s, hanaWindow); own["why"] == "left" || own["pending"] == "" {
		t.Fatalf("대화가 브라우저를 쓰면 대화의 결제입니다: %v", own)
	}
}

// With no job's payment live there, a stopped request with nobody driving is
// the conversation's, as before - its own card waiting there comes first too.
func TestNoLeftPageWithoutAJobsPayment(t *testing.T) {
	runner := &driverRunner{}
	jobs := &fakeJobs{}
	s, props, _ := carryServer(t, runner, jobs)
	own := postPayment(s, tossWindow)
	if own["why"] != "waiting" || own["pending"] == "" {
		t.Fatalf("대화의 결제는 카드로 묻습니다: %v", own)
	}
	again := postPayment(s, tossLog)
	if again["pending"] != own["pending"] || len(props.List()) != 1 {
		t.Fatalf("대화의 카드가 먼저입니다: %v", again)
	}
}
