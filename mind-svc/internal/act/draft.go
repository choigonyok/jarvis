package act

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// DraftActuator does message.draft: a message the operator may send - an
// apology for running late, a reply - written as they would write it to
// that person. Nothing is sent: approving keeps the draft to copy.
type DraftActuator struct{ Drafts Drafter }

type draft struct {
	To      string `json:"to"`
	Text    string `json:"text"`
	Purpose string `json:"purpose"`
	PlanID  string `json:"planId,omitempty"`
	Room    string `json:"room,omitempty"`
}

func (DraftActuator) Kinds() []string { return []string{"message.draft"} }

func (a DraftActuator) Prepare(ctx context.Context, _ string, payload json.RawMessage) (json.RawMessage, error) {
	var d draft
	if err := json.Unmarshal(payload, &d); err != nil {
		return nil, err
	}
	if a.Drafts != nil && d.Purpose != "" {
		if text, err := a.Drafts.Draft(ctx, DraftRequest{To: d.To, Purpose: d.Purpose, Facts: []string{d.Text}}); err == nil && strings.TrimSpace(text) != "" {
			d.Text = strings.TrimSpace(text)
		}
	}
	return json.Marshal(d)
}

func (DraftActuator) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	var d draft
	if err := json.Unmarshal(payload, &d); err != nil {
		return "", err
	}
	if strings.TrimSpace(d.Text) == "" || strings.TrimSpace(d.To) == "" {
		return "", errors.New("받는 사람과 문장이 필요합니다")
	}
	return d.To + "에게 보낼 문장: “" + d.Text + "” (보내기는 직접)", nil
}

func (DraftActuator) Execute(_ context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	return payload, json.RawMessage(`{"op":"none"}`), nil
}

func (DraftActuator) Undo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
