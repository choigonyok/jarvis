package act

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ReviewActuator does review.place and preference.confirm: an answer to a
// question. Approving is submitting; what the answer means is the
// preferences' to work out. Undoing withdraws the answer.
type ReviewActuator struct{}

func (ReviewActuator) Kinds() []string { return []string{"review.place", "preference.confirm"} }

type answered struct {
	Subject string            `json:"subject"`
	Text    string            `json:"text"`
	Answers map[string]string `json:"answers"`
}

func (ReviewActuator) Preview(_ context.Context, kind string, payload json.RawMessage) (string, error) {
	var a answered
	if err := json.Unmarshal(payload, &a); err != nil {
		return "", err
	}
	if kind == "review.place" {
		return fmt.Sprintf("%s 회고를 기록합니다 (이 장소의 특징과 내 취향을 따로 배워요)", a.Subject), nil
	}
	return fmt.Sprintf("답을 취향으로 기록합니다: %s", a.Text), nil
}

func (ReviewActuator) Execute(_ context.Context, kind string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var a answered
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, nil, err
	}
	switch kind {
	case "review.place":
		if n, err := strconv.Atoi(strings.TrimSpace(a.Answers["score"])); err != nil || n < 1 || n > 5 {
			return nil, nil, errors.New("점수(1~5)가 필요합니다")
		}
	default:
		if strings.TrimSpace(a.Answers["answer"]) == "" {
			return nil, nil, errors.New("답이 필요합니다")
		}
	}
	return mustJSON(map[string]any{"answers": a.Answers}), json.RawMessage(`{"op":"forget"}`), nil
}

func (ReviewActuator) Undo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
