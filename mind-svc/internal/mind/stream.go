package mind

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
)

// The deterministic lane: what comes back to mind from its own loop, or is
// asked of it - an answer to a card, a reminder to keep, a timer come due. No
// model is called here, and it runs on its own consumer, so a long read of a
// conversation holds none of it up.

// CommandKinds is what the lane reads of the operator's stream. An answer
// (intent.answer) is here for the cards of mind's own, which the agent asks
// of it; the cards the agent asked itself it carries on with (HeldCard).
var CommandKinds = []string{"intent.answer", "payment.requested", "reminder.*", "checklist.*", "note.*", "link.*", record.KindSpendingWrite, obs.TimerFired}

// GuestCommandKinds is what the lane reads of the guest's: their reminders
// and lists, and nothing that decides anything of the operator's.
var GuestCommandKinds = []string{"reminder.*", "checklist.*"}

// EmitTimer sends a due timer into the stream.
func EmitTimer(l obs.Log) func(ctx context.Context, t model.Timer) error {
	return func(ctx context.Context, t model.Timer) error {
		data, err := json.Marshal(t)
		if err != nil {
			return err
		}
		return l.Append(ctx, obs.Observation{
			Source: "mind", Type: obs.TimerFired, Subject: t.Skill + ":" + t.EntityID, Data: data,
			Key: fmt.Sprintf("timer:%d:%d", t.ID, t.FireAt.Unix()), OccurredAt: t.FireAt,
		})
	}
}

// TimerFired is a timer come back from the stream.
func (m *Mind) TimerFired(ctx context.Context, msg queue.Message) error {
	var t model.Timer
	if err := json.Unmarshal(msg.Payload, &t); err != nil || t.Skill == "" {
		return fmt.Errorf("%w: 타이머를 읽지 못했습니다", queue.ErrRefused)
	}
	m.Attn.Fire(ctx, t)
	return nil
}

// remind is a reminder's timer.
func (m *Mind) remind(ctx context.Context, t model.Timer) {
	if m.Moments == nil {
		return
	}
	if err := m.Moments.Fire(ctx, t.EntityID); err != nil {
		m.Opts.Logger.Warn("리마인드 때를 보지 못했습니다", "reminder", t.EntityID, "err", err)
	}
}

// answeredCard is an intent.answer as the agent writes it for any card.
type answeredCard struct {
	Card     string `json:"card"`
	Action   string `json:"action"`
	Title    string `json:"title"`
	Decision string `json:"decision"`
}

// payment.requested says a payment card went up; the answer closes it.
type raisedCard struct {
	Card  string `json:"card"`
	Title string `json:"title"`
}

func heldID(card string) string { return "card:" + card }

// mindActions are the agent's actions that are answers to mind's own cards,
// which come here as commands and are not the agent's to hold.
var mindActions = map[string]bool{"mind.approve": true, "mind.undo": true}

// PaymentRaised enters a card the agent holds in the ledger.
func (m *Mind) PaymentRaised(ctx context.Context, msg queue.Message) error {
	var c raisedCard
	if err := json.Unmarshal(msg.Payload, &c); err != nil || c.Card == "" {
		return fmt.Errorf("%w: 결제 카드를 읽지 못했습니다", queue.ErrRefused)
	}
	return m.Opts.Store.PutIntent(ctx, model.Intent{
		ID: heldID(c.Card), Skill: model.HeldSkill, Kind: "payment", Title: c.Title, Holder: msg.TraceID,
		Status: model.Pending, CreatedAt: msg.OccurredAt,
	})
}

// HeldCard is the answer to a card the agent asked itself: the agent carries
// on with it whether or not mind is up, and mind closes its ledger. Nothing is
// done here.
func (m *Mind) HeldCard(ctx context.Context, msg queue.Message) error {
	var a answeredCard
	if err := json.Unmarshal(msg.Payload, &a); err != nil || a.Card == "" || mindActions[a.Action] {
		return nil
	}
	in, err := m.Opts.Store.Intent(ctx, heldID(a.Card))
	if err != nil {
		in = model.Intent{ID: heldID(a.Card), Skill: model.HeldSkill, Kind: a.Action, Title: a.Title, Holder: msg.TraceID, CreatedAt: msg.OccurredAt}
		if in.Holder == "" {
			in.Holder = "chat"
		}
	}
	in.Status = model.Rejected
	if a.Decision == "approve" {
		in.Status = model.Executed
	}
	return m.Opts.Store.PutIntent(ctx, in)
}

// scheduleReminders gives every waiting reminder its timers.
func (m *Mind) scheduleReminders(ctx context.Context) {
	if err := m.Moments.Schedule(ctx); err != nil {
		m.Opts.Logger.Warn("리마인드 타이머를 맞추지 못했습니다", "err", err)
	}
}
