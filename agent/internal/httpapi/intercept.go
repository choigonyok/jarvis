package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
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
// raised, as the CLI's permission gate does: approving it grants the host for
// a few minutes and tells the assistant to take the step again; rejecting it
// tells the assistant not to.
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
	// JobID is the background job whose run reached the payment; zero is the
	// conversation. Set here from the run holding the browser, never taken
	// from the request.
	JobID      int64  `json:"jobId,omitempty"`
	JobTitle   string `json:"jobTitle,omitempty"`
	JobSummary string `json:"jobSummary,omitempty"`
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
		if s.payments.granted(siteOf(host), p.JobID) {
			s.log.Info("작업에 승인된 결제 흐름의 요청을 통과시켰습니다", "job", p.JobID, "host", host, "method", p.Method, "event", p.Event)
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
	if s.payments.granted(siteOf(host), 0) {
		s.log.Info("승인된 결제 흐름의 요청을 통과시켰습니다", "host", host, "method", p.Method)
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Approve)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject), "pending": s.raisePayment(p)})
}

// raisePayment answers a stopped payment with a card, and says which: the
// one already waiting for this site and this payer (the conversation, or one
// job) - a flow sends several requests, and the page retrying is the same
// question - or a new one. Another payer's card is not this one's: its
// decision goes to whoever raised it.
func (s *Server) raisePayment(p Payment) string {
	if waiting, ok := s.proposals.FindPendingWhere(func(q proposal.Proposal) bool {
		w := paymentOf(q.Action.Input)
		return q.Origin == proposal.OriginIntercept && siteOf(w.Host) == siteOf(p.Host) && w.JobID == p.JobID
	}); ok {
		return waiting.ID
	}
	raw, _ := json.Marshal(p)
	opened, _ := s.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginIntercept,
		Action: action.Action{Kind: "browser.payment", Input: raw},
		Card:   paymentCard(p),
	})
	// A job's card is the job's: the 작업 tab and the phone show it, and the
	// conversation, which did not stop at a payment, does not.
	if s.thread != nil && p.JobID == 0 {
		s.thread.AttachProposal(opened.ID)
	}
	return opened.ID
}

// paymentGrantTTL is how long an approved payment host stays open: the time
// it takes the assistant to come back and take the step again.
const paymentGrantTTL = 10 * time.Minute

// jobPaymentGrantTTL is the same for a job, which comes back on its own
// schedule: the scheduler starts it only once the conversation has been
// quiet for a while, and the run has to open the order sheet again.
const jobPaymentGrantTTL = 30 * time.Minute

// paymentGrants are the sites a person approved a payment on, for whom (a
// job, or the conversation as job 0), until when. One's approval is not
// another's: a job's card does not open the site to the conversation.
type paymentGrants struct {
	mu    sync.Mutex
	sites map[grantKey]time.Time
}

type grantKey struct {
	site string
	job  int64
}

func (g *paymentGrants) grant(site string, job int64, ttl time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sites == nil {
		g.sites = map[grantKey]time.Time{}
	}
	g.sites[grantKey{strings.ToLower(site), job}] = time.Now().Add(ttl)
}

func (g *paymentGrants) granted(site string, job int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := grantKey{strings.ToLower(site), job}
	until, ok := g.sites[k]
	if ok && time.Now().After(until) {
		delete(g.sites, k)
		return false
	}
	return ok
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
		s.payments.grant(siteOf(pay.Host), 0, paymentGrantTTL)
		s.proposals.Settle(p.ID, proposal.Executed, "승인했습니다. 결제를 다시 진행합니다.")
		s.followUp.Enqueue(fmt.Sprintf("[결제 승인됨] 운영자가 결제 카드 %s(%s)를 승인했습니다. 막혔던 결제 요청은 아직 보내지지 않았습니다.\n"+
			"지금 브라우저에서 같은 결제 단계를 다시 진행하세요(같은 결제 버튼을 다시 누르는 것). %d분 동안 %s 의 결제 요청은 다시 묻지 않습니다. "+
			"결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요. 결제가 끝나면 무엇을 얼마에 결제했는지 짧게 알리세요.",
			p.ID, pay.Host, int(paymentGrantTTL.Minutes()), siteOf(pay.Host)))
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
	site := siteOf(pay.Host)
	approved := d == proposal.Approve
	var text string
	if approved {
		s.payments.grant(site, pay.JobID, jobPaymentGrantTTL)
		text = fmt.Sprintf("[결제 승인됨] 운영자가 결제 카드 %s(%s)를 승인했습니다. 막혔던 결제 요청은 아직 보내지지 않았습니다.\n"+
			"주문 내역에 이 주문이 아직 없는지 먼저 확인한 뒤, 같은 결제 단계를 다시 진행하세요(결제하기를 한 번 누르면 이번에는 통과합니다). "+
			"%d분 동안 이 작업의 %s 결제는 다시 묻지 않습니다. 결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요.",
			p.ID, pay.Host, int(jobPaymentGrantTTL.Minutes()), site)
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
		s.log.Warn("결제 카드 결정을 작업에 전하지 못했습니다", "job", pay.JobID, "card", p.ID, "err", err)
	}
	if !approved {
		return
	}
	note := fmt.Sprintf("승인했습니다. 작업이 결제를 이어서 합니다(%d분 동안 열려 있습니다).", int(jobPaymentGrantTTL.Minutes()))
	if err != nil {
		note = "승인했지만 작업을 깨우지 못했습니다: " + err.Error()
	}
	s.proposals.Settle(p.ID, proposal.Executed, note)
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
	if p.JobID != 0 {
		// What is being bought is the job's to say: the request carries no
		// amount, and the job's last report is what it said it is buying.
		job := "작업: " + p.JobTitle
		if s := strings.TrimSpace(p.JobSummary); s != "" {
			job += "\n작업이 남긴 현황: " + s
		}
		body = job + "\n\n" + body
	}
	if site := siteOf(p.Host); site == "coupang.com" {
		consequence := "결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 10분 동안 쿠팡 결제는 다시 묻지 않습니다"
		if p.JobID != 0 {
			consequence = fmt.Sprintf("결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 이 작업이 결제를 다시 진행하고, %d분 동안 이 작업의 쿠팡 결제는 다시 묻지 않습니다",
				int(jobPaymentGrantTTL.Minutes()))
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
	consequence := "결제가 청구됩니다 · 되돌릴 수 없습니다"
	if p.JobID != 0 {
		consequence += " · 승인하면 이 작업이 결제를 다시 진행합니다"
	}
	return action.Card{
		Title:       "결제를 진행합니다",
		Body:        body,
		Consequence: consequence,
	}
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
