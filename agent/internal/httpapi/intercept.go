package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// Payment is a request the browser container stopped on its way out, because
// it was headed for a payment provider. The call blocks here until a person
// decides, exactly as the CLI's permission gate does - the difference is only
// where the interception happened.
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

	raw, _ := json.Marshal(p)
	opened, decisions := s.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginIntercept,
		Action: action.Action{Kind: "browser.payment", Input: raw},
		Card:   paymentCard(p),
	})

	// Timing out is a rejection. A payment that goes through because nobody
	// was watching is the failure this whole path exists to prevent.
	select {
	case d := <-decisions:
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(d)})
	case <-time.After(s.approvalWait):
		s.proposals.Abandon(opened.ID, "제한 시간 안에 결정이 나지 않았습니다.")
		writeJSON(w, http.StatusOK, map[string]string{"decision": string(proposal.Reject)})
	case <-r.Context().Done():
		// The browser hung up - the request it was holding is gone with it.
		s.proposals.Abandon(opened.ID, "요청이 취소되었습니다.")
	}
}

// paymentCard says what is being asked. The login host is on the ask list
// because a session there is what lets a payment go through - but opening
// its sign-in page charges nothing, and a card that says "결제가 청구됩니다"
// for it teaches a person that the card's words are not to be believed.
func paymentCard(p Payment) action.Card {
	// The host is the part a person can actually judge; the path is there
	// for the case where two flows share a provider.
	body := strings.TrimSpace(p.Host + "\n" + clampURL(p.URL))
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
