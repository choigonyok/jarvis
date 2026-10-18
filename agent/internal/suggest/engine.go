// Package suggest turns mind's cards into approval cards.
//
// mind-svc's gate is the one place that decides what reaches the operator:
// it keeps the ledger (what was raised, approved, turned down), the daily
// budget and the trust level, and it reads the events worth a suggestion
// (a portfolio leaving its band, a plan settled in a chat). When it decides a
// question should ring, it sends a mind.card; this opens it as an approval
// card in the conversation, closes it when mind answers it elsewhere, and
// tells mind when the operator turns it down.
package suggest

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
	"github.com/choigonyok/jarvis/gocommon/notify"
)

// Types is what the assistant subscribes to: mind's cards.
var Types = []string{mind.CardRaised, mind.CardClosed}

// Event is one event as events-svc delivers it.
type Event struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject"`
	Data       json.RawMessage `json:"data"`
	Key        string          `json:"key"`
	OccurredAt time.Time       `json:"occurredAt"`
}

type Config struct {
	EventsURL string
	Token     string
	// Out is where decisions are reported (the events outbox). Nil posts
	// straight to EventsURL.
	Out *notify.Client
	// Say puts a raised card in the conversation, under the line on why:
	// there it stays, approved or rejected, instead of floating below the
	// transcript only while it waits. Nil leaves it floating.
	Say func(lead string, proposalID string)
	// Mind is mind-svc's API, told when a card is turned down.
	Mind *mind.Client
}

type Engine struct {
	proposals *proposal.Store
	cfg       Config
	log       *slog.Logger
	http      *http.Client
}

func New(p *proposal.Store, cfg Config, log *slog.Logger) *Engine {
	e := &Engine{proposals: p, cfg: cfg, log: log, http: &http.Client{Timeout: 10 * time.Second}}
	if e.cfg.Out == nil {
		e.cfg.Out = notify.New(cfg.EventsURL, "", cfg.Token, log)
	}
	return e
}

// Consume is POST /consume: events-svc delivering.
func (e *Engine) Consume(w http.ResponseWriter, r *http.Request) {
	var ev Event
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&ev); err != nil {
		http.Error(w, "JSON 을 읽지 못했습니다", http.StatusBadRequest)
		return
	}
	switch ev.Type {
	case mind.CardRaised:
		e.raiseCard(ev)
	case mind.CardClosed:
		e.closeCard(ev)
	}
	w.WriteHeader(http.StatusNoContent)
}

// raiseCard opens an approval card for a question mind's gate decided
// should reach the operator. A card mind sent before (a redelivery) is not
// opened twice.
func (e *Engine) raiseCard(ev Event) {
	var c mind.Card
	if json.Unmarshal(ev.Data, &c) != nil || c.IntentID == "" {
		return
	}
	if _, ok := e.findCard(c.IntentID); ok {
		return
	}
	kind := mind.Approve
	if c.Mode == "undo" {
		kind = mind.Undo
	}
	card := mind.CardText(c)
	in := mind.Input{IntentID: c.IntentID}
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

// Subscribe registers this agent with events-svc for Types, retrying until
// events-svc answers.
func (e *Engine) Subscribe(ctx context.Context, self string) {
	if e.cfg.EventsURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"url": self, "types": Types})
	for wait := time.Second; ; wait = min(wait*2, time.Minute) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(e.cfg.EventsURL, "/")+"/subscriptions/agent", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if e.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+e.cfg.Token)
		}
		if resp, err := e.http.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				e.log.Info("events-svc 에 제안용으로 구독했습니다", "types", Types)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
