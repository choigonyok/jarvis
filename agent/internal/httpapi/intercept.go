package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/holder"
	"github.com/choigonyok/jarvis/gocommon/site"
)

// Payment is a request the browser container stopped on its way out, because
// it was headed for a payment provider.
//
// A person is waited for only a little. Holding the request until a decision
// would hold the browser, and the turn driving it, for as long as the card
// sits unread; refusing it at once would send the payer back over the whole
// order sheet after every approval, typed again from the top. So the request
// is held for holder.Hold while its card waits: approved in that time, it
// goes on as it was; otherwise it is refused, and approving later lets that
// one payment through (holder.Payments) and tells the payer to take the step
// again. Rejecting it tells the payer not to.
//
// This is the only place a payment is let through. The browser's guard
// keeps no approval of its own: it asks here for every request it stops.
//
// Nothing about this path trusts the browser's own tool boundary. The agent
// may click whatever it likes; money leaves over the network, and that is the
// line this endpoint draws.
type Payment struct {
	URL    string `json:"url"`
	Method string `json:"method"`
	Host   string `json:"host"`
	// Event is what the guard read off the request when the address alone
	// does not say: "order" is Coupang's 결제하기.
	Event string `json:"event,omitempty"`
	// Window is the guard's word that the request goes to a PG's payment
	// window (이니시스, KCP, 토스페이먼츠...) - what a mall opens when its only
	// method is 신용카드. Which of the window's ways a payment then takes is
	// on no address, so the card stands before the window opens, and its
	// approval covers the window, whichever PG's, and the 네이버페이 picked
	// inside it.
	Window bool `json:"window,omitempty"`
	// Amount and Item are what the request hands the window (price,
	// goodname...), when the guard could read them: shown on the card, never
	// decided on.
	Amount string `json:"amount,omitempty"`
	Item   string `json:"item,omitempty"`
	// FromWindow is the guard's word that the request comes from a page a
	// payment window opened (the 네이버페이 popup of a card company's window):
	// what an approved window lets 네이버페이 through for. A 네이버페이 payment
	// of a tab of its own is its own payment, with its own card.
	FromWindow bool `json:"fromWindow,omitempty"`
	// PIN is the browser's PIN tool asking before it types the payment
	// password on a keypad (쿠페이, 네이버페이): the password goes in only for a
	// payer with an approved payment that can still happen, on any site - the
	// 네이버페이 keypad a card company's window opens is that window's payment.
	// With none, the card is for the password itself: a coupon or a point
	// conversion asks for it first, and what follows it is the first request
	// the guard would stop - after the password is in.
	PIN bool `json:"pin,omitempty"`
	// JobID is the background job whose run reached the payment; zero is the
	// conversation. Set here from the run holding the browser, never taken
	// from the request.
	JobID      int64  `json:"jobId,omitempty"`
	JobTitle   string `json:"jobTitle,omitempty"`
	JobSummary string `json:"jobSummary,omitempty"`
}

// windowSite is what an approved payment window is held under: one for
// every PG, as one payment may pass through two (포트원, then 이니시스).
const windowSite = "pg-window"

// naverPaySite is the easy payment an open window lets through: the
// operator's own, the one the guard does not block, picked inside the
// window - the same payment as the window's card.
const naverPaySite = "naver.com"

// grantSite is the site an approval of this payment is held under.
func (p Payment) grantSite() string {
	if p.Window {
		return windowSite
	}
	return siteOf(p.Host)
}

// JobPayments is the jobs module's half of a payment a background job runs
// into: which job holds the browser, and where its card's decision goes.
type JobPayments interface {
	// RunningJob is the job whose run holds the browser now, with its
	// latest one-line status.
	RunningJob() (id int64, title, summary string, ok bool)
	// PaymentRaised tells the job a payment card is waiting for it, its
	// request held until until.
	PaymentRaised(job int64, card string, until time.Time)
	// PinRaised is PaymentRaised for a card that asks for the payment
	// password itself (Payment.PIN): the PIN tool waits on it until until.
	PinRaised(job int64, card string, until time.Time)
	// PaymentDecided hands the decision to the job and wakes it. passed: the
	// held request went on with the approval, in the run still going. A flow
	// card's decision comes here too.
	PaymentDecided(job int64, card string, approved, passed bool, text string) error
	// FlowRaised tells the job a flow card is waiting for it (leave.go): a
	// page of its run held until until, which joins the payment flow - site -
	// if the card is approved. The run waits on it as on a payment card.
	FlowRaised(job int64, card, site string, until time.Time)
	// Stopped tells the job's run what the guard stopped of it: line for the
	// job's log, tell for the run itself.
	Stopped(job int64, line, tell string)
}

func (s *Server) postIntercept(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "인증에 실패했습니다.")
		return
	}

	var p Payment
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	if strings.TrimSpace(p.Host) == "" {
		writeErr(w, http.StatusBadRequest, "host가 비어 있습니다.")
		return
	}
	p.JobID, p.JobTitle, p.JobSummary = 0, "", ""
	host := strings.ToLower(p.Host)

	background := false
	if bg, ok := s.runner.(interface{ InBackground() bool }); ok {
		background = bg.InBackground()
	}
	isJob := false
	if background && s.jobs != nil {
		p.JobID, p.JobTitle, p.JobSummary, isJob = s.jobs.RunningJob()
	}
	// Nobody is driving the browser, but a job's payment of this site is
	// still live: the request is from a page that job's run left open.
	left, waiting := false, ""
	if !background {
		var job int64
		if job, waiting, left = s.leftBy(p.grantSite()); left {
			p.JobID = job
		}
	}

	asked := s.recordQuery(p, background)
	var v verdict
	if left {
		// Not the conversation's - its card would ask about nothing anyone
		// did, and approving it would tell the conversation to pay - and not
		// a new card of the job's either: the run is over. The page waits on
		// the job's card if one is up; the job's next run takes the step
		// again under its approval.
		s.log.Info("작업이 남긴 페이지의 결제 요청을 카드 없이 막았습니다", "job", p.JobID, "host", host, "waiting", waiting, "url", clampURL(urlShape(p.URL)))
		v = verdict{decision: proposal.Reject, pending: waiting, card: waiting, why: "left"}
	} else {
		v = s.judgePayment(r.Context(), p, host, background, isJob, asked)
	}
	s.recordAnswer(asked, p, v)

	// The guard reads decision and pending; why and card are what a replay
	// compares with what was recorded.
	out := map[string]string{"decision": string(v.decision), "why": v.why}
	if v.pending != "" {
		out["pending"] = v.pending
	}
	if v.card != "" {
		out["card"] = v.card
	}
	writeJSON(w, http.StatusOK, out)
}

// verdict is what a stopped request came to.
type verdict struct {
	decision proposal.Decision
	// pending is the card a refused request waits on, what the guard tells
	// the page; empty when nothing waits (a refusal with no card).
	pending string
	// card is the card the request went through or stopped at, however it
	// came out; why is how the verdict was reached, in a word a replay can
	// compare: login | background | granted | held | waiting - and for a page
	// leaving a job's sites (leave.go) flow | held | waiting | turned | site.
	card string
	why  string
}

// judgePayment decides one stopped request. asked is the record of the
// question, what the card it raises came from.
func (s *Server) judgePayment(ctx context.Context, p Payment, host string, background, isJob bool, asked string) verdict {
	// A background task has no one behind it. Asking a person here would put
	// a card in front of someone who did not start anything - which is what a
	// background order lookup on 네이버페이 did. So it is decided
	// on the spot: the sign-in hosts it is allowed to use (signing in with the
	// browser's saved password, chosen by the operator) go through, and every
	// other host - every payment host - is refused.
	//
	// A job is the exception: the operator asked for it in conversation, and a
	// purchase they asked for reaches its payment there - the conversation
	// hands every browser task to a job. Refused outright, its order sheet fails
	// with "주문중 오류" and nobody is asked. So a job's payment raises a card
	// bound to that job, and the job waits on it.
	if background {
		if s.backgroundLogin[host] {
			s.log.Info("백그라운드 작업의 로그인 요청을 허용했습니다", "host", p.Host, "method", p.Method)
			return verdict{decision: proposal.Approve, why: "login"}
		}
		if !isJob {
			s.log.Warn("백그라운드 작업 중 결제 도메인 요청을 묻지 않고 거부했습니다", "host", p.Host, "url", clampURL(p.URL))
			return verdict{decision: proposal.Reject, why: "background"}
		}
		if s.granted(p) {
			s.log.Info("작업에 승인된 결제 흐름의 요청을 통과시켰습니다", "job", p.JobID, "host", host, "method", p.Method, "event", p.Event, "window", p.Window, "pin", p.PIN)
			return verdict{decision: proposal.Approve, why: "granted"}
		}
		if s.carried(p) {
			return verdict{decision: proposal.Approve, why: "carried"}
		}
		card := s.raisePayment(ctx, p, asked)
		until := time.Now().Add(s.holdDur)
		if p.PIN {
			s.jobs.PinRaised(p.JobID, card, until)
		} else {
			s.jobs.PaymentRaised(p.JobID, card, until)
		}
		s.log.Info("작업의 결제 요청을 멈추고 카드를 올렸습니다", "job", p.JobID, "card", card, "host", host, "event", p.Event, "pin", p.PIN)
		if s.payments.Wait(ctx, card, until) && s.grantedNow(p) {
			s.log.Info("기다리는 동안 승인돼 멈춘 결제 요청을 그대로 보냈습니다", "job", p.JobID, "card", card, "host", host, "pin", p.PIN)
			return verdict{decision: proposal.Approve, card: card, why: "held"}
		}
		return verdict{decision: proposal.Reject, pending: card, card: card, why: "waiting"}
	}

	// The second half of an approval: the operator said yes, and the browser
	// is back making the payment. Approval covers the site: one payment can
	// cross several of its hosts (checkout, then pay.coupang.com for the PIN).
	if s.granted(p) {
		s.log.Info("승인된 결제 흐름의 요청을 통과시켰습니다", "host", host, "method", p.Method, "window", p.Window, "pin", p.PIN)
		return verdict{decision: proposal.Approve, why: "granted"}
	}
	if s.carried(p) {
		return verdict{decision: proposal.Approve, why: "carried"}
	}
	card := s.raisePayment(ctx, p, asked)
	if s.payments.Wait(ctx, card, time.Now().Add(s.holdDur)) && s.grantedNow(p) {
		s.log.Info("기다리는 동안 승인돼 멈춘 결제 요청을 그대로 보냈습니다", "card", card, "host", host, "pin", p.PIN)
		return verdict{decision: proposal.Approve, card: card, why: "held"}
	}
	return verdict{decision: proposal.Reject, pending: card, card: card, why: "waiting"}
}

// granted is a stopped request of a payer's approved payment: the payment
// going on at its site, or the 네이버페이 picked inside its open window. The
// PIN tool's keypad goes by any approved payment of its payer that can still
// happen (Payment.PIN) - typing the password sends nothing yet: the request
// it then sends is judged here again, as the payment's own.
func (s *Server) granted(p Payment) bool {
	if p.PIN {
		return s.payments.Live(p.JobID)
	}
	return s.payments.Allow(p.grantSite(), p.JobID, p.Event, p.Amount) || s.insideWindow(p)
}

// grantedNow is granted for a request held on its card that was just
// approved: that approval, at that site.
func (s *Server) grantedNow(p Payment) bool {
	if p.PIN {
		return s.payments.Live(p.JobID)
	}
	return s.payments.Allow(p.grantSite(), p.JobID, p.Event, p.Amount)
}

// carried is a payment window opened by a payment this payer approved at the
// mall and is making right now (holder.Payments.Carry): the mall's 결제하기
// handed the order to its PG - one payment, one card. The window is then held
// as approved, and what passes inside it passes as it would on a window card.
func (s *Server) carried(p Payment) bool {
	if !p.Window {
		return false
	}
	from, ok := s.payments.Carry(windowSite, p.JobID, p.Amount)
	if ok {
		s.log.Info("쇼핑몰 결제의 승인으로 그 결제의 결제창을 열었습니다", "job", p.JobID, "from", from, "host", p.Host, "amount", p.Amount)
	}
	return ok
}

// leftBy is whose page a stopped request is from when nobody is driving the
// browser - no run holds it, and the conversation is not in a turn that took
// it: a job whose payment of this site is still live, its card up or its
// approval waiting for its next run. A job's run that ends on wait_payment
// leaves its payment page open, and a payment window goes on writing. The
// conversation's own payment there comes first. card is the job's card the
// page waits on, if one is up.
func (s *Server) leftBy(site string) (job int64, card string, ok bool) {
	if s.jobs == nil || s.chatOnBrowser() {
		return 0, "", false
	}
	pending := func(match func(Payment) bool) (proposal.Proposal, bool) {
		return s.proposals.FindPendingWhere(func(q proposal.Proposal) bool {
			if q.Origin != proposal.OriginIntercept || q.Action.Kind != holder.KindPayment {
				return false
			}
			w := paymentOf(q.Action.Input)
			return w.grantSite() == site && match(w)
		})
	}
	live := s.payments.LiveOn(site)
	if slices.Contains(live, 0) {
		return 0, "", false
	}
	if _, mine := pending(func(w Payment) bool { return w.JobID == 0 }); mine {
		return 0, "", false
	}
	if q, ok := pending(func(w Payment) bool { return w.JobID != 0 }); ok {
		return paymentOf(q.Action.Input).JobID, q.ID, true
	}
	for _, j := range live {
		if j != 0 {
			return j, "", true
		}
	}
	return 0, "", false
}

// chatOnBrowser is whether the conversation is driving the browser now.
func (s *Server) chatOnBrowser() bool {
	c, ok := s.runner.(interface{ ChatOnBrowser() bool })
	return ok && c.ChatOnBrowser()
}

// insideWindow is a 네이버페이 request of a payer whose approved payment
// window is open, from a page that window opened: the easy payment picked
// inside the window, which its card already covers. Only while that window
// is open - not before the payer is back at it, nor after its time - and
// only from the window's own popup, so an approval at a PG does not open
// 네이버페이 for anything else - a 네이버페이 order in a tab of its own is
// another payment, even within the ten minutes.
func (s *Server) insideWindow(p Payment) bool {
	return !p.Window && p.FromWindow && siteOf(p.Host) == naverPaySite && s.payments.IsOpen(windowSite, p.JobID)
}

// raisePayment answers a stopped payment with a card, and says which: the
// one already waiting for this site and this payer (the conversation, or one
// job) - a flow sends several requests, and the page retrying is the same
// question - or a new one. Another payer's card is not this one's: its
// decision goes to whoever raised it.
func (s *Server) raisePayment(ctx context.Context, p Payment, asked string) string {
	// Finding and opening are one step: a page sends its writes together, and
	// two that both found none opened two cards 5ms apart.
	s.raising.Lock()
	defer s.raising.Unlock()
	if waiting, ok := s.proposals.FindPendingWhere(func(q proposal.Proposal) bool {
		w := paymentOf(q.Action.Input)
		return q.Origin == proposal.OriginIntercept && q.Action.Kind == holder.KindPayment && w.grantSite() == p.grantSite() && w.JobID == p.JobID
	}); ok {
		return waiting.ID
	}
	var addr *SavedAddress
	if s.savedAddress != nil && !p.PIN {
		actx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if a, ok := s.savedAddress(actx); ok {
			addr = &a
		}
		cancel()
	}
	raw, _ := json.Marshal(p)
	opened, _ := s.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginIntercept,
		Action: action.Action{Kind: holder.KindPayment, Input: raw},
		Card:   paymentCard(p, addr),
	})
	s.recordRaised(asked, p, opened)
	// In the conversation either way - a job's too, so it stays where it was
	// raised once answered instead of floating below the thread only while it
	// waits. The card names the job it is for.
	if s.thread != nil {
		s.thread.AttachProposal(opened.ID)
	}
	return opened.ID
}

func paymentOf(raw json.RawMessage) Payment {
	var p Payment
	_ = json.Unmarshal(raw, &p)
	return p
}

// resolvePayment acts on a decision about a payment card: approved, the host
// is open for a few minutes and the assistant is told to take the step again;
// rejected, it is told not to pay. A flow card's goes to resolveFlow.
func (s *Server) resolvePayment(p proposal.Proposal, d proposal.Decision) {
	if p.Origin != proposal.OriginIntercept {
		return
	}
	if p.Action.Kind == holder.KindFlow {
		s.resolveFlow(p, d)
		return
	}
	pay := paymentOf(p.Action.Input)
	if pay.JobID != 0 {
		s.resolveJobPayment(p, pay, d)
		return
	}
	if d == proposal.Approve {
		// The grant first: a held request that goes on asks it next.
		s.payments.Grant(pay.grantSite(), 0, grantAmount(pay))
	}
	passed := s.payments.Release(p.ID, d == proposal.Approve) && d == proposal.Approve
	if s.followUp == nil {
		return
	}
	if d == proposal.Approve && pay.PIN {
		if passed {
			s.proposals.Settle(p.ID, proposal.Executed, "승인했습니다. 결제 비밀번호를 넣습니다.")
		} else {
			s.proposals.Settle(p.ID, proposal.Executed, "승인했습니다. 결제 비밀번호를 다시 넣게 합니다.")
		}
		s.followUp.Enqueue(pinApproved(p.ID, pay, passed))
		return
	}
	if d == proposal.Approve {
		if passed {
			s.proposals.Settle(p.ID, proposal.Executed, "승인했습니다. 멈춰 있던 결제 요청이 그대로 나갔습니다.")
			s.followUp.Enqueue(fmt.Sprintf("[결제 승인됨] 운영자가 결제 카드 %s(%s)를 승인했고, 멈춰 있던 결제 요청이 승인과 함께 그대로 나갔습니다.\n"+
				"결제하기를 다시 누르지 말고(결제창을 다시 열지도 말고) 브라우저 화면을 보며 이어 가세요. %s", p.ID, pay.Host, approvedScope(pay)))
			return
		}
		s.proposals.Settle(p.ID, proposal.Executed, "승인했습니다. 결제를 다시 진행합니다.")
		step := "같은 결제 버튼을 다시 누르는 것"
		if pay.Window {
			step = "주문서의 결제하기를 다시 눌러 결제창을 여는 것"
		}
		s.followUp.Enqueue(fmt.Sprintf("[결제 승인됨] 운영자가 결제 카드 %s(%s)를 승인했습니다. 막혔던 결제 요청은 아직 보내지지 않았습니다.\n"+
			"지금 브라우저에서 같은 결제 단계를 다시 진행하세요(%s). %s",
			p.ID, pay.Host, step, approvedScope(pay)))
		return
	}
	why := ""
	if p.Reason != "" {
		why = "\n반려 이유: " + p.Reason
	}
	if pay.PIN {
		s.followUp.Enqueue(fmt.Sprintf("[결제 반려됨] 운영자가 결제 비밀번호 카드 %s(%s)를 반려했습니다.%s\n"+
			"결제 비밀번호를 넣지 마세요(browser_enter_payment_pin 을 다시 부르지 않습니다). 이유를 캐묻지 말고 짧게 확인만 하세요.", p.ID, pay.Host, why))
		return
	}
	s.followUp.Enqueue(fmt.Sprintf("[결제 반려됨] 운영자가 결제 카드 %s(%s)를 반려했습니다.%s\n"+
		"결제하지 마세요. 이유를 캐묻지 말고 짧게 확인만 하세요.", p.ID, pay.Host, why))
}

// pinApproved is what the payer hears of an approved password card: the
// held PIN tool goes on and types it, or - its wait over - the payer calls
// it again on the keypad. Not 결제하기 again: what the password then sends
// is the step itself, and taking the step twice is a second payment.
func pinApproved(card string, pay Payment, passed bool) string {
	scope := fmt.Sprintf("이 승인으로 %d분 동안 %s 의 결제 비밀번호 입력과 그 뒤의 요청이 통과합니다. 그 뒤의 다른 결제는 다시 카드를 올립니다.",
		int(holder.Window.Minutes()), siteOf(pay.Host))
	if passed {
		return fmt.Sprintf("[결제 승인됨] 운영자가 결제 비밀번호 카드 %s(%s)를 승인했고, 기다리던 browser_enter_payment_pin 이 이어서 비밀번호를 넣습니다.\n"+
			"도구의 결과를 보고 화면을 보며 이어 가세요 - 버튼을 다시 누르지 마세요. %s", card, pay.Host, scope)
	}
	return fmt.Sprintf("[결제 승인됨] 운영자가 결제 비밀번호 카드 %s(%s)를 승인했습니다. 비밀번호는 아직 넣지 않았습니다.\n"+
		"먼저 화면에서 그 일이 이미 끝났는지(등록·결제 완료) 확인하세요. 아니면 비밀번호 키패드가 떠 있을 때 browser_enter_payment_pin 을 다시 부르세요(이번에는 넣습니다). "+
		"키패드가 사라졌으면 그 단계를 한 번 다시 해서 키패드를 띄운 뒤 부르세요. %s", card, pay.Host, scope)
}

// resolveJobPayment is a decision on a card a job's run raised. Approved,
// the site opens to that job alone for a while and the job wakes to take the
// step again; rejected, it is told not to pay. Either way the job hears it,
// not the conversation.
func (s *Server) resolveJobPayment(p proposal.Proposal, pay Payment, d proposal.Decision) {
	approved := d == proposal.Approve
	if approved {
		// The grant first: a held request that goes on asks it next.
		s.payments.Grant(pay.grantSite(), pay.JobID, grantAmount(pay))
	}
	// A held request goes on only while the job's run still holds the
	// browser: the window it opens is that run's to carry on, and with no run
	// its next requests would be the conversation's.
	passes := approved && s.jobs != nil && s.jobHolds(pay.JobID)
	passed := s.payments.Release(p.ID, passes) && passes
	var text string
	switch {
	case approved && pay.PIN:
		text = pinApproved(p.ID, pay, passed)
	case pay.PIN:
		text = fmt.Sprintf("[결제 반려됨] 운영자가 결제 비밀번호 카드 %s(%s)를 반려했습니다.", p.ID, pay.Host)
		if p.Reason != "" {
			text += "\n반려 이유: " + p.Reason
		}
		text += "\n결제 비밀번호를 넣지 마세요(browser_enter_payment_pin 을 다시 부르지 않습니다)."
	case passed:
		text = fmt.Sprintf("[결제 승인됨] 운영자가 결제 카드 %s(%s)를 승인했고, 멈춰 있던 결제 요청이 승인과 함께 그대로 나갔습니다.\n"+
			"결제하기를 다시 누르지 말고(결제창을 다시 열지도 말고) 화면을 보며 이어 가세요. %s", p.ID, pay.Host, approvedScope(pay))
	case approved:
		step := "결제하기를 한 번 누르면 이번에는 통과합니다"
		if pay.Window {
			step = "주문서에서 결제하기를 다시 누르면 이번에는 결제창이 열립니다"
		}
		text = fmt.Sprintf("[결제 승인됨] 운영자가 결제 카드 %s(%s)를 승인했습니다. 막혔던 결제 요청은 아직 보내지지 않았습니다.\n"+
			"주문 내역에 이 주문이 아직 없는지 먼저 확인한 뒤, 같은 결제 단계를 다시 진행하세요(%s). %s",
			p.ID, pay.Host, step, approvedScope(pay))
	default:
		text = fmt.Sprintf("[결제 반려됨] 운영자가 결제 카드 %s(%s)를 반려했습니다.", p.ID, pay.Host)
		if p.Reason != "" {
			text += "\n반려 이유: " + p.Reason
		}
		text += "\n결제하지 마세요."
	}
	if s.jobs == nil {
		return
	}
	err := s.jobs.PaymentDecided(pay.JobID, p.ID, approved, passed, text)
	if err != nil {
		// The card keeps the decision; the jobs' sweep hands it on.
		s.log.Warn("결제 카드 결정을 작업에 전하지 못했습니다", "job", pay.JobID, "card", p.ID, "err", err)
	}
	if !approved {
		return
	}
	note := fmt.Sprintf("승인했습니다. 작업이 이 결제 한 건을 이어서 합니다(다시 진행한 때부터 %d분).", int(holder.Window.Minutes()))
	switch {
	case pay.PIN && passed:
		note = "승인했습니다. 작업이 결제 비밀번호를 넣습니다."
	case pay.PIN:
		note = "승인했습니다. 작업이 결제 비밀번호를 다시 넣습니다."
	case passed:
		note = "승인했습니다. 멈춰 있던 결제 요청이 그대로 나갔고, 작업이 이어서 합니다."
	}
	if err != nil {
		note = "승인했지만 작업을 깨우지 못했습니다: " + err.Error()
	}
	s.proposals.Settle(p.ID, proposal.Executed, note)
}

// grantAmount is the amount an approval is held to: what a payment window was
// handed, or - for a mall's own payment, whose requests carry none - what its
// card said it was for, which a window the payment then opens must match
// (holder.Payments.Carry). Empty when neither is known.
func grantAmount(p Payment) string {
	if p.Window {
		return p.Amount
	}
	amount, _ := paidAmount(p)
	return amount
}

// jobHolds is whether job's run holds the browser now.
func (s *Server) jobHolds(job int64) bool {
	id, _, _, ok := s.jobs.RunningJob()
	return ok && id == job
}

// approvedScope is what an approval lets through, told to the payer the
// way holder.Payments holds it.
func approvedScope(p Payment) string {
	if p.Window {
		// The window's ways out that are not 네이버페이 the guard blocks only as
		// far as it knows them; the payer is told which one to take.
		return fmt.Sprintf("이 승인은 결제창(%s)에서 하는 결제 한 건%s입니다: 결제창을 다시 연 때부터 %d분 동안 결제창·카드사 창과, 그 안에서 고른 네이버페이가 통과합니다. "+
			"금액이 다른 주문의 결제창은 다시 카드를 올립니다. "+
			"결제창에서는 간편결제의 네이버페이나 신용카드(운영자 지시의 카드, 없으면 하나카드)로 결제하세요 - 카드사 창에서는 간편결제의 네이버페이로 하고, 카드사 앱 결제(하나Pay 같은 앱카드·QR·결제코드)는 쓰지 않습니다. 카드사 창에 네이버페이가 없으면 결제하지 말고 알리세요. "+
			"카드사 창 안의 네이버페이는 비밀번호를 넣은 뒤 네이버페이 창에 뜨는 카드사의 인증 결과 화면에서 확인을 눌러야 카드사 창으로 넘어갑니다 - 카드사 창의 결제완료는 그 전에는 '인증을 완료하여 주십시오'만 띄우니 누르지 말고, 탭도 닫지 마세요. "+
			"다른 간편결제, 휴대폰 결제, 계좌이체, 가상계좌, 카드번호 직접 입력은 하지 않습니다. "+
			"네이버페이 결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요 - 넣지 못했다고 하면 다시 부르지도 누르지도 말고 알리세요. "+
			"카드사 앱 인증(폰 푸시·QR·결제코드)이 뜨면 진행하지 말고 알리세요. "+
			"결제가 끝나면 무엇을 얼마에 결제했는지 짧게 알리세요.", p.Host, amountNote(p), int(holder.Window.Minutes()))
	}
	return fmt.Sprintf("이 승인은 %s 의 결제 한 건입니다: 다시 진행을 시작한 때부터 %d분 안에, 결제하기는 한 번 통과합니다. "+
		"결제하기가 곧바로 여는 결제창(PG)과 그 안의 카드사 창도 같은 결제라 이 승인으로 지나갑니다 - 결제창에서는 네이버페이나 신용카드(운영자 지시의 카드, 없으면 하나카드)로 결제합니다. "+
		"그 뒤의 다른 결제는 다시 카드를 올립니다. 결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요. "+
		"결제가 끝나면 무엇을 얼마에 결제했는지 짧게 알리세요.", siteOf(p.Host), int(holder.Window.Minutes()))
}

// SavedAddress is the shipping address the vault keeps, as a payment card
// shows it.
type SavedAddress struct {
	Recipient string
	Address   string
	Detail    string
	// Doubt is why it may be out of date: a move since it was saved.
	Doubt string
}

// trailingParens is what a person may add after a road address's building
// number: (태평로1가).
var trailingParens = regexp.MustCompile(`\s*\([^()]*\)\s*$`)

// roadKey is a road address's road and building number with no space - "세종대로110" -
// the part an order sheet's address search goes by, and the part a wrong one
// differs in.
func roadKey(address string) string {
	f := strings.Fields(trailingParens.ReplaceAllString(address, ""))
	if len(f) < 2 {
		return strings.Join(f, "")
	}
	return f[len(f)-2] + f[len(f)-1]
}

// addressLines is what a payment card says of where the order goes: the
// saved address, and what to check first when the job's report names another
// or the operator may have moved since.
func addressLines(p Payment, addr *SavedAddress) (warns []string, saved string) {
	if addr == nil || addr.Address == "" {
		return nil, ""
	}
	saved = strings.TrimSpace(addr.Address + " " + addr.Detail)
	if addr.Recipient != "" {
		saved += " (" + addr.Recipient + ")"
	}
	summary := strings.Join(strings.Fields(p.JobSummary), "")
	if key := roadKey(addr.Address); key != "" && strings.Contains(summary, "배송지") && !strings.Contains(summary, key) {
		warns = append(warns, "작업이 남긴 배송지가 저장된 배송지와 다릅니다 - 승인하기 전에 주문서의 주소를 확인하세요.")
	}
	if addr.Doubt != "" {
		warns = append(warns, addr.Doubt+" - 지금 사는 곳이 맞는지 확인하고 승인하세요.")
	}
	return warns, saved
}

// warnFacts are what to check before deciding, as the facts that go first.
func warnFacts(warns []string) []action.Fact {
	var out []action.Fact
	for _, w := range warns {
		out = append(out, action.Fact{Label: "주의", Value: w, Warn: true})
	}
	return out
}

// amountFacts are how much a payment card is for and where that was read -
// lead: the title says the amount as it is - or, unread, a warning that says
// so and what to do instead.
func amountFacts(p Payment, lead bool, unread string) []action.Fact {
	amount, from := paidAmount(p)
	if amount == "" {
		return []action.Fact{{Label: "결제 금액", Value: "읽지 못했습니다 - " + unread, Warn: true}}
	}
	return []action.Fact{{Label: "결제 금액", Value: won(amount), Lead: lead}, {Label: "금액을 읽은 곳", Value: from}}
}

// jobFacts are the job a payment card is for and what it last said it was
// doing: what is being bought is the job's to say - the request carries no
// amount but a payment window's.
func jobFacts(p Payment) []action.Fact {
	if p.JobID == 0 {
		return nil
	}
	out := []action.Fact{{Label: "작업", Value: p.JobTitle}}
	if s := strings.TrimSpace(p.JobSummary); s != "" {
		out = append(out, action.Fact{Label: "작업이 남긴 현황", Value: s})
	}
	return out
}

// paymentCard says what is being asked: a payment, every time - a sign-in host
// is let through without a card (backgroundLogin) and none is on the ask list,
// so no card is for opening a sign-in page. How much is said first - in the
// title, which is also how the phone's notice begins, and first among the
// facts with where it was read - because it is the thing being approved, even
// when it is only a word inside the job's report. What to check goes above it.
func paymentCard(p Payment, addr *SavedAddress) action.Card {
	if p.PIN {
		return pinCard(p)
	}
	amount, _ := paidAmount(p)
	warns, saved := addressLines(p, addr)
	if told := reportedAmount(p.JobSummary); p.Amount != "" && told != "" && told != p.Amount {
		warns = append(warns, "작업이 남긴 금액("+won(told)+")과 결제창이 받은 금액("+won(p.Amount)+")이 다릅니다 - 주문서를 확인하고 승인하세요.")
	}
	facts := warnFacts(warns)
	facts = append(facts, amountFacts(p, true, "결제 화면에서 금액을 확인하고 승인하세요")...)
	facts = append(facts, jobFacts(p)...)
	if saved != "" {
		facts = append(facts, action.Fact{Label: "저장된 배송지", Value: saved})
	}
	if item := windowItem(p); item != "" {
		facts = append(facts, action.Fact{Label: "결제창이 받은 상품", Value: item})
	}
	// The host is the part a person can actually judge; the path is there
	// for the case where two flows share a provider.
	where := p.Host
	if label := eventLabel(p.Event); label != "" {
		where += " · " + label
	}
	facts = append(facts, action.Fact{Label: "결제하는 곳", Value: where})
	if u := clampURL(p.URL); u != "" {
		facts = append(facts, action.Fact{Label: "주소", Value: u})
	}
	note := ""
	if amount != "" {
		note = "(" + won(amount) + ")"
	}
	// What an approval lets through, as a fact: the words may say it more
	// shortly, or not at all, and it is what the approval is held to.
	minutes := int(holder.Window.Minutes())
	scope := func(how string) []action.Fact {
		return append(facts, action.Fact{Label: "승인 범위", Value: fmt.Sprintf("결제 한 건%s · %s", note, fmt.Sprintf(how, minutes))})
	}
	if p.Window {
		consequence := fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 결제창을 다시 연 때부터 %d분 동안 결제 한 건%s이 진행됩니다(결제창 안에서는 네이버페이·신용카드)",
			int(holder.Window.Minutes()), note)
		if p.JobID != 0 {
			consequence = fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 작업이 결제창을 다시 열어 결제 한 건%s을 진행합니다(연 때부터 %d분, 결제창 안에서는 네이버페이·신용카드)",
				note, int(holder.Window.Minutes()))
		}
		return action.Card{
			Title:       payTitle("결제창에서 ", amount, "결제창에서 결제합니다"),
			Facts:       scope("결제창을 다시 연 때부터 %d분 · 결제창 안의 네이버페이·신용카드까지"),
			Consequence: consequence,
		}
	}
	if site := siteOf(p.Host); site == "coupang.com" {
		consequence := fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 결제 한 건%s이 진행됩니다(다시 진행한 때부터 %d분 안에, 결제하기 한 번)",
			note, int(holder.Window.Minutes()))
		if p.JobID != 0 {
			consequence = fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 작업이 결제 한 건%s을 다시 진행합니다(진행한 때부터 %d분 안에, 결제하기 한 번)",
				note, int(holder.Window.Minutes()))
		}
		return action.Card{
			Title:       payTitle("쿠팡에서 쿠페이로 ", amount, "쿠팡에서 쿠페이로 결제합니다"),
			Facts:       scope("다시 진행한 때부터 %d분 · 결제하기 한 번"),
			Consequence: consequence,
		}
	}
	consequence := fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 결제 한 건%s이 진행됩니다(다시 진행한 때부터 %d분 안에, 이 결제가 여는 결제창까지)", note, int(holder.Window.Minutes()))
	if p.JobID != 0 {
		consequence = fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 작업이 결제 한 건%s을 다시 진행합니다(진행한 때부터 %d분 안에, 이 결제가 여는 결제창까지)", note, int(holder.Window.Minutes()))
	}
	return action.Card{
		Title:       payTitle("", amount, "결제를 진행합니다"),
		Facts:       scope("다시 진행한 때부터 %d분 · 이 결제가 여는 결제창까지"),
		Consequence: consequence,
	}
}

// pinCard asks for the payment password itself: no payment was approved
// before its keypad - a coupon or a point conversion asks for the password
// first, or a payment the guard did not stop. What it is for is the job's
// to say, and the keypad's page; an amount only when the job wrote one.
func pinCard(p Payment) action.Card {
	name := "결제 비밀번호"
	switch siteOf(p.Host) {
	case naverPaySite:
		name = "네이버페이 비밀번호"
	case "coupang.com":
		name = "쿠페이 비밀번호"
	}
	facts := amountFacts(p, false, "쿠폰 등록·포인트 전환처럼 결제가 아닐 수도 있습니다. 무엇을 위한 비밀번호인지 아래를 보고 승인하세요")
	facts = append(facts, jobFacts(p)...)
	facts = append(facts, action.Fact{Label: "비밀번호를 묻는 화면", Value: p.Host})
	if u := clampURL(p.URL); u != "" {
		facts = append(facts, action.Fact{Label: "주소", Value: u})
	}
	who := "자비스가"
	if p.JobID != 0 {
		who = "이 작업이"
	}
	return action.Card{
		Title: name + "를 넣습니다",
		Facts: facts,
		Consequence: fmt.Sprintf("결제나 포인트 사용이 일어나면 되돌릴 수 없습니다 · 승인하면 %s 저장된 %s를 넣고, %d분 동안 %s 의 비밀번호 입력과 그 뒤의 요청이 통과합니다",
			who, name, int(holder.Window.Minutes()), siteOf(p.Host)),
	}
}

// payTitle is a payment card's title: how much, when the card knows it -
// "결제창에서 64,000원을 결제합니다" - and unknown, the title without it.
func payTitle(where, amount, unknown string) string {
	if amount == "" {
		return unknown
	}
	return where + won(amount) + "을 결제합니다"
}

// paidAmount is how much a payment card says it is for, and where it read
// that: what the request hands a payment window, else what the job said right
// before paying. No other payment's request carries one - 네이버페이's held
// writes were a log line, a password check and the order sheet's ids
// - and a guess is worse than none on this card.
func paidAmount(p Payment) (amount, from string) {
	if p.Amount != "" {
		return p.Amount, "결제창이 받은 금액"
	}
	if a := reportedAmount(p.JobSummary); a != "" {
		return a, "작업이 결제 직전에 남긴 금액"
	}
	return "", ""
}

var (
	// "결제 금액 17,100원", as the job is told to write it before paying.
	paidLabelRE = regexp.MustCompile(`(?:결제|총|합계)\s*금액\s*:?\s*([0-9][0-9,]*)\s*원`)
	// The total of a sum written out: "108,000원+배송비 5,000원=113,000원".
	paidTotalRE = regexp.MustCompile(`=\s*([0-9][0-9,]*)\s*원`)
	// A part of the report that is an amount: "17,100원(배송비 0)".
	paidPartRE = regexp.MustCompile(`^\s*([0-9][0-9,]*)\s*원`)
)

// reportedAmount is the amount in the job's report right before paying,
// '결제 직전 · 상품 · 수량 · 결제 금액 N원 · 결제수단 · 배송지': the amount named as
// such, else - in a report of that shape - the total of a sum written out, or
// the first part that is an amount (items' own prices sit inside the part
// naming them). Nothing when the report does not say it plainly.
func reportedAmount(summary string) string {
	if m := paidLabelRE.FindStringSubmatch(summary); m != nil {
		return amountDigits(m[1])
	}
	if !strings.Contains(summary, "결제 직전") {
		return ""
	}
	if all := paidTotalRE.FindAllStringSubmatch(summary, -1); len(all) > 0 {
		return amountDigits(all[len(all)-1][1])
	}
	for _, part := range strings.Split(summary, "·") {
		if m := paidPartRE.FindStringSubmatch(part); m != nil {
			return amountDigits(m[1])
		}
	}
	return ""
}

// amountDigits is an amount as the card keeps one, digits only: "17,100" is
// "17100"; nothing of none.
func amountDigits(s string) string {
	n, err := strconv.ParseUint(strings.ReplaceAll(s, ",", ""), 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	return strconv.FormatUint(n, 10)
}

// windowItem is what a payment window was handed besides its amount, as the
// card shows it - the mall's own words, so cut short and kept on one line.
func windowItem(p Payment) string {
	item := strings.Join(strings.Fields(p.Item), " ")
	if r := []rune(item); len(r) > 60 {
		item = string(r[:60]) + "…"
	}
	return item
}

// amountNote is the amount an approval of a window is held to, in
// parentheses for the card's words: "(64,000원)", or nothing unread.
func amountNote(p Payment) string {
	if p.Amount == "" {
		return ""
	}
	return "(" + won(p.Amount) + ")"
}

// won writes an amount in digits as the card says it: 64000 is 64,000원.
func won(digits string) string {
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return digits
	}
	s := strconv.FormatUint(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String() + "원"
}

// eventLabel names, for the card, the request the guard read the body of.
func eventLabel(event string) string {
	switch event {
	case "":
		return ""
	case "order":
		return "결제하기(주문)"
	case "?":
		return "내용을 읽지 못한 요청"
	}
	return event
}

// authorized checks the shared secret the browser container also uses for MCP.
// One secret, because it is one trust boundary: the containers beside this one.
func (s *Server) authorized(r *http.Request) bool {
	if s.interceptToken == "" {
		return false
	}
	_, value, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	return subtle.ConstantTimeCompare([]byte(value), []byte(s.interceptToken)) == 1
}

func clampURL(u string) string {
	const max = 120
	if len([]rune(u)) <= max {
		return u
	}
	return string([]rune(u)[:max]) + "…"
}

// siteOf is a host's site, the unit a payment approval covers: its registrable
// domain (coupang.com, 11st.co.kr). A host that has none stands for itself.
func siteOf(host string) string {
	if domain, err := site.Domain(host); err == nil {
		return domain
	}
	return strings.Trim(strings.ToLower(host), ".")
}
