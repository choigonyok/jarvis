package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/gocommon/site"
)

// Payment is a request the browser container stopped on its way out, because
// it was headed for a payment provider.
//
// A person is waited for only a little. Holding the request until a decision
// held the browser, and the turn driving it, for as long as the card sat
// unread (up to 40 minutes); refusing it at once sent the payer back over
// the whole order sheet after every approval - a guest order sheet typed
// again from the top, which is where a wrong phone number and a wrong
// building number got in (2026-10-30). So the request is held for
// paymentHold while its card waits: approved in that time, it goes on as it
// was; otherwise it is refused, and approving later lets that one payment
// through (paymentGrants) and tells the payer to take the step again.
// Rejecting it tells the payer not to.
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
	// inside it (2026-10-29).
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
	// PaymentDecided hands the decision to the job and wakes it. passed: the
	// held request went on with the approval, in the run still going.
	PaymentDecided(job int64, card string, approved, passed bool, text string) error
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

	asked := s.recordQuery(p, background)
	v := s.judgePayment(r.Context(), p, host, background, isJob, asked)
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
	// compare: login | background | granted | held | waiting.
	card string
	why  string
}

// judgePayment decides one stopped request. asked is the record of the
// question, what the card it raises came from.
func (s *Server) judgePayment(ctx context.Context, p Payment, host string, background, isJob bool, asked string) verdict {
	// A background task has no one behind it. Asking a person here would put
	// a card in front of someone who did not start anything - which is what a
	// background order lookup on 네이버페이 did (2026-10-07). So it is decided
	// on the spot: the sign-in hosts it is allowed to use (signing in with the
	// browser's saved password, chosen by the operator) go through, and every
	// other host - every payment host - is refused.
	//
	// A job is the exception: the operator asked for it in conversation, and a
	// purchase they asked for reaches its payment there - the conversation
	// hands every browser task to a job. Refusing it outright made Coupang's
	// order sheet fail with "주문중 오류" four times and never asked anyone
	// (job 16, 2026-10-17). So a job's payment raises a card bound to that
	// job, and the job waits on it.
	if background {
		if s.backgroundLogin[host] {
			s.log.Info("백그라운드 작업의 로그인 요청을 허용했습니다", "host", p.Host, "method", p.Method)
			return verdict{decision: proposal.Approve, why: "login"}
		}
		if !isJob {
			s.log.Warn("백그라운드 작업 중 결제 도메인 요청을 묻지 않고 거부했습니다", "host", p.Host, "url", clampURL(p.URL))
			return verdict{decision: proposal.Reject, why: "background"}
		}
		if s.payments.allow(p.grantSite(), p.JobID, p.Event, p.Amount) || s.insideWindow(p) {
			s.log.Info("작업에 승인된 결제 흐름의 요청을 통과시켰습니다", "job", p.JobID, "host", host, "method", p.Method, "event", p.Event, "window", p.Window)
			return verdict{decision: proposal.Approve, why: "granted"}
		}
		card := s.raisePayment(ctx, p, asked)
		until := time.Now().Add(s.holdDur)
		s.jobs.PaymentRaised(p.JobID, card, until)
		s.log.Info("작업의 결제 요청을 멈추고 카드를 올렸습니다", "job", p.JobID, "card", card, "host", host, "event", p.Event)
		if s.holdFor(ctx, card, until) && s.payments.allow(p.grantSite(), p.JobID, p.Event, p.Amount) {
			s.log.Info("기다리는 동안 승인돼 멈춘 결제 요청을 그대로 보냈습니다", "job", p.JobID, "card", card, "host", host)
			return verdict{decision: proposal.Approve, card: card, why: "held"}
		}
		return verdict{decision: proposal.Reject, pending: card, card: card, why: "waiting"}
	}

	// The second half of an approval: the operator said yes, and the browser
	// is back making the payment. Approval covers the site: one payment can
	// cross several of its hosts (checkout, then pay.coupang.com for the PIN).
	if s.payments.allow(p.grantSite(), 0, p.Event, p.Amount) || s.insideWindow(p) {
		s.log.Info("승인된 결제 흐름의 요청을 통과시켰습니다", "host", host, "method", p.Method, "window", p.Window)
		return verdict{decision: proposal.Approve, why: "granted"}
	}
	card := s.raisePayment(ctx, p, asked)
	if s.holdFor(ctx, card, time.Now().Add(s.holdDur)) && s.payments.allow(p.grantSite(), 0, p.Event, p.Amount) {
		s.log.Info("기다리는 동안 승인돼 멈춘 결제 요청을 그대로 보냈습니다", "card", card, "host", host)
		return verdict{decision: proposal.Approve, card: card, why: "held"}
	}
	return verdict{decision: proposal.Reject, pending: card, card: card, why: "waiting"}
}

// insideWindow is a 네이버페이 request of a payer whose approved payment
// window is open, from a page that window opened: the easy payment picked
// inside the window, which its card already covers. Only while that window
// is open - not before the payer is back at it, nor after its time - and
// only from the window's own popup, so an approval at a PG does not open
// 네이버페이 for anything else - a 네이버페이 order in a tab of its own is
// another payment, even within the ten minutes.
func (s *Server) insideWindow(p Payment) bool {
	return !p.Window && p.FromWindow && siteOf(p.Host) == naverPaySite && s.payments.isOpen(windowSite, p.JobID)
}

// paymentHold is how long a stopped payment request waits for its card
// before it is refused: long enough for an operator who has the phone in
// hand, short enough that a page and a run are not left hanging. The guard
// waits for the answer a little longer than this (JARVIS_DECISION_TIMEOUT).
const paymentHold = 2 * time.Minute

// paymentHolds are the stopped requests held while their card waits, by
// card: one payment can stop more than one request (two PG hosts).
type paymentHolds struct {
	mu    sync.Mutex
	cards map[string]*held
}

type held struct {
	done     chan struct{}
	waiting  int
	released bool
	passes   bool
}

func (h *paymentHolds) join(card string) *held {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cards == nil {
		h.cards = map[string]*held{}
	}
	w := h.cards[card]
	if w == nil {
		w = &held{done: make(chan struct{})}
		h.cards[card] = w
	}
	w.waiting++
	return w
}

// leave is a held request done waiting, and whether it goes on: it does if
// the card was decided while it waited and the decision lets it.
func (h *paymentHolds) leave(card string, w *held) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.waiting--
	if w.waiting <= 0 && h.cards[card] == w {
		delete(h.cards, card)
	}
	return w.released && w.passes
}

// release hands a card's decision to the requests held on it, and says
// whether there were any: if not, the payer takes the step again.
func (h *paymentHolds) release(card string, passes bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	w := h.cards[card]
	if w == nil || w.released {
		return false
	}
	w.released, w.passes = true, passes
	delete(h.cards, card)
	close(w.done)
	return true
}

// holdFor holds a stopped request until its card is decided or until, and
// says whether it goes on.
func (s *Server) holdFor(ctx context.Context, card string, until time.Time) bool {
	w := s.holds.join(card)
	t := time.NewTimer(time.Until(until))
	defer t.Stop()
	select {
	case <-w.done:
	case <-t.C:
	case <-ctx.Done():
	}
	return s.holds.leave(card, w)
}

// raisePayment answers a stopped payment with a card, and says which: the
// one already waiting for this site and this payer (the conversation, or one
// job) - a flow sends several requests, and the page retrying is the same
// question - or a new one. Another payer's card is not this one's: its
// decision goes to whoever raised it.
func (s *Server) raisePayment(ctx context.Context, p Payment, asked string) string {
	if waiting, ok := s.proposals.FindPendingWhere(func(q proposal.Proposal) bool {
		w := paymentOf(q.Action.Input)
		return q.Origin == proposal.OriginIntercept && w.grantSite() == p.grantSite() && w.JobID == p.JobID
	}); ok {
		return waiting.ID
	}
	var addr *SavedAddress
	if s.savedAddress != nil {
		actx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if a, ok := s.savedAddress(actx); ok {
			addr = &a
		}
		cancel()
	}
	raw, _ := json.Marshal(p)
	opened, _ := s.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginIntercept,
		Action: action.Action{Kind: "browser.payment", Input: raw},
		Card:   paymentCard(p, addr),
	})
	s.recordRaised(asked, p, opened)
	// In the conversation either way - a job's too, which used to float below
	// the thread only while it waited and was gone the moment it was
	// answered (2026-10-21 22:03). The card names the job it is for.
	if s.thread != nil {
		s.thread.AttachProposal(opened.ID)
	}
	return opened.ID
}

// One approval is one payment - for whoever raised the card (the
// conversation, or one job) on one site - and these are its bounds, the same
// for every payer:
const (
	// paymentWindow is how long the approved payment stays open, counted
	// from the payer's first request after the approval: the payer is back
	// at the payment. A job may start a while after its card was answered
	// (it waits for the conversation to be idle), so the clock starts when
	// it is back, not at the answer.
	paymentWindow = 10 * time.Minute
	// paymentWait is how long an approval waits for its payer to come back.
	paymentWait = time.Hour
)

// commits says whether a request the guard stopped is the one that charges:
// Coupang's 결제하기 (the order sheet's "order" event). An approval lets one
// through; a second is a second payment, and gets a card of its own. What
// comes after it in the same payment - the PIN pad - goes through in the
// window.
func commits(event string) bool { return event == "order" }

// paymentGrants are the payments a person approved: for whom (a job, or the
// conversation as job 0), on which site. One's approval is not another's: a
// job's card does not open the site to the conversation.
type paymentGrants struct {
	mu    sync.Mutex
	sites map[grantKey]*grant
	now   func() time.Time
}

type grantKey struct {
	site string
	job  int64
}

type grant struct {
	approved time.Time
	opened   time.Time // the payer's first request after approval; zero until then
	charged  bool      // the one 결제하기 has gone through
	// amount is what the approved card showed a payment window was handed.
	// A window has no 결제하기 to count: each order opens it anew, so the
	// approval is held to that amount - a window opened for another is
	// another payment. Empty when the guard could not read it.
	amount string
}

func (g *paymentGrants) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *paymentGrants) grant(site string, job int64, amount string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sites == nil {
		g.sites = map[grantKey]*grant{}
	}
	g.sites[grantKey{strings.ToLower(site), job}] = &grant{approved: g.clock(), amount: amount}
}

// allow says whether a stopped request of this payer's on this site is part
// of the payment approved for it. amount is what the request hands a
// payment window, if the guard read one.
func (g *paymentGrants) allow(site string, job int64, event, amount string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := grantKey{strings.ToLower(site), job}
	gr := g.sites[k]
	if gr == nil {
		return false
	}
	if amount != "" && gr.amount != "" && amount != gr.amount {
		return false
	}
	now := g.clock()
	if gr.opened.IsZero() {
		if now.After(gr.approved.Add(paymentWait)) {
			delete(g.sites, k)
			return false
		}
		gr.opened = now
	}
	if now.After(gr.opened.Add(paymentWindow)) {
		delete(g.sites, k)
		return false
	}
	if commits(event) {
		if gr.charged {
			return false
		}
		gr.charged = true
	}
	return true
}

// live says whether this payer has an approved payment that can still happen,
// on any site: approved and not yet come back to, or under way.
func (g *paymentGrants) live(job int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	for k, gr := range g.sites {
		if k.job != job {
			continue
		}
		if gr.opened.IsZero() {
			if !now.After(gr.approved.Add(paymentWait)) {
				return true
			}
		} else if !now.After(gr.opened.Add(paymentWindow)) {
			return true
		}
	}
	return false
}

// isOpen says whether this payer's approved payment on site is under way:
// the payer came back to it and its window has not run out. It opens
// nothing itself.
func (g *paymentGrants) isOpen(site string, job int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	gr := g.sites[grantKey{strings.ToLower(site), job}]
	return gr != nil && !gr.opened.IsZero() && !g.clock().After(gr.opened.Add(paymentWindow))
}

func paymentOf(raw json.RawMessage) Payment {
	var p Payment
	_ = json.Unmarshal(raw, &p)
	return p
}

// resolvePayment acts on a decision about a payment card: approved, the host
// is open for a few minutes and the assistant is told to take the step again;
// rejected, it is told not to pay.
func (s *Server) resolvePayment(p proposal.Proposal, d proposal.Decision) {
	if p.Origin != proposal.OriginIntercept {
		return
	}
	pay := paymentOf(p.Action.Input)
	if pay.JobID != 0 {
		s.resolveJobPayment(p, pay, d)
		return
	}
	if d == proposal.Approve {
		// The grant first: a held request that goes on asks it next.
		s.payments.grant(pay.grantSite(), 0, pay.Amount)
	}
	passed := s.holds.release(p.ID, d == proposal.Approve) && d == proposal.Approve
	if s.followUp == nil {
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
	s.followUp.Enqueue(fmt.Sprintf("[결제 반려됨] 운영자가 결제 카드 %s(%s)를 반려했습니다.%s\n"+
		"결제하지 마세요. 이유를 캐묻지 말고 짧게 확인만 하세요.", p.ID, pay.Host, why))
}

// resolveJobPayment is a decision on a card a job's run raised. Approved,
// the site opens to that job alone for a while and the job wakes to take the
// step again; rejected, it is told not to pay. Either way the job hears it,
// not the conversation.
func (s *Server) resolveJobPayment(p proposal.Proposal, pay Payment, d proposal.Decision) {
	approved := d == proposal.Approve
	if approved {
		// The grant first: a held request that goes on asks it next.
		s.payments.grant(pay.grantSite(), pay.JobID, pay.Amount)
	}
	// A held request goes on only while the job's run still holds the
	// browser: the window it opens is that run's to carry on, and with no run
	// its next requests would be the conversation's.
	passes := approved && s.jobs != nil && s.jobHolds(pay.JobID)
	passed := s.holds.release(p.ID, passes) && passes
	var text string
	switch {
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
	note := fmt.Sprintf("승인했습니다. 작업이 이 결제 한 건을 이어서 합니다(다시 진행한 때부터 %d분).", int(paymentWindow.Minutes()))
	if passed {
		note = "승인했습니다. 멈춰 있던 결제 요청이 그대로 나갔고, 작업이 이어서 합니다."
	}
	if err != nil {
		note = "승인했지만 작업을 깨우지 못했습니다: " + err.Error()
	}
	s.proposals.Settle(p.ID, proposal.Executed, note)
}

// jobHolds is whether job's run holds the browser now.
func (s *Server) jobHolds(job int64) bool {
	id, _, _, ok := s.jobs.RunningJob()
	return ok && id == job
}

// approvedScope is what an approval lets through, told to the payer the
// way paymentGrants holds it.
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
			"결제가 끝나면 무엇을 얼마에 결제했는지 짧게 알리세요.", p.Host, amountNote(p), int(paymentWindow.Minutes()))
	}
	return fmt.Sprintf("이 승인은 %s 의 결제 한 건입니다: 다시 진행을 시작한 때부터 %d분 안에, 결제하기는 한 번 통과합니다. "+
		"그 뒤의 다른 결제는 다시 카드를 올립니다. 결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요. "+
		"결제가 끝나면 무엇을 얼마에 결제했는지 짧게 알리세요.", siteOf(p.Host), int(paymentWindow.Minutes()))
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

// roadKey is a road address's road and building number with no space -
// "세종대로110" - the part an order sheet's address search goes by, and
// the part a wrong one differs in (2026-10-30: a building number one digit short).
func roadKey(address string) string {
	f := strings.Fields(trailingParens.ReplaceAllString(address, ""))
	if len(f) < 2 {
		return strings.Join(f, "")
	}
	return f[len(f)-2] + f[len(f)-1]
}

// addressLines is what a payment card says of where the order goes: the
// saved address, and a warning first when the job's report names another.
func addressLines(p Payment, addr *SavedAddress) (warn, saved string) {
	if addr == nil || addr.Address == "" {
		return "", ""
	}
	saved = "저장된 배송지: " + strings.TrimSpace(addr.Address+" "+addr.Detail)
	if addr.Recipient != "" {
		saved += " (" + addr.Recipient + ")"
	}
	summary := strings.Join(strings.Fields(p.JobSummary), "")
	if key := roadKey(addr.Address); key != "" && strings.Contains(summary, "배송지") && !strings.Contains(summary, key) {
		warn = "⚠ 작업이 남긴 배송지가 저장된 배송지와 다릅니다 - 승인하기 전에 주문서의 주소를 확인하세요."
	}
	if addr.Doubt != "" {
		line := "⚠ " + addr.Doubt + " - 지금 사는 곳이 맞는지 확인하고 승인하세요."
		if warn == "" {
			warn = line
		} else {
			warn += "\n" + line
		}
	}
	return warn, saved
}

// paymentCard says what is being asked. The login host is on the ask list
// because a session there is what lets a payment go through - but opening
// its sign-in page charges nothing, and a card that says "결제가 청구됩니다"
// for it teaches a person that the card's words are not to be believed.
func paymentCard(p Payment, addr *SavedAddress) action.Card {
	// The host is the part a person can actually judge; the path is there
	// for the case where two flows share a provider.
	where := p.Host
	if label := eventLabel(p.Event); label != "" {
		where += " · " + label
	}
	body := strings.TrimSpace(where + "\n" + clampURL(p.URL))
	if order := windowOrder(p); order != "" {
		body = order + "\n" + body
	}
	warn, saved := addressLines(p, addr)
	if saved != "" {
		body = saved + "\n" + body
	}
	if p.JobID != 0 {
		// What is being bought is the job's to say: the request carries no
		// amount (but a payment window's), and the job's last report is what
		// it said it is buying.
		job := "작업: " + p.JobTitle
		if s := strings.TrimSpace(p.JobSummary); s != "" {
			job += "\n작업이 남긴 현황: " + s
		}
		body = job + "\n\n" + body
	}
	if warn != "" {
		body = warn + "\n\n" + body
	}
	if p.Window {
		consequence := fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 결제창을 다시 연 때부터 %d분 동안 결제 한 건%s이 진행됩니다(결제창 안에서는 네이버페이·신용카드)",
			int(paymentWindow.Minutes()), amountNote(p))
		if p.JobID != 0 {
			consequence = fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 작업이 결제창을 다시 열어 결제 한 건%s을 진행합니다(연 때부터 %d분, 결제창 안에서는 네이버페이·신용카드)",
				amountNote(p), int(paymentWindow.Minutes()))
		}
		return action.Card{
			Title:       "결제창에서 결제합니다",
			Body:        body,
			Consequence: consequence,
		}
	}
	if site := siteOf(p.Host); site == "coupang.com" {
		consequence := fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 결제 한 건이 진행됩니다(다시 진행한 때부터 %d분 안에, 결제하기 한 번)",
			int(paymentWindow.Minutes()))
		if p.JobID != 0 {
			consequence = fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 작업이 결제 한 건을 다시 진행합니다(진행한 때부터 %d분 안에, 결제하기 한 번)",
				int(paymentWindow.Minutes()))
		}
		return action.Card{
			Title:       "쿠팡에서 쿠페이로 결제합니다",
			Body:        body,
			Consequence: consequence,
		}
	}
	if strings.EqualFold(p.Host, "nid.naver.com") {
		return action.Card{
			Title:       "네이버 로그인 화면을 엽니다",
			Body:        body,
			Consequence: "로그인만 진행됩니다 · 결제는 따로 묻습니다",
		}
	}
	consequence := fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 결제 한 건이 진행됩니다(다시 진행한 때부터 %d분 안에)", int(paymentWindow.Minutes()))
	if p.JobID != 0 {
		consequence = fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 작업이 결제 한 건을 다시 진행합니다(진행한 때부터 %d분 안에)", int(paymentWindow.Minutes()))
	}
	return action.Card{
		Title:       "결제를 진행합니다",
		Body:        body,
		Consequence: consequence,
	}
}

// windowOrder is what a payment window was handed, as the card shows it -
// the mall's own words, so cut short and kept on one line.
func windowOrder(p Payment) string {
	var lines []string
	if p.Amount != "" {
		lines = append(lines, "결제창이 받은 금액: "+won(p.Amount))
	}
	if item := strings.Join(strings.Fields(p.Item), " "); item != "" {
		if r := []rune(item); len(r) > 60 {
			item = string(r[:60]) + "…"
		}
		lines = append(lines, "결제창이 받은 상품: "+item)
	}
	return strings.Join(lines, "\n")
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
