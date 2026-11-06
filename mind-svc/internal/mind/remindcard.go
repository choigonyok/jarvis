package mind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
)

// remindAct carries out the card loops raises for the operator's own promise
// (skill.KindRemind): approved, the reminder it offered is kept - at the time
// and in the words the operator left on the card - and undone, it is
// cancelled. One reminder waits per loop: approved again, it is changed.
type remindAct struct{ m *Mind }

func (remindAct) Kinds() []string { return []string{skill.KindRemind} }

// remindPayload is what the card carries, and the operator's answers to it.
type remindPayload struct {
	Loop string `json:"loop"`
	Text string `json:"text"`
	Body string `json:"body"`
	At   string `json:"at"`
	// Choices is each time offered, as the card says it, to the time it is.
	Choices map[string]string `json:"choices"`
	Answers map[string]string `json:"answers"`
}

// reminder is the reminder the card stands for, as answered.
func (a remindAct) reminder(payload json.RawMessage) (moments.Reminder, error) {
	var p remindPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return moments.Reminder{}, fmt.Errorf("카드를 읽지 못했습니다: %w", err)
	}
	at, text := p.At, p.Text
	if v := strings.TrimSpace(p.Answers["at"]); v != "" {
		at = v
		if t, ok := p.Choices[v]; ok {
			at = t
		}
	}
	if v := strings.TrimSpace(p.Answers["text"]); v != "" {
		text = v
	}
	t, err := moments.ParseTime(at, a.m.Opts.Loc)
	if err != nil {
		return moments.Reminder{}, err
	}
	return moments.Reminder{Text: text, Body: p.Body, At: &t, Source: moments.Asked, Topic: "loop:" + p.Loop, Ref: p.Loop}, nil
}

func (a remindAct) Preview(_ context.Context, _ string, payload json.RawMessage) (string, error) {
	r, err := a.reminder(payload)
	if err != nil {
		return "", err
	}
	return moments.Stamp(*r.At, a.m.Opts.Loc) + "에 폰으로 알립니다: " + r.Text, nil
}

func (a remindAct) Execute(ctx context.Context, _ string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	if a.m.Moments == nil {
		return nil, nil, errors.New("리마인드를 둘 곳이 없습니다")
	}
	r, err := a.reminder(payload)
	if err != nil {
		return nil, nil, err
	}
	if r.At.Before(a.m.Opts.Now().Add(time.Minute)) {
		// Answered after the time it offered: nothing is set for a moment gone.
		return nil, nil, fmt.Errorf("고른 시각(%s)이 이미 지났어요. 대화에서 다시 정해 주세요", moments.Stamp(*r.At, a.m.Opts.Loc))
	}
	saved, err := a.m.Moments.Set(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	res, _ := json.Marshal(map[string]string{"reminder": saved.ID, "when": saved.When(a.m.Opts.Loc), "text": saved.Text})
	undo, _ := json.Marshal(map[string]string{"reminder": saved.ID})
	return res, undo, nil
}

func (a remindAct) Undo(ctx context.Context, _ string, undo json.RawMessage) (json.RawMessage, error) {
	var u struct {
		Reminder string `json:"reminder"`
	}
	if err := json.Unmarshal(undo, &u); err != nil || u.Reminder == "" {
		return nil, errors.New("되돌릴 리마인드를 모릅니다")
	}
	if a.m.Moments == nil {
		return nil, errors.New("리마인드를 둘 곳이 없습니다")
	}
	_, err := a.m.Moments.Cancel(ctx, u.Reminder)
	var gone moments.ErrInvalid
	if errors.As(err, &gone) || errors.Is(err, moments.ErrNotFound) {
		// Said, or cancelled, already: nothing is left to take back.
		return json.RawMessage(`{}`), nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"cancelled":true}`), nil
}
