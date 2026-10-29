// Package jobs is how the assistant does things over time, for any kind of
// task: "이거 중고나라에 올리고 반응 봐줘", "이 상품 가격 내려가면 알려줘",
// "택배 도착하면 알려줘".
//
// In conversation the model hands such a task over with create_job. From then
// on the scheduler (scheduler.go) runs it in the background with the shared
// browser, and each run decides for itself what comes next - check again at
// a time it picks, wait for the operator on a card, or finish. Between runs
// the job lives in jobs-svc: what it remembers, what it is tracking, and a
// log the 작업 tab shows as progress.
//
// What every job may need is built once here, as tools a run can call
// (runtools.go): ask the operator on a card (with a form if it needs values),
// ask for a login on a site, add a site it finds it needs, record what it
// tracks, schedule its next look.
//
// A card stands only where something changes: making or redirecting a job
// takes none, nor does adding a site to it - a run asks before it posts,
// sends, deletes or books (ask), and the payment guard's card stands before
// money leaves.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

var (
	_ module.Editor        = (*Module)(nil)
	_ module.ContextSource = (*Module)(nil)
)

const (
	Name       = "job"
	ServerName = "jobs"
	// KindAsk is a card a job raises: a question, an approval before acting,
	// or a form to fill in.
	KindAsk = "job.ask"
	// KindLogin asks the operator to log in to a site in the 화면 tab.
	KindLogin = "job.login"
	// KindSites was a job asking to open sites it was not given. Since
	// 2026-10-20 a run adds them itself (request_sites); a card of this kind
	// still waiting from before is decided as it was.
	KindSites = "job.sites"
	// KindFirst asks whether a job the operator just asked for should go
	// ahead of the one on the browser: browser jobs go in the order they
	// were asked for, and this is the way to the front (2026-10-28).
	KindFirst = "job.first"
)

// QualifiedToolName is how Claude Code refers to one of the chat's tools.
func QualifiedToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, tool)
}

// Notifier puts a job's news in the conversation without a model turn: one
// card per run, replaced by the run's later word (thread.Store.JobNews) -
// and the cards a run raises, where they then stay, decided or not.
type Notifier interface {
	JobNews(o thread.Origin, paragraphs []string) *thread.Turn
	AttachProposal(proposalID string)
}

type Module struct {
	store     *Store
	uploads   *uploads.Store
	proposals *proposal.Store
	notify    Notifier
	log       *slog.Logger
	// kick wakes the scheduler: a job created, a card answered, a login done.
	kick chan struct{}
	// current is the run in flight; the run tools answer only for it.
	current running
	// pay is each job's payment card the browser's guard raised (intercept).
	pay payments
	// cut is what each job's last run did before the conversation cut it
	// short, for the next run to pick up from.
	cutMu sync.Mutex
	cut   map[int64][]string
	// active is the jobs with a run in flight here, so a due job is not
	// started twice while its run is being opened.
	activeMu sync.Mutex
	active   map[int64]struct{}
	// guard and hold are the scheduler's (Run): a site a run adds is held
	// open for the rest of the run at once.
	guard SiteGuard
	hold  time.Duration
	// noBrowser: this agent's jobs open no website (the guest's: the
	// browser is the operator's, signed in as them).
	noBrowser bool
	// heard is each card whose decision this process took to its job, and
	// when: the sweep leaves those to the delivery under way. retryAt is
	// when the sweep should look again because a delivery failed.
	heardMu sync.Mutex
	heard   map[string]time.Time
	retryAt time.Time
	// bg is the scheduler's runner (Run): a job put first stops the run on
	// the browser through it.
	bg Background
	// offered is each job's 먼저 card, raised while it waits for the
	// browser behind another job - one per wait, answered or not.
	offerMu sync.Mutex
	offered map[int64]string
	// onEnd hears each job that is over: done, or stopped after failing.
	onEnd func(EndNote)
	// vault and charges are what a run that buys needs (order.go): the
	// saved shipping address and the vault's cards, and the card alerts.
	vault   VaultCards
	charges ChargeLog
}

// EndNote is a job that is over, for the phone: done (Text is what its
// finish said) or stopped after failing (Text is why).
type EndNote struct {
	Job  Job
	Run  int64
	Done bool
	Text string
}

// OnEnd has fn hear every job that is over - done, or stopped for failing -
// whatever the conversation is doing (2026-10-28: "무슨 작업이든 작업이
// 끝나면 대화 진행 여부와 무관하게 알리기"). A job the operator called off is
// not told back to them.
func (m *Module) OnEnd(fn func(EndNote)) { m.onEnd = fn }

func (m *Module) ended(n EndNote) {
	if m.onEnd != nil {
		m.onEnd(n)
	}
}

func New(store *Store, up *uploads.Store, proposals *proposal.Store, notify Notifier, log *slog.Logger) *Module {
	m := &Module{store: store, uploads: up, proposals: proposals, notify: notify, log: log, kick: make(chan struct{}, 1), cut: map[int64][]string{}}
	proposals.OnDecide(m.decided)
	return m
}

// NoBrowser makes this agent's jobs ones that open no website and take no
// photo - the guest's, whose agent has neither.
func (m *Module) NoBrowser() { m.noBrowser = true }

// CardFree says whether a job call may go through without an approval card
// (the permission gate asks): making a job or instructing one, always. A job
// changes nothing by being made - a run asks before it posts, sends, deletes
// or books, and the payment guard's card stands before money - so a card at
// the start stood only between the operator and work they had just asked for
// (2026-10-20: "바꾸거나 결제하는 게 아니면 브라우저 작업도 카드 없이"). The
// calls still come through the gate, which logs each.
func (m *Module) CardFree(tool string, _ json.RawMessage) bool {
	return tool == QualifiedToolName("create_job") || tool == QualifiedToolName("instruct_job")
}

// BrowserTool is whether a tool is the shared browser's own - reading,
// pressing, typing, the PIN at a keypad a payment card already let through.
// None of them takes a card: the browser's guard stops a payment on the
// network and raises the payment card itself.
func BrowserTool(tool string) bool {
	return strings.HasPrefix(tool, "mcp__"+BrowserServer+"__")
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Wake has the scheduler look now: a place came free (the runner's OnFree),
// or a job changed where this agent does not see it (jobs-svc's knock).
func (m *Module) Wake() { m.wake() }

// hear notes that a card's decision is on its way to the job.
func (m *Module) hear(card string) {
	m.heardMu.Lock()
	defer m.heardMu.Unlock()
	if m.heard == nil {
		m.heard = map[string]time.Time{}
	}
	m.heard[card] = time.Now()
}

// deliveryWindow is how long after a decision was taken to its job the
// sweep leaves it alone: its answer may still be on the way.
var deliveryWindow = 2 * time.Minute

// delivering is whether a card's decision was taken to its job a moment
// ago.
func (m *Module) delivering(card string) bool {
	m.heardMu.Lock()
	defer m.heardMu.Unlock()
	at, ok := m.heard[card]
	return ok && time.Since(at) < deliveryWindow
}

// retrySoon has the sweep look again just past the delivery window: a
// decision that could not reach its job (jobs-svc down a moment) is handed
// on then, not half an hour later.
func (m *Module) retrySoon() {
	m.heardMu.Lock()
	m.retryAt = time.Now().Add(deliveryWindow + 5*time.Second)
	m.heardMu.Unlock()
	time.AfterFunc(deliveryWindow+5*time.Second, m.wake)
}

// retryDue is whether a retry asked for is due now; asking clears it.
func (m *Module) retryDue(now time.Time) bool {
	m.heardMu.Lock()
	defer m.heardMu.Unlock()
	if m.retryAt.IsZero() || now.Before(m.retryAt) {
		return false
	}
	m.retryAt = time.Time{}
	return true
}

// Specs: the two cards a job can raise. Nothing here is a tool the chat's
// model calls through the gate - the cards are opened by runs directly.
func (m *Module) Specs() []action.Spec {
	return []action.Spec{
		{Kind: KindAsk, Summary: "작업이 운영자에게 묻거나 승인을 받습니다."},
		{Kind: KindLogin, Summary: "작업이 사이트 로그인을 요청합니다."},
		{Kind: KindFirst, Summary: "브라우저를 기다리는 작업을 먼저 할지 묻습니다."},
	}
}

// FirstInput is the 먼저 card: a job the operator asked for, waiting for the
// browser behind another job's run.
type FirstInput struct {
	JobID    int64  `json:"jobId"`
	JobTitle string `json:"jobTitle"`
	// Holder is the job on the browser when it was asked.
	Holder string `json:"holder"`
}

func firstCard(in FirstInput) action.Card {
	return action.Card{
		Title:       "'" + in.JobTitle + "' 먼저 할까요?",
		Body:        "기다리는 작업: " + in.JobTitle + "\n지금 하는 작업: " + in.Holder,
		Consequence: "승인하면 지금 하는 작업을 잠시 멈추고 이 작업을 먼저 한 뒤, 멈춘 작업을 이어서 합니다. 반려하면 요청한 순서대로 합니다.",
	}
}

// AskInput is a job's card, stored as the proposal's action input.
type AskInput struct {
	JobID    int64          `json:"jobId"`
	JobTitle string         `json:"jobTitle"`
	Question string         `json:"question"`
	Body     string         `json:"body,omitempty"`
	Images   []string       `json:"images,omitempty"`
	Fields   []action.Field `json:"fields,omitempty"`
}

type LoginInput struct {
	Site   string `json:"site"`
	Reason string `json:"reason,omitempty"`
}

// SitesInput is a job's request for more sites.
type SitesInput struct {
	JobID    int64    `json:"jobId"`
	JobTitle string   `json:"jobTitle"`
	Sites    []string `json:"sites"`
	Reason   string   `json:"reason,omitempty"`
}

func sitesCard(in SitesInput) action.Card {
	body := "작업: " + in.JobTitle + "\n더할 사이트: " + strings.Join(in.Sites, ", ")
	if in.Reason != "" {
		body += "\n이유: " + in.Reason
	}
	return action.Card{
		Title:       "작업이 사이트를 더 열려고 해요",
		Body:        body,
		Consequence: "승인하면 이 작업이 이 사이트들(같은 도메인의 다른 주소 포함)도 열 수 있습니다. 반려하면 지금 사이트만 씁니다.",
	}
}

func askCard(in AskInput) action.Card {
	body := "작업: " + in.JobTitle
	if strings.TrimSpace(in.Body) != "" {
		body += "\n\n" + in.Body
	}
	return action.Card{
		Title:       in.Question,
		Body:        body,
		Consequence: "승인하면 작업이 이 내용으로 이어서 합니다. 반려하면 하지 않습니다.",
		Images:      in.Images,
		Fields:      in.Fields,
	}
}

func loginCard(in LoginInput) action.Card {
	body := in.Site + " 에 로그인이 필요합니다."
	if in.Reason != "" {
		body += "\n" + in.Reason
	}
	return action.Card{
		Title:       "다시 로그인해 주세요: " + in.Site,
		Body:        body + "\n\n화면 탭에서 브라우저로 로그인한 뒤 승인을 누르세요.",
		Consequence: "승인하면 이 사이트를 쓰는 작업을 바로 이어서 합니다. 반려하면 작업이 로그인 없이 할 수 있는 만큼만 하고 알려 드립니다.",
	}
}

func (m *Module) Preview(_ context.Context, a action.Action) (action.Card, error) {
	switch a.Kind {
	case KindAsk:
		var in AskInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		return askCard(in), nil
	case KindLogin:
		var in LoginInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		return loginCard(in), nil
	case KindFirst:
		var in FirstInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		return firstCard(in), nil
	}
	return action.Card{}, fmt.Errorf("모르는 동작입니다: %s", a.Kind)
}

// Execute is never reached for these cards - their decisions go through
// decided - but the registry wants an Actuator.
func (m *Module) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{}, errors.New("작업 카드는 실행하지 않습니다")
}

// Revise applies a form card's edits and checks required fields.
func (m *Module) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	if a.Kind != KindAsk {
		return a, action.Card{}, errors.New("이 카드는 고칠 수 없습니다.")
	}
	var in AskInput
	if err := json.Unmarshal(a.Input, &in); err != nil {
		return a, action.Card{}, err
	}
	known := map[string]bool{}
	for i, f := range in.Fields {
		known[f.Key] = true
		if v, ok := edits[f.Key]; ok {
			in.Fields[i].Value = strings.TrimSpace(v)
		}
		if f.Kind == "select" && in.Fields[i].Value != "" && len(f.Options) > 0 && !contains(f.Options, in.Fields[i].Value) {
			return a, action.Card{}, fmt.Errorf("%s: 고를 수 있는 값이 아닙니다.", f.Label)
		}
		if in.Fields[i].Required && in.Fields[i].Value == "" {
			return a, action.Card{}, fmt.Errorf("%s 을(를) 적어 주세요.", f.Label)
		}
	}
	for k := range edits {
		if !known[k] {
			return a, action.Card{}, fmt.Errorf("고칠 수 없는 항목입니다: %s", k)
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return a, action.Card{}, err
	}
	return action.Action{Kind: a.Kind, Input: raw}, askCard(in), nil
}

// decided carries a card's decision back to the job that raised it, and
// wakes the job: approved or turned down, it goes on from the answer at once
// rather than on a clock.
func (m *Module) decided(p proposal.Proposal, d proposal.Decision) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m.current.unpark(p.ID)
	m.hear(p.ID)
	switch p.Action.Kind {
	case vault.KindSavePin, vault.KindSaveShipping:
		// A job's own (request_payment_pin, request_shipping_address); the
		// conversation's go through the permission gate.
		m.decidedVault(ctx, p, d)
	case KindAsk:
		var in AskInput
		if json.Unmarshal(p.Action.Input, &in) != nil {
			return
		}
		if err := m.store.Answer(ctx, in.JobID, p.ID, answerText(in, d)); err != nil {
			if JobEnded(err) {
				m.endedAnswer(p.ID, d)
				return
			}
			// The decision stands on the card; the sweep hands it on in a
			// few minutes. Marked failed, it would read as no decision at all.
			state := proposal.Approved
			if d != proposal.Approve {
				state = proposal.Rejected
			}
			m.proposals.Settle(p.ID, state, "작업에 아직 전하지 못해 곧 다시 전합니다: "+err.Error())
			m.retrySoon()
			return
		}
		if d == proposal.Approve {
			m.proposals.Settle(p.ID, proposal.Executed, "작업에 전했습니다. 작업 탭에서 이어지는 걸 볼 수 있습니다.")
		} else {
			m.proposals.Settle(p.ID, proposal.Rejected, "작업에 반려로 전했습니다.")
		}
		m.wake()
	case KindSites:
		var in SitesInput
		if json.Unmarshal(p.Action.Input, &in) != nil {
			return
		}
		text := "[반려] 사이트 추가: " + strings.Join(in.Sites, ", ")
		if d == proposal.Approve {
			if err := m.store.AddSites(ctx, in.JobID, in.Sites); err != nil {
				m.proposals.Settle(p.ID, proposal.Failed, "사이트를 더하지 못했습니다: "+err.Error())
				return
			}
			text = "[승인] 사이트 추가: " + strings.Join(in.Sites, ", ") + " - 이제 열 수 있습니다."
		}
		if err := m.store.Answer(ctx, in.JobID, p.ID, text); err != nil {
			if JobEnded(err) {
				m.endedAnswer(p.ID, d)
				return
			}
			m.proposals.Settle(p.ID, proposal.Failed, "작업에 전하지 못했습니다: "+err.Error())
			return
		}
		if d == proposal.Approve {
			m.proposals.Settle(p.ID, proposal.Executed, "사이트를 더했습니다. 작업이 이어서 합니다.")
		} else {
			m.proposals.Settle(p.ID, proposal.Rejected, "지금 사이트만 쓰도록 작업에 전했습니다.")
		}
		m.wake()
	case KindFirst:
		var in FirstInput
		if json.Unmarshal(p.Action.Input, &in) != nil {
			return
		}
		m.putFirst(ctx, p, in, d)
	case KindLogin:
		var in LoginInput
		if json.Unmarshal(p.Action.Input, &in) != nil {
			return
		}
		if d != proposal.Approve {
			// Turned down: no login is coming. Waiting on it kept the site
			// blocked and its jobs silent for good (job 27, 2026-10-20), so the
			// site opens again and its jobs are told to go on without one.
			if err := m.store.SetLogin(ctx, in.Site, false, ""); err != nil {
				m.proposals.Settle(p.ID, proposal.Failed, err.Error())
				return
			}
			n := m.tellSite(ctx, in.Site, p.ID, "[로그인 반려] "+in.Site+": 운영자가 로그인 요청을 반려했습니다. "+
				"로그인 없이 할 수 있는 일만 하고, 로그인이 꼭 필요하면 다시 요청하지 말고 그렇게 finish 로 알리고 끝내세요.")
			m.proposals.Settle(p.ID, proposal.Rejected, fmt.Sprintf("이 사이트를 쓰는 작업 %d개에 반려로 전했습니다.", n))
			m.wake()
			return
		}
		if err := m.store.SetLogin(ctx, in.Site, false, ""); err != nil {
			m.proposals.Settle(p.ID, proposal.Failed, err.Error())
			return
		}
		m.proposals.Settle(p.ID, proposal.Executed, "이 사이트의 작업을 이어서 합니다.")
		m.wake()
	}
}

// endedNote is what a card of a job that is over says.
const endedNote = "작업이 이미 끝나 답을 전하지 않았습니다. 이어서 하려면 작업 탭에서 지시하세요."

// endedAnswer settles a card answered after its job was over: the decision
// went nowhere, and the card says so rather than "작업에 전했습니다".
func (m *Module) endedAnswer(id string, d proposal.Decision) {
	state := proposal.Rejected
	if d == proposal.Approve {
		state = proposal.Failed
	}
	m.proposals.Settle(id, state, endedNote)
}

// withdrawCards takes down the cards a job still waits on once it is over -
// done, or called off - and says how many. Job 18 asked and finished in the
// same run (2026-10-18); its card stayed up and was answered forty minutes
// later, into a job that would never read it.
func (m *Module) withdrawCards(job int64) int {
	n := 0
	for _, p := range m.waitingOn(job) {
		m.proposals.Abandon(p.ID, "작업이 끝나 내렸습니다. 이어서 하려면 작업 탭에서 지시하세요.")
		n++
	}
	return n
}

// tellSite hands text to every live job that uses site, as an answer that
// wakes it, and says how many there were.
func (m *Module) tellSite(ctx context.Context, site, card, text string) int {
	js, err := m.store.List(ctx)
	if err != nil {
		m.log.Warn("사이트의 작업을 찾지 못했습니다", "site", site, "err", err)
		return 0
	}
	n := 0
	for _, j := range js {
		if (j.State != "active" && j.State != "waiting") || !contains(j.Sites, site) {
			continue
		}
		if err := m.store.Answer(ctx, j.ID, card, text); err != nil {
			m.log.Warn("작업에 전하지 못했습니다", "job", j.ID, "err", err)
			continue
		}
		n++
	}
	return n
}

// waitingOn is the cards a job's question still waits on, oldest first: the
// payment card its run stopped at, and the questions it asked.
func (m *Module) waitingOn(job int64) []proposal.Proposal {
	return m.proposals.PendingWhere(func(p proposal.Proposal) bool {
		switch {
		case p.Action.Kind == KindAsk:
			var in AskInput
			return json.Unmarshal(p.Action.Input, &in) == nil && in.JobID == job
		case vaultKind(p.Action.Kind):
			return vaultJob(p) == job
		case p.Origin == proposal.OriginIntercept:
			var pay struct {
				JobID int64 `json:"jobId"`
			}
			return json.Unmarshal(p.Action.Input, &pay) == nil && pay.JobID == job
		}
		return false
	})
}

// cardSweep is how often the scheduler makes sure, without waking any model,
// that no job is left waiting on a card that was decided while the job was
// not told (a restart between the two) or that is gone - the long check
// behind the decision events, there only for what they miss.
const cardSweep = 30 * time.Minute

// sweep gives each job still waiting on a decided or vanished card what
// became of it, which wakes the job.
func (m *Module) sweep(ctx context.Context) {
	js, err := m.store.List(ctx)
	if err != nil {
		return
	}
	for _, j := range js {
		// Over - called off from the 작업 tab, say, which the agent does not
		// hear at once: its questions come down.
		if j.State == "done" || j.State == "cancelled" {
			if n := m.withdrawCards(j.ID); n > 0 {
				m.log.Info("끝난 작업의 카드를 내렸습니다", "job", j.ID, "cards", n)
			}
			continue
		}
		if j.State != "waiting" || j.WaitingCard == "" || j.Running || m.delivering(j.WaitingCard) {
			continue
		}
		p, ok := m.proposals.Get(j.WaitingCard)
		if ok && p.State == proposal.Pending {
			continue
		}
		text := "[카드 없음] 기다리던 카드(" + j.WaitingCard + ")가 없어졌습니다. 운영자의 답을 받지 못했으니, 필요하면 다시 물으세요."
		if ok {
			text = lateAnswer(p)
		}
		if err := m.store.Answer(ctx, j.ID, j.WaitingCard, text); err != nil {
			m.log.Warn("결정을 작업에 늦게라도 전하지 못했습니다", "job", j.ID, "card", j.WaitingCard, "err", err)
			continue
		}
		m.log.Info("작업이 듣지 못한 카드 결정을 전했습니다", "job", j.ID, "card", j.WaitingCard, "found", ok, "state", p.State)
		m.wake()
	}
}

// lateAnswer is a decided card's answer, told to a job that never heard it.
func lateAnswer(p proposal.Proposal) string {
	approved := p.State == proposal.Approved || p.State == proposal.Executed
	if p.Action.Kind == KindAsk {
		var in AskInput
		if json.Unmarshal(p.Action.Input, &in) == nil {
			d := proposal.Reject
			if approved {
				d = proposal.Approve
			}
			return answerText(in, d) + "\n(늦게 전해진 답입니다)"
		}
	}
	word := "반려"
	if approved {
		word = "승인"
	}
	text := fmt.Sprintf("[카드 결정 · 늦게 전함] '%s' 카드(%s)가 %s됐습니다.", p.Card.Title, p.ID, word)
	if p.Origin == proposal.OriginIntercept && approved {
		text += " 결제 단계를 다시 진행하세요 - 다시 막히면 결제 카드가 새로 올라갑니다(wait_payment)."
	} else if p.Origin == proposal.OriginIntercept {
		text += " 결제하지 마세요."
	}
	if p.Note != "" {
		text += "\n메모: " + p.Note
	}
	return text
}

func answerText(in AskInput, d proposal.Decision) string {
	if d != proposal.Approve {
		return fmt.Sprintf("[반려] %s", in.Question)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[승인] %s", in.Question)
	for _, f := range in.Fields {
		fmt.Fprintf(&b, "\n- %s: %s", f.Label, f.Value)
	}
	return b.String()
}

func (m *Module) Facts(ctx context.Context, _ string) ([]module.Fact, error) {
	js, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []module.Fact
	for _, j := range js {
		if j.State == "active" || j.State == "waiting" {
			out = append(out, module.Fact{Source: Name, Text: m.describe(j)})
		}
	}
	return out, nil
}

// --- 대화 도구 ---------------------------------------------------------------

type CreateInput struct {
	Title        string   `json:"title" jsonschema:"작업 탭에 보일 짧은 이름. 예: '에어팟 프로 2 중고나라 판매'"`
	Goal         string   `json:"goal" jsonschema:"끝났을 때 무엇이 이뤄져 있어야 하는지. 예: '중고나라에 판매 글을 올리고, 팔릴 때까지 반응을 지켜보다 판매되면 끝낸다.'"`
	Instructions string   `json:"instructions,omitempty" jsonschema:"운영자가 말한 조건과 선호를 빠짐없이: 가격 정책, 거래 방식, 알려 달라는 때, 하지 말라는 것. 대화에서 알아낸 사실(사진 속 물건 정보 등)도."`
	Sites        []string `json:"sites,omitempty" jsonschema:"이 작업이 브라우저로 열 사이트 호스트 전부 - 로그인해서 쓰는 곳도, 조사하며 볼 곳도. 실행 중에는 여기 적힌 사이트(와 같은 도메인의 다른 주소) 밖으로는 열리지 않는다. 예: ['web.joongna.com'], ['search.shopping.naver.com', 'www.danawa.com']"`
	Photos       []string `json:"photos,omitempty" jsonschema:"쓸 첨부 사진 파일 이름(경로의 마지막 부분)."`
}

type InstructInput struct {
	ID   int64  `json:"id" jsonschema:"작업 id. list_jobs 로 확인한다."`
	Text string `json:"text" jsonschema:"작업에 전할 지시. 예: '가격을 6만 원으로 내려줘'"`
}

type ListInput struct{}

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "create_job",
		Description: "시간이 걸리거나 나중에 다시 봐야 하는 일, 웹사이트에서 백그라운드로 해야 하는 일을 작업으로 맡긴다. " +
			"작업은 백그라운드에서 브라우저로 진행되고, 필요하면 스스로 주기적으로 다시 확인하며, 운영자에게 카드로 묻거나 로그인을 요청한다. " +
			"진행 상황은 작업 탭에서 보인다. 정해진 시각이나 집 도착·출발 때 알려 주기만 하는 일은 작업이 아니라 set_reminder 로 맡긴다(작업은 폰 푸시를 못 보내고 위치를 모른다).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateInput) (*mcp.CallToolResult, any, error) {
		if m.noBrowser && (len(in.Sites) > 0 || len(in.Photos) > 0) {
			return toolText("이 계정의 작업은 웹사이트를 열거나 사진을 쓸 수 없습니다(브라우저는 운영자 계정이에요). sites·photos 없이 검색·정리로 되는 일만 맡길 수 있습니다.", true), nil, nil
		}
		for i, p := range in.Photos {
			if j := strings.LastIndex(p, "/"); j >= 0 {
				in.Photos[i] = p[j+1:]
			}
			if _, err := m.uploads.Path(in.Photos[i]); err != nil {
				return toolText(fmt.Sprintf("사진 %s 를 찾을 수 없습니다. 대화에 첨부된 사진만 쓸 수 있습니다.", in.Photos[i]), true), nil, nil
			}
		}
		j, err := m.store.Create(ctx, NewJob(in))
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		m.wake()
		return toolText(fmt.Sprintf("작업 %d '%s' 를 만들었습니다. 운영자는 이걸 이미 압니다: "+
			"작업을 만들었다, 백그라운드에서 시작한다, 작업 탭에서 볼 수 있다 같은 안내는 하지 마세요. "+
			"운영자에게 물어볼 것이나 알려야 할 다른 사실이 있을 때만 그것을 말하고, 없으면 '맡겼습니다.' 한마디로 끝내세요.", j.ID, j.Title), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_jobs",
		Description: "맡긴 작업들의 현황: 진행 중인 작업마다 상태, 한 줄 요약, 다음 확인 시각, 기다리는 카드. 끝난 작업은 개수와 최근 몇 개만(all=true 면 전부 짧게). " +
			"작업 하나의 최근 기록까지 보려면 get_job. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListJobsInput) (*mcp.CallToolResult, any, error) {
		js, err := m.store.List(ctx)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(m.renderList(js, in.All), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_job",
		Description: "작업 하나의 자세한 현황: 목표, 지시, 쓰는 사이트, 기다리는 카드, 최근 기록(진행·카드·답·보고). " +
			"작업이 지금 무엇을 하고 있는지, 결제가 어디까지 갔는지 운영자에게 답하기 전에 본다. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetJobInput) (*mcp.CallToolResult, any, error) {
		d, err := m.store.Detail(ctx, in.ID)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(m.renderDetail(d), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "instruct_job",
		Description: "진행 중인 작업에 지시를 더한다(가격 변경, 중단 조건 등). 작업이 곧 다시 실행되어 반영한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in InstructInput) (*mcp.CallToolResult, any, error) {
		if err := m.store.Instruct(ctx, in.ID, in.Text); err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		m.wake()
		return toolText("작업에 전했습니다. 곧 반영합니다.", false), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// ListJobsInput: all lists the finished jobs too, one short line each.
type ListJobsInput struct {
	All bool `json:"all,omitempty" jsonschema:"끝난 작업까지 모두 볼지(짧게). 기본은 진행 중인 것만."`
}

type GetJobInput struct {
	ID int64 `json:"id" jsonschema:"작업 id. list_jobs 로 확인한다."`
}

// "waiting" is waiting on a card - any card, a question as much as a
// payment: "승인 대기" read as "the payment waits for approval" when the card
// was "결제는 성공했는데 주문 반영이 안 보여요" (2026-10-30 01:12).
var stateLabel = map[string]string{
	"active": "진행 중", "waiting": "카드 대기", "paused": "멈춤",
	"done": "끝남", "cancelled": "그만둠", "failed": "실패",
}

// summaryShown is how much of a job's one-line status a list shows: a run's
// report can run long, and a list of every job came to 55,000 characters
// (2026-10-29), too much for the conversation to read at once.
const summaryShown = 160

func clampRunes(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func line(j Job) string {
	s := fmt.Sprintf("[%d · %s] %s", j.ID, stateLabel[j.State], j.Title)
	if j.Summary != "" {
		s += " — " + clampRunes(j.Summary, summaryShown)
	}
	if len(j.BlockedSites) > 0 {
		s += " (로그인 대기: " + strings.Join(j.BlockedSites, ", ") + ")"
	} else if j.State == "active" && j.NextAt != nil {
		s += " (다음 " + j.NextAt.Local().Format("1/2 15:04") + ")"
	}
	return s
}

// describe is line with the card a waiting job waits on, by its title.
func (m *Module) describe(j Job) string {
	s := line(j)
	if j.State == "waiting" && j.WaitingCard != "" && m.proposals != nil {
		if p, ok := m.proposals.Get(j.WaitingCard); ok {
			s += " · 기다리는 카드: '" + clampRunes(p.Card.Title, 60) + "'"
		}
	}
	return s
}

func finished(j Job) bool {
	return j.State == "done" || j.State == "cancelled" || j.State == "failed"
}

// renderList is the jobs under way in full and the finished ones counted,
// with the latest few named - or, with all, every finished one in short.
func (m *Module) renderList(js []Job, all bool) string {
	if len(js) == 0 {
		return "맡긴 작업이 없습니다."
	}
	var b strings.Builder
	var over []Job
	for _, j := range js {
		if finished(j) {
			over = append(over, j)
			continue
		}
		b.WriteString("- " + m.describe(j) + "\n")
	}
	if b.Len() == 0 {
		b.WriteString("진행 중인 작업이 없습니다.\n")
	}
	if len(over) == 0 {
		return b.String()
	}
	sort.SliceStable(over, func(i, k int) bool { return lastTouched(over[i]).After(lastTouched(over[k])) })
	if !all {
		fmt.Fprintf(&b, "끝난 작업 %d개", len(over))
		var recent []string
		for _, j := range over[:min(3, len(over))] {
			recent = append(recent, fmt.Sprintf("[%d] %s", j.ID, j.Title))
		}
		fmt.Fprintf(&b, "(최근: %s) - 모두 보려면 all=true, 하나를 자세히는 get_job.\n", strings.Join(recent, ", "))
		return b.String()
	}
	b.WriteString("끝난 작업:\n")
	for _, j := range over[:min(100, len(over))] {
		fmt.Fprintf(&b, "- [%d · %s] %s\n", j.ID, stateLabel[j.State], j.Title)
	}
	return b.String()
}

func lastTouched(j Job) time.Time {
	if j.LastRunAt != nil {
		return *j.LastRunAt
	}
	return j.CreatedAt
}

// eventsShown is how many of a job's latest log lines get_job shows.
const eventsShown = 15

// renderDetail is one job as get_job tells it: what it is for, what it waits
// on, and its latest log, newest last. What it remembers is named, not shown
// (an order's phone number and address are in it).
func (m *Module) renderDetail(d Detail) string {
	j := d.Job
	var b strings.Builder
	b.WriteString(m.describe(j) + "\n")
	if g := clampRunes(j.Goal, 300); g != "" {
		b.WriteString("목표: " + g + "\n")
	}
	if in := clampRunes(j.Instructions, 400); in != "" {
		b.WriteString("지시: " + in + "\n")
	}
	if len(j.Sites) > 0 {
		b.WriteString("쓰는 사이트: " + strings.Join(j.Sites, ", ") + "\n")
	}
	if len(d.Memory) > 0 {
		keys := make([]string, 0, len(d.Memory))
		for k := range d.Memory {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("기억(키만): " + strings.Join(keys, ", ") + "\n")
	}
	if len(d.Records) > 0 {
		fmt.Fprintf(&b, "추적 중인 대상 %d개\n", len(d.Records))
	}
	ev := d.Events
	sort.SliceStable(ev, func(i, k int) bool { return ev[i].At.Before(ev[k].At) })
	var kept []Event
	for _, e := range ev {
		if e.Kind == "step" {
			continue // a press, a look: the progress and the cards say what happened
		}
		kept = append(kept, e)
	}
	if len(kept) > eventsShown {
		kept = kept[len(kept)-eventsShown:]
	}
	if len(kept) > 0 {
		b.WriteString("최근 기록:\n")
		for _, e := range kept {
			fmt.Fprintf(&b, "- %s %s: %s\n", e.At.Local().Format("1/2 15:04"), e.Kind, clampRunes(e.Text, 200))
		}
	}
	return b.String()
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func toolText(s string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
