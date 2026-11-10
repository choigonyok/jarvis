package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/holder"
)

// jobsSvc stands in for mind: it keeps what the run tools sent.
type jobsSvc struct {
	mu    sync.Mutex
	calls []string
}

func (f *jobsSvc) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *jobsSvc) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/wait"), strings.HasSuffix(r.URL.Path, "/answer"):
			f.calls = append(f.calls, r.URL.Path+" "+body["card"].(string))
		default:
			f.calls = append(f.calls, r.URL.Path)
		}
		f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/schedule") {
			_ = json.NewEncoder(w).Encode(map[string]any{"at": time.Now()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runSession(t *testing.T, m *Module, run string) *mcp.ClientSession {
	t.Helper()
	server := httptest.NewServer(m.RunHandler())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp/job-run/" + run}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callRun(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out.WriteString(tc.Text)
		}
	}
	return out.String(), res.IsError
}

// A run the browser's guard stopped at a payment hears of its card through
// wait_payment and waits on it; the decision reaches the job as a card
// answer, never as a refusal with no card.
func TestPaymentCardParksTheRunAndTheDecisionWakesIt(t *testing.T) {
	defer func(d time.Duration) { payAppear = d }(payAppear)
	payAppear = 50 * time.Millisecond
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, nil, nil)
	m.current.add(5, Job{ID: 16, Title: "탁상시계 구매"}, true)
	s := runSession(t, m, "5")

	if text, isErr := callRun(t, s, "wait_payment", nil); !isErr || !strings.Contains(text, "사이트 쪽 오류") {
		t.Fatalf("카드가 없으면 사이트의 오류라고 해야 합니다: %q", text)
	}

	// What the run reports just before paying is what the card shows.
	callRun(t, s, "report", map[string]any{"summary": "결제 직전 · 블랙 1개 · 34,350원 · 하나카드"})
	if id, title, summary, ok := m.RunningJob(); !ok || id != 16 || title != "탁상시계 구매" || !strings.Contains(summary, "34,350원") {
		t.Fatalf("실행 중인 작업: %d %q %q %v", id, title, summary, ok)
	}

	m.PaymentRaised(16, "p-1", time.Now())
	// Another card, or a run on its own clock, would leave the payment card
	// with nobody to wake.
	if text, isErr := callRun(t, s, "ask", map[string]any{"question": "주문중 오류가 났습니다. 어떻게 할까요?"}); !isErr || !strings.Contains(text, "wait_payment") {
		t.Fatalf("결제 카드가 떠 있는 동안 ask 는 막아야 합니다: %q", text)
	}
	if _, isErr := callRun(t, s, "schedule_next", map[string]any{"afterMinutes": 30, "reason": "다시"}); !isErr {
		t.Fatal("결제 카드가 떠 있는 동안 schedule_next 는 막아야 합니다")
	}
	if n := len(props.List()); n != 0 {
		t.Fatalf("카드가 %d 장 더 열렸습니다", n)
	}

	if text, isErr := callRun(t, s, "wait_payment", nil); isErr || !strings.Contains(text, "기다립니다") {
		t.Fatalf("결제 카드를 기다려야 합니다: %q", text)
	}
	if got := svc.got(); !contains(got, "/runs/5/wait p-1") {
		t.Fatalf("작업을 결제 카드에 걸어야 합니다: %v", got)
	}

	if err := m.PaymentDecided(16, "p-1", true, false, "[결제 승인됨] 운영자가 승인했습니다."); err != nil {
		t.Fatal(err)
	}
	if got := svc.got(); !contains(got, "/jobs/16/answer p-1") {
		t.Fatalf("결정은 카드 답으로 작업에 가야 합니다: %v", got)
	}
	// Decided while the run is still going: it is told to go on now.
	if text, _ := callRun(t, s, "wait_payment", nil); !strings.Contains(text, "승인됐습니다") {
		t.Fatalf("이미 승인된 카드: %q", text)
	}
	// A later run hears the decision from the card answer, not from here.
	m.pay.settled(16)
	if _, up := m.pay.get(16); up {
		t.Fatal("결정된 카드는 다음 실행이 시작되면 잊어야 합니다")
	}
}

// A run that asked the operator does not end the job in the same breath: the
// answer would have nowhere to go. Once the card is decided, it may.
func TestFinishWaitsForTheCardItsRunRaised(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, nil, nil)
	m.current.add(5, Job{ID: 18, Title: "탁상시계 구매"}, true)
	s := runSession(t, m, "5")

	if _, isErr := callRun(t, s, "ask", map[string]any{"question": "쿠페이 비밀번호 입력이 자동으로 안 됩니다. 어떻게 할까요?"}); isErr {
		t.Fatal("ask 실패")
	}
	card := props.List()[0]
	if text, isErr := callRun(t, s, "finish", map[string]any{"summary": "멈춤"}); !isErr || !strings.Contains(text, card.ID) {
		t.Fatalf("카드를 올린 실행은 끝내지 못해야 합니다: %q", text)
	}
	if contains(svc.got(), "/runs/5/finish") {
		t.Fatal("작업을 끝냈습니다")
	}
	m.decided(card, proposal.Approve)
	if _, isErr := callRun(t, s, "finish", map[string]any{"summary": "답을 받고 끝냄"}); isErr {
		t.Fatal("카드가 결정된 뒤에는 끝낼 수 있어야 합니다")
	}
}

// A job that ends does not pay: its payment card, still up, goes with it.
func TestFinishingDropsAPendingPaymentCard(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, nil, nil)
	m.current.add(5, Job{ID: 16, Title: "탁상시계 구매"}, true)
	s := runSession(t, m, "5")

	card, _ := props.Open(proposal.Proposal{Origin: proposal.OriginIntercept, Action: action.Action{Kind: "browser.payment", Input: json.RawMessage(`{}`)}})
	m.PaymentRaised(16, card.ID, time.Now())
	if _, isErr := callRun(t, s, "finish", map[string]any{"summary": "사지 않기로 했습니다"}); isErr {
		t.Fatal("finish 실패")
	}
	if p, _ := props.Get(card.ID); p.State != proposal.Rejected {
		t.Fatalf("끝난 작업의 결제 카드가 남았습니다: %s", p.State)
	}
	if _, up := m.pay.get(16); up {
		t.Fatal("끝난 작업의 결제 카드를 잊어야 합니다")
	}
}

// The run is given the PIN tool and wait_payment: a job is now how a purchase
// reaches its payment.
func TestRunToolsCanPay(t *testing.T) {
	tools := runTools(true)
	for _, want := range []string{"mcp__" + BrowserServer + "__browser_enter_payment_pin", "mcp__" + RunServerName + "__wait_payment"} {
		if !contains(tools, want) {
			t.Fatalf("%s 가 없습니다: %v", want, tools)
		}
	}
	if !strings.Contains(runSystem, "wait_payment") || strings.Contains(runSystem, "결제는 하지 않습니다") {
		t.Fatal("실행 지시문이 결제 승인 카드를 설명해야 합니다")
	}
}

// A flow card - the page of a paying run held on its way back - is waited on
// like a payment card, and wait_payment says what became of the page: gone
// on with the approval, or stopped before it, so the orders come first.
func TestAFlowCardIsWaitedOnLikeAPaymentCard(t *testing.T) {
	defer func(d time.Duration) { payAppear = d }(payAppear)
	payAppear = 50 * time.Millisecond
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, nil, nil)
	m.current.add(5, Job{ID: 137, Title: "무신사 구매"}, true)
	s := runSession(t, m, "5")

	m.FlowRaised(137, "p-9", "musinsapayments.com", time.Now().Add(5*time.Second))
	if text, isErr := callRun(t, s, "ask", map[string]any{"question": "결제가 됐나요?"}); !isErr || !strings.Contains(text, "결제 흐름 카드가 이미") {
		t.Fatalf("결제 흐름 카드가 떠 있는 동안 ask 는 막아야 합니다: %q", text)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = m.PaymentDecided(137, "p-9", true, true, "[결제 흐름 승인됨] ...")
	}()
	if text, isErr := callRun(t, s, "wait_payment", nil); isErr || !strings.Contains(text, "결제 흐름에 더했고") || !strings.Contains(text, "그대로 나갔습니다") {
		t.Fatalf("승인과 함께 이동이 나갔다고 해야 합니다: %q", text)
	}
	if got := svc.got(); !contains(got, "/runs/5/step") || contains(got, "/jobs/137/answer p-9") {
		t.Fatalf("나간 결정은 이 실행의 기록에만 남습니다: %v", got)
	}

	// Run out of time with no decision: the job waits on the card, and a
	// later approval says the payment may not have gone through.
	m.FlowRaised(137, "p-10", "musinsapayments.com", time.Now())
	if text, isErr := callRun(t, s, "wait_payment", nil); isErr || !strings.Contains(text, "결제 흐름 카드를 기다립니다") {
		t.Fatalf("결제 흐름 카드를 기다려야 합니다: %q", text)
	}
	if got := svc.got(); !contains(got, "/runs/5/wait p-10") {
		t.Fatalf("작업을 그 카드에 걸어야 합니다: %v", got)
	}
	m.pay.decided(137, "p-10", true, false)
	c, _ := m.pay.get(137)
	if res := flowDecided(c); res == nil || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "주문 내역에서 이 주문이 생겼는지") {
		t.Fatalf("늦은 승인은 주문 내역부터: %+v", res)
	}
	m.pay.decided(137, "p-10", false, false)
	c, _ = m.pay.get(137)
	if res := flowDecided(c); res == nil || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "이어 가지 말고") {
		t.Fatalf("반려: %+v", res)
	}
}

// What the guard stopped of a run goes in the job's log and to the run
// itself, through the bridge - only for the run holding the browser.
func TestStoppedTellsTheRunHoldingTheBrowser(t *testing.T) {
	svc := &jobsSvc{}
	m := New(NewStore(svc.serve(t).URL, ""), nil, proposal.NewStore(bus.New()), nil, nil)
	g := &fakeGuard{}
	m.guard = g
	m.current.add(5, Job{ID: 134, Title: "배송 확인"}, true)
	m.current.add(6, Job{ID: 22, Title: "할 일 알림"}, false)

	m.Stopped(22, "막은 이동: x", "알림")
	m.Stopped(134, "막은 이동: view.queryship.com/track/", "request_sites 로 더하세요")
	for i := 0; i < 200 && (len(g.heard()) == 0 || !contains(svc.got(), "/runs/5/step")); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if got := g.heard(); len(got) != 1 || got[0] != "request_sites 로 더하세요" {
		t.Fatalf("브라우저를 쥔 실행에만 알려야 합니다: %v", got)
	}
	if got := svc.got(); !contains(got, "/runs/5/step") || contains(got, "/runs/6/step") {
		t.Fatalf("그 실행의 기록에 남겨야 합니다: %v", got)
	}
}

// A flow card's decision that reached no run is told as a flow card's.
func TestALateFlowAnswerSaysWhatToDo(t *testing.T) {
	p := proposal.Proposal{ID: "p-9", Origin: proposal.OriginIntercept, Action: action.Action{Kind: holder.KindFlow}, Card: action.Card{Title: "결제 흐름에 더하기 · musinsapayments.com"}, State: proposal.Executed}
	if text := lateAnswer(p); !strings.Contains(text, "주문 내역") || strings.Contains(text, "결제 단계를 다시 진행하세요") {
		t.Fatalf("승인: %q", text)
	}
	p.State = proposal.Rejected
	if text := lateAnswer(p); !strings.Contains(text, "이어 가지 마세요") {
		t.Fatalf("반려: %q", text)
	}
	p.Action.Kind = holder.KindPayment
	if text := lateAnswer(p); !strings.Contains(text, "결제하지 마세요") {
		t.Fatalf("결제 카드: %q", text)
	}
}

// fakeGuard keeps what the browser was held to, how often it was let go, and
// what the runs were told.
type fakeGuard struct {
	mu       sync.Mutex
	sites    [][]string
	released int
	told     []string
}

func (g *fakeGuard) Tell(_ context.Context, text string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.told = append(g.told, text)
	return nil
}

func (g *fakeGuard) heard() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.told...)
}

func (g *fakeGuard) Restrict(_ context.Context, sites []string, _ time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sites = append(g.sites, append([]string(nil), sites...))
	return nil
}

func (g *fakeGuard) Release(context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.released++
	return nil
}

// A site a run finds it needs goes on the job without a card - in its log,
// where the operator sees it - and a run on the browser may open it at once;
// one without the browser hears to come back on the next run.
func TestSitesAreAddedWithoutACard(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, nil, nil)
	g := &fakeGuard{}
	m.guard, m.hold = g, time.Minute
	m.current.add(7, Job{ID: 21, Title: "캠핑 장작 구매", Sites: []string{"www.coupang.com"}}, true)
	s := runSession(t, m, "7")

	text, isErr := callRun(t, s, "request_sites", map[string]any{"sites": []string{"https://smartstore.naver.com/x", "www.coupang.com"}, "reason": "최저가 비교"})
	if isErr || !strings.Contains(text, "지금 열 수 있습니다") {
		t.Fatalf("바로 더해야 합니다: %q", text)
	}
	if n := len(props.List()); n != 0 {
		t.Fatalf("카드가 %d 장 열렸습니다", n)
	}
	if got := svc.got(); !contains(got, "/jobs/21/sites") || !contains(got, "/runs/7/step") {
		t.Fatalf("작업에 더하고 기록에 남겨야 합니다: %v", got)
	}
	if len(g.sites) != 1 || strings.Join(g.sites[0], ",") != "www.coupang.com,smartstore.naver.com" {
		t.Fatalf("이번 실행의 브라우저도 넓혀야 합니다: %v", g.sites)
	}
	if text, isErr := callRun(t, s, "request_sites", map[string]any{"sites": []string{"smartstore.naver.com"}, "reason": "다시"}); !isErr || !strings.Contains(text, "더할 사이트가 없습니다") {
		t.Fatalf("이미 더한 사이트는 다시 더하지 않습니다: %q", text)
	}

	m.current.add(8, Job{ID: 22, Title: "개발 할 일 알림"}, false)
	s = runSession(t, m, "8")
	if text, isErr := callRun(t, s, "request_sites", map[string]any{"sites": []string{"github.com"}, "reason": "이슈 보기"}); isErr || !strings.Contains(text, "schedule_next") {
		t.Fatalf("브라우저가 없는 실행은 다음 실행을 잡으라고 해야 합니다: %q", text)
	}
	if len(g.sites) != 1 {
		t.Fatalf("브라우저가 없는 실행은 가드를 건드리지 않습니다: %v", g.sites)
	}
}
