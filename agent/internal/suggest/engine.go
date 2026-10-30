// Package suggest turns mind's cards into approval cards - and lays in the
// conversation what mind tells the operator at its moment.
//
// mind-svc's gate is the one place that decides what reaches the operator:
// it keeps the ledger (what was raised, approved, turned down), the daily
// budget and the trust level, and it reads the events worth a suggestion
// (a portfolio leaving its band, a plan settled in a chat). When it decides a
// question should ring, it sends a mind.card; this opens it as an approval
// card in the conversation, closes it when mind answers it elsewhere, and
// tells mind when the operator turns it down.
//
// What mind says at a moment - a reminder, the rain at the door - goes to
// the phone through output-svc; a notice that asks for a card (data.card)
// is laid in the conversation here too, with the checklist it carries. And
// a list mind puts in front of the operator (checklist.shown) is shown here.
//
// The guest's agent runs this for the guest's notices alone (Config.Guest):
// a reminder of the guest's lays its card in the guest's conversation, as
// the operator's do in theirs. It raises no card of mind's - it has none of
// the operator's data - and reads the guest's effects, under a role that sees
// nothing else.
package suggest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/gocommon/queue"
)

// Types is what the assistant subscribes to: mind's cards, what mind says
// at a moment, and the lists it shows.
var Types = []string{mind.CardRaised, mind.CardClosed, "remind.*", ChecklistShown, JobsWake}

// GuestTypes is what the guest's agent subscribes to: what mind says to the
// guest at a moment.
var GuestTypes = []string{"remind.*", JobsWake}

// JobsWake says a job may be due now: changed on the 작업 tab, answered, made.
const JobsWake = "jobs.wake"

// ChecklistShown is mind asking for a list in the conversation now.
const ChecklistShown = "checklist.shown"

// Event is one record of the stream, as an event.
type Event struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject"`
	Data       json.RawMessage `json:"data"`
	Notify     *Wording        `json:"notify,omitempty"`
	Key        string          `json:"key"`
	OccurredAt time.Time       `json:"occurredAt"`
}

// Wording is what a notice says, as it goes to the phone.
type Wording struct {
	Tier  string `json:"tier"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Audience is whose phone: "guest" the guest's. input-svc sets it on
	// everything the guest's token posts.
	Audience string `json:"audience,omitempty"`
}

// Notice is what mind told at its moment, for a card in the conversation.
type Notice struct {
	// Key is the event's key: one notice, one card, however often delivered.
	Key   string
	Title string // what was said: the reminder's words
	Body  string // its line more
	Why   string // why now: "집에 도착하셔서 알려 드려요"
	// Checklist is the list it shows, if it carries one.
	Checklist string
}

type Config struct {
	// Guest is the guest's agent: only the guest's notices are laid, it
	// reads the guest's stream for GuestTypes, and nothing else is done.
	Guest bool
	// Out is where decisions are reported (the outbox). Nil reports nothing.
	Out *notify.Client
	// Say puts a raised card in the conversation, under the line on why:
	// there it stays, approved or rejected, instead of floating below the
	// transcript only while it waits. Nil leaves it floating.
	Say func(lead string, proposalID string)
	// Mind is mind-svc's API, told when a card is turned down.
	Mind *mind.Client
	// Notice lays a notice that asked for a card in the conversation; nil
	// leaves notices to the phone.
	Notice func(n Notice)
	// Show lays a list mind put in front of the operator in the
	// conversation, under lead; key is the event's, for redeliveries.
	Show func(id, lead, key string)
	// WakeJobs has the job scheduler look now.
	WakeJobs func()
}

type Engine struct {
	proposals *proposal.Store
	cfg       Config
	log       *slog.Logger
}

func New(p *proposal.Store, cfg Config, log *slog.Logger) *Engine {
	return &Engine{proposals: p, cfg: cfg, log: log}
}

// Consumer is this engine's place on what mind sends out: the guest's agent on
// the guest's, under its own name.
func (e *Engine) Consumer(pool *pgxpool.Pool) *queue.Consumer {
	if e.cfg.Guest {
		return &queue.Consumer{Pool: pool, Topic: queue.GuestEffects, Name: "agent-guest", Kinds: GuestTypes, Handle: e.Take, Log: e.log}
	}
	return &queue.Consumer{Pool: pool, Topic: queue.Effects, Name: "agent", Kinds: Types, Handle: e.Take, Log: e.log}
}

// Take takes one record of the stream.
func (e *Engine) Take(_ context.Context, m queue.Message) error {
	raw, err := m.Event()
	if err != nil {
		return fmt.Errorf("%w: %v", queue.ErrRefused, err)
	}
	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrRefused, err)
	}
	e.handle(ev)
	return nil
}

func (e *Engine) handle(ev Event) {
	switch {
	case ev.Type == JobsWake:
		if e.cfg.WakeJobs != nil {
			e.cfg.WakeJobs()
		}
	case e.cfg.Guest:
		// The guest's: what mind tells the guest at its moment, and no more.
		if strings.HasPrefix(ev.Type, "remind.") {
			e.notice(ev)
		}
	case ev.Type == mind.CardRaised:
		e.raiseCard(ev)
	case ev.Type == mind.CardClosed:
		e.closeCard(ev)
	case strings.HasPrefix(ev.Type, "remind."):
		e.notice(ev)
	case ev.Type == ChecklistShown:
		e.showList(ev)
	}
}

// notice lays what mind told at its moment in the conversation, when the
// notice asked for a card - the reminder's own words and line, why now, and
// its list - instead of leaving it to the phone and the inbox alone.
func (e *Engine) notice(ev Event) {
	if e.cfg.Notice == nil || ev.Notify == nil || ev.Notify.Tier != "now" {
		return
	}
	var d struct {
		Card      bool    `json:"card"`
		Why       string  `json:"why"`
		Body      *string `json:"body"`
		Checklist string  `json:"checklist"`
		Who       string  `json:"who"`
	}
	if json.Unmarshal(ev.Data, &d) != nil || !d.Card {
		return
	}
	// Each side's reminders are its own: the guest's reach the guest's
	// phone and conversation, never the operator's - and the other way round.
	// What goes to the guest's phone is the guest's, whoever the data names.
	if guests := ev.Notify.Audience == "guest" || d.Who == "guest"; guests != e.cfg.Guest {
		return
	}
	// The push's body carries what is left on the list and why now, both of
	// which the card shows its own way; the reminder's line is its own.
	body := ev.Notify.Body
	if d.Body != nil {
		body = *d.Body
	}
	key := ev.Key
	if key == "" {
		key = "event:" + strconv.FormatInt(ev.ID, 10)
	}
	e.cfg.Notice(Notice{Key: key, Title: strings.TrimSpace(ev.Notify.Title), Body: strings.TrimSpace(body), Why: strings.TrimSpace(d.Why), Checklist: d.Checklist})
}

// showList is mind putting a list in front of the operator.
func (e *Engine) showList(ev Event) {
	if e.cfg.Show == nil {
		return
	}
	var d struct {
		Checklist string `json:"checklist"`
		Lead      string `json:"lead"`
		Who       string `json:"who"`
	}
	if json.Unmarshal(ev.Data, &d) != nil || d.Checklist == "" || d.Who == "guest" {
		return
	}
	key := ev.Key
	if key == "" {
		key = "event:" + strconv.FormatInt(ev.ID, 10)
	}
	e.cfg.Show(d.Checklist, d.Lead, key)
}

// raiseCard opens an approval card for a question mind's gate decided
// should reach the operator. A card mind sent before (a redelivery) is not
// opened twice, and one the conversation already asks - the assistant's
// own card for the same calendar change waiting (calendar.go) - not at all,
// once mind knows; if mind cannot be told, it opens as before.
func (e *Engine) raiseCard(ev Event) {
	var c mind.Card
	if json.Unmarshal(ev.Data, &c) != nil || c.IntentID == "" {
		return
	}
	if _, ok := e.findCard(c.IntentID); ok {
		return
	}
	if c.Mode == "ask" {
		if chat, ok := e.chatCard(c.Kind, c.Calendar); ok {
			err := e.cover(c.IntentID, chat.ID)
			if err == nil {
				e.log.Info("대화에 같은 일정 카드가 있어 mind 카드를 열지 않았습니다", "intent", c.IntentID, "card", chat.ID)
				return
			}
			e.log.Warn("mind 에 대화의 카드가 있다고 알리지 못해 카드를 엽니다", "intent", c.IntentID, "card", chat.ID, "err", err)
		}
	}
	kind := mind.Approve
	if c.Mode == "undo" {
		kind = mind.Undo
	}
	card := mind.CardText(c)
	in := mind.Input{IntentID: c.IntentID, Kind: c.Kind, Calendar: c.Calendar}
	if len(card.Fields) > 0 {
		in.Card = &card // a form: redrawn with the answers on approval
	}
	input, _ := json.Marshal(in)
	p, _ := e.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginSuggest, Action: action.Action{Kind: kind, Input: input},
		Card: card, Confidence: c.Confidence, Quiet: c.Quiet,
	})
	if e.cfg.Say != nil {
		lead := strings.TrimSpace(c.Reason)
		if c.Mode == "undo" {
			lead = "믿고 맡기신 일이라 먼저 했어요. 아니면 되돌릴게요."
		}
		if lead == "" {
			lead = "먼저 여쭤볼게요."
		}
		e.cfg.Say(lead, p.ID)
	}
	e.emit(map[string]any{
		"source": "agent", "type": "suggestion.raised", "subject": p.ID,
		"data": map[string]any{"topic": "mind:" + c.Skill, "proposalId": p.ID, "title": c.Title, "intentId": c.IntentID},
		"key":  "suggestion:raised:" + p.ID,
	})
}

// closeCard settles the card of an intent answered or withdrawn in mind
// itself (the console, a newer judgement), so it stops asking here.
func (e *Engine) closeCard(ev Event) {
	var d struct {
		IntentID string `json:"intentId"`
		Status   string `json:"status"`
	}
	if json.Unmarshal(ev.Data, &d) != nil {
		return
	}
	p, ok := e.findCard(d.IntentID)
	if !ok || p.State != proposal.Pending {
		return
	}
	switch d.Status {
	case "executed", "undone":
		e.proposals.Settle(p.ID, proposal.Executed, "다른 곳에서 처리했습니다.")
	case "superseded":
		e.proposals.Abandon(p.ID, "새 판단으로 바뀌었습니다.")
	default:
		e.proposals.Abandon(p.ID, "다른 곳에서 정리했습니다.")
	}
}

// findCard is the card of a mind intent, if one was opened.
func (e *Engine) findCard(intentID string) (proposal.Proposal, bool) {
	for _, p := range e.proposals.List() {
		if p.Action.Kind != mind.Approve && p.Action.Kind != mind.Undo {
			continue
		}
		var in mind.Input
		if json.Unmarshal(p.Action.Input, &in) == nil && in.IntentID == intentID {
			return p, true
		}
	}
	return proposal.Proposal{}, false
}

// OnDecide hears every decision. Turning a mind card down is told to mind,
// whose gate learns from it; every decision goes out as an event.
func (e *Engine) OnDecide(p proposal.Proposal, d proposal.Decision) {
	if p.Origin != proposal.OriginSuggest {
		return
	}
	if p.Action.Kind == mind.Approve && d == proposal.Reject && e.cfg.Mind != nil {
		// Approving runs through the module (mind.approve); turning down
		// is told to mind here, so its gate learns from it.
		var in mind.Input
		if json.Unmarshal(p.Action.Input, &in) == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := e.cfg.Mind.Reject(ctx, in.IntentID, p.Reason); err != nil {
				e.log.Warn("mind 에 거절을 알리지 못했습니다", "intent", in.IntentID, "err", err)
			}
		}
	}
	e.emit(map[string]any{
		"source": "agent", "type": "suggestion.decided", "subject": p.ID,
		"data": map[string]any{"topic": "mind", "proposalId": p.ID, "title": p.Card.Title, "decision": string(d), "reason": p.Reason, "edits": len(p.Edits)},
		"key":  "suggestion:decided:" + p.ID,
	})
}

func (e *Engine) emit(ev map[string]any) { e.cfg.Out.Emit(ev) }
