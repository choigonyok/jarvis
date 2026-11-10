package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/holder"
	"github.com/choigonyok/jarvis/gocommon/record"
)

// Leave is a page of a job's run on its way out of the job's sites, which
// the browser's guard stopped (browser/guard.py leaves_job) and asks about.
//
// The guard keeps a job inside its sites: the job runs with nobody watching,
// in the browser that is logged in to the operator's shops. But a payment can
// finish outside the mall's own domain - musinsa.com hands an order to
// pay.musinsapayments.com with the payment window's result, and the order is
// made there. A page stopped on that way loses the payment: the window said
// 0000, and no order came of it.
//
// So the guard asks. A site the operator added to the payment flow on a card
// goes through. While the job pays - an approved payment of its under way
// (holder.Payments.UnderWay) - or when a payment window opened the page's tab
// or its address reads as a payment's, the page is held for holder.Hold and a
// card asks whether its site joins the payment flow: approved in that time,
// the page goes on and the payment with it; approved later, the site is added
// and the job is told to look at its orders and pay again. Anything else is
// refused as the guard would have on its own, and the run is told: the
// guard's notice on the page is not always where the run looks.
type Leave struct {
	URL    string `json:"url"`
	Method string `json:"method"`
	Host   string `json:"host"`
	// From is the shape of the address the page was at before (no values),
	// when the guard knew it.
	From string `json:"from,omitempty"`
	// FromWindow: a payment window opened the page's tab.
	FromWindow bool `json:"fromWindow,omitempty"`
}

// FlowHost is what a flow card asks: whether Site joins the payment flow -
// the sites a job's page may go to whatever the job's own - and the page that
// was stopped going there.
type FlowHost struct {
	// Site is what an approval adds: the stopped page's registrable domain,
	// every address under it.
	Site string `json:"site"`
	Host string `json:"host"`
	// URL and From are the shapes of the address the page went to and of the
	// one it left (urlShape): no values.
	URL  string `json:"url"`
	From string `json:"from,omitempty"`
	// Paying is the site of the job's payment under way when the page left;
	// empty when the page only looked like a payment's.
	Paying   string `json:"paying,omitempty"`
	JobID    int64  `json:"jobId"`
	JobTitle string `json:"jobTitle,omitempty"`
}

func flowOf(raw json.RawMessage) FlowHost {
	var f FlowHost
	_ = json.Unmarshal(raw, &f)
	return f
}

func (s *Server) postSite(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "인증에 실패했습니다.")
		return
	}
	var l Leave
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&l); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	host := leaveHost(l)
	if host == "" {
		writeErr(w, http.StatusBadRequest, "host가 비어 있습니다.")
		return
	}
	background := false
	if bg, ok := s.runner.(interface{ InBackground() bool }); ok {
		background = bg.InBackground()
	}
	var job int64
	var title string
	isJob := false
	if background && s.jobs != nil {
		job, title, _, isJob = s.jobs.RunningJob()
	}

	v := s.judgeLeave(r.Context(), l, host, job, title, isJob)
	s.recordLeave(l, host, job, v)

	out := map[string]string{"decision": string(v.decision), "why": v.why}
	if v.pending != "" {
		out["pending"] = v.pending
	}
	if v.card != "" {
		out["card"] = v.card
	}
	writeJSON(w, http.StatusOK, out)
}

// leaveHost is the host the page goes to: from its address, else as the
// guard named it - without a port either way.
func leaveHost(l Leave) string {
	if u, err := url.Parse(l.URL); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	if u, err := url.Parse("//" + strings.TrimSpace(l.Host)); err == nil {
		return strings.ToLower(u.Hostname())
	}
	return ""
}

// judgeLeave decides one page on its way out of a job's sites. why says how,
// in a word the guard reads (and a record keeps): flow (a site of the payment
// flow) | held (the card was approved while the page was held) | waiting
// (held until the card ran out of time; it still waits) | turned (the
// operator turned the card down) | site (refused, no card).
func (s *Server) judgeLeave(ctx context.Context, l Leave, host string, job int64, title string, isJob bool) verdict {
	site := siteOf(host)
	if s.flows.Has(site) {
		s.log.Info("결제 흐름에 더한 곳이라 작업의 사이트 밖 이동을 열었습니다", "site", site, "job", job, "url", urlShape(l.URL))
		return verdict{decision: proposal.Approve, why: "flow"}
	}
	if !isJob {
		// No job's run behind the page - a background sign-in held to its
		// site, a run already over: the guard's refusal stands, and nobody is
		// there to tell.
		return verdict{decision: proposal.Reject, why: "site"}
	}
	paying, under := s.payments.UnderWay(job)
	if !under && !l.FromWindow && !paymentLike(l.URL) {
		s.jobs.Stopped(job, "막은 이동: "+urlShape(l.URL)+" (작업의 사이트 밖)", siteRefusal(l))
		s.log.Info("작업의 사이트 밖 이동을 막고 작업에 알렸습니다", "job", job, "site", site, "url", urlShape(l.URL))
		return verdict{decision: proposal.Reject, why: "site"}
	}

	f := FlowHost{Site: site, Host: host, URL: urlShape(l.URL), From: l.From, Paying: paying, JobID: job, JobTitle: title}
	card := s.raiseFlow(f)
	until := time.Now().Add(s.holdDur)
	s.jobs.FlowRaised(job, card, site, until)
	s.jobs.Stopped(job, "결제 흐름 카드: "+site+" ("+f.URL+") - 승인을 기다리는 동안 이동을 붙잡았습니다", flowHeld(f))
	s.log.Info("작업의 사이트 밖 이동을 붙잡고 결제 흐름 카드를 올렸습니다", "job", job, "card", card, "site", site, "paying", paying)
	if s.payments.Wait(ctx, card, until) {
		s.log.Info("기다리는 동안 승인돼 붙잡은 이동을 보냈습니다", "job", job, "card", card, "site", site)
		return verdict{decision: proposal.Approve, card: card, why: "held"}
	}
	if p, ok := s.proposals.Get(card); ok && p.State != proposal.Pending {
		// Turned down by the operator - or put away by the run behind it
		// (Abandon leaves a note), or approved once the run had let the
		// browser go: refused either way, said apart on the page.
		why := "site"
		if p.State == proposal.Rejected && p.Note == "" {
			why = "turned"
		}
		return verdict{decision: proposal.Reject, card: card, why: why}
	}
	return verdict{decision: proposal.Reject, pending: card, card: card, why: "waiting"}
}

// paymentLike is an address that reads as a payment's: a host with "pay" in
// one of its labels (pay.musinsapayments.com), a path with "pay" or
// "checkout" in one of its parts (/mps/payment/…), or a payment's result
// handed back (resultCode). A job's page going there may be on a payment's
// way before any payment card - a mall whose payment page is its payment
// company's.
func paymentLike(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	for _, label := range strings.Split(strings.ToLower(u.Hostname()), ".") {
		if strings.Contains(label, "pay") {
			return true
		}
	}
	for _, part := range strings.Split(strings.ToLower(u.Path), "/") {
		if strings.Contains(part, "pay") || strings.Contains(part, "checkout") {
			return true
		}
	}
	for name := range u.Query() {
		if strings.EqualFold(name, "resultcode") {
			return true
		}
	}
	return false
}

// raiseFlow answers a held page with a flow card, and says which: the one
// already waiting for this site and this job - the page retried, or another
// page on the same way - or a new one.
func (s *Server) raiseFlow(f FlowHost) string {
	s.raising.Lock()
	defer s.raising.Unlock()
	if waiting, ok := s.proposals.FindPendingWhere(func(q proposal.Proposal) bool {
		if q.Action.Kind != holder.KindFlow {
			return false
		}
		w := flowOf(q.Action.Input)
		return w.Site == f.Site && w.JobID == f.JobID
	}); ok {
		return waiting.ID
	}
	raw, _ := json.Marshal(f)
	opened, _ := s.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginIntercept,
		Action: action.Action{Kind: holder.KindFlow, Input: raw},
		Card:   flowCard(f),
	})
	// In the conversation, where a job's payment card goes too: it stays
	// where it was raised once answered.
	if s.thread != nil {
		s.thread.AttachProposal(opened.ID)
	}
	return opened.ID
}

// flowCard says what is asked - that a site joins the payment flow, for
// every job - and why it came up: the payment the job was making, or an
// address that reads as a payment's; and the page that was stopped.
func flowCard(f FlowHost) action.Card {
	var lines []string
	if f.JobTitle != "" {
		lines = append(lines, "작업: "+f.JobTitle)
	}
	if f.Paying != "" {
		lines = append(lines, "진행 중인 결제: "+f.Paying+" (승인한 결제가 마무리하러 가는 길입니다)")
	} else {
		lines = append(lines, "물은 까닭: 결제 페이지로 보이는 주소입니다")
	}
	move := f.URL
	if f.From != "" {
		move = f.From + " → " + f.URL
	}
	lines = append(lines, "막힌 이동: "+move, "더하는 곳: "+f.Site+" 아래 모든 주소")
	return action.Card{
		Title: "결제 흐름에 더하기 · " + f.Site,
		Body:  strings.Join(lines, "\n"),
		Consequence: fmt.Sprintf("승인하면 이곳을 결제 흐름에 남겨, 모든 작업이 작업의 사이트와 상관없이 이곳으로 페이지를 옮길 수 있습니다(결제 요청은 지금처럼 결제 카드로 묻습니다) · "+
			"%d분 안에 승인하면 멈춰 있는 이동이 그대로 이어집니다", int(holder.Hold.Minutes())),
	}
}

// siteRefusal is what a run is told of a page refused for leaving its
// job's sites.
func siteRefusal(l Leave) string {
	return "이 작업의 사이트 밖이라 페이지 이동을 막았습니다: " + urlShape(l.URL) + ". 막힌 탭에는 '자비스 · 작업의 사이트 밖' 화면이 떠 있습니다. " +
		"목표에 꼭 필요한 사이트면 request_sites 로 더한 뒤 다시 여세요. 결제하던 중이었다면 결제가 끝나지 않았을 수 있으니 주문 내역부터 확인하세요."
}

// flowHeld is what a run is told of a page held on a flow card.
func flowHeld(f FlowHost) string {
	what := "결제를 이어 가는 페이지 이동이"
	if f.Paying == "" {
		what = "결제 페이지로 보이는 이동이"
	}
	return fmt.Sprintf("%s 작업의 사이트 밖(%s)이라 멈춰 있습니다. 운영자에게 결제 흐름 카드(%s)를 올렸고, 그 이동을 %d분 동안 붙잡아 둡니다. "+
		"결제하기를 다시 누르거나 탭을 옮기거나 닫지 말고 곧바로 wait_payment 를 부르세요.", what, f.Host, f.Site, int(holder.Hold.Minutes()))
}

// resolveFlow acts on a decision about a flow card. Approved, the site joins
// the payment flow for good, and the page held on the card goes on - and the
// payment with it - if the job's run still holds the browser. Turned down,
// the page is refused. Either way the job hears it: from wait_payment in the
// run still going, or as a card answer that wakes it.
func (s *Server) resolveFlow(p proposal.Proposal, d proposal.Decision) {
	f := flowOf(p.Action.Input)
	approved := d == proposal.Approve
	var saveErr error
	if approved {
		// The site first: the held page that goes on asks it next, and so
		// does every page after it.
		saveErr = s.flows.Add(flowSite{Site: f.Site, Card: p.ID, Job: f.JobID, Seen: f.URL})
		if saveErr != nil {
			s.log.Error("결제 흐름에 더한 곳을 저장하지 못했습니다", "site", f.Site, "card", p.ID, "err", saveErr)
		} else {
			s.log.Info("결제 흐름에 더했습니다", "site", f.Site, "card", p.ID, "job", f.JobID)
		}
	}
	// The page goes on only while the job's run still holds the browser: the
	// payment it finishes is that run's to carry on.
	passes := approved && s.jobs != nil && s.jobHolds(f.JobID)
	passed := s.payments.Release(p.ID, passes) && passes
	if s.jobs != nil && f.JobID != 0 {
		if err := s.jobs.PaymentDecided(f.JobID, p.ID, approved, passed, flowAnswer(f, approved, passed, p.Reason)); err != nil {
			// The card keeps the decision; the jobs' sweep hands it on.
			s.log.Warn("결제 흐름 카드 결정을 작업에 전하지 못했습니다", "job", f.JobID, "card", p.ID, "err", err)
		}
	}
	if !approved {
		return
	}
	note := "결제 흐름에 더했습니다. 작업이 결제를 다시 확인합니다."
	switch {
	case saveErr != nil:
		note = "결제 흐름에 더했지만 저장하지 못해, 에이전트가 다시 시작하면 잊습니다: " + saveErr.Error()
	case passed:
		note = "결제 흐름에 더했고, 멈춰 있던 이동이 그대로 나가 작업이 결제를 이어 갑니다."
	}
	s.proposals.Settle(p.ID, proposal.Executed, note)
}

// flowAnswer is how a job hears the decision on its flow card.
func flowAnswer(f FlowHost, approved, passed bool, reason string) string {
	card := "결제 흐름 카드(" + f.Site + ")"
	switch {
	case passed:
		return "[결제 흐름 승인됨] 운영자가 " + card + "를 승인해 그곳을 결제 흐름에 더했고, 멈춰 있던 페이지 이동이 그대로 나갔습니다. " +
			"결제하기를 다시 누르지 말고 화면을 보며 이어 가세요 - 주문 완료 화면이 뜨는지 보고, 주문 내역에서 주문을 확인합니다."
	case approved:
		return "[결제 흐름 승인됨] 운영자가 " + card + "를 승인해 그곳을 결제 흐름에 더했습니다. 멈춰 있던 페이지 이동은 그 전에 막혀서 결제가 끝나지 않았을 수 있습니다. " +
			"주문 내역에서 이 주문이 생겼는지 먼저 확인하고, 없으면 주문서에서 결제를 처음부터 다시 진행하세요(결제 승인 카드가 다시 올라갈 수 있습니다)."
	}
	text := "[결제 흐름 반려됨] 운영자가 " + card + "를 반려했습니다."
	if reason != "" {
		text += "\n반려 이유: " + reason
	}
	return text + "\n이 결제는 이어 가지 말고, 주문 내역을 확인한 뒤 결과를 알리세요."
}

// KindGuardLeave is a page of a job's run the guard stopped on its way out
// of the job's sites, and what came of it - one record, written once decided.
const KindGuardLeave = "guard.leave"

func (s *Server) recordLeave(l Leave, host string, job int64, v verdict) {
	body := map[string]any{"url": urlShape(l.URL), "method": l.Method, "host": host, "decision": string(v.decision), "why": v.why}
	if l.From != "" {
		body["from"] = l.From
	}
	if l.FromWindow {
		body["fromWindow"] = true
	}
	if v.card != "" {
		body["card"] = v.card
	}
	if job != 0 {
		body["job"] = job
	}
	s.records.Write(record.Record{
		Kind: KindGuardLeave, Source: recordSource, SourceKey: recordKey("l"),
		Actor: payerOf(job), Channel: browserChannel, Subject: siteOf(host),
		Tier: record.Durable, TraceID: payerOf(job),
		Payload: body,
	})
}

// flowSites are the sites the operator added to the payment flow on a flow
// card, beside the browser's own list (payment-hosts.json flow): a job's
// page may go to them whatever the job's sites. Kept on disk beside the
// cards - the approval is the operator's, and a restart that forgot it would
// ask the same thing again.
type flowSites struct {
	mu    sync.Mutex
	path  string
	sites map[string]flowSite
}

// flowSite is one site added, and the card that added it.
type flowSite struct {
	Site  string    `json:"site"`
	Added time.Time `json:"added"`
	Card  string    `json:"card,omitempty"`
	Job   int64     `json:"job,omitempty"`
	// Seen is the shape of the address the card showed stopped.
	Seen string `json:"seen,omitempty"`
}

type flowFile struct {
	Sites []flowSite `json:"sites"`
}

// openFlowSites reads the sites kept at path; an empty path keeps them in
// memory only. A file it cannot read is an error, and the sites start empty:
// a site missing is one more card, never a page let through unasked.
func openFlowSites(path string) (*flowSites, error) {
	f := &flowSites{path: path, sites: map[string]flowSite{}}
	if path == "" {
		return f, nil
	}
	var saved flowFile
	if err := jsonfile.Load(path, &saved); err != nil {
		return f, err
	}
	for _, e := range saved.Sites {
		if e.Site = strings.ToLower(strings.TrimSpace(e.Site)); e.Site != "" {
			f.sites[e.Site] = e
		}
	}
	return f, nil
}

// Has is whether site is one the operator added.
func (f *flowSites) Has(site string) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sites[strings.ToLower(site)]
	return ok
}

// Add keeps a site, and says whether it was written down: one that was not
// is open until the agent restarts.
func (f *flowSites) Add(e flowSite) error {
	if f == nil {
		return errors.New("결제 흐름을 둘 곳이 없습니다")
	}
	if e.Site = strings.ToLower(strings.TrimSpace(e.Site)); e.Site == "" {
		return errors.New("더할 사이트가 비었습니다")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sites[e.Site]; ok {
		return nil
	}
	if e.Added.IsZero() {
		e.Added = time.Now()
	}
	f.sites[e.Site] = e
	if f.path == "" {
		return nil
	}
	return jsonfile.Save(f.path, flowFile{Sites: f.listLocked()})
}

// List is every site added, by name.
func (f *flowSites) List() []flowSite {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listLocked()
}

func (f *flowSites) listLocked() []flowSite {
	out := make([]flowSite, 0, len(f.sites))
	for _, e := range f.sites {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Site < out[j].Site })
	return out
}
