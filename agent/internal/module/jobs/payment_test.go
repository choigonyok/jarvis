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
)

// jobsSvc stands in for jobs-svc: it keeps what the run tools sent.
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
// wait_payment and waits on it; the decision reaches the job as a card answer
// (job 16, 2026-10-17: the payment was refused with no card, four times).
func TestPaymentCardParksTheRunAndTheDecisionWakesIt(t *testing.T) {
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

	m.PaymentRaised(16, "p-1")
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

	if err := m.PaymentDecided(16, "p-1", true, "[결제 승인됨] 운영자가 승인했습니다."); err != nil {
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

// A run that asked the operator does not end the job in the same breath:
// the answer would have nowhere to go (job 18, 2026-10-18). Once the card is
// decided, it may.
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
	if text, isErr := callRun(t, s, "finish", map[string]any{"summary": "멈춤", "notify": false}); !isErr || !strings.Contains(text, card.ID) {
		t.Fatalf("카드를 올린 실행은 끝내지 못해야 합니다: %q", text)
	}
	if contains(svc.got(), "/runs/5/finish") {
		t.Fatal("작업을 끝냈습니다")
	}
	m.decided(card, proposal.Approve)
	if _, isErr := callRun(t, s, "finish", map[string]any{"summary": "답을 받고 끝냄", "notify": false}); isErr {
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
	m.PaymentRaised(16, card.ID)
	if _, isErr := callRun(t, s, "finish", map[string]any{"summary": "사지 않기로 했습니다", "notify": false}); isErr {
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
