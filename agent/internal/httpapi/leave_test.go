package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/holder"
)

// The way back of a musinsa order: the payment window said 0000,
// and the page that makes the order was outside the job's sites.
const musinsaBack = "https://pay.musinsapayments.com/mps/payment/PP5TESTPAYMENT0001?resultCode=0000&resultMessage=%EC%A0%95%EC%83%81"

func leaveServer(t *testing.T, jobs *fakeJobs, bg bool) (*Server, *proposal.Store, string) {
	t.Helper()
	props := proposal.NewStore(bus.New())
	path := filepath.Join(t.TempDir(), "flow-sites.json")
	s := New(Deps{Proposals: props, Runner: &switchRunner{bg: bg}, InterceptToken: "tok", Jobs: jobs, FlowSitesPath: path,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return s, props, path
}

func postLeave(s *Server, body string) map[string]string {
	req := httptest.NewRequest(http.MethodPost, "/intercept/site", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.postSite(rec, req)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func leaveBody(url, from string) string {
	b, _ := json.Marshal(Leave{URL: url, Method: "GET", Host: strings.SplitN(strings.TrimPrefix(url, "https://"), "/", 2)[0], From: from})
	return string(b)
}

// paying opens an approved payment of job on site, the way its payment
// request going through does.
func paying(s *Server, site string, job int64) {
	s.payments.Grant(site, job, "")
	s.payments.Allow(site, job, "", "")
}

// waitFor polls cond for a second.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(what)
}

// While the job pays, its page on the way back is held on a flow card;
// approved in time, the page goes on, the site joins the payment flow for
// good, and the next page there - after a restart too - goes through
// without a card.
func TestAPayingJobsWayBackIsHeldOnAFlowCard(t *testing.T) {
	jobs := &fakeJobs{id: 137, title: "무신사 방한화 구매"}
	s, props, path := leaveServer(t, jobs, true)
	s.holdDur = 5 * time.Second
	paying(s, "musinsa.com", 137)

	got := make(chan map[string]string, 1)
	go func() { got <- postLeave(s, leaveBody(musinsaBack, "order.musinsa.com/order/sheet")) }()
	waitFor(t, "결제 흐름 카드가 올라가야 합니다", func() bool { return len(props.List()) == 1 && s.payments.Holding() == 1 })

	card := props.List()[0]
	if card.Action.Kind != holder.KindFlow || card.Origin != proposal.OriginIntercept {
		t.Fatalf("카드 종류: %s %s", card.Action.Kind, card.Origin)
	}
	for _, want := range []string{"musinsapayments.com"} {
		if !strings.Contains(card.Card.Title, want) {
			t.Fatalf("제목에 %q: %q", want, card.Card.Title)
		}
	}
	for _, want := range []string{"작업: 무신사 방한화 구매", "진행 중인 결제: musinsa.com",
		"막힌 이동: order.musinsa.com/order/sheet → pay.musinsapayments.com/mps/payment/PP5TESTPAYMENT0001?resultCode=0000&resultMessage",
		"더하는 곳: musinsapayments.com 아래 모든 주소"} {
		if !strings.Contains(card.Card.Body, want) {
			t.Fatalf("본문에 %q 가 없습니다:\n%s", want, card.Card.Body)
		}
	}
	if strings.Contains(card.Card.Body, "%EC") {
		t.Fatalf("주소의 값은 카드에 남기지 않습니다:\n%s", card.Card.Body)
	}
	jobs.mu.Lock()
	flows, stopped := append([]string(nil), jobs.flows...), append([]string(nil), jobs.stopped...)
	jobs.mu.Unlock()
	if len(flows) != 1 || flows[0] != "137 "+card.ID+" musinsapayments.com" {
		t.Fatalf("작업이 카드를 기다리게 해야 합니다: %v", flows)
	}
	if len(stopped) != 1 || !strings.Contains(stopped[0], "wait_payment") {
		t.Fatalf("실행에 기다리라고 알려야 합니다: %v", stopped)
	}

	if err := props.Decide(card.ID, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	out := <-got
	if out["decision"] != string(proposal.Approve) || out["why"] != "held" || out["card"] != card.ID {
		t.Fatalf("승인되면 붙잡은 이동이 나가야 합니다: %v", out)
	}
	if d := jobs.wait(1); len(d) != 1 || !strings.Contains(d[0], "[결제 흐름 승인됨]") || !strings.Contains(d[0], "그대로 나갔습니다") {
		t.Fatalf("작업은 이동이 나갔다고 들어야 합니다: %v", d)
	}
	waitFor(t, "카드가 끝나야 합니다", func() bool { p, _ := props.Get(card.ID); return p.State == proposal.Executed })

	// The next page there goes through, with no card.
	if again := postLeave(s, leaveBody("https://pay.musinsapayments.com/mps/complete", "")); again["decision"] != string(proposal.Approve) || again["why"] != "flow" {
		t.Fatalf("결제 흐름에 더한 곳은 그냥 열립니다: %v", again)
	}
	if n := len(props.List()); n != 1 {
		t.Fatalf("카드가 %d 장", n)
	}
	// Kept on disk: a restart remembers it.
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `"musinsapayments.com"`) {
		t.Fatalf("저장된 결제 흐름: %s %v", b, err)
	}
	again, _, _ := leaveServer(t, &fakeJobs{}, true)
	again.flows, _ = openFlowSites(path)
	if out := postLeave(again, leaveBody("https://pay.musinsapayments.com/x", "")); out["why"] != "flow" {
		t.Fatalf("다시 시작해도 열립니다: %v", out)
	}
}

// Held until its card ran out of time, the page is refused with the card
// still waiting; approved later, the site joins all the same and the job
// hears that its payment may not have gone through.
func TestALateFlowApprovalTellsTheJobToLookAtItsOrders(t *testing.T) {
	jobs := &fakeJobs{id: 137, title: "무신사 구매"}
	s, props, _ := leaveServer(t, jobs, true)
	s.holdDur = 0
	paying(s, "musinsa.com", 137)

	out := postLeave(s, leaveBody(musinsaBack, ""))
	if out["decision"] != string(proposal.Reject) || out["why"] != "waiting" || out["pending"] == "" {
		t.Fatalf("카드가 기다리는 채로 막아야 합니다: %v", out)
	}
	if again := postLeave(s, leaveBody(musinsaBack, "")); again["pending"] != out["pending"] || len(props.List()) != 1 {
		t.Fatalf("같은 곳은 카드 하나: %v, cards %d", again, len(props.List()))
	}
	if err := props.Decide(out["pending"], proposal.Approve); err != nil {
		t.Fatal(err)
	}
	if d := jobs.wait(1); len(d) != 1 || !strings.Contains(d[0], "주문 내역") || strings.Contains(d[0], "그대로 나갔습니다") {
		t.Fatalf("작업은 주문 내역부터 보라고 들어야 합니다: %v", d)
	}
	waitFor(t, "결제 흐름에 더해야 합니다", func() bool { return s.flows.Has("musinsapayments.com") })
}

// Turned down, the page is refused as turned down, and the site stays out.
func TestATurnedDownFlowCardRefusesThePage(t *testing.T) {
	jobs := &fakeJobs{id: 137, title: "무신사 구매"}
	s, props, _ := leaveServer(t, jobs, true)
	s.holdDur = 5 * time.Second
	paying(s, "musinsa.com", 137)

	got := make(chan map[string]string, 1)
	go func() { got <- postLeave(s, leaveBody(musinsaBack, "")) }()
	waitFor(t, "카드가 올라가야 합니다", func() bool { return len(props.List()) == 1 && s.payments.Holding() == 1 })
	if err := props.DecideWhy(props.List()[0].ID, proposal.Reject, "이상한 주소"); err != nil {
		t.Fatal(err)
	}
	out := <-got
	if out["decision"] != string(proposal.Reject) || out["why"] != "turned" || out["pending"] != "" {
		t.Fatalf("반려되면 반려로 막아야 합니다: %v", out)
	}
	if d := jobs.wait(1); len(d) != 1 || !strings.Contains(d[0], "[결제 흐름 반려됨]") || !strings.Contains(d[0], "이상한 주소") {
		t.Fatalf("작업은 반려를 들어야 합니다: %v", d)
	}
	if s.flows.Has("musinsapayments.com") {
		t.Fatal("반려된 곳을 더했습니다")
	}
}

// A page that has nothing of a payment about it is refused as the guard
// always did, with no card - and the run is told, with how to go on.
func TestALeaveThatIsNoPaymentIsRefusedAndTold(t *testing.T) {
	jobs := &fakeJobs{id: 134, title: "배송 시작 확인"}
	s, props, _ := leaveServer(t, jobs, true)
	s.holdDur = 5 * time.Second

	out := postLeave(s, leaveBody("https://view.queryship.com/track/?invoice=123456", "www.29cm.co.kr/order"))
	if out["decision"] != string(proposal.Reject) || out["why"] != "site" || out["pending"] != "" {
		t.Fatalf("그냥 막아야 합니다: %v", out)
	}
	if n := len(props.List()); n != 0 {
		t.Fatalf("카드가 %d 장 열렸습니다", n)
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if len(jobs.stopped) != 1 || !strings.Contains(jobs.stopped[0], "view.queryship.com/track/?invoice") ||
		!strings.Contains(jobs.stopped[0], "request_sites") || strings.Contains(jobs.stopped[0], "123456") {
		t.Fatalf("실행에 막은 것을 값 없이 알려야 합니다: %v", jobs.stopped)
	}
}

// Before any payment card, a page whose address reads as a payment's - or
// that a payment window opened - is asked about too: a mall whose payment
// page is its payment company's.
func TestAPaymentLookingLeaveIsAskedBeforeAnyPayment(t *testing.T) {
	jobs := &fakeJobs{id: 121, title: "장바구니 구매"}
	s, props, _ := leaveServer(t, jobs, true)
	s.holdDur = 0

	out := postLeave(s, leaveBody("https://pay.musinsapayments.com/mps/payment/PP5TESTPAYMENT0002", "www.29cm.co.kr/order/checkout"))
	if out["why"] != "waiting" || out["pending"] == "" {
		t.Fatalf("결제 페이지로 보이면 카드로 묻습니다: %v", out)
	}
	if body := props.List()[0].Card.Body; !strings.Contains(body, "물은 까닭: 결제 페이지로 보이는 주소입니다") || strings.Contains(body, "진행 중인 결제") {
		t.Fatalf("왜 물었는지: %s", body)
	}

	b, _ := json.Marshal(Leave{URL: "https://auth.example.com/verify", Method: "POST", Host: "auth.example.com", FromWindow: true})
	if out := postLeave(s, string(b)); out["why"] != "waiting" {
		t.Fatalf("결제창이 연 창이면 묻습니다: %v", out)
	}
	if n := len(props.List()); n != 2 {
		t.Fatalf("카드 %d 장", n)
	}
}

// With no job's run behind the page - a background sign-in, a run already
// over - the refusal stands, with no card and nobody told.
func TestALeaveOfNoJobIsRefusedQuietly(t *testing.T) {
	jobs := &fakeJobs{}
	s, props, _ := leaveServer(t, jobs, true)
	paying(s, "musinsa.com", 0)
	if out := postLeave(s, leaveBody(musinsaBack, "")); out["decision"] != string(proposal.Reject) || out["why"] != "site" {
		t.Fatalf("작업이 없으면 막습니다: %v", out)
	}
	s2, props2, _ := leaveServer(t, &fakeJobs{id: 9}, false)
	if out := postLeave(s2, leaveBody(musinsaBack, "")); out["why"] != "site" {
		t.Fatalf("대화가 브라우저를 쥐고 있으면 작업의 실행이 아닙니다: %v", out)
	}
	if len(props.List())+len(props2.List()) != 0 || len(jobs.stopped) != 0 {
		t.Fatal("카드도 알림도 없어야 합니다")
	}
	req := httptest.NewRequest(http.MethodPost, "/intercept/site", strings.NewReader(leaveBody(musinsaBack, "")))
	req.Header.Set("Authorization", "Bearer nope")
	rec := httptest.NewRecorder()
	s.postSite(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %d", rec.Code)
	}
}

// A flow card and a payment card are not one another's: a payment of the
// same job on the flow's site raises its own card, and a decision on the flow
// card is not a payment's approval.
func TestAFlowCardIsNoPaymentCard(t *testing.T) {
	jobs := &fakeJobs{id: 137, title: "무신사 구매"}
	s, props, _ := leaveServer(t, jobs, true)
	s.holdDur = 0
	paying(s, "musinsa.com", 137)
	flow := postLeave(s, leaveBody(musinsaBack, ""))["pending"]

	req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(
		`{"url":"https://pay.musinsapayments.com/mps/api/approve","method":"POST","host":"pay.musinsapayments.com"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.postIntercept(rec, req)
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["pending"] == "" || out["pending"] == flow {
		t.Fatalf("결제는 따로 카드를 올려야 합니다: flow %s, payment %v", flow, out)
	}
	if err := props.Decide(flow, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	jobs.wait(1)
	if s.payments.Allow("musinsapayments.com", 137, "", "") {
		t.Fatal("결제 흐름 승인이 결제를 열었습니다")
	}
}

func TestPaymentLike(t *testing.T) {
	for url, want := range map[string]bool{
		"https://pay.musinsapayments.com/mps/payment/PP5":   true,
		"https://shop.example.com/order/payment/result":     true,
		"https://shop.example.com/checkout/complete":        true,
		"https://shop.example.com/return?resultCode=0000":   true,
		"https://view.queryship.com/track/":                 false,
		"https://safe.ok-name.co.kr/CommonSvl":              false,
		"https://member.one.musinsa.com/login?entryToken=x": false,
		"https://www.display.com/play/list":                 false,
		"not a url\x7f":                                     false,
	} {
		if got := paymentLike(url); got != want {
			t.Errorf("%s: %v", url, got)
		}
	}
}

// The flow sites read back what was written, and a file that cannot be read
// starts them empty - never a page let through unasked.
func TestFlowSitesAreKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow-sites.json")
	f, err := openFlowSites(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(flowSite{Site: " MusinsaPayments.com ", Card: "p-1", Job: 137}); err != nil {
		t.Fatal(err)
	}
	if err := f.Add(flowSite{Site: ""}); err == nil {
		t.Fatal("빈 사이트는 더하지 않습니다")
	}
	g, err := openFlowSites(path)
	if err != nil || !g.Has("musinsapayments.com") || g.Has("musinsa.com") || len(g.List()) != 1 || g.List()[0].Card != "p-1" {
		t.Fatalf("다시 읽기: %+v %v", g.List(), err)
	}
	if err := os.WriteFile(path, []byte("{깨짐"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := openFlowSites(path); err == nil || h.Has("musinsapayments.com") {
		t.Fatal("읽지 못한 파일은 오류이고 비어 있어야 합니다")
	}
	var none *flowSites
	if none.Has("x.com") || none.Add(flowSite{Site: "x.com"}) == nil {
		t.Fatal("없는 저장소")
	}
}

// What the guard asked and what came of it is one record, without the
// address's values.
func TestALeaveIsRecorded(t *testing.T) {
	jobs := &fakeJobs{id: 134}
	s, mem := recordingServer(t, Deps{Proposals: proposal.NewStore(bus.New()), Runner: &switchRunner{bg: true}, Jobs: jobs})
	postLeave(s, leaveBody("https://view.queryship.com/track/?invoice=123456", "www.29cm.co.kr/order"))
	r, ok := mem.Find(kindIs(KindGuardLeave))
	if !ok {
		t.Fatal("기록이 남아야 합니다")
	}
	body := r.Payload.(map[string]any)
	if body["why"] != "site" || body["decision"] != "rejected" || body["from"] != "www.29cm.co.kr/order" || r.Subject != "queryship.com" || r.TraceID != "job:134" {
		t.Fatalf("기록: %+v", r)
	}
	if b, _ := json.Marshal(r.Payload); strings.Contains(string(b), "123456") {
		t.Fatalf("주소의 값이 남았습니다: %s", b)
	}
}
