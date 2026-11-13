// Package action defines what a module can be asked to do, and how that ask
// is shown to a person before it runs.
//
// Everything downstream keys off Kind: the approval card, the confidence
// gate, and the module registry all read the same string. A module declares
// its actions once and those three places are fed from that one declaration.
package action

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Action is one concrete request: a kind plus its arguments, still unparsed.
// The raw JSON survives from the tool call to the executor untouched, so the
// operator approves the same bytes that eventually run.
type Action struct {
	Kind  string          `json:"kind"`  // "calendar.create_event"
	Input json.RawMessage `json:"input"` // 도구가 받은 인자 원본
}

// Module returns the registry key: the part before the first dot.
func (a Action) Module() string {
	name, _, _ := strings.Cut(a.Kind, ".")
	return name
}

// Card is what a person actually judges. Body is the whole point: approving a
// change while seeing only its title is approving blind, so a card that edits
// or removes something renders both sides of the change.
//
// The words - title, body, consequence, the fields' labels, the choices - may
// be a model's (Writer). Facts are the code's, and so are the fields' keys and
// kinds: what the card is about and what runs are never put in words alone.
type Card struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Consequence string `json:"consequence"`
	// Images are upload names shown on the card - what a listing will look
	// like is half its photos.
	Images []string `json:"images,omitempty"`
	// Fields make the card a form: the operator may change these before
	// approving, and the module turns the edits into a new input (Editor).
	Fields []Field `json:"fields,omitempty"`
	// Facts are what the card is about as the code that raised it knows it:
	// the amount a payment window took, the site, the entry. They are shown
	// as they are, apart from the words, and no writer changes them.
	Facts []Fact `json:"facts,omitempty"`
	// Choices are the answers the card offers, in the order shown. None: 승인
	// and 반려.
	Choices []Choice `json:"choices,omitempty"`
	// Writer is whose words these are: empty, the code's that raised the card;
	// WriterModel, mind's writer's; WriterJob, the job's own model.
	Writer string `json:"writer,omitempty"`
	// Draft is the digest of the code's card the words were written from: the
	// same card asked again keeps its words (proposal.Store.Refresh).
	Draft string `json:"draft,omitempty"`
}

// Whose words a card's are (Card.Writer).
const (
	WriterModel = "model"
	WriterJob   = "job"
)

// Fact is one thing a card is about, as a label and a value.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Lead: the title says this value as it is - a payment's amount, which
	// the operator must read before anything else.
	Lead bool `json:"lead,omitempty"`
	// Warn: something to see before deciding - two amounts that differ.
	Warn bool `json:"warn,omitempty"`
}

// Choice is one answer a card offers. Every answer approves or turns down the
// card's one action: approving runs it as the form stands with Values put in,
// turning down runs nothing. What a button may do is the card's action, and
// nothing a writer can make up.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Decision is ChoiceApprove or ChoiceReject.
	Decision string `json:"decision"`
	// Values fill the card's fields when it is chosen: "22일로 옮기기" is the
	// form with its day set.
	Values map[string]string `json:"values,omitempty"`
	// Tone is how it is drawn: ToneApprove, ToneNeutral (an answer that is
	// neither a yes to run nor a turn-down: 아니요 of a question), ToneReject.
	Tone string `json:"tone,omitempty"`
	// Why: choosing it asks why first (a turn-down the next judgement learns from).
	Why bool `json:"why,omitempty"`
}

// A choice's decision: proposal.Approve and proposal.Reject, which this
// package cannot import.
const (
	ChoiceApprove = "approved"
	ChoiceReject  = "rejected"
)

// A choice's tone.
const (
	ToneApprove = "approve"
	ToneNeutral = "neutral"
	ToneReject  = "reject"
)

// MaxChoices is as many answers as fit side by side on a phone, two rows.
const MaxChoices = 4

// maxChoiceRunes is a button's label at most.
const maxChoiceRunes = 24

// Choice is the answer of this id the card offers.
func (c Card) Choice(id string) (Choice, bool) {
	for _, ch := range c.Choices {
		if ch.ID == id {
			return ch, true
		}
	}
	return Choice{}, false
}

// CheckChoices says what is wrong with the card's choices, if anything: an
// answer that runs and one that does not are both offered, each says what it
// is, and what it fills in are the card's own fields with values they take.
// A private field (a password) is never filled in by a choice. An answer that
// fills in fields is a shortcut: on a form, one answer that runs takes the
// form as the operator left it, or what they pick in a field has no button.
func (c Card) CheckChoices() error {
	if len(c.Choices) == 0 {
		return nil
	}
	if len(c.Choices) > MaxChoices {
		return fmt.Errorf("답은 %d개까지입니다", MaxChoices)
	}
	fields := map[string]Field{}
	for _, f := range c.Fields {
		fields[f.Key] = f
	}
	ids := map[string]bool{}
	var yes, no bool
	for _, ch := range c.Choices {
		if ch.ID == "" || ids[ch.ID] {
			return fmt.Errorf("답의 id 가 비었거나 겹칩니다: %q", ch.ID)
		}
		ids[ch.ID] = true
		label := strings.TrimSpace(ch.Label)
		if label == "" || utf8.RuneCountInString(label) > maxChoiceRunes {
			return fmt.Errorf("답의 이름은 1~%d자입니다: %q", maxChoiceRunes, ch.Label)
		}
		switch ch.Decision {
		case ChoiceApprove:
			yes = true
			if ch.Tone == ToneReject || ch.Why {
				return fmt.Errorf("실행하는 답은 반려처럼 그리거나 이유를 묻지 않습니다: %q", ch.Label)
			}
		case ChoiceReject:
			no = true
			if ch.Tone == ToneApprove {
				return fmt.Errorf("실행하지 않는 답은 승인처럼 그리지 않습니다: %q", ch.Label)
			}
			if len(ch.Values) > 0 {
				return fmt.Errorf("실행하지 않는 답은 값을 채우지 않습니다: %q", ch.Label)
			}
		default:
			return fmt.Errorf("답의 결정은 %s 이나 %s 입니다: %q", ChoiceApprove, ChoiceReject, ch.Decision)
		}
		switch ch.Tone {
		case "", ToneApprove, ToneNeutral, ToneReject:
		default:
			return fmt.Errorf("모르는 답의 색입니다: %q", ch.Tone)
		}
		for k, v := range ch.Values {
			f, ok := fields[k]
			switch {
			case !ok:
				return fmt.Errorf("카드에 없는 칸입니다: %s", k)
			case f.IsPrivate():
				return fmt.Errorf("비밀 칸은 답이 채우지 않습니다: %s", k)
			case f.Kind == "select" && len(f.Options) > 0 && !contains(f.Options, v):
				return fmt.Errorf("%s: 고를 수 있는 값이 아닙니다: %q", f.Label, v)
			}
		}
	}
	if !yes || !no {
		return errors.New("실행하는 답과 실행하지 않는 답이 하나씩은 있어야 합니다")
	}
	form, plain := false, false
	for _, f := range c.Fields {
		form = form || !f.IsPrivate()
	}
	for _, ch := range c.Choices {
		plain = plain || (ch.Decision == ChoiceApprove && len(ch.Values) == 0)
	}
	if form && !plain {
		return errors.New("칸이 있는 카드에는 칸에 적힌 대로 실행하는 답(values 없음)이 하나 있어야 합니다")
	}
	return nil
}

// Text is the card as lines of text, for whoever reads it rather than sees
// it - a push, the conversation's list of waiting cards: its body, then its
// facts as "항목: 값".
func (c Card) Text() string {
	lines := []string{}
	if b := strings.TrimSpace(c.Body); b != "" {
		lines = append(lines, b)
	}
	for _, f := range c.Facts {
		if strings.TrimSpace(f.Value) != "" {
			lines = append(lines, f.Label+": "+f.Value)
		}
	}
	return strings.Join(lines, "\n")
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Field is one value on a card the operator may edit.
type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Kind is "text", "textarea", "number", "select" or "secret" (typed
	// hidden, like a password).
	Kind    string   `json:"kind"`
	Value   string   `json:"value"`
	Options []string `json:"options,omitempty"`
	// Required blocks approval while the field is empty.
	Required bool `json:"required,omitempty"`
	// Hint is a line under the field (a price reference, a format).
	Hint string `json:"hint,omitempty"`
	// Private is a value that is never kept with the card nor told to the
	// model: at approval it goes to the module (module.SecretSink) and is
	// dropped from the edits. A "secret" field is always private.
	Private bool `json:"private,omitempty"`
}

// IsPrivate is whether the field's value must bypass the card's record.
func (f Field) IsPrivate() bool { return f.Private || f.Kind == "secret" }

// Spec is a module's declaration of one action it accepts.
type Spec struct {
	Kind    string
	Summary string // 모델이 읽는 한 줄. MCP 도구 설명으로 그대로 나간다.
	// Reversible decides whether this action may ever run without a card.
	// The confidence gate can raise its own bar but never lower this one:
	// an irreversible action is a card no matter how sure the model is.
	Reversible bool
}

// Result is what the operator is told after an action ran.
type Result struct {
	Note string `json:"note"`
}
