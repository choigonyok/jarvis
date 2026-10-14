// Package mind is the agent's side of the one gate. mind-svc decides what
// reaches the operator - its skills' intents and the suggestions this
// agent's judging turn makes - and sends each question that should ring as
// a mind.card event. Here a card becomes an approval card like any other,
// and the operator's answer goes back to mind, which acts on it.
//
// Nothing here decides anything: no ledger, no budget, no trust levels.
// Those live in mind's gate, once.
package mind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
)

// Name is the module, and the prefix of the actions it runs.
const Name = "mind"

// Kinds: approving an asked card does the intent; approving a done-and-told
// card takes it back.
const (
	Approve = "mind.approve"
	Undo    = "mind.undo"
)

// Event types of the one path a card takes (mind-svc internal/gate).
const (
	CardRaised = "mind.card"
	CardClosed = "mind.card.closed"
)

// Card is mind.card's data (mind-svc internal/gate.Card).
type Card struct {
	IntentID   string     `json:"intentId"`
	Mode       string     `json:"mode"` // ask | undo
	Skill      string     `json:"skill"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Preview    string     `json:"preview"`
	Reason     string     `json:"reason"`
	Why        string     `json:"why"`
	Evidence   []Evidence `json:"evidence"`
	Confidence float64    `json:"confidence"`
	Reversible bool       `json:"reversible"`
}

type Evidence struct {
	Ref   string `json:"ref"`
	Quote string `json:"quote"`
}

// Input is what an approval card carries to mind.
type Input struct {
	IntentID string `json:"intentId"`
}

// Client talks to mind-svc's API.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// Module is the actuator for mind.approve and mind.undo.
type Module struct{ c Client }

func New(c Client) *Module {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 90 * time.Second}
	}
	return &Module{c: c}
}

func (*Module) Name() string                { return Name }
func (*Module) Start(context.Context) error { return nil }
func (*Module) Stop(context.Context) error  { return nil }
func (m *Module) Client() Client            { return m.c }
func (*Module) Specs() []action.Spec {
	return []action.Spec{
		{Kind: Approve, Summary: "mind 가 물은 일을 한다", Reversible: true},
		{Kind: Undo, Summary: "mind 가 한 일을 되돌린다", Reversible: true},
	}
}

func (m *Module) Preview(_ context.Context, a action.Action) (action.Card, error) {
	var in Input
	if err := json.Unmarshal(a.Input, &in); err != nil || in.IntentID == "" {
		return action.Card{}, errors.New("intentId 가 필요합니다")
	}
	return action.Card{Title: a.Kind}, nil
}

func (m *Module) Execute(ctx context.Context, a action.Action) (action.Result, error) {
	var in Input
	if err := json.Unmarshal(a.Input, &in); err != nil || in.IntentID == "" {
		return action.Result{}, errors.New("intentId 가 필요합니다")
	}
	op := "approve"
	if a.Kind == Undo {
		op = "undo"
	}
	var out struct {
		Status  string `json:"status"`
		Preview string `json:"preview"`
		Error   string `json:"error"`
	}
	if err := m.c.Do(ctx, http.MethodPost, "/api/intents/"+in.IntentID+"/"+op, nil, &out); err != nil {
		return action.Result{}, err
	}
	if out.Status == "failed" {
		return action.Result{}, errors.New(out.Error)
	}
	if op == "undo" {
		return action.Result{Note: "되돌렸습니다."}, nil
	}
	return action.Result{Note: strings.TrimSpace("했습니다. " + out.Preview)}, nil
}

// Reject tells mind the operator turned a question down.
func (c Client) Reject(ctx context.Context, intentID, reason string) error {
	return c.Do(ctx, http.MethodPost, "/api/intents/"+intentID+"/reject", map[string]string{"reason": reason}, nil)
}

// Submit hands mind's gate an intent from the agent's judging turn; mind
// answers with what it decided (a card goes out as mind.card if it rings).
func (c Client) Submit(ctx context.Context, intent map[string]any) (Decided, error) {
	var out Decided
	return out, c.Do(ctx, http.MethodPost, "/api/intents", intent, &out)
}

// Decided is the part of mind's answer the judging turn reports.
type Decided struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Why      string `json:"why"`
	Status   string `json:"status"`
	Tier     string `json:"tier"`
	Card     bool   `json:"card"`
	Error    string `json:"error"`
}

func (c Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("mind %d: %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// CardText is how a mind card reads on an approval card: what, then what
// will run, then why and on what grounds.
func CardText(c Card) action.Card {
	var b strings.Builder
	if c.Body != "" {
		b.WriteString(c.Body)
	}
	if c.Preview != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(c.Preview)
	}
	if c.Reason != "" {
		b.WriteString("\n\n왜: " + c.Reason)
	}
	for i, e := range c.Evidence {
		if i == 3 {
			break
		}
		if strings.TrimSpace(e.Quote) != "" {
			b.WriteString("\n근거: “" + e.Quote + "”")
		}
	}
	card := action.Card{Title: c.Title, Body: strings.TrimSpace(b.String())}
	switch {
	case c.Mode == "undo":
		card.Title = "처리했어요: " + c.Title
		card.Consequence = "승인하면 되돌립니다. 반려하면 그대로 둡니다."
	case c.Kind == "note":
		card.Consequence = "승인하면 확인한 것으로 기록만 합니다. 실행되는 것은 없습니다."
	default:
		card.Consequence = "승인하면 바로 실행합니다."
	}
	return card
}
