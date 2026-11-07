package act

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// ReviewActuator does review.place and preference.confirm: an answer to a
// question. Approving is submitting; what the answer means is the
// preferences' to work out. Undoing withdraws the answer.
type ReviewActuator struct{}

func (ReviewActuator) Kinds() []string { return []string{model.ReviewPlace, model.PreferenceConfirm} }

type answered struct {
	SubjectType string            `json:"subjectType"`
	Subject     string            `json:"subject"`
	Text        string            `json:"text"`
	Answers     map[string]string `json:"answers"`
}

func (ReviewActuator) Preview(_ context.Context, kind string, payload json.RawMessage) (string, error) {
	var a answered
	if err := json.Unmarshal(payload, &a); err != nil {
		return "", err
	}
	if kind == model.ReviewPlace {
		switch a.SubjectType {
		case "캠핑장", "숙소", "식당", "카페", "":
			return fmt.Sprintf("%s 회고를 기록합니다 (이 장소의 특징과 내 취향을 따로 배워요)", a.Subject), nil
		case "주":
			return "이번 주 회고를 기록합니다", nil
		}
		return fmt.Sprintf("%s 회고를 기록합니다", a.Subject), nil
	}
	return fmt.Sprintf("답을 취향으로 기록합니다: %s", a.Text), nil
}

func (ReviewActuator) Execute(_ context.Context, kind string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var a answered
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, nil, err
	}
	switch kind {
	case model.ReviewPlace:
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

// PhotoPlaceActuator does photos.place: an answer, which mind files under the
// plan named (answered). Undoing takes the photos off it.
type PhotoPlaceActuator struct{}

func (PhotoPlaceActuator) Kinds() []string { return []string{model.PhotosPlace} }

func (PhotoPlaceActuator) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	var a struct {
		Photos []string `json:"photos"`
	}
	if err := json.Unmarshal(payload, &a); err != nil {
		return "", err
	}
	if len(a.Photos) == 0 {
		return "", errors.New("사진이 없습니다")
	}
	return fmt.Sprintf("사진 %d장을 고른 일정에 묶어 둡니다", len(a.Photos)), nil
}

func (PhotoPlaceActuator) Execute(_ context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var a answered
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(a.Answers["plan"]) == "" && strings.TrimSpace(a.Answers["note"]) == "" {
		return nil, nil, errors.New("어느 일정인지 골라 주세요")
	}
	return mustJSON(map[string]any{"answers": a.Answers}), json.RawMessage(`{"op":"forget"}`), nil
}

func (PhotoPlaceActuator) Undo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
