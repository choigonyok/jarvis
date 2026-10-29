package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

type bgRunner struct{ bg bool }

func (r bgRunner) Send(string, []string) error { return nil }
func (r bgRunner) InBackground() bool          { return r.bg }

// While a background task drives the browser, a payment host is refused on
// the spot: no card is opened for a person who did not start anything.
func TestInterceptRefusesDuringBackground(t *testing.T) {
	props := proposal.NewStore(bus.New())
	s := &Server{
		runner:         bgRunner{bg: true},
		proposals:      props,
		interceptToken: "tok",
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodPost, "/intercept",
		strings.NewReader(`{"url":"https://pay.naver.com/pcpay","method":"GET","host":"pay.naver.com"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.postIntercept(rec, req)

	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["decision"] != string(proposal.Reject) {
		t.Fatalf("decision %q", body["decision"])
	}
	if n := len(props.List()); n != 0 {
		t.Fatalf("카드가 %d 장 열렸습니다", n)
	}

	// The sign-in host the operator allowed goes through, still without a card.
	s.backgroundLogin = hostSet([]string{"nid.naver.com"})
	req = httptest.NewRequest(http.MethodPost, "/intercept",
		strings.NewReader(`{"url":"https://nid.naver.com/nidlogin.login","method":"POST","host":"nid.naver.com"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	s.postIntercept(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["decision"] != string(proposal.Approve) || len(props.List()) != 0 {
		t.Fatalf("로그인 호스트: decision %q, cards %d", body["decision"], len(props.List()))
	}
	// ...and a payment host is still refused.
	req = httptest.NewRequest(http.MethodPost, "/intercept",
		strings.NewReader(`{"url":"https://pay.naver.com/pcpay","method":"POST","host":"pay.naver.com"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	s.postIntercept(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["decision"] != string(proposal.Reject) {
		t.Fatalf("결제 호스트가 통과했습니다: %q", body["decision"])
	}
}

type followUps struct {
	mu      sync.Mutex
	prompts []string
}

func (f *followUps) Enqueue(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, p)
}

// wait returns the prompts once there are n (decision hooks run on their own
// goroutines), or what there is after a second.
func (f *followUps) wait(n int) []string {
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		got := append([]string(nil), f.prompts...)
		f.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prompts...)
}

// 11: a payment request is refused at once with a card raised - nothing waits
// for the person. The same host asking again is the same card. Approving it
// opens the host for a retry and tells the assistant to take the step again.
func TestInterceptRaisesACardAndReturnsAtOnce(t *testing.T) {
	props := proposal.NewStore(bus.New())
	fu := &followUps{}
	s := New(Deps{Proposals: props, Runner: bgRunner{}, InterceptToken: "tok", FollowUp: fu,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	post := func(body string) map[string]string {
		req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	pay := `{"url":"https://pay.naver.com/pcpay/order","method":"POST","host":"pay.naver.com"}`
	first := post(pay)
	if first["decision"] != string(proposal.Reject) || first["pending"] == "" {
		t.Fatalf("카드를 올리고 바로 막아야 합니다: %v", first)
	}
	if again := post(pay); again["pending"] != first["pending"] || len(props.List()) != 1 {
		t.Fatalf("같은 호스트는 카드 하나: %v, cards %d", again, len(props.List()))
	}
	if err := props.Decide(first["pending"], proposal.Approve); err != nil {
		t.Fatal(err)
	}
	if got := fu.wait(1); len(got) != 1 || !strings.Contains(got[0], "결제 승인됨") {
		t.Fatalf("승인되면 다시 진행하라고 알려야 합니다: %v", got)
	}
	if retry := post(pay); retry["decision"] != string(proposal.Approve) {
		t.Fatalf("승인된 호스트의 재시도는 통과해야 합니다: %v", retry)
	}
	// Approval covers the site: the same payment crosses its hosts.
	if same := post(`{"url":"https://npay.naver.com/x","method":"POST","host":"npay.naver.com"}`); same["decision"] != string(proposal.Approve) {
		t.Fatalf("같은 사이트의 다른 호스트는 통과합니다: %v", same)
	}
	other := post(`{"url":"https://checkout.coupang.com/x/payment","method":"POST","host":"checkout.coupang.com"}`)
	if other["decision"] != string(proposal.Reject) || other["pending"] == "" {
		t.Fatalf("다른 사이트는 따로 묻습니다: %v", other)
	}
	if c := props.List(); c[len(c)-1].Card.Title != "쿠팡에서 쿠페이로 결제합니다" {
		t.Fatalf("쿠팡 카드: %+v", c[len(c)-1].Card)
	}
}

// switchRunner is a runner whose background state a test changes.
type switchRunner struct {
	mu sync.Mutex
	bg bool
}

func (r *switchRunner) Send(string, []string) error { return nil }
func (r *switchRunner) InBackground() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bg
}
func (r *switchRunner) set(bg bool) {
	r.mu.Lock()
	r.bg = bg
	r.mu.Unlock()
}

// fakeJobs is the jobs module as /intercept sees it.
type fakeJobs struct {
	mu      sync.Mutex
	id      int64
	title   string
	summary string
	raised  []string
	decided []string
}

func (f *fakeJobs) RunningJob() (int64, string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.id, f.title, f.summary, f.id != 0
}

func (f *fakeJobs) PaymentRaised(job int64, card string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raised = append(f.raised, fmt.Sprintf("%d %s", job, card))
}

func (f *fakeJobs) PaymentDecided(job int64, card string, approved bool, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decided = append(f.decided, fmt.Sprintf("%d %s %v %s", job, card, approved, text))
	return nil
}

func (f *fakeJobs) running(id int64) {
	f.mu.Lock()
	f.id = id
	f.mu.Unlock()
}

// wait returns the decisions once there are n (hooks run on their own
// goroutines), or what there is after a second.
func (f *fakeJobs) wait(n int) []string {
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		got := append([]string(nil), f.decided...)
		f.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.decided...)
}

// A job's run that reaches a payment raises a card for that job - not a
// refusal nobody is asked about (job 16, 2026-10-17: Coupang's order sheet
// failed with "주문중 오류" four times), and not the conversation's card.
// Its approval opens the site to that job alone.
func TestInterceptRaisesAJobsOwnCard(t *testing.T) {
	props := proposal.NewStore(bus.New())
	fu := &followUps{}
	jobs := &fakeJobs{id: 16, title: "탁상시계 구매", summary: "결제 직전 · 블랙 1개 · 34,350원 · 하나카드"}
	runner := &switchRunner{bg: true}
	s := New(Deps{Proposals: props, Runner: runner, InterceptToken: "tok", FollowUp: fu, Jobs: jobs,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	post := func(body string) map[string]string {
		req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	// The job is the agent's to name: a jobId in the request is ignored.
	order := `{"url":"https://checkout.coupang.com/api/node/checkout/events","method":"POST","host":"checkout.coupang.com","event":"order","jobId":99}`

	first := post(order)
	if first["decision"] != string(proposal.Reject) || first["pending"] == "" {
		t.Fatalf("작업의 결제는 카드를 올리고 막아야 합니다: %v", first)
	}
	cards := props.List()
	if len(cards) != 1 {
		t.Fatalf("카드 %d 장", len(cards))
	}
	if pay := paymentOf(cards[0].Action.Input); pay.JobID != 16 {
		t.Fatalf("카드는 실행 중인 작업 16 의 것이어야 합니다: %+v", pay)
	}
	for _, want := range []string{"탁상시계 구매", "34,350원", "결제하기(주문)"} {
		if !strings.Contains(cards[0].Card.Body, want) {
			t.Fatalf("카드에 %q 가 없습니다: %s", want, cards[0].Card.Body)
		}
	}
	if len(jobs.raised) != 1 || jobs.raised[0] != "16 "+first["pending"] {
		t.Fatalf("작업에 카드를 알려야 합니다: %v", jobs.raised)
	}
	if again := post(order); again["pending"] != first["pending"] || len(props.List()) != 1 {
		t.Fatalf("같은 결제는 카드 하나: %v", again)
	}

	if err := props.Decide(first["pending"], proposal.Approve); err != nil {
		t.Fatal(err)
	}
	if got := jobs.wait(1); len(got) != 1 || !strings.HasPrefix(got[0], "16 "+first["pending"]+" true [결제 승인됨]") {
		t.Fatalf("승인은 작업에 가야 합니다: %v", got)
	}
	if got := fu.wait(1); len(got) != 0 {
		t.Fatalf("작업의 카드를 대화에 알렸습니다: %v", got)
	}
	if retry := post(order); retry["decision"] != string(proposal.Approve) {
		t.Fatalf("승인된 작업의 재시도는 통과해야 합니다: %v", retry)
	}

	// The job's approval is not the conversation's...
	runner.set(false)
	if conv := post(order); conv["decision"] != string(proposal.Reject) {
		t.Fatalf("작업의 승인으로 대화의 결제가 통과했습니다: %v", conv)
	}
	// ...nor another job's.
	runner.set(true)
	jobs.running(17)
	if other := post(order); other["decision"] != string(proposal.Reject) {
		t.Fatalf("작업 16 의 승인으로 작업 17 의 결제가 통과했습니다: %v", other)
	}
}

// A background task that is not a job (an order lookup, a login check) still
// never pays and never asks.
func TestInterceptRefusesABackgroundTaskThatIsNotAJob(t *testing.T) {
	props := proposal.NewStore(bus.New())
	s := New(Deps{Proposals: props, Runner: &switchRunner{bg: true}, InterceptToken: "tok", Jobs: &fakeJobs{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	req := httptest.NewRequest(http.MethodPost, "/intercept",
		strings.NewReader(`{"url":"https://checkout.coupang.com/api/node/checkout/events","method":"POST","host":"checkout.coupang.com","event":"order"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.postIntercept(rec, req)
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["decision"] != string(proposal.Reject) || body["pending"] != "" || len(props.List()) != 0 {
		t.Fatalf("작업이 아닌 백그라운드는 묻지 않고 거부: %v, cards %d", body, len(props.List()))
	}
}

// Turned down, the job is told not to pay and nothing opens.
func TestInterceptRejectedJobCard(t *testing.T) {
	props := proposal.NewStore(bus.New())
	jobs := &fakeJobs{id: 16, title: "탁상시계 구매"}
	s := New(Deps{Proposals: props, Runner: &switchRunner{bg: true}, InterceptToken: "tok", Jobs: jobs,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	post := func() map[string]string {
		req := httptest.NewRequest(http.MethodPost, "/intercept",
			strings.NewReader(`{"url":"https://checkout.coupang.com/api/node/checkout/events","method":"POST","host":"checkout.coupang.com","event":"order"}`))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	first := post()
	if err := props.DecideWhy(first["pending"], proposal.Reject, "안 살래"); err != nil {
		t.Fatal(err)
	}
	if got := jobs.wait(1); len(got) != 1 || !strings.Contains(got[0], "false [결제 반려됨]") || !strings.Contains(got[0], "안 살래") {
		t.Fatalf("반려는 작업에 이유와 함께: %v", got)
	}
	if again := post(); again["decision"] != string(proposal.Reject) || again["pending"] == first["pending"] {
		t.Fatalf("반려 뒤의 결제는 새 카드로 다시 묻습니다: %v", again)
	}
}

func TestGuard(t *testing.T) {
	reached := false
	next := func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}

	cases := []struct {
		name   string
		token  string
		header string
		want   int
	}{
		{"unset token lets everything through", "", "", http.StatusOK},
		{"correct token passes", "s3cret", "Bearer s3cret", http.StatusOK},
		{"missing header is refused", "s3cret", "", http.StatusUnauthorized},
		{"wrong token is refused", "s3cret", "Bearer nope", http.StatusUnauthorized},
		{"bare token without the scheme is refused", "s3cret", "s3cret", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			s := &Server{apiToken: tc.token}
			req := httptest.NewRequest(http.MethodGet, "/thread", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			s.guard(next)(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := reached; got != (tc.want == http.StatusOK) {
				t.Fatalf("handler reached = %v, want %v", got, tc.want == http.StatusOK)
			}
		})
	}
}

// editor is a module whose card is a form: it needs a "price" before it may
// run, and an edit replaces the input.
type editor struct{}

func (editor) Name() string                { return "shop" }
func (editor) Start(context.Context) error { return nil }
func (editor) Stop(context.Context) error  { return nil }
func (editor) Specs() []action.Spec        { return []action.Spec{{Kind: "shop.sell"}} }
func (editor) Preview(context.Context, action.Action) (action.Card, error) {
	return action.Card{Title: "sell"}, nil
}
func (editor) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{}, nil
}
func (editor) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	if edits["price"] == "" {
		return a, action.Card{}, errors.New("가격을 적어 주세요.")
	}
	raw, _ := json.Marshal(map[string]string{"price": edits["price"]})
	return action.Action{Kind: a.Kind, Input: raw}, action.Card{Title: "sell", Fields: []action.Field{{Key: "price"}}}, nil
}

// Approving a form card goes through the module: a missing value stops the
// approval, and an edited value is what the proposal then carries.
func TestDecisionWithEdits(t *testing.T) {
	props := proposal.NewStore(bus.New())
	reg := module.NewRegistry()
	reg.Add(editor{})
	s := &Server{proposals: props, modules: reg, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, _ := props.Open(proposal.Proposal{
		Action: action.Action{Kind: "shop.sell", Input: json.RawMessage(`{}`)},
		Card:   action.Card{Title: "sell", Fields: []action.Field{{Key: "price"}}},
	})

	decide := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/proposals/"+p.ID+"/decision", strings.NewReader(body))
		req.SetPathValue("id", p.ID)
		rec := httptest.NewRecorder()
		s.postDecision(rec, req)
		return rec.Code
	}

	if code := decide(`{"decision":"approved"}`); code != http.StatusBadRequest {
		t.Fatalf("approval without a price: %d", code)
	}
	if got, _ := props.Get(p.ID); got.State != proposal.Pending {
		t.Fatalf("state after refused approval: %s", got.State)
	}
	if code := decide(`{"decision":"approved","edits":{"price":"9000"}}`); code != http.StatusNoContent {
		t.Fatalf("approval with a price: %d", code)
	}
	got, _ := props.Get(p.ID)
	if got.State != proposal.Approved || string(got.Action.Input) != `{"price":"9000"}` {
		t.Fatalf("after approval: %s %s", got.State, got.Action.Input)
	}
}

// /mcp and everything under it answer only the CLI holding the internal
// token; the console's API token does not open it.
func TestMCPGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	s := New(Deps{MCP: map[string]http.Handler{"/mcp": ok, "/mcp/suggest": ok}, MCPToken: "inner", APIToken: "outer"})
	h := s.Handler()
	for _, tc := range []struct {
		path, header string
		want         int
	}{
		{"/mcp", "", http.StatusUnauthorized},
		{"/mcp/suggest", "", http.StatusUnauthorized},
		{"/mcp/suggest", "Bearer outer", http.StatusUnauthorized},
		{"/mcp/suggest/x", "Bearer inner", http.StatusOK},
		{"/mcp", "Bearer inner", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %q: %d, want %d", tc.path, tc.header, rec.Code, tc.want)
		}
	}
}

func TestSiteOf(t *testing.T) {
	for host, want := range map[string]string{
		"checkout.coupang.com": "coupang.com", "pay.naver.com": "naver.com", "new-m.pay.naver.com": "naver.com",
		"shop.co.kr": "shop.co.kr", "m.shop.co.kr": "shop.co.kr", "localhost": "localhost",
	} {
		if got := siteOf(host); got != want {
			t.Errorf("siteOf(%q) = %q, want %q", host, got, want)
		}
	}
}

// One approval is one payment, whoever the payer: its window opens when the
// payer is back at the payment (a job may start long after the card was
// answered), runs paymentWindow from there, and lets one 결제하기 through - a
// second payment on the same site gets a card of its own. The browser's
// guard keeps no window of its own, so this is the whole of it.
func TestAnApprovalIsOnePayment(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	g := &paymentGrants{now: func() time.Time { return now }}
	g.grant("coupang.com", 16, "")

	now = now.Add(40 * time.Minute) // the job starts when the conversation is idle
	if !g.allow("coupang.com", 16, "order", "") {
		t.Fatal("돌아온 결제자의 결제하기는 통과합니다")
	}
	if !g.allow("coupang.com", 16, "", "") {
		t.Fatal("같은 결제의 비밀번호 단계는 통과합니다")
	}
	if g.allow("coupang.com", 16, "order", "") {
		t.Fatal("두 번째 결제하기는 새 카드를 올려야 합니다")
	}
	if g.allow("coupang.com", 0, "", "") || g.allow("coupang.com", 17, "", "") {
		t.Fatal("다른 결제자에게는 열리지 않습니다")
	}
	now = now.Add(paymentWindow + time.Second)
	if g.allow("coupang.com", 16, "", "") {
		t.Fatal("돌아온 때부터 창이 지나면 닫힙니다")
	}

	g.grant("naver.com", 0, "")
	now = now.Add(paymentWait + time.Second)
	if g.allow("naver.com", 0, "", "") {
		t.Fatal("돌아오지 않은 승인은 기다림이 지나면 닫힙니다")
	}
}

// Through /intercept: after one approved 결제하기, the next one on the site
// raises a new card instead of going through.
func TestASecondPaymentRaisesItsOwnCard(t *testing.T) {
	props := proposal.NewStore(bus.New())
	fu := &followUps{}
	s := New(Deps{Proposals: props, Runner: bgRunner{}, InterceptToken: "tok", FollowUp: fu,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	post := func(body string) map[string]string {
		req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	order := `{"url":"https://checkout.coupang.com/api/node/checkout/events","method":"POST","host":"checkout.coupang.com","event":"order"}`
	first := post(order)
	if err := props.Decide(first["pending"], proposal.Approve); err != nil {
		t.Fatal(err)
	}
	if got := fu.wait(1); len(got) != 1 || !strings.Contains(got[0], "결제 한 건") || strings.Contains(got[0], "다시 묻지 않습니다") {
		t.Fatalf("승인이 무엇을 여는지 그대로 말해야 합니다: %v", got)
	}
	if p := post(order); p["decision"] != string(proposal.Approve) {
		t.Fatalf("승인한 결제하기: %v", p)
	}
	if pin := post(`{"url":"https://rocketpay.coupang.com/rocketpay/pay/authentication","method":"POST","host":"rocketpay.coupang.com"}`); pin["decision"] != string(proposal.Approve) {
		t.Fatalf("같은 결제의 키패드: %v", pin)
	}
	second := post(order)
	if second["decision"] != string(proposal.Reject) || second["pending"] == "" || second["pending"] == first["pending"] {
		t.Fatalf("두 번째 결제는 새 카드: %v", second)
	}
	if c := props.List(); !strings.Contains(c[len(c)-1].Card.Consequence, "결제 한 건") {
		t.Fatalf("카드의 결과 문구: %q", c[len(c)-1].Card.Consequence)
	}
}

// A mall whose only method is 신용카드 opens a PG's payment window, and the
// 네이버페이 is picked inside it (장범준 LP, 2026-10-29: the guard blocked
// POST stdpay.inicis.com/payMain/pay and the window came up blank). One card
// stands before the window opens and shows what the window was handed; its
// approval covers the window, whichever PG's host, and the 네이버페이 inside
// it - only while the window is open, and only for that payer.
func TestAPaymentWindowIsOneCard(t *testing.T) {
	props := proposal.NewStore(bus.New())
	jobs := &fakeJobs{id: 1, title: "장범준 2집·3집 LP 재발매 구매", summary: "결제 직전 · 2집 LP · 1 · 64,000원 · 네이버페이(결제창)"}
	runner := &switchRunner{bg: true}
	s := New(Deps{Proposals: props, Runner: runner, InterceptToken: "tok", FollowUp: &followUps{}, Jobs: jobs,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	now := time.Date(2026, 10, 29, 9, 31, 0, 0, time.UTC)
	s.payments.now = func() time.Time { return now }
	post := func(body string) map[string]string {
		req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	window := `{"url":"https://stdpay.inicis.com/payMain/pay","method":"POST","host":"stdpay.inicis.com","window":true,"amount":"64000","item":"장범준 2집 LP"}`
	naverPay := `{"url":"https://pay.naver.com/payments/abc","method":"POST","host":"pay.naver.com"}`

	first := post(window)
	if first["decision"] != string(proposal.Reject) || first["pending"] == "" {
		t.Fatalf("결제창을 여는 요청은 카드를 올리고 막아야 합니다: %v", first)
	}
	cards := props.List()
	if len(cards) != 1 || cards[0].Card.Title != "결제창에서 결제합니다" {
		t.Fatalf("결제창 카드: %+v", cards)
	}
	for _, want := range []string{"결제창이 받은 금액: 64,000원", "결제창이 받은 상품: 장범준 2집 LP", "stdpay.inicis.com", "작업: 장범준"} {
		if !strings.Contains(cards[0].Card.Body, want) {
			t.Fatalf("카드에 %q 가 없습니다: %s", want, cards[0].Card.Body)
		}
	}
	if c := cards[0].Card.Consequence; !strings.Contains(c, "결제 한 건(64,000원)") || !strings.Contains(c, "네이버페이·신용카드") {
		t.Fatalf("카드의 결과 문구: %q", c)
	}
	// The same payment passing through another PG's host is the same card.
	again := post(`{"url":"https://service.iamport.kr/inicis_payments/prepare","method":"POST","host":"service.iamport.kr","window":true}`)
	if again["pending"] != first["pending"] || len(props.List()) != 1 {
		t.Fatalf("같은 결제의 다른 결제창 호스트는 같은 카드: %v, cards %d", again, len(props.List()))
	}

	if err := props.Decide(first["pending"], proposal.Approve); err != nil {
		t.Fatal(err)
	}
	// 2026-10-30: twice the job went straight from the 네이버페이 PIN to the
	// card company's 결제완료 and got only '네이버페이 인증을 완료하여
	// 주십시오' - the result reaches that window only from the 확인 on the
	// card company's result page in the 네이버페이 window.
	if got := jobs.wait(1); len(got) != 1 || !strings.Contains(got[0], "결제창이 열립니다") || !strings.Contains(got[0], "신용카드") ||
		!strings.Contains(got[0], "하나카드") || !strings.Contains(got[0], "카드사 앱 결제") || !strings.Contains(got[0], "다른 간편결제") ||
		!strings.Contains(got[0], "카드사 창에서는 간편결제의 네이버페이") || !strings.Contains(got[0], "인증 결과 화면에서 확인을 눌러야") {
		t.Fatalf("승인은 결제창을 다시 열고, 카드사 창에서는 네이버페이로, 비밀번호 뒤에는 그 확인을 누르라고 작업에 가야 합니다: %v", got)
	}
	if open := post(window); open["decision"] != string(proposal.Approve) {
		t.Fatalf("승인된 결제창이 열려야 합니다: %v", open)
	}
	now = now.Add(2 * time.Minute)
	if inside := post(`{"url":"https://stdux.inicis.com/x","method":"POST","host":"stdux.inicis.com","window":true}`); inside["decision"] != string(proposal.Approve) {
		t.Fatalf("결제창 안의 다른 요청: %v", inside)
	}
	if pay := post(naverPay); pay["decision"] != string(proposal.Approve) {
		t.Fatalf("결제창 안에서 고른 네이버페이는 같은 카드로 통과합니다: %v", pay)
	}
	// Another order opening the window in the same minutes (3집, another
	// amount) is another payment: a card of its own.
	third := post(`{"url":"https://stdpay.inicis.com/payMain/pay","method":"POST","host":"stdpay.inicis.com","window":true,"amount":"54000","item":"장범준 3집 LP"}`)
	if third["decision"] != string(proposal.Reject) || third["pending"] == "" || third["pending"] == first["pending"] {
		t.Fatalf("금액이 다른 주문의 결제창은 새 카드: %v", third)
	}
	if coupang := post(`{"url":"https://checkout.coupang.com/api/node/checkout/events","method":"POST","host":"checkout.coupang.com","event":"order"}`); coupang["decision"] != string(proposal.Reject) {
		t.Fatalf("결제창의 승인으로 다른 사이트의 결제가 통과했습니다: %v", coupang)
	}
	runner.set(false)
	if conv := post(naverPay); conv["decision"] != string(proposal.Reject) {
		t.Fatalf("작업의 결제창 승인으로 대화의 네이버페이가 통과했습니다: %v", conv)
	}
	runner.set(true)
	now = now.Add(paymentWindow)
	if late := post(naverPay); late["decision"] != string(proposal.Reject) {
		t.Fatalf("결제창의 시간이 지나면 네이버페이도 닫힙니다: %v", late)
	}
}

// The 네이버페이 inside a window goes through while that window is under
// way: not before the payer is back at it (an approval waiting for a job to
// start opens nothing), and not after its time.
func TestAWindowIsOpenOnlyWhileItsPaymentIs(t *testing.T) {
	now := time.Date(2026, 10, 29, 9, 31, 0, 0, time.UTC)
	g := &paymentGrants{now: func() time.Time { return now }}
	g.grant(windowSite, 1, "64000")
	if g.isOpen(windowSite, 1) {
		t.Fatal("결제자가 결제창으로 돌아오기 전에는 열려 있지 않습니다")
	}
	if g.allow(windowSite, 1, "", "54000") || g.isOpen(windowSite, 1) {
		t.Fatal("다른 금액의 결제창은 승인을 쓰지도 열지도 않습니다")
	}
	if !g.allow(windowSite, 1, "", "64000") || !g.allow(windowSite, 1, "", "") {
		t.Fatal("돌아온 결제자의 그 금액 결제창과, 금액이 없는 결제창 안의 요청은 통과합니다")
	}
	if !g.isOpen(windowSite, 1) || g.isOpen(windowSite, 0) || g.isOpen(windowSite, 2) {
		t.Fatal("열린 결제창은 그 결제자의 것입니다")
	}
	now = now.Add(paymentWindow + time.Second)
	if g.isOpen(windowSite, 1) {
		t.Fatal("시간이 지난 결제창은 닫혀 있습니다")
	}
	if won("64000") != "64,000원" || won("900") != "900원" || won("1234567") != "1,234,567원" {
		t.Fatalf("금액 표기: %s %s %s", won("64000"), won("900"), won("1234567"))
	}
}
