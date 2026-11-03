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
	"sync/atomic"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/registry"
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

// DefaultPolicy is the policy as it ships.
func DefaultPolicy() Policy { return PolicyFrom(registry.Default()) }

// PolicyFrom is the policy as the registry has it.
func PolicyFrom(r *registry.Snapshot) Policy {
	g := r.Gate()
	p := Policy{Levels: map[string]string{}, Fixed: map[string]bool{},
		Window: time.Duration(g.Window), DailyInterrupts: g.DailyInterrupts, DailyCards: g.DailyCards,
		MinConfidence: g.MinConfidence, InterruptCost: g.InterruptCost, QuietRaised: time.Duration(g.QuietRaised),
		QuietApproved: time.Duration(g.QuietApproved), QuietRejected: time.Duration(g.QuietRejected)}
	for kind, a := range r.Actions() {
		p.Levels[kind] = a.Level
		if a.Fixed {
			p.Fixed[kind] = true
		}
	}
	return p
}

// lowValue is below what a thing done and told rings for: it waits for the
// evening (a charge filed the usual way).
const lowValue = 0.5

// BriefSkill is the morning brief's skill (skill.Brief): the one notice the
// day's push budget never holds back.
const BriefSkill = "brief"

// ScheduleSkill is the skill that reads plans onto the calendar (skill.Schedule).
const ScheduleSkill = "schedule"

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
	// OnDecide hears every intent the operator answered (approved, turned
	// down, undid) - what notes learn from.
	OnDecide func(ctx context.Context, in model.Intent)
	// Judge hears what the rules leave open of two plans (Same). Nil leaves
	// them apart.
	Judge func(ctx context.Context, a, b model.State) bool
	// Turned hears whether an entry the assistant would put on the calendar is
	// the like of one the operator turned down, and which (TurnedDown): the
	// number of it in past, 0 for none. Nil asks nothing.
	Turned func(ctx context.Context, past []model.Turned, ev model.CalendarEvent, said string) int
	// book is the result ledger's record, replaced whole when the ledger is
	// made again. Nil knows nothing and every intent stands as stated.
	book atomic.Pointer[engine.Book]
}

// SetBook gives the gate the ledger's record.
func (g *Gate) SetBook(b *engine.Book) { g.book.Store(b) }

// Unlisted is the actions something can carry out that have no row: they
// would be refused, so a new one has to come with its row.
func (g *Gate) Unlisted() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, k := range g.acts.Kinds() {
		if _, ok := g.policy.Levels[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// SetPolicy gives the gate the policy as the registry now has it.
func (g *Gate) SetPolicy(p Policy) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.policy = p
}

// Book is the ledger's record as the gate has it.
func (g *Gate) Book() *engine.Book { return g.book.Load() }

// Reading is an intent's confidence corrected by how its kind has gone.
func (g *Gate) Reading(in model.Intent) engine.Reading {
	return g.book.Load().Read(engine.Key{Source: "card", Kind: in.Kind, Skill: in.Skill, Context: in.ContextKey()}, in.Confidence)
}

func New(st store.Store, acts *act.Registry, n act.Notifier, log obs.Log, p Policy, loc *time.Location, now func() time.Time, url string, out *slog.Logger) *Gate {
	return &Gate{st: st, acts: acts, notify: n, log: log, policy: p, loc: loc, now: now, url: url, out: out}
}

// Submit takes an intent from a skill and does with it what the policy says.
func (g *Gate) Submit(ctx context.Context, in model.Intent) (model.Intent, error) {
	g.mu.Lock()
	out, answered, err := g.submit(ctx, in)
	g.mu.Unlock()
	if answered {
		g.hear(ctx, out)
	}
	return out, err
}

// submit is Submit with the gate held. answered: the intent came out answered
// already (turned down in the conversation), for whoever learns from answers.
func (g *Gate) submit(ctx context.Context, in model.Intent) (model.Intent, bool, error) {
	in.ID = newID()
	in.CreatedAt = g.now()
	in.Status = model.Pending
	if in.Evidence == nil {
		in.Evidence = []model.Evidence{}
	}
	// The ledger first: a topic raised, approved or turned down lately is
	// not raised again - not even to replace its own open question.
	if quiet, why, err := g.quietTopic(ctx, in); err != nil {
		return in, false, err
	} else if quiet {
		at := g.now()
		in.Decision, in.Status, in.Why, in.DecidedBy, in.DecidedAt = model.Drop, model.Dropped, why, "policy", &at
		return in, false, g.st.PutIntent(ctx, in)
	}
	open, asked := g.openCard(ctx, in)
	if strings.HasPrefix(in.Kind, "calendar.") {
		// Asked in the conversation: the assistant's own card is the question,
		// and turned down there, it is turned down here.
		if held, ok := g.askedInChat(ctx, in); ok {
			at := g.now()
			in.Status, in.DecidedBy, in.DecidedAt, in.Error = model.Superseded, model.ByConversation, &at, coveredBy(heldCard(held))
			if held.Status == model.Rejected {
				in.Status, in.Error = model.Rejected, held.Error
			}
			return in, in.Status == model.Rejected, g.st.PutIntent(ctx, in)
		}
		// One entry asked about twice - the same days at the same place, read
		// in two rooms as two plans (2026-11-01), or a move and a move of it -
		// is one question: the card still open for the other is closed and
		// this one, the newer, stands.
		by := in.ID
		if asked {
			by = open.ID
		}
		if err := g.withdrawAlike(ctx, in, by); err != nil {
			return in, false, err
		}
	}
	// A newer intent about the same thing replaces a question still open: the
	// plan moved again before anyone answered. The card that asks the same thing
	// stays up until this one is known to be a question too.
	if in.EntityID != "" {
		if err := g.withdraw(ctx, in.EntityID, in.Skill, "새 판단으로 대체됨", open.ID); err != nil {
			return in, false, err
		}
	}
	out, err := g.admit(ctx, in, open, asked)
	if asked && out.ID != open.ID {
		// Done, dropped or failed instead: there is no question left to keep.
		if err := g.replace(ctx, open, "새 판단으로 대체됨"); err != nil {
			return out, false, err
		}
	}
	return out, false, err
}

// admit does with an intent what the policy says. A question that asks what
// open asks (asked) goes on that card.
func (g *Gate) admit(ctx context.Context, in, open model.Intent, asked bool) (model.Intent, error) {
	if in.Kind == "info" {
		in.Decision, in.Status, in.DecidedBy = model.Digest, model.Executed, "policy"
		in.Tier, in.Why = "digest", "알려 둘 일"
		if in.Urgent {
			in.Decision, in.Tier, in.Why = model.Notify, "now", "지금 알 일"
			// The morning brief is the day's first word and is not held
			// for the evening by what rang before it: nine loops with one
			// friend at 07:00 once sent the 08:00 brief to the evening
			// digest (2026-10-25).
			if in.Moment == "" && in.Skill != BriefSkill && g.overBudget(ctx) {
				in.Decision, in.Tier, in.Why = model.Digest, "digest", "오늘 알림 한도를 넘어 저녁 요약으로"
			}
		}
		at := g.now()
		in.DecidedAt, in.ExecutedAt = &at, &at
		g.tell(ctx, in, in.Title, in.Body)
		return in, g.st.PutIntent(ctx, in)
	}

	// The way out is narrow: an action with no row is not done.
	if _, ok := g.policy.Levels[in.Kind]; !ok && len(g.policy.Levels) > 0 {
		in.Status, in.Error = model.Failed, "등록되지 않은 행동입니다"
		return in, g.st.PutIntent(ctx, in)
	}
	a, ok := g.acts.For(in.Kind)
	if !ok {
		in.Status, in.Error = model.Failed, "이 행동을 할 수 있는 곳이 없습니다"
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
		in.Decision = model.Execute
		g.linkOnly(ctx, &in, "policy", already)
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
		// Linked only (the entry put there meanwhile): nothing to take back.
		if decision == model.Notify && in.Status == model.Executed && len(in.Undo) > 0 {
			in.Tier = "now"
			if g.overBudget(ctx) || (in.Value > 0 && in.Value < lowValue) {
				in.Tier = "digest"
			}
			// Done and told: the card offers the undo.
			in.Card = true
			g.card(ctx, in, "undo")
			g.tellLocal(ctx, in, "처리했어요", in.Title+"\n되돌리려면 눌러서 확인하세요")
		}
	case model.Ask:
		if asked {
			return g.retell(ctx, in, open)
		}
		in.Tier = "now"
		if quiet, why := g.quietCard(ctx, in); quiet {
			in.Tier, in.Why = "digest", why
		}
		// The one way a question reaches the operator: a card the agent
		// shows, answered back here. A quiet one (low confidence, the day's
		// interruptions used up, not worth a buzz) is a card all the same -
		// it just rings with the evening digest instead of now. A notice
		// with no card behind it read as "확인해 주세요" with nothing to
		// confirm and no calendar change (2026-10-18).
		in.Card = true
		g.card(ctx, in, "ask")
		g.tellLocal(ctx, in, "확인해 주세요", in.Title+"\n"+in.Reason)
		_ = g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), model.OutcomeShown, g.now())
	case model.Drop:
		in.Status = model.Dropped
	}
	return in, g.st.PutIntent(ctx, in)
}

// openCard is the card still open that asks what in asks: the same plan, the
// same skill, the same kind of calendar change.
func (g *Gate) openCard(ctx context.Context, in model.Intent) (model.Intent, bool) {
	if in.EntityID == "" || !strings.HasPrefix(in.Kind, "calendar.") {
		return model.Intent{}, false
	}
	about, err := g.st.IntentsFor(ctx, in.EntityID)
	if err != nil {
		return model.Intent{}, false
	}
	for _, p := range about {
		if p.Status == model.Pending && p.Card && p.Skill == in.Skill && p.Kind == in.Kind {
			return p, true
		}
	}
	return model.Intent{}, false
}

// retell puts what a question has become on the card asking it. A time moved
// again before anyone answered is one question: closed and raised anew each
// time it rang each time, and 45 entries took 81 cards (to 2026-11-03). The card
// keeps its place, its ring and its count; only what it says changes.
func (g *Gate) retell(ctx context.Context, in, open model.Intent) (model.Intent, error) {
	in.ID, in.CreatedAt, in.Card, in.Tier, in.Why = open.ID, open.CreatedAt, true, open.Tier, open.Why
	if len(in.Evidence) == 0 {
		in.Evidence = open.Evidence
	}
	g.send(ctx, in, "ask", CardUpdated, fmt.Sprintf("%s:%s:%d", CardUpdated, in.ID, g.now().UnixNano()))
	return in, g.st.PutIntent(ctx, in)
}

// Approve does an intent that was asked.
func (g *Gate) Approve(ctx context.Context, id string) (model.Intent, error) {
	return g.ApproveWith(ctx, id, nil)
}

// ApproveWith does it with the operator's answers to its form, put in the
// payload's "answers" for the action to read; a form with a required
// question unanswered is not approved.
func (g *Gate) ApproveWith(ctx context.Context, id string, answers map[string]string) (model.Intent, error) {
	g.mu.Lock()
	in, err := g.approveWithLocked(ctx, id, answers)
	g.mu.Unlock()
	if err == nil {
		g.hear(ctx, in)
	}
	return in, err
}

func (g *Gate) approveWithLocked(ctx context.Context, id string, answers map[string]string) (model.Intent, error) {
	in, err := g.pending(ctx, id)
	if err != nil {
		return in, err
	}
	// A form filled in and approved as it stands is answered by what it said.
	for _, f := range in.Fields {
		if strings.TrimSpace(answers[f.Key]) != "" {
			continue
		}
		if f.Value != "" {
			if answers == nil {
				answers = map[string]string{}
			}
			answers[f.Key] = f.Value
			continue
		}
		if f.Required {
			return in, fmt.Errorf("%s 에 답해 주세요", f.Label)
		}
	}
	if len(answers) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(in.Payload, &payload); err != nil || payload == nil {
			payload = map[string]any{}
		}
		payload["answers"] = answers
		in.Payload, _ = json.Marshal(payload)
	}
	a, ok := g.acts.For(in.Kind)
	if !ok {
		return in, errors.New("이 행동을 할 수 있는 곳이 없습니다")
	}
	// 실행보다 먼저 상태를 저장한다. 실행 뒤 저장이 실패해도 제안이 pending 으로
	// 남아 다시 승인되면, 같은 일정·메모가 한 번 더 만들어진다.
	in.Status = model.Executing
	if err := g.st.PutIntent(ctx, in); err != nil {
		return in, err
	}
	g.run(ctx, a, &in, "user")
	_ = g.st.CountPolicy(ctx, in.Skill, in.ContextKey(), model.OutcomeApproved, g.now())
	g.closeCard(ctx, in)
	return in, g.decided(ctx, in)
}

// Reject turns down an intent that was asked.
func (g *Gate) Reject(ctx context.Context, id string) (model.Intent, error) {
	return g.RejectWhy(ctx, id, "")
}

// RejectWhy turns it down with the operator's reason, kept with the intent:
// what the next judgement about the same thing reads.
func (g *Gate) RejectWhy(ctx context.Context, id, reason string) (model.Intent, error) {
	g.mu.Lock()
	in, err := g.rejectWhyLocked(ctx, id, reason)
	g.mu.Unlock()
	if err == nil {
		g.hear(ctx, in)
	}
	return in, err
}

func (g *Gate) rejectWhyLocked(ctx context.Context, id, reason string) (model.Intent, error) {
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
	return in, g.decided(ctx, in)
}

// Undo takes back something done. The result is fed back as an observation,
// so the world model learns the entry is gone.
func (g *Gate) Undo(ctx context.Context, id string) (model.Intent, error) {
	g.mu.Lock()
	in, err := g.undoLocked(ctx, id)
	g.mu.Unlock()
	if err == nil {
		g.hear(ctx, in)
	}
	return in, err
}

func (g *Gate) undoLocked(ctx context.Context, id string) (model.Intent, error) {
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
	return in, g.decided(ctx, in)
}

// decided records an answered intent; hear, once the gate is free again,
// tells whoever learns from it (which may raise a new intent of its own).
func (g *Gate) decided(ctx context.Context, in model.Intent) error {
	return g.st.PutIntent(ctx, in)
}

func (g *Gate) hear(ctx context.Context, in model.Intent) {
	if g.OnDecide != nil && (in.Status == model.Executed || in.Status == model.Rejected || in.Status == model.Undone) {
		g.OnDecide(ctx, in)
	}
}

// Withdraw drops a skill's open questions about an entity.
func (g *Gate) Withdraw(ctx context.Context, entityID, skill string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.withdraw(ctx, entityID, skill, "더 이상 필요 없음", "")
}

// withdraw closes a skill's open questions about an entity, but for keep.
func (g *Gate) withdraw(ctx context.Context, entityID, skill, why, keep string) error {
	open, err := g.st.IntentsFor(ctx, entityID)
	if err != nil {
		return err
	}
	for _, p := range open {
		if p.Status == model.Pending && p.Skill == skill && p.ID != keep {
			if err := g.replace(ctx, p, why); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *Gate) replace(ctx context.Context, p model.Intent, why string) error {
	p.Status, p.Error = model.Superseded, why
	if err := g.st.PutIntent(ctx, p); err != nil {
		return err
	}
	g.closeCard(ctx, p)
	return nil
}

// alikeWithin is how far back an open calendar question is looked for by
// its entry: one older than this is stale whatever it asked.
const alikeWithin = 30 * 24 * time.Hour

// withdrawAlike closes the open calendar questions about other plans that ask
// about the same entry as in (sameEntry): the card by asks it now.
func (g *Gate) withdrawAlike(ctx context.Context, in model.Intent, by string) error {
	var a model.CalendarAction
	if json.Unmarshal(in.Payload, &a) != nil {
		return nil
	}
	open, err := g.st.IntentsSince(ctx, g.now().Add(-alikeWithin))
	if err != nil {
		return err
	}
	for _, p := range open {
		// One's own plan: replaced by skill below, and another skill's card
		// about it waits beside this one.
		if p.Status != model.Pending || !strings.HasPrefix(p.Kind, "calendar.") || p.EntityID == in.EntityID {
			continue
		}
		var b model.CalendarAction
		if json.Unmarshal(p.Payload, &b) != nil || !g.sameEntry(ctx, in, a, p, b) {
			continue
		}
		at := g.now()
		p.Status, p.DecidedBy, p.DecidedAt, p.Tier = model.Superseded, "policy", &at, ""
		p.Error = "같은 일정의 새 카드로 바뀜 (" + by + ")"
		if err := g.st.PutIntent(ctx, p); err != nil {
			return err
		}
		g.closeCard(ctx, p)
	}
	return nil
}

// sameEntry is whether two calendar questions are about one entry: the entry
// itself, or plans that are one event.
func (g *Gate) sameEntry(ctx context.Context, x model.Intent, a model.CalendarAction, y model.Intent, b model.CalendarAction) bool {
	if a.EventID != "" && a.EventID == b.EventID {
		return true
	}
	return g.Same(ctx, g.planOf(ctx, x, a), g.planOf(ctx, y, b))
}

// Same is the one answer to whether two plans are one event, for all that asks
// the operator about plans: the rules (model.Compare), and what they leave open,
// the judgement.
func (g *Gate) Same(ctx context.Context, a, b model.State) bool {
	switch model.Compare(a, b) {
	case model.Same:
		return true
	case model.Unsure:
		return g.Judge != nil && g.Judge(ctx, a, b)
	}
	return false
}

// planOf is the plan a calendar question is about, or what the question itself
// says of it when there is none.
func (g *Gate) planOf(ctx context.Context, in model.Intent, a model.CalendarAction) model.State {
	s := model.State{Title: a.Event.Title, Date: a.Event.Date, EndDate: a.Event.EndDate, Start: a.Event.Start, End: a.Event.End, Place: a.Event.Place}
	if e, err := g.st.Entity(ctx, in.EntityID); err == nil {
		s.People = e.State.People
	}
	return s
}

// askedWithin is how long a card raised in the conversation speaks for its
// question, answered or not: mind reads the same conversation a minute later,
// and turned down there, it is not asked again here.
const askedWithin = 48 * time.Hour

// Asked enters a calendar card the assistant raised in the conversation. What
// the operator asked for by name is the card they are looking at: mind read the
// same words as a plan, found no entry and asked as well, and 6 of the 7 entries
// asked for that way took two cards (to 2026-11-03). mind's card still open about
// it stands aside, and none is raised while the row speaks (askedInChat). With
// no card in the conversation mind's is the only question, and stands.
func (g *Gate) Asked(ctx context.Context, card, kind, title string, a model.CalendarAction) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	payload, err := json.Marshal(a)
	if err != nil {
		return model.Intent{}, err
	}
	in, err := g.st.Intent(ctx, model.HeldID(card))
	switch {
	case errors.Is(err, model.ErrNotFound):
		in = model.Intent{ID: model.HeldID(card), Skill: model.HeldSkill, Kind: kind, Title: title, Holder: "chat", Status: model.Asked, CreatedAt: g.now()}
	case err != nil:
		return in, err
	}
	in.Payload = payload
	if err := g.st.PutIntent(ctx, in); err != nil {
		return in, err
	}
	open, err := g.st.IntentsSince(ctx, g.now().Add(-alikeWithin))
	if err != nil {
		return in, err
	}
	for _, p := range open {
		if p.Status == model.Pending && strings.HasPrefix(p.Kind, "calendar.") && g.asksSame(ctx, p, in) {
			if _, err := g.cover(ctx, p, card); err != nil {
				return in, err
			}
		}
	}
	return in, nil
}

// askedInChat is the card in the conversation that asks what in asks.
func (g *Gate) askedInChat(ctx context.Context, in model.Intent) (model.Intent, bool) {
	rows, err := g.st.HeldSince(ctx, g.now().Add(-askedWithin))
	if err != nil {
		return model.Intent{}, false
	}
	for _, held := range rows {
		if g.asksSame(ctx, in, held) {
			return held, true
		}
	}
	return model.Intent{}, false
}

// heldCard is the agent's card a held row stands for.
func heldCard(held model.Intent) string { return strings.TrimPrefix(held.ID, model.HeldID("")) }

// HeldAnswer is how the operator answered a card the agent holds (an
// intent.answer of the agent's).
type HeldAnswer struct {
	Card, Kind, Title, Holder string
	Approved                  bool
	// Reason is the operator's words turning a calendar card down.
	Reason string
	At     time.Time
}

// Answered enters the answer to a card the agent asked itself, which the agent
// carries on with whether or not mind is up. A calendar card of the
// conversation's turned down is turned down for mind as well: its own question
// that stood aside for it (cover) is turned down with the operator's words, so
// the next read and the next card hear them - they reached that session's model
// alone, and the next session asked the same again (2026-11-03). Only a card
// mind was told of (Asked) speaks for the operator: the guest's agent tells mind
// nothing, and the guest's words are not theirs.
func (g *Gate) Answered(ctx context.Context, a HeldAnswer) (model.Intent, error) {
	g.mu.Lock()
	in, turned, err := g.answered(ctx, a)
	g.mu.Unlock()
	for _, p := range turned {
		g.hear(ctx, p)
	}
	return in, err
}

func (g *Gate) answered(ctx context.Context, a HeldAnswer) (model.Intent, []model.Intent, error) {
	in, err := g.st.Intent(ctx, model.HeldID(a.Card))
	told := err == nil
	switch {
	case errors.Is(err, model.ErrNotFound):
		in = model.Intent{ID: model.HeldID(a.Card), Skill: model.HeldSkill, Kind: a.Kind, Title: a.Title, Holder: a.Holder, CreatedAt: a.At}
		if in.Holder == "" {
			in.Holder = "chat"
		}
	case err != nil:
		return in, nil, err
	}
	at := g.now()
	in.Status, in.DecidedBy, in.DecidedAt = model.Rejected, "user", &at
	if a.Approved {
		in.Status = model.Executed
	}
	var turned []model.Intent
	if told && in.Status == model.Rejected && strings.HasPrefix(in.Kind, "calendar.") {
		in.Error = strings.TrimSpace(a.Reason)
		if turned, err = g.turnDownCovered(ctx, in); err != nil {
			return in, turned, err
		}
	}
	return in, turned, g.st.PutIntent(ctx, in)
}

// turnDownCovered turns down the questions of mind's that stood aside for a
// card of the conversation's the operator turned down: theirs is its answer.
func (g *Gate) turnDownCovered(ctx context.Context, held model.Intent) ([]model.Intent, error) {
	recent, err := g.st.IntentsSince(ctx, held.CreatedAt.Add(-alikeWithin))
	if err != nil {
		return nil, err
	}
	by := coveredBy(heldCard(held))
	var out []model.Intent
	for _, p := range recent {
		if !p.Covered() || p.Error != by {
			continue
		}
		at := g.now()
		p.Status, p.DecidedAt, p.Error = model.Rejected, &at, held.Error
		if err := g.st.PutIntent(ctx, p); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

// TurnedDown is the no the operator already gave to the like of an entry the
// assistant would ask for in the conversation: its card passed no gate, and
// asked again what "차 예약은 일정 카드 필요없어" turned down (2026-11-03).
// What they said just now (said) is the newer word, and a request in it stands.
func (g *Gate) TurnedDown(ctx context.Context, ev model.CalendarEvent, said string) (model.Turned, bool, error) {
	if g.Turned == nil {
		return model.Turned{}, false, nil
	}
	since := g.now().Add(-model.TurnedWithin)
	mine, err := g.st.IntentsSince(ctx, since)
	if err != nil {
		return model.Turned{}, false, err
	}
	held, err := g.st.HeldSince(ctx, since)
	if err != nil {
		return model.Turned{}, false, err
	}
	past := model.TurnedDown(mine, held, model.TurnedMax)
	if len(past) == 0 {
		return model.Turned{}, false, nil
	}
	if i := g.Turned(ctx, past, ev, said); i >= 1 && i <= len(past) {
		return past[i-1], true, nil
	}
	return model.Turned{}, false, nil
}

// asksSame is whether a card in the conversation (held) asks what in asks: the
// same entry, unless it is moved two ways - two changes to one entry are two
// questions - or plans that are one event. What the operator said to the
// assistant is a plan read from the very words of the card, under a title of the
// interpreter's: there, the day and the hour say it.
func (g *Gate) asksSame(ctx context.Context, in, held model.Intent) bool {
	var a, b model.CalendarAction
	if len(held.Payload) == 0 || !strings.HasPrefix(held.Kind, "calendar.") ||
		json.Unmarshal(in.Payload, &a) != nil || json.Unmarshal(held.Payload, &b) != nil {
		return false
	}
	told := false
	if e, err := g.st.Entity(ctx, in.EntityID); err == nil && in.Skill == ScheduleSkill {
		told = model.IsOwnerRoom(e.Room)
	}
	one, other := g.planOf(ctx, in, a), g.planOf(ctx, held, b)
	switch {
	case a.EventID != "" && a.EventID == b.EventID:
		return told || in.Kind != "calendar.update" || !strings.HasPrefix(held.Kind, "calendar.update") || sameMove(a.Event, b.Event)
	case told:
		return model.SameHour(one, other)
	case a.EventID != "" && b.EventID != "":
		return false
	}
	return g.Same(ctx, one, other)
}

// sameMove is whether what the conversation's card says of an entry (b, only
// what it changes) is where mind would move it (a).
func sameMove(a, b model.CalendarEvent) bool {
	return b.Date+b.Start != "" && (b.Date == "" || b.Date == a.Date) && (b.Start == "" || b.Start == a.Start)
}

// Covered stands an asked intent aside for the same question asked in the
// conversation, on the assistant's own card (by). Nobody answered it, so there is
// nothing to learn from, and it is none of the day's cards.
func (g *Gate) Covered(ctx context.Context, id, by string) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, err := g.pending(ctx, id)
	if err != nil {
		return in, err
	}
	return g.cover(ctx, in, by)
}

func (g *Gate) cover(ctx context.Context, in model.Intent, by string) (model.Intent, error) {
	at := g.now()
	in.Status, in.DecidedBy, in.DecidedAt, in.Tier, in.Error = model.Superseded, model.ByConversation, &at, "", coveredBy(by)
	if err := g.st.PutIntent(ctx, in); err != nil {
		return in, err
	}
	g.closeCard(ctx, in)
	return in, nil
}

func coveredBy(card string) string {
	why := "대화에서 같은 카드를 먼저 올렸어요"
	if card != "" {
		why += " (" + card + ")"
	}
	return why
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
	if already := (*act.Already)(nil); errors.As(err, &already) {
		// Put there while the card waited - the assistant's own card for
		// the same entry approved first: linked, not made twice.
		g.linkOnly(ctx, in, by, already)
		return
	}
	if err != nil {
		in.Status, in.Error = model.Failed, err.Error()
		g.out.Warn("행동을 하지 못했습니다", "kind", in.Kind, "err", err)
		return
	}
	in.Status, in.Result, in.Undo, in.ExecutedAt = model.Executed, res, undo, &at
	g.observe(ctx, obs.ActionExecuted, *in, res)
}

// linkOnly records an action whose result was there already: done, with
// the entry found as its result (linked, for whoever reads it) and nothing
// to take back.
func (g *Gate) linkOnly(ctx context.Context, in *model.Intent, by string, already *act.Already) {
	at := g.now()
	in.Status, in.DecidedBy, in.DecidedAt, in.ExecutedAt = model.Executed, by, &at, &at
	in.Why, in.Preview = "이미 있어서 연결만 했어요: "+already.Line, already.Line
	in.Result, _ = json.Marshal(map[string]any{"calendarId": already.ID, "linked": true})
	g.observe(ctx, obs.ActionExecuted, *in, in.Result)
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
	n := act.Notice{
		Title: title, Body: strings.TrimSpace(body), URL: g.link(in),
		Tier: in.Tier, Level: level, Key: "mind:" + in.ID, At: g.now(),
	}
	if in.Moment != "" && in.Tier == "now" {
		// Said at its moment (the rain at the door): in the conversation too.
		n.Kind, n.Card, n.Data = "remind."+in.Moment, true, map[string]any{"why": in.Reason}
	}
	g.notify.Notify(ctx, n)
}

// link is where a tap on a notice goes: what the intent names (a calendar
// day, the 자산 screen) when the gate's url is the console's own - a path -
// and otherwise that url, marked with the intent.
func (g *Gate) link(in model.Intent) string {
	if in.Link != "" && strings.HasPrefix(g.url, "/") {
		return in.Link
	}
	return g.url + "#" + in.ID
}

// decide is the policy applied to one action.
func (g *Gate) decide(ctx context.Context, in model.Intent) (string, string, error) {
	stat, err := g.st.Policy(ctx, in.Skill, in.ContextKey())
	if err != nil {
		return "", "", err
	}
	// A context kept fixed is asked whatever its kind: moving a trip for
	// the weather is a calendar update like any other, and never done
	// without asking.
	kind := in.Kind
	if g.policy.Fixed[in.ContextKey()] {
		kind = in.ContextKey()
	}
	level, why := g.policy.Level(kind, stat)
	if level == Never {
		return model.Drop, why, nil
	}
	// How far it goes is the engine's: a slot comes graded against its own
	// kind, anything else is graded here against how its kind has gone. What
	// is always asked stays asked, and so does a kind just turned down.
	fixed := g.policy.Fixed[kind]
	grade, r := engine.Grade(in.Grade), engine.Reading{}
	if grade == "" {
		r = g.Reading(in)
		spec := engine.Spec{Reversible: in.Reversible}
		if fixed || turnedDown(stat) {
			spec.Cap = engine.AskFilled
		}
		grade = engine.Decide(spec, r, engine.Explored(in.ID))
	}
	switch {
	case grade == engine.Silent && in.Grade != "":
		return model.Drop, "확신이 낮아 묻지 않아요", nil
	case grade == engine.Silent && !fixed:
		// A kind turned down again and again stops being raised, but for the
		// odd one that keeps a way back.
		return model.Drop, fmt.Sprintf("이런 제안은 %d번 답하신 기록상 원하지 않으셔서 묻지 않아요 (%.0f%%)", r.N, r.P*100), nil
	case grade == engine.Act && in.Reversible && !fixed:
		return model.Notify, "늘 맞아서 바로 하고 알려요", nil
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

// turnedDown is whether the last answer to a kind's cards was no.
func turnedDown(stat model.PolicyStat) bool {
	return stat.Last == model.OutcomeRejected || stat.Last == model.OutcomeUndone
}

// Level is where a kind of action starts, and why. A last answer that was a
// rejection or an undo always brings it back to asking. Going further than it
// starts is the engine's.
func (p Policy) Level(kind string, stat model.PolicyStat) (string, string) {
	base, ok := p.Levels[kind]
	if !ok {
		base = AskFor
	}
	switch {
	case p.Fixed[kind]:
		return base, "항상 먼저 묻는 행동"
	case base == AskFor:
		return AskFor, "먼저 물어보는 행동"
	case turnedDown(stat):
		return AskFor, "최근에 거절하거나 되돌리셔서 다시 물어요"
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
	// One subject rings once: a second card about it, from another skill,
	// waits beside the first (a plan's "add it" and its "move it").
	if in.EntityID != "" {
		if open, err := g.st.IntentsFor(ctx, in.EntityID); err == nil {
			for _, p := range open {
				if p.ID != in.ID && p.Status == model.Pending && p.Card {
					return true, "같은 일에 대한 카드가 이미 떠 있어 함께 볼 수 있게 두었어요"
				}
			}
		}
	}
	r := g.Reading(in)
	if r.P < g.policy.MinConfidence {
		return true, fmt.Sprintf("확신도 %.0f%%라 저녁 요약으로", r.P*100)
	}
	if g.overBudget(ctx) {
		return true, "오늘 알림 한도를 넘어 저녁 요약으로"
	}
	cards := g.cardsToday(ctx)
	if g.policy.DailyCards > 0 && cards >= g.policy.DailyCards {
		return true, "오늘 카드 한도를 넘어 저녁 요약으로"
	}
	value := in.Value
	if value == 0 {
		value = 1
	}
	// A kind with answers on the ledger is read by them. The approval rate
	// is what the ledger already corrected for: counted again it would
	// halve the same doubt twice.
	worth := r.P * value
	if r.N == 0 {
		stat, _ := g.st.Policy(ctx, in.Skill, in.ContextKey())
		worth *= stat.Rate()
	}
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

// cardsToday is how many questions rang today - the quiet ones, raised for
// the digest, are not a day's worth of interruption.
func (g *Gate) cardsToday(ctx context.Context) int {
	n := 0
	for _, in := range g.today(ctx) {
		if in.Card && in.Decision == model.Ask && in.Tier == "now" {
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
	Fields     []model.Field    `json:"fields,omitempty"`
	Photos     []string         `json:"photos,omitempty"`
	// Quiet: raise the card, but do not ring for it - it goes out with the
	// evening digest (quietCard said why).
	Quiet bool `json:"quiet,omitempty"`
}

// The one path a card takes to the operator, and back.
const (
	CardRaised  = "mind.card"
	CardUpdated = "mind.card.updated"
	CardClosed  = "mind.card.closed"
)

func (g *Gate) card(ctx context.Context, in model.Intent, mode string) {
	g.send(ctx, in, mode, CardRaised, CardRaised+":"+in.ID)
}

func (g *Gate) send(ctx context.Context, in model.Intent, mode, typ, key string) {
	c := Card{IntentID: in.ID, Mode: mode, Skill: in.Skill, Kind: in.Kind, Title: in.Title, Body: in.Body,
		Preview: in.Preview, Reason: in.Reason, Why: in.Why, Evidence: in.Evidence, Confidence: in.Confidence, Reversible: in.Reversible,
		Fields: in.Fields, Photos: in.Photos, Quiet: in.Tier != "now"}
	data, _ := json.Marshal(c)
	g.append(ctx, obs.Observation{Source: "mind", Type: typ, Subject: in.ID, Data: data, Key: key, OccurredAt: g.now()})
}

// Close tells the agent a card went stale (withdrawn from outside the gate).
func (g *Gate) Close(ctx context.Context, in model.Intent) { g.closeCard(ctx, in) }

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
