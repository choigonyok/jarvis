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

	// A background task has no one behind it. Asking a person here would put
	// a card in front of someone who did not start anything - which is what a
	// background order lookup on 네이버페이 did (2026-10-07). So it is decided
	// on the spot: the sign-in hosts it is allowed to use (signing in with the
	// browser's saved password, chosen by the operator) go through, and every
	// other host - every payment host - is refused.
	if bg, ok := s.runner.(interface{ InBackground() bool }); ok && bg.InBackground() {
		if s.backgroundLogin[strings.ToLower(p.Host)] {
			s.log.Info("백그라운드 작업의 로그인 요청을 허용했습니다", "host", p.Host, "method", p.Method)
			writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Approve)})
			return
		}
		s.log.Warn("백그라운드 작업 중 결제 도메인 요청을 묻지 않고 거부했습니다", "host", p.Host, "url", clampURL(p.URL))
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject)})
		return
	}

	host := strings.ToLower(p.Host)
	// The second half of an approval: the operator said yes, and the browser
	// is back making the payment. Approval covers the site: one payment can
	// cross several of its hosts (checkout, then pay.coupang.com for the PIN).
	if s.payments.granted(siteOf(host)) {
		s.log.Info("승인된 결제 흐름의 요청을 통과시켰습니다", "host", host, "method", p.Method)
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Approve)})
		return
	}
	raw, _ := json.Marshal(p)
	// One card per host while it waits: a flow sends several requests, and
	// the page retrying is the same question.
	if waiting, ok := s.proposals.FindPendingWhere(func(q proposal.Proposal) bool {
		return q.Origin == proposal.OriginIntercept && siteOf(paymentHost(q.Action.Input)) == siteOf(host)
	}); ok {
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject), "pending": waiting.ID})
		return
	}
	opened, _ := s.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginIntercept,
		Action: action.Action{Kind: "browser.payment", Input: raw},
		Card:   paymentCard(p),
	})
	if s.thread != nil {
		s.thread.AttachProposal(opened.ID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject), "pending": opened.ID})
}

// paymentGrantTTL is how long an approved payment host stays open: the time
// it takes the assistant to come back and take the step again.
const paymentGrantTTL = 10 * time.Minute

// paymentGrants are the hosts a person approved a payment on, until when.
type paymentGrants struct {
	mu    sync.Mutex
	hosts map[string]time.Time
}

func (g *paymentGrants) grant(host string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hosts == nil {
		g.hosts = map[string]time.Time{}
	}
	g.hosts[strings.ToLower(host)] = time.Now().Add(paymentGrantTTL)
}

func (g *paymentGrants) granted(host string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.hosts[strings.ToLower(host)]
	if ok && time.Now().After(until) {
		delete(g.hosts, strings.ToLower(host))
		return false
	}
	return ok
}

func paymentHost(raw json.RawMessage) string {
	var p Payment
	_ = json.Unmarshal(raw, &p)
	return p.Host
}

// resolvePayment acts on a decision about a payment card: approved, the host
// is open for a few minutes and the assistant is told to take the step again;
// rejected, it is told not to pay.
func (s *Server) resolvePayment(p proposal.Proposal, d proposal.Decision) {
	if p.Origin != proposal.OriginIntercept || s.followUp == nil {
		return
	}
	var pay Payment
	_ = json.Unmarshal(p.Action.Input, &pay)
	if d == proposal.Approve {
		s.payments.grant(siteOf(pay.Host))
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

// paymentCard says what is being asked. The login host is on the ask list
// because a session there is what lets a payment go through - but opening
// its sign-in page charges nothing, and a card that says "결제가 청구됩니다"
// for it teaches a person that the card's words are not to be believed.
func paymentCard(p Payment) action.Card {
	// The host is the part a person can actually judge; the path is there
	// for the case where two flows share a provider.
	body := strings.TrimSpace(p.Host + "\n" + clampURL(p.URL))
	if site := siteOf(p.Host); site == "coupang.com" {
		return action.Card{
			Title:       "쿠팡에서 쿠페이로 결제합니다",
			Body:        body,
			Consequence: "결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 10분 동안 쿠팡 결제는 다시 묻지 않습니다",
		}
	}
	if strings.EqualFold(p.Host, "nid.naver.com") {
		return action.Card{
			Title:       "네이버 로그인 화면을 엽니다",
			Body:        body,
			Consequence: "로그인만 진행됩니다 · 결제는 따로 묻습니다",
		}
	}
	return action.Card{
		Title:       "결제를 진행합니다",
		Body:        body,
		Consequence: "결제가 청구됩니다 · 되돌릴 수 없습니다",
	}
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
