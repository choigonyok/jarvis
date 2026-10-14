// Package gate is the one door between the assistant and the person. Every
// intent from every skill passes here, and here alone is decided whether it
// is done quietly, done and then told (with a way to undo), asked first,
// kept for the evening's summary, or dropped.
//
// What decides it: how much each kind of action is trusted - learned from
// what the person approved, rejected and undid - how sure the skill was,
// whether the action can be taken back, and how many times today the person
// has already been interrupted.
package gate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Trust levels of a kind of action.
const (
	Auto   = "auto"   // done quietly
	Tell   = "notify" // done, then told
	AskFor = "ask"    // asked first
	Never  = "never"
)

type Policy struct {
	// Levels is where each kind of action starts. Unknown kinds are asked.
	Levels map[string]string
	// Fixed kinds never move from their level, whatever the history.
	Fixed map[string]bool
	// Promote: once this many cards of a (skill, context) were answered and
	// the approval rate (with a uniform prior) is at least PromoteRate, with
	// the last answer an approval, an asked kind is done-and-told instead.
	Promote     int
	PromoteRate float64
	// Window is how far back approvals count for the console.
	Window time.Duration
	// DailyInterrupts is how many pushes a day the gate allows itself; past
	// it, what is not urgent waits for the evening.
	DailyInterrupts int
	// DailyCards is how many cards (questions that ring) a day; past it, a
	// question waits in the evening summary.
	DailyCards int
	// MinConfidence: an action a skill is less sure of than this is asked,
	// and quietly.
	MinConfidence float64
	// InterruptCost is what the first card of the day costs the operator;
	// each card already raised today makes the next dearer, up to double at
	// the day's limit. A card rings only if confidence ×
	// approval rate × value is more than that.
	InterruptCost float64
	// The topic ledger: how long a topic stays quiet after it was raised,
	// approved, or turned down.
	QuietRaised, QuietApproved, QuietRejected time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		Levels:          map[string]string{"calendar.create": AskFor, "calendar.update": AskFor, "calendar.delete": AskFor},
		Fixed:           map[string]bool{"calendar.delete": true},
		Promote:         3,
		PromoteRate:     0.8,
		Window:          30 * 24 * time.Hour,
		DailyInterrupts: 8,
		DailyCards:      5,
		MinConfidence:   0.7,
		InterruptCost:   0.25,
		QuietRaised:     3 * 24 * time.Hour,
		QuietApproved:   7 * 24 * time.Hour,
		QuietRejected:   14 * 24 * time.Hour,
	}
}

type Gate struct {
	st     store.Store
	acts   *act.Registry
	notify act.Notifier
	log    obs.Log
	policy Policy
	loc    *time.Location
	now    func() time.Time
	url    string // where a notice links to
	out    *slog.Logger

	mu sync.Mutex
}

func New(st store.Store, acts *act.Registry, n act.Notifier, log obs.Log, p Policy, loc *time.Location, now func() time.Time, url string, out *slog.Logger) *Gate {
	return &Gate{st: st, acts: acts, notify: n, log: log, policy: p, loc: loc, now: now, url: url, out: out}
}

// Submit takes an intent from a skill and does with it what the policy says.
func (g *Gate) Submit(ctx context.Context, in model.Intent) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	in.ID = newID()
	in.CreatedAt = g.now()
	in.Status = model.Pending
	if in.Evidence == nil {
		in.Evidence = []model.Evidence{}
	}
	// A newer intent about the same thing replaces a question still open:
	// the plan moved again before anyone answered.
	if in.EntityID != "" {
		if err := g.withdraw(ctx, in.EntityID, in.Skill, "새 판단으로 대체됨"); err != nil {
			return in, err
		}
	}

	if in.Kind == "info" {
		in.Decision, in.Status, in.DecidedBy = model.Digest, model.Executed, "policy"
		in.Tier, in.Why = "digest", "알려 둘 일"
		if in.Urgent {
			in.Decision, in.Tier, in.Why = model.Notify, "now", "지금 알 일"
			if g.overBudget(ctx) {
				in.Decision, in.Tier, in.Why = model.Digest, "digest", "오늘 알림 한도를 넘어 저녁 요약으로"
			}
		}
		at := g.now()
		in.DecidedAt, in.ExecutedAt = &at, &at
		g.tell(ctx, in, in.Title, in.Body)
		return in, g.st.PutIntent(ctx, in)
	}

	a, ok := g.acts.For(in.Kind)
	if !ok {
		in.Status, in.Error = model.Failed, "이 행동을 할 수 있는 곳이 없습니다"
		return in, g.st.PutIntent(ctx, in)
	}
	if quiet, why, err := g.quietTopic(ctx, in); err != nil {
		return in, err
	} else if quiet {
		at := g.now()
		in.Decision, in.Status, in.Why, in.DecidedBy, in.DecidedAt = model.Drop, model.Dropped, why, "policy", &at
		return in, g.st.PutIntent(ctx, in)
	}
	if p, ok := a.(act.Preparer); ok {
		prepared, err := p.Prepare(ctx, in.Kind, in.Payload)
		if err != nil {
			in.Status, in.Error = model.Failed, "준비 실패: "+err.Error()
			return in, g.st.PutIntent(ctx, in)
		}
		in.Payload = prepared
	}
	preview, err := a.Preview(ctx, in.Kind, in.Payload)
	if already := (*act.Already)(nil); errors.As(err, &already) {
		// Nothing to do but know it: linked, not asked.
		at := g.now()
		in.Decision, in.Status, in.DecidedBy, in.DecidedAt, in.ExecutedAt = model.Execute, model.Executed, "policy", &at, &at
		in.Why, in.Preview = "이미 있어서 연결만 했어요: "+already.Line, already.Line
		in.Result = json.RawMessage(fmt.Sprintf(`{"calendarId":%q}`, already.ID))
		g.observe(ctx, obs.ActionExecuted, in, in.Result)
		return in, g.st.PutIntent(ctx, in)
	}
	if err != nil {
		in.Status, in.Error = model.Failed, "미리보기 실패: "+err.Error()
		return in, g.st.PutIntent(ctx, in)
	}
	in.Preview = preview

	decision, why, err := g.decide(ctx, in)
	if err != nil {
		return in, err
	}
	in.Decision, in.Why = decision, why
	switch decision {
	case model.Execute, model.Notify:
		g.run(ctx, a, &in, "policy")
		if decision == model.Notify && in.Status == model.Executed {
			in.Tier = "now"
			if g.overBudget(ctx) {
				in.Tier = "digest"
			}
			// Done and told: the card offers the undo.
			in.Card = true
			g.card(ctx, in, "undo")
			g.tellLocal(ctx, in, "처리했어요", in.Title+"\n되돌리려면 눌러서 확인하세요")
		}
	case model.Ask:
		in.Tier = "now"
		if quiet, why := g.quietCard(ctx, in); quiet {
			in.Tier, in.Why = "digest", why
		}
		if in.Tier == "now" {
			// The one way a question reaches the operator: a card the agent
			// shows, answered back here.
			in.Card = true
			g.card(ctx, in, "ask")
			g.tellLocal(ctx, in, "확인해 주세요", in.Title+"\n"+in.Reason)
			_ = g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), model.OutcomeShown, g.now())
		} else {
			g.tell(ctx, in, "확인해 주세요", in.Title+"\n"+in.Reason)
		}
	case model.Drop:
		in.Status = model.Dropped
	}
	return in, g.st.PutIntent(ctx, in)
}

// Approve does an intent that was asked.
func (g *Gate) Approve(ctx context.Context, id string) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, err := g.pending(ctx, id)
	if err != nil {
		return in, err
	}
	a, ok := g.acts.For(in.Kind)
	if !ok {
		return in, errors.New("이 행동을 할 수 있는 곳이 없습니다")
	}
	g.run(ctx, a, &in, "user")
	_ = g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), model.OutcomeApproved, g.now())
	g.closeCard(ctx, in)
	return in, g.st.PutIntent(ctx, in)
}

// Reject turns down an intent that was asked.
func (g *Gate) Reject(ctx context.Context, id string) (model.Intent, error) {
	return g.RejectWhy(ctx, id, "")
}

// RejectWhy turns it down with the operator's reason, kept with the intent:
// what the next judgement about the same thing reads.
func (g *Gate) RejectWhy(ctx context.Context, id, reason string) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, err := g.pending(ctx, id)
	if err != nil {
		return in, err
	}
	at := g.now()
	in.Status, in.DecidedBy, in.DecidedAt = model.Rejected, "user", &at
	if reason != "" {
		in.Error = reason
	}
	_ = g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), model.OutcomeRejected, at)
	g.closeCard(ctx, in)
	return in, g.st.PutIntent(ctx, in)
}

// Undo takes back something done. The result is fed back as an observation,
// so the world model learns the entry is gone.
func (g *Gate) Undo(ctx context.Context, id string) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, err := g.st.Intent(ctx, id)
	if err != nil {
		return in, err
	}
	if in.Status != model.Executed || len(in.Undo) == 0 {
		return in, errors.New("되돌릴 수 있는 상태가 아닙니다")
	}
	a, ok := g.acts.For(in.Kind)
	if !ok {
		return in, errors.New("이 행동을 할 수 있는 곳이 없습니다")
	}
	res, err := a.Undo(ctx, in.Kind, in.Undo)
	if err != nil {
		return in, fmt.Errorf("되돌리지 못했습니다: %w", err)
	}
	in.Status = model.Undone
	g.observe(ctx, obs.ActionUndone, in, res)
	_ = g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), model.OutcomeUndone, g.now())
	g.closeCard(ctx, in)
	return in, g.st.PutIntent(ctx, in)
}

// Withdraw drops a skill's open questions about an entity.
func (g *Gate) Withdraw(ctx context.Context, entityID, skill string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.withdraw(ctx, entityID, skill, "더 이상 필요 없음")
}

func (g *Gate) withdraw(ctx context.Context, entityID, skill, why string) error {
	open, err := g.st.IntentsFor(ctx, entityID)
	if err != nil {
		return err
	}
	for _, p := range open {
		if p.Status == model.Pending && p.Skill == skill {
			p.Status, p.Error = model.Superseded, why
			if err := g.st.PutIntent(ctx, p); err != nil {
				return err
			}
			g.closeCard(ctx, p)
		}
	}
	return nil
}

func (g *Gate) pending(ctx context.Context, id string) (model.Intent, error) {
	in, err := g.st.Intent(ctx, id)
	if err != nil {
		return in, err
	}
	if in.Status != model.Pending {
		return in, fmt.Errorf("이미 처리된 제안입니다 (%s)", in.Status)
	}
	return in, nil
}

func (g *Gate) run(ctx context.Context, a act.Actuator, in *model.Intent, by string) {
	at := g.now()
	in.DecidedBy, in.DecidedAt = by, &at
	res, undo, err := a.Execute(ctx, in.Kind, in.Payload)
	if err != nil {
		in.Status, in.Error = model.Failed, err.Error()
		g.out.Warn("행동을 하지 못했습니다", "kind", in.Kind, "err", err)
		return
	}
	in.Status, in.Result, in.Undo, in.ExecutedAt = model.Executed, res, undo, &at
	g.observe(ctx, obs.ActionExecuted, *in, res)
}

func (g *Gate) observe(ctx context.Context, typ string, in model.Intent, res json.RawMessage) {
	data, _ := json.Marshal(obs.ActionData{IntentID: in.ID, Kind: in.Kind, EntityID: in.EntityID, Result: res})
	err := g.log.Append(ctx, obs.Observation{
		Source: "mind", Type: typ, Subject: in.EntityID, Data: data,
		Key: typ + ":" + in.ID, OccurredAt: g.now(),
	})
	if err != nil {
		g.out.Warn("행동 결과를 기록하지 못했습니다", "err", err)
	}
}

func (g *Gate) tell(ctx context.Context, in model.Intent, title, body string) {
	if g.notify == nil || in.Tier == "" {
		return
	}
	level := "info"
	if in.Tier == "now" {
		level = "warn"
	}
	g.notify.Notify(ctx, act.Notice{
		Title: title, Body: strings.TrimSpace(body), URL: g.url + "#" + in.ID,
		Tier: in.Tier, Level: level, Key: "mind:" + in.ID, At: g.now(),
	})
}

// decide is the policy applied to one action.
func (g *Gate) decide(ctx context.Context, in model.Intent) (string, string, error) {
	stat, err := g.st.Policy(ctx, in.Skill, in.ContextKey())
	if err != nil {
		return "", "", err
	}
	level, why := g.policy.Level(in.Kind, stat)
	if level == Never {
		return model.Drop, why, nil
	}
	if in.Confidence < g.policy.MinConfidence {
		return model.Ask, fmt.Sprintf("확신도 %.0f%%라 직접 확인받아요", in.Confidence*100), nil
	}
	if !in.Reversible && level != AskFor {
		return model.Ask, "되돌릴 수 없는 일이라 먼저 물어요", nil
	}
	switch level {
	case Auto:
		return model.Execute, why, nil
	case Tell:
		return model.Notify, why, nil
	}
	return model.Ask, why, nil
}

// Level is where a kind of action stands now, given how the cards of its
// (skill, context) went, and why. A last answer that was a rejection or an
// undo always brings it back to asking.
func (p Policy) Level(kind string, stat model.PolicyStat) (string, string) {
	base, ok := p.Levels[kind]
	if !ok {
		base = AskFor
	}
	if p.Fixed[kind] {
		return base, "항상 먼저 묻는 행동"
	}
	if stat.Last == model.OutcomeRejected || stat.Last == model.OutcomeUndone {
		if base != AskFor {
			return AskFor, "최근에 거절하거나 되돌리셔서 다시 물어요"
		}
		return AskFor, "먼저 물어보는 행동"
	}
	decided := stat.Approved + stat.Rejected + stat.Undone
	if base == AskFor && p.Promote > 0 && decided >= p.Promote && stat.Rate() >= p.PromoteRate {
		return Tell, fmt.Sprintf("%d번 중 %d번 승인하셔서 바로 하고 알려요", decided, stat.Approved)
	}
	if base == AskFor {
		return AskFor, "먼저 물어보는 행동"
	}
	return base, "믿고 맡긴 행동"
}

// Levels is where every (skill, context) the gate has learned about stands,
// for the console, with its approval rate.
func (g *Gate) Levels(ctx context.Context) (map[string][2]string, error) {
	out := map[string][2]string{}
	stats, err := g.st.Policies(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range stats {
		l, why := g.policy.Level(st.Context, st)
		out[st.Skill+" · "+st.Context] = [2]string{l, fmt.Sprintf("%s (승인율 %.0f%%, 카드 %d)", why, st.Rate()*100, st.Shown)}
	}
	return out, nil
}

// quietTopic is the ledger: an intent on a topic raised, approved or turned
// down lately is not raised again. An intent without a topic is about its
// entity, and a newer one about it replaces an unanswered one instead.
func (g *Gate) quietTopic(ctx context.Context, in model.Intent) (bool, string, error) {
	if in.Topic == "" {
		return false, "", nil
	}
	now := g.now()
	past, err := g.st.IntentsSince(ctx, now.Add(-max(g.policy.QuietRaised, g.policy.QuietApproved, g.policy.QuietRejected)))
	if err != nil {
		return false, "", err
	}
	for _, p := range past { // newest first
		if p.Topic != in.Topic || p.Status == model.Superseded || p.Status == model.Failed {
			continue
		}
		at := p.CreatedAt
		if p.DecidedAt != nil {
			at = *p.DecidedAt
		}
		switch {
		case (p.Status == model.Rejected || p.Status == model.Undone) && now.Sub(at) < g.policy.QuietRejected:
			return true, "최근에 거절하거나 되돌리신 주제라 다시 묻지 않아요", nil
		case p.Status == model.Executed && p.DecidedBy == "user" && now.Sub(at) < g.policy.QuietApproved:
			return true, "최근에 승인하신 주제예요", nil
		case p.Status != model.Dropped && now.Sub(p.CreatedAt) < g.policy.QuietRaised:
			return true, "최근에 제안한 주제예요", nil
		}
	}
	return false, "", nil
}

// quietCard is whether a question that would ring waits for the evening
// summary instead: the skill is unsure, today's budget is spent, or it is
// not worth the interruption.
func (g *Gate) quietCard(ctx context.Context, in model.Intent) (bool, string) {
	if in.Confidence < g.policy.MinConfidence {
		return true, fmt.Sprintf("확신도 %.0f%%라 저녁 요약으로", in.Confidence*100)
	}
	if g.overBudget(ctx) {
		return true, "오늘 알림 한도를 넘어 저녁 요약으로"
	}
	cards := g.cardsToday(ctx)
	if g.policy.DailyCards > 0 && cards >= g.policy.DailyCards {
		return true, "오늘 카드 한도를 넘어 저녁 요약으로"
	}
	stat, _ := g.st.Policy(ctx, in.Skill, in.ContextKey())
	value := in.Value
	if value == 0 {
		value = 1
	}
	worth := in.Confidence * stat.Rate() * value
	cost := g.policy.InterruptCost * (1 + float64(cards)/float64(max(g.policy.DailyCards, 1)))
	if worth <= cost {
		return true, fmt.Sprintf("방해할 만큼은 아니라 저녁 요약으로 (가치 %.2f ≤ 비용 %.2f)", worth, cost)
	}
	return false, ""
}

// overBudget is whether today's pushes are used up.
func (g *Gate) overBudget(ctx context.Context) bool {
	if g.policy.DailyInterrupts <= 0 {
		return false
	}
	n := 0
	for _, in := range g.today(ctx) {
		if in.Tier == "now" {
			n++
		}
	}
	return n >= g.policy.DailyInterrupts
}

func (g *Gate) cardsToday(ctx context.Context) int {
	n := 0
	for _, in := range g.today(ctx) {
		if in.Card && in.Decision == model.Ask {
			n++
		}
	}
	return n
}

func (g *Gate) today(ctx context.Context) []model.Intent {
	now := g.now().In(g.loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, g.loc)
	today, err := g.st.IntentsSince(ctx, midnight)
	if err != nil {
		return nil
	}
	return today
}

// Card is what goes out as mind.card: what the agent needs to show the
// question, and the intent to answer it on.
type Card struct {
	IntentID   string           `json:"intentId"`
	Mode       string           `json:"mode"` // ask | undo
	Skill      string           `json:"skill"`
	Kind       string           `json:"kind"`
	Title      string           `json:"title"`
	Body       string           `json:"body,omitempty"`
	Preview    string           `json:"preview,omitempty"`
	Reason     string           `json:"reason,omitempty"`
	Why        string           `json:"why,omitempty"`
	Evidence   []model.Evidence `json:"evidence,omitempty"`
	Confidence float64          `json:"confidence"`
	Reversible bool             `json:"reversible"`
}

// The one path a card takes to the operator, and back.
const (
	CardRaised = "mind.card"
	CardClosed = "mind.card.closed"
)

func (g *Gate) card(ctx context.Context, in model.Intent, mode string) {
	data, _ := json.Marshal(Card{IntentID: in.ID, Mode: mode, Skill: in.Skill, Kind: in.Kind, Title: in.Title, Body: in.Body,
		Preview: in.Preview, Reason: in.Reason, Why: in.Why, Evidence: in.Evidence, Confidence: in.Confidence, Reversible: in.Reversible})
	g.append(ctx, obs.Observation{Source: "mind", Type: CardRaised, Subject: in.ID, Data: data, Key: CardRaised + ":" + in.ID, OccurredAt: g.now()})
}

// closeCard tells the agent a card it shows was answered here, or went
// stale, so it stops asking.
func (g *Gate) closeCard(ctx context.Context, in model.Intent) {
	if !in.Card {
		return
	}
	data, _ := json.Marshal(map[string]string{"intentId": in.ID, "status": in.Status})
	g.append(ctx, obs.Observation{Source: "mind", Type: CardClosed, Subject: in.ID, Data: data,
		Key: CardClosed + ":" + in.ID + ":" + in.Status, OccurredAt: g.now()})
}

// tellLocal puts a card in the console's notices when there is no agent to
// show it (memory mode); in the stack the agent's card rings the phone.
func (g *Gate) tellLocal(ctx context.Context, in model.Intent, title, body string) {
	if l, ok := g.notify.(interface{ Local() bool }); ok && l.Local() {
		g.tell(ctx, in, title, body)
	}
}

func (g *Gate) append(ctx context.Context, o obs.Observation) {
	if err := g.log.Append(ctx, o); err != nil {
		g.out.Warn("카드를 보내지 못했습니다", "type", o.Type, "err", err)
	}
}

// Rebuild recounts the approval stats from the intents: what a card was
// shown for, and how each was answered. They are a derived table; this is
// how it is made again from what it is derived from.
func (g *Gate) Rebuild(ctx context.Context) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	all, err := g.st.IntentsSince(ctx, time.Time{})
	if err != nil {
		return 0, err
	}
	if err := g.st.ResetPolicy(ctx); err != nil {
		return 0, err
	}
	for i := len(all) - 1; i >= 0; i-- { // oldest first, so the last answer is last
		in := all[i]
		at := in.CreatedAt
		if in.DecidedAt != nil {
			at = *in.DecidedAt
		}
		if in.Card && in.Decision == model.Ask {
			if err := g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), model.OutcomeShown, in.CreatedAt); err != nil {
				return 0, err
			}
		}
		outcome := ""
		switch {
		case in.Status == model.Rejected:
			outcome = model.OutcomeRejected
		case in.Status == model.Undone:
			outcome = model.OutcomeUndone
		case in.DecidedBy == "user" && in.Status == model.Executed:
			outcome = model.OutcomeApproved
		}
		if outcome != "" {
			if err := g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), outcome, at); err != nil {
				return 0, err
			}
		}
	}
	return len(all), nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "int_" + hex.EncodeToString(b)
}
