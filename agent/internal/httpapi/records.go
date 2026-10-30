package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/gocommon/record"
)

// The payment path as records: what the guard asked, what it was answered,
// the card a question raised and how each card was answered. They are what a
// replay reads (scripts/harness), and they are written beside the decision,
// never in its way: a record that does not get written costs nothing but the
// record.
//
// Nothing here holds a value a person typed. An address is reduced to its
// shape (urlShape) - a query string can carry a session or an order - and a
// card's body and a form's fields stay out.
const (
	KindGuardQuery  = "guard.query"
	KindGuardAnswer = "guard.answer"
	// KindPaymentRequested and KindIntentAnswer are the names they keep in
	// the to-be structure, where mind reads them.
	KindPaymentRequested = "payment.requested"
	KindIntentAnswer     = "intent.answer"
	// KindPolicyDenied is a tool call the policy refused.
	KindPolicyDenied = "policy.denied"

	recordSource   = "agent"
	browserChannel = "browser"
)

// recordSeq keeps keys of the same instant apart.
var recordSeq atomic.Int64

func recordKey(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), recordSeq.Add(1))
}

// payerOf is who a stopped request is for: a job, or the conversation.
func payerOf(job int64) string {
	if job != 0 {
		return fmt.Sprintf("job:%d", job)
	}
	return "chat"
}

// recordQuery writes the question and returns its key.
func (s *Server) recordQuery(p Payment, background bool) string {
	key := recordKey("q")
	body := paymentPayload(p)
	if background {
		body["background"] = true
	}
	s.records.Write(record.Record{
		Kind: KindGuardQuery, Source: recordSource, SourceKey: key,
		Actor: payerOf(p.JobID), Channel: browserChannel, Subject: p.grantSite(),
		Tier: record.Durable, TraceID: payerOf(p.JobID),
		Payload: body,
	})
	return key
}

// paymentPayload is what the request said, without its values.
func paymentPayload(p Payment) map[string]any {
	out := map[string]any{"url": urlShape(p.URL), "method": p.Method, "host": p.Host}
	if p.Event != "" {
		out["event"] = p.Event
	}
	if p.Window {
		out["window"] = true
	}
	if p.Amount != "" {
		out["amount"] = p.Amount
	}
	if p.Item != "" {
		out["item"] = p.Item
	}
	if p.FromWindow {
		out["fromWindow"] = true
	}
	if p.JobID != 0 {
		out["job"] = p.JobID
	}
	return out
}

// recordAnswer writes what the question came to.
func (s *Server) recordAnswer(asked string, p Payment, v verdict) {
	s.records.Write(record.Record{
		Kind: KindGuardAnswer, Source: recordSource, SourceKey: recordKey("a"),
		Actor: recordSource, Channel: browserChannel, Subject: p.grantSite(),
		Tier: record.Durable, TraceID: payerOf(p.JobID), CausationID: asked,
		Payload: map[string]any{"decision": string(v.decision), "why": v.why, "card": v.card},
	})
}

// recordRaised writes a new payment card.
func (s *Server) recordRaised(asked string, p Payment, opened *proposal.Proposal) {
	if opened == nil {
		return
	}
	body := paymentPayload(p)
	body["card"] = opened.ID
	body["title"] = opened.Card.Title
	body["site"] = p.grantSite()
	s.records.Write(record.Record{
		Kind: KindPaymentRequested, Source: recordSource, SourceKey: "requested:" + opened.ID,
		Actor: payerOf(p.JobID), Channel: browserChannel, Subject: p.grantSite(),
		Tier: record.Durable, TraceID: payerOf(p.JobID), CausationID: asked,
		Payload: body,
	})
}

// recordDecision writes how a card was answered, every card's: what the
// answers come to is what the result ledger starts from. A payment's is
// durable. Other cards name a person's plans in their titles, so they stay
// where they can be deleted. The reason is the operator's own words: only
// whether there was one.
func (s *Server) recordDecision(p proposal.Proposal, d proposal.Decision) {
	tier, trace, subject, cause := record.Raw, "chat", p.Action.Kind, ""
	if p.Origin == proposal.OriginIntercept {
		pay := paymentOf(p.Action.Input)
		tier, trace, subject, cause = record.Durable, payerOf(pay.JobID), pay.grantSite(), "requested:"+p.ID
	}
	s.records.Write(record.Record{
		Kind: KindIntentAnswer, Source: recordSource, SourceKey: "answer:" + p.ID,
		Actor: "operator", Channel: "console", Subject: subject,
		Tier: tier, TraceID: trace, CausationID: cause,
		Payload: map[string]any{
			"card": p.ID, "origin": string(p.Origin), "action": p.Action.Kind, "title": p.Card.Title,
			"decision": string(d), "hasReason": p.Reason != "",
		},
	})
}

// urlShape is an address without its values: host and path, with the names
// of its parameters. resultCode keeps its value - it says how a payment came
// back and carries nobody's data. browser/guard.py url_shape is the same.
func urlShape(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	path := u.EscapedPath()
	if i := strings.IndexByte(path, ';'); i >= 0 {
		path = path[:i] + ";…"
	}
	var names []string
	for _, pair := range strings.Split(u.RawQuery, "&") {
		name, value, _ := strings.Cut(pair, "=")
		if name == "" {
			continue
		}
		if strings.EqualFold(name, "resultcode") {
			names = append(names, name+"="+clip(value, 12))
		} else {
			names = append(names, name)
		}
		if len(names) == 10 {
			break
		}
	}
	shape := u.Host + path
	if len(names) > 0 {
		shape += "?" + strings.Join(names, "&")
	}
	return shape
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// guardRecord is what the guard did on its own - a stopped request it sent
// on or refused without asking, the way a payment went. The agent is its
// only way out of the browser network.
type guardRecord struct {
	Kind    string          `json:"kind"`
	Key     string          `json:"key"`
	At      time.Time       `json:"at"`
	Subject string          `json:"subject"`
	Payload json.RawMessage `json:"payload"`
}

// guardKinds is what the guard may report, and how long each is kept: what
// it refused is a decision, the rest is the way there.
var guardKinds = map[string]string{
	"guard.flow":    record.Raw,
	"guard.passed":  record.Raw,
	"guard.refused": record.Durable,
}

const guardBatch = 100

func (s *Server) postGuardRecords(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "인증에 실패했습니다.")
		return
	}
	var body struct {
		Records []guardRecord `json:"records"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	if len(body.Records) > guardBatch {
		writeErr(w, http.StatusBadRequest, "한 번에 너무 많습니다.")
		return
	}
	trace := "chat"
	if s.jobs != nil {
		if id, _, _, ok := s.jobs.RunningJob(); ok {
			trace = payerOf(id)
		}
	}
	for _, g := range body.Records {
		tier, ok := guardKinds[g.Kind]
		if !ok || g.Key == "" {
			writeErr(w, http.StatusBadRequest, "모르는 기록입니다.")
			return
		}
		s.records.Write(record.Record{
			Kind: g.Kind, Source: "guard", SourceKey: g.Key,
			Actor: trace, Channel: browserChannel, Subject: g.Subject,
			OccurredAt: g.At, Tier: tier, TraceID: trace,
			Payload: g.Payload,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}
