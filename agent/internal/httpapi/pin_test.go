package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/holder"
)

// pinServer is /intercept as the PIN tool asks it: jobs, a switchable runner,
// and no hold unless a test sets one.
func pinServer(t *testing.T, jobs *fakeJobs, runner *switchRunner) (*Server, *proposal.Store, *followUps, func(string) map[string]string) {
	t.Helper()
	props := proposal.NewStore(bus.New())
	fu := &followUps{}
	s := New(Deps{Proposals: props, Runner: runner, InterceptToken: "tok", FollowUp: fu, Jobs: jobs,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.holdDur = 0
	post := func(body string) map[string]string {
		req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	return s, props, fu, post
}

// What the PIN tool sends: the keypad's host and its address with the values
// left out.
const couponPIN = `{"url":"https://pay.naver.com/authentication/pw/check?action&couponNo&rurl&sessionKey&surl","method":"PIN","host":"pay.naver.com","pin":true}`

// A coupon asks for the 네이버페이 password before anything the guard stops:
// with no approved payment, the PIN tool's question raises the job's own card
// for the password, and its approval lets the password in and what the
// password sends - the password check, the coupon - through for that job.
func TestAPasswordAskedFirstRaisesItsOwnCard(t *testing.T) {
	jobs := &fakeJobs{id: 165, title: "네이버페이 포인트 쿠폰 등록", summary: "첫 번째 쿠폰을 넣고 등록하기를 누르자 비밀번호 키패드가 떴습니다"}
	runner := &switchRunner{bg: true}
	_, props, fu, post := pinServer(t, jobs, runner)

	first := post(couponPIN)
	if first["decision"] != string(proposal.Reject) || first["pending"] == "" || first["why"] != "waiting" {
		t.Fatalf("승인 없이 넣지 않고 카드를 올려야 합니다: %v", first)
	}
	cards := props.List()
	if len(cards) != 1 {
		t.Fatalf("카드 %d 장", len(cards))
	}
	card := cards[0]
	if pay := paymentOf(card.Action.Input); pay.JobID != 165 || !pay.PIN {
		t.Fatalf("작업 165 의 비밀번호 카드여야 합니다: %+v", pay)
	}
	if card.Card.Title != "네이버페이 비밀번호를 넣습니다" {
		t.Fatalf("제목: %q", card.Card.Title)
	}
	for _, want := range []string{"네이버페이 포인트 쿠폰 등록", "비밀번호 키패드가 떴습니다", "pay.naver.com", "읽지 못했습니다"} {
		if !strings.Contains(card.Card.Text(), want) {
			t.Fatalf("카드에 %q 가 없습니다: %s", want, card.Card.Text())
		}
	}
	if strings.Contains(card.Card.Text(), "결제하기") || strings.Contains(card.Card.Text(), "배송지") {
		t.Fatalf("비밀번호 카드는 주문서를 말하지 않습니다: %s", card.Card.Text())
	}
	if len(jobs.raised) != 1 || jobs.raised[0] != "165 "+first["pending"]+" pin" {
		t.Fatalf("작업에 비밀번호 카드로 알려야 합니다: %v", jobs.raised)
	}
	if again := post(couponPIN); again["pending"] != first["pending"] || len(props.List()) != 1 {
		t.Fatalf("다시 물어도 카드 하나: %v", again)
	}

	if err := props.Decide(first["pending"], proposal.Approve); err != nil {
		t.Fatal(err)
	}
	got := jobs.wait(1)
	if len(got) != 1 || !strings.Contains(got[0], "browser_enter_payment_pin 을 다시 부르세요") || strings.Contains(got[0], "결제하기") {
		t.Fatalf("승인은 비밀번호 도구를 다시 부르라고 작업에 가야 합니다: %v", got)
	}
	if got := fu.wait(1); len(got) != 0 {
		t.Fatalf("작업의 카드를 대화에 알렸습니다: %v", got)
	}

	if pin := post(couponPIN); pin["decision"] != string(proposal.Approve) {
		t.Fatalf("승인 뒤의 비밀번호는 들어가야 합니다: %v", pin)
	}
	for _, req := range []string{
		`{"url":"https://pay.naver.com/authentication/api/secure/password-check","method":"POST","host":"pay.naver.com"}`,
		`{"url":"https://point.pay.naver.com/pd/api/point-conversion/coupon/use","method":"POST","host":"point.pay.naver.com"}`,
	} {
		if out := post(req); out["decision"] != string(proposal.Approve) {
			t.Fatalf("비밀번호가 보내는 요청은 그 승인으로 지나가야 합니다: %s → %v", req, out)
		}
	}
	if n := len(props.List()); n != 1 {
		t.Fatalf("카드가 더 올라갔습니다: %d", n)
	}

	// The job's approval is not the conversation's.
	runner.set(false)
	if conv := post(couponPIN); conv["decision"] == string(proposal.Approve) {
		t.Fatalf("작업의 승인으로 대화가 비밀번호를 넣었습니다: %v", conv)
	}
}

// Approved while the PIN tool still waits: it types at once, and the job is
// told the tool goes on - not to press anything again.
func TestAPasswordApprovedInTimeGoesIn(t *testing.T) {
	jobs := &fakeJobs{id: 165, title: "네이버페이 포인트 쿠폰 등록"}
	s, props, _, post := pinServer(t, jobs, &switchRunner{bg: true})
	s.holdDur = 3 * time.Second

	got := make(chan map[string]string, 1)
	go func() { got <- post(couponPIN) }()
	var card string
	for i := 0; i < 100 && card == ""; i++ {
		if list := props.List(); len(list) == 1 {
			card = list[0].ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	if card == "" {
		t.Fatal("카드가 올라가야 합니다")
	}
	if err := props.Decide(card, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	if res := <-got; res["decision"] != string(proposal.Approve) || res["why"] != "held" {
		t.Fatalf("기다리는 동안 승인되면 그대로 넣어야 합니다: %v", res)
	}
	if d := jobs.wait(1); len(d) != 1 || !strings.Contains(d[0], "이어서 비밀번호를 넣습니다") {
		t.Fatalf("작업에는 도구가 이어서 넣는다고 가야 합니다: %v", d)
	}
}

// A rejected password card tells the job not to type it.
func TestARejectedPasswordCard(t *testing.T) {
	jobs := &fakeJobs{id: 165, title: "네이버페이 포인트 쿠폰 등록"}
	_, props, _, post := pinServer(t, jobs, &switchRunner{bg: true})
	first := post(couponPIN)
	if err := props.Decide(first["pending"], proposal.Reject); err != nil {
		t.Fatal(err)
	}
	if d := jobs.wait(1); len(d) != 1 || !strings.Contains(d[0], "[결제 반려됨]") || !strings.Contains(d[0], "넣지 마세요") {
		t.Fatalf("반려: %v", d)
	}
	if again := post(couponPIN); again["decision"] == string(proposal.Approve) {
		t.Fatalf("반려 뒤에 넣었습니다: %v", again)
	}
}

// Inside an approved payment the password goes in without a card - on any
// site of that payer's: the 네이버페이 keypad a card company's window opens
// is the window's payment. Only the payer's own, and only while it lasts.
func TestThePasswordFollowsThePaymentsApproval(t *testing.T) {
	jobs := &fakeJobs{}
	runner := &switchRunner{}
	s, props, _, post := pinServer(t, jobs, runner)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.payments.Now = func() time.Time { return now }
	coupang := `{"url":"https://rocketpay.coupang.com/pin","method":"PIN","host":"rocketpay.coupang.com","pin":true}`

	s.payments.Grant("coupang.com", 0, "")
	if out := post(coupang); out["decision"] != string(proposal.Approve) || out["why"] != "granted" {
		t.Fatalf("대화의 승인: %v", out)
	}
	// A job's run holds the browser: the conversation's approval is not its.
	jobs.running(7)
	runner.set(true)
	if out := post(couponPIN); out["decision"] == string(proposal.Approve) || out["pending"] == "" {
		t.Fatalf("다른 결제자의 승인으로 넣었습니다: %v", out)
	}
	for _, p := range props.List() {
		_ = props.Decide(p.ID, proposal.Reject)
	}
	jobs.wait(1)
	s.payments.Grant("pg-window", 7, "113000")
	if out := post(couponPIN); out["decision"] != string(proposal.Approve) {
		t.Fatalf("결제창의 승인 안의 네이버페이 비밀번호: %v", out)
	}
	if !s.payments.Allow("pg-window", 7, "", "113000") {
		t.Fatal("결제자가 돌아왔습니다")
	}
	now = now.Add(holder.Window + time.Minute)
	if out := post(couponPIN); out["decision"] == string(proposal.Approve) {
		t.Fatalf("창이 지난 뒤에 넣었습니다: %v", out)
	}
}
