package act

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Preparer is an actuator that fills in its payload before anyone sees it -
// a draft message written for the person it goes to - so the card shows
// exactly what approving it will carry.
type Preparer interface {
	Prepare(ctx context.Context, kind string, payload json.RawMessage) (json.RawMessage, error)
}

// Drafter writes a message the operator would send to someone, in the
// operator's own way with them. Sending it is always the operator's.
type Drafter interface {
	Draft(ctx context.Context, d DraftRequest) (string, error)
}

// DraftRequest is one message to write: to whom, what it must say, and how
// the operator writes to that person (their recent messages to them).
type DraftRequest struct {
	To       string   `json:"to"`
	Purpose  string   `json:"purpose"` // what the message must get across
	Facts    []string `json:"facts"`   // numbers and names it must keep exactly
	Examples []string `json:"examples,omitempty"`
	// Room is the conversation it goes to, when known; Situation the kind
	// of moment (reconnect) whose replies are shown as examples; Incoming
	// the message it answers (the drafter reads it by IncomingRef).
	Room        string `json:"room,omitempty"`
	Situation   string `json:"situation,omitempty"`
	Incoming    string `json:"-"`
	IncomingRef string `json:"incomingRef,omitempty"`
}

// Template is the drafter with no model: the facts in a plain sentence.
type Template struct{}

func (Template) Draft(_ context.Context, d DraftRequest) (string, error) {
	return fmt.Sprintf("%s~ %s", d.To, d.Purpose), nil
}

// SettleActuator does settle.request: a draft asking each person for their
// share, and (as the result says) an open loop per person until it is paid.
// Nothing is sent: a message leaves only by the operator's hand.
type SettleActuator struct{ Drafts Drafter }

type settle struct {
	PlanID    string            `json:"planId"`
	Title     string            `json:"title"`
	Date      string            `json:"date"`
	Total     int64             `json:"total"`
	PerPerson int64             `json:"perPerson"`
	People    []string          `json:"people"`
	Drafts    map[string]string `json:"drafts,omitempty"`
}

func (SettleActuator) Kinds() []string { return []string{"settle.request"} }

func (a SettleActuator) Prepare(ctx context.Context, _ string, payload json.RawMessage) (json.RawMessage, error) {
	var s settle
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, err
	}
	d := a.Drafts
	if d == nil {
		d = Template{}
	}
	s.Drafts = map[string]string{}
	for _, p := range s.People {
		text, err := d.Draft(ctx, DraftRequest{To: p,
			Purpose: fmt.Sprintf("%s 정산 1인 %s원 보내 달라고", s.Title, Won(s.PerPerson)),
			Facts:   []string{s.Title, Won(s.PerPerson) + "원"}})
		if err != nil || strings.TrimSpace(text) == "" {
			text, _ = Template{}.Draft(ctx, DraftRequest{To: p, Purpose: fmt.Sprintf("오늘 %s 정산 1인 %s원이야!", s.Title, Won(s.PerPerson))})
		}
		s.Drafts[p] = strings.TrimSpace(text)
	}
	return json.Marshal(s)
}

func (SettleActuator) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	var s settle
	if err := json.Unmarshal(payload, &s); err != nil {
		return "", err
	}
	if s.PerPerson <= 0 || len(s.People) == 0 {
		return "", errors.New("나눌 금액과 사람이 필요합니다")
	}
	lines := []string{fmt.Sprintf("보낼 메시지 초안 %d개 (보내기는 직접):", len(s.People))}
	people := slices.Clone(s.People)
	slices.Sort(people)
	for _, p := range people {
		lines = append(lines, "· "+p+": "+s.Drafts[p])
	}
	lines = append(lines, "받을 때까지 사람마다 열린 고리로 기억합니다")
	return strings.Join(lines, "\n"), nil
}

func (SettleActuator) Execute(_ context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var s settle
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, nil, err
	}
	return payload, json.RawMessage(`{"op":"none"}`), nil
}

func (SettleActuator) Undo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// Won is 96000 as "96,000".
func Won(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
