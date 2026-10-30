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
	"net/url"
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
	// Quiet: mind's gate raised the card but did not find it worth a buzz
	// now - its notification goes with the evening digest.
	Quiet bool `json:"quiet,omitempty"`
	// Fields make it a form: a question (a review, "맞나요?") answered on
	// the card, the answers sent back with the approval.
	Fields []action.Field `json:"fields,omitempty"`
	// Calendar is a calendar card's change: what tells it from the same
	// change asked in the conversation.
	Calendar *Calendar `json:"calendar,omitempty"`
}

type Evidence struct {
	Ref   string `json:"ref"`
	Quote string `json:"quote"`
}

// Calendar is the change a calendar card asks for (mind-svc
// model.CalendarAction): the entry it is about, if it is about one, and the
// entry to be.
type Calendar struct {
	EventID string `json:"eventId,omitempty"`
	Event   Event  `json:"event"`
}

// Event is an entry as mind says it.
type Event struct {
	Title string `json:"title"`
	Date  string `json:"date"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

// Input is what an approval card carries to mind. A form card also keeps
// its own card, to redraw it with the operator's answers, and the answers.
// A calendar card keeps its change, to be told from the same change raised
// in the conversation after it.
type Input struct {
	IntentID string            `json:"intentId"`
	Card     *action.Card      `json:"card,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Kind     string            `json:"kind,omitempty"`
	Calendar *Calendar         `json:"calendar,omitempty"`
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

// Revise is the operator's answers to a form card: kept in the input (sent
// with the approval), shown back on the card. A required answer left empty
// stops the approval.
func (m *Module) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	var in Input
	if err := json.Unmarshal(a.Input, &in); err != nil || in.Card == nil {
		return a, action.Card{}, errors.New("답을 받을 수 있는 카드가 아닙니다")
	}
	card := *in.Card
	card.Fields = append([]action.Field(nil), card.Fields...)
	answers := map[string]string{}
	for i, f := range card.Fields {
		v := strings.TrimSpace(edits[f.Key])
		if v == "" {
			v = strings.TrimSpace(f.Value) // filled in by mind, and left as it was
		}
		if f.Required && v == "" {
			return a, action.Card{}, fmt.Errorf("%s을(를) 골라 주세요.", f.Label)
		}
		card.Fields[i].Value = v
		if v != "" {
			answers[f.Key] = v
		}
	}
	in.Answers = answers
	input, _ := json.Marshal(in)
	return action.Action{Kind: a.Kind, Input: input}, card, nil
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
		Status  string          `json:"status"`
		Preview string          `json:"preview"`
		Error   string          `json:"error"`
		Result  json.RawMessage `json:"result"`
	}
	var body any
	if len(in.Answers) > 0 {
		body = map[string]any{"answers": in.Answers}
	}
	if err := m.c.Do(ctx, http.MethodPost, "/api/intents/"+in.IntentID+"/"+op, body, &out); err != nil {
		return action.Result{}, err
	}
	if out.Status == "failed" {
		return action.Result{}, errors.New(out.Error)
	}
	if op == "undo" {
		return action.Result{Note: "되돌렸습니다."}, nil
	}
	// The entry went on the calendar while the card waited (the
	// conversation's own card for it, approved first): mind tied the two
	// rather than put it there twice.
	var res struct {
		Linked bool `json:"linked"`
	}
	if json.Unmarshal(out.Result, &res) == nil && res.Linked {
		return action.Result{Note: strings.TrimSpace("캘린더에 이미 있어서 연결만 했어요. " + out.Preview)}, nil
	}
	return action.Result{Note: strings.TrimSpace("했습니다. " + out.Preview)}, nil
}

// Usage is mind's model cost (its /status usage), passed through as is.
func (c Client) Usage(ctx context.Context) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var st struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := c.Do(ctx, http.MethodGet, "/status", nil, &st); err != nil {
		return nil, err
	}
	if len(st.Usage) == 0 || string(st.Usage) == "null" {
		return nil, errors.New("사용량이 없습니다")
	}
	return st.Usage, nil
}

// Reject tells mind the operator turned a question down.
func (c Client) Reject(ctx context.Context, intentID, reason string) error {
	return c.Do(ctx, http.MethodPost, "/api/intents/"+intentID+"/reject", map[string]string{"reason": reason}, nil)
}

// Covered tells mind its card is not shown: the conversation asks the same
// already, on the assistant's own card (by). Nobody answered it - mind
// learns nothing from it and does not ask it again.
func (c Client) Covered(ctx context.Context, intentID, by string) error {
	return c.Do(ctx, http.MethodPost, "/api/intents/"+intentID+"/covered", map[string]string{"by": by}, nil)
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

// KindCategory is mind's action that files a charge.
const KindCategory = "spending.category"

// KindOf is the action a mind card stands for, or the proposal's own kind.
func KindOf(a action.Action) string {
	if a.Kind == Approve || a.Kind == Undo {
		var in Input
		if json.Unmarshal(a.Input, &in) == nil && in.Kind != "" {
			return in.Kind
		}
	}
	return a.Kind
}

// Slot is what mind says of a value kept elsewhere: whether it still holds,
// and if not, why.
type Slot struct {
	Holds bool    `json:"holds"`
	Why   string  `json:"why"`
	P     float64 `json:"p"`
	Grade string  `json:"grade"`
}

// Slot asks about a slot's value saved at saved (RFC3339).
func (c Client) Slot(ctx context.Context, name, saved string) (Slot, error) {
	var out Slot
	err := c.Do(ctx, http.MethodGet, "/api/slots/"+url.PathEscape(name)+"?saved="+url.QueryEscape(saved), nil, &out)
	return out, err
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
	card := action.Card{Title: c.Title, Body: strings.TrimSpace(b.String()), Fields: c.Fields}
	switch {
	case c.Kind == KindCategory && c.Mode != "undo":
		card.Consequence = "승인하면 이 분류로 바꿉니다. '앞으로도'면 이 가맹점의 지난·다음 결제도 같은 분류가 됩니다. 반려하면 기타로 두고 다시 묻지 않습니다."
	case len(c.Fields) > 0:
		card.Consequence = "승인하면 답을 보냅니다. 장소의 특징과 내 취향을 따로 배워요."
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
