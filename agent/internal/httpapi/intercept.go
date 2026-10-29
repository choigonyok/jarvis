package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// Payment is a request the browser container stopped on its way out, because
// it was headed for a payment provider.
//
// Nothing waits here for a person. Holding the request until a decision held
// the browser, and the turn driving it, for as long as the card sat unread
// (up to 40 minutes). Instead the request is refused at once with a card
// raised, as the CLI's permission gate does: approving it lets that one
// payment through (paymentGrants) and tells the payer to take the step
// again; rejecting it tells the payer not to.
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
	// PaymentRaised tells the job a payment card is waiting for it.
	PaymentRaised(job int64, card string)
	// PaymentDecided hands the decision to the job and wakes it.
	PaymentDecided(job int64, card string, approved bool, text string) error
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
	if bg, ok := s.runner.(interface{ InBackground() bool }); ok && bg.InBackground() {
		if s.backgroundLogin[host] {
			s.log.Info("백그라운드 작업의 로그인 요청을 허용했습니다", "host", p.Host, "method", p.Method)
			writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Approve)})
			return
		}
		isJob := false
		if s.jobs != nil {
			p.JobID, p.JobTitle, p.JobSummary, isJob = s.jobs.RunningJob()
		}
		if !isJob {
			s.log.Warn("백그라운드 작업 중 결제 도메인 요청을 묻지 않고 거부했습니다", "host", p.Host, "url", clampURL(p.URL))
			writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject)})
			return
		}
		if s.payments.allow(p.grantSite(), p.JobID, p.Event, p.Amount) || s.insideWindow(p) {
			s.log.Info("작업에 승인된 결제 흐름의 요청을 통과시켰습니다", "job", p.JobID, "host", host, "method", p.Method, "event", p.Event, "window", p.Window)
			writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Approve)})
			return
		}
		card := s.raisePayment(p)
		s.jobs.PaymentRaised(p.JobID, card)
		s.log.Info("작업의 결제 요청을 막고 카드를 올렸습니다", "job", p.JobID, "card", card, "host", host, "event", p.Event)
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject), "pending": card})
		return
	}

	// The second half of an approval: the operator said yes, and the browser
	// is back making the payment. Approval covers the site: one payment can
	// cross several of its hosts (checkout, then pay.coupang.com for the PIN).
	if s.payments.allow(p.grantSite(), 0, p.Event, p.Amount) || s.insideWindow(p) {
		s.log.Info("승인된 결제 흐름의 요청을 통과시켰습니다", "host", host, "method", p.Method, "window", p.Window)
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Approve)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject), "pending": s.raisePayment(p)})
}

// insideWindow is a 네이버페이 request of a payer whose approved payment
// window is open: the easy payment picked inside the window, which its card
// already covers. Only while that window is open - not before the payer is
// back at it, nor after its time - so an approval at a PG does not open
// 네이버페이 for anything else.
func (s *Server) insideWindow(p Payment) bool {
	return !p.Window && siteOf(p.Host) == naverPaySite && s.payments.isOpen(windowSite, p.JobID)
}

// raisePayment answers a stopped payment with a card, and says which: the
// one already waiting for this site and this payer (the conversation, or one
// job) - a flow sends several requests, and the page retrying is the same
// question - or a new one. Another payer's card is not this one's: its
// decision goes to whoever raised it.
func (s *Server) raisePayment(p Payment) string {
	if waiting, ok := s.proposals.FindPendingWhere(func(q proposal.Proposal) bool {
		w := paymentOf(q.Action.Input)
		return q.Origin == proposal.OriginIntercept && w.grantSite() == p.grantSite() && w.JobID == p.JobID
	}); ok {
		return waiting.ID
	}
	raw, _ := json.Marshal(p)
	opened, _ := s.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginIntercept,
		Action: action.Action{Kind: "browser.payment", Input: raw},
		Card:   paymentCard(p),
	})
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
	if s.followUp == nil {
		return
	}
	if d == proposal.Approve {
		s.payments.grant(pay.grantSite(), 0, pay.Amount)
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
	var text string
	if approved {
		s.payments.grant(pay.grantSite(), pay.JobID, pay.Amount)
		step := "결제하기를 한 번 누르면 이번에는 통과합니다"
		if pay.Window {
			step = "주문서에서 결제하기를 다시 누르면 이번에는 결제창이 열립니다"
		}
		text = fmt.Sprintf("[결제 승인됨] 운영자가 결제 카드 %s(%s)를 승인했습니다. 막혔던 결제 요청은 아직 보내지지 않았습니다.\n"+
			"주문 내역에 이 주문이 아직 없는지 먼저 확인한 뒤, 같은 결제 단계를 다시 진행하세요(%s). %s",
			p.ID, pay.Host, step, approvedScope(pay))
	} else {
		text = fmt.Sprintf("[결제 반려됨] 운영자가 결제 카드 %s(%s)를 반려했습니다.", p.ID, pay.Host)
		if p.Reason != "" {
			text += "\n반려 이유: " + p.Reason
		}
		text += "\n결제하지 마세요."
	}
	if s.jobs == nil {
		return
	}
	err := s.jobs.PaymentDecided(pay.JobID, p.ID, approved, text)
	if err != nil {
		// The card keeps the decision; the jobs' sweep hands it on.
		s.log.Warn("결제 카드 결정을 작업에 전하지 못했습니다", "job", pay.JobID, "card", p.ID, "err", err)
	}
	if !approved {
		return
	}
	note := fmt.Sprintf("승인했습니다. 작업이 이 결제 한 건을 이어서 합니다(다시 진행한 때부터 %d분).", int(paymentWindow.Minutes()))
	if err != nil {
		note = "승인했지만 작업을 깨우지 못했습니다: " + err.Error()
	}
	s.proposals.Settle(p.ID, proposal.Executed, note)
}

// approvedScope is what an approval lets through, told to the payer the
// way paymentGrants holds it.
func approvedScope(p Payment) string {
	if p.Window {
		// The window's ways out that are not 네이버페이 the guard blocks only as
		// far as it knows them; the payer is told which one to take.
		return fmt.Sprintf("이 승인은 결제창(%s)에서 하는 결제 한 건%s입니다: 결제창을 다시 연 때부터 %d분 동안 결제창·카드사 창과, 그 안에서 고른 네이버페이가 통과합니다. "+
			"금액이 다른 주문의 결제창은 다시 카드를 올립니다. "+
			"결제창에서는 간편결제의 네이버페이나 신용카드(운영자 지시의 카드, 없으면 하나카드)로 결제하세요 - 카드사 창에서는 그 카드사의 결제(앱카드)로 하고, 카드사 창 안의 네이버페이는 쓰지 않습니다(인증이 끝나도 카드사 창이 넘어가지 않았습니다). "+
			"다른 간편결제, 휴대폰 결제, 계좌이체, 가상계좌, 카드번호 직접 입력은 하지 않습니다. "+
			"네이버페이 결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요 - 넣지 못했다고 하면 다시 부르지도 누르지도 말고 알리세요. "+
			"카드사 앱 인증(폰 푸시·QR)이 나오면 운영자가 폰에서 승인해야 합니다: 알리고, 그 화면을 그대로 두고 기다리세요. "+
			"결제가 끝나면 무엇을 얼마에 결제했는지 짧게 알리세요.", p.Host, amountNote(p), int(paymentWindow.Minutes()))
	}
	return fmt.Sprintf("이 승인은 %s 의 결제 한 건입니다: 다시 진행을 시작한 때부터 %d분 안에, 결제하기는 한 번 통과합니다. "+
		"그 뒤의 다른 결제는 다시 카드를 올립니다. 결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요. "+
		"결제가 끝나면 무엇을 얼마에 결제했는지 짧게 알리세요.", siteOf(p.Host), int(paymentWindow.Minutes()))
}

// paymentCard says what is being asked. The login host is on the ask list
// because a session there is what lets a payment go through - but opening
// its sign-in page charges nothing, and a card that says "결제가 청구됩니다"
// for it teaches a person that the card's words are not to be believed.
func paymentCard(p Payment) action.Card {
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

// siteOf is a host's site, the unit a payment approval covers: its last two
// labels (coupang.com), or three under a second-level domain like co.kr.
func siteOf(host string) string {
	labels := strings.Split(strings.Trim(strings.ToLower(host), "."), ".")
	n := 2
	if len(labels) >= 3 && len(labels[len(labels)-1]) == 2 {
		switch labels[len(labels)-2] {
		case "co", "or", "go", "ne", "ac", "re", "pe", "com", "net", "org":
			n = 3
		}
	}
	if len(labels) < n {
		return strings.Join(labels, ".")
	}
	return strings.Join(labels[len(labels)-n:], ".")
}
