package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// formModule is an actuator whose card is a form with one field, the day:
// the edits become its input.
type formModule struct{}

func (formModule) Name() string                { return "form" }
func (formModule) Start(context.Context) error { return nil }
func (formModule) Stop(context.Context) error  { return nil }
func (formModule) Specs() []action.Spec {
	return []action.Spec{{Kind: "form.move", Reversible: true}}
}
func (formModule) Preview(context.Context, action.Action) (action.Card, error) {
	return action.Card{}, nil
}
func (formModule) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{}, nil
}
func (formModule) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	raw, _ := json.Marshal(map[string]string{"date": edits["date"]})
	return action.Action{Kind: a.Kind, Input: raw}, action.Card{Title: "일정 옮기기",
		Fields: []action.Field{{Key: "date", Label: "날짜", Kind: "text", Value: edits["date"]}}}, nil
}

// An answer chosen among a card's choices decides as the choice says, with
// what it fills in over the form as the operator left it; one the card does
// not offer is refused and the card waits on.
func TestAChosenAnswerDecides(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := module.NewRegistry()
	reg.Add(formModule{})
	props := proposal.NewStore(bus.New())
	s := &Server{proposals: props, modules: reg, log: quiet}
	card := action.Card{Title: "캠핑을 옮길까요?", Writer: action.WriterModel,
		Fields: []action.Field{{Key: "date", Label: "날짜", Kind: "text", Value: "2026-11-15"}},
		Choices: []action.Choice{
			{ID: "c1", Label: "22일로 옮기기", Decision: action.ChoiceApprove, Values: map[string]string{"date": "2026-11-22"}},
			{ID: "c2", Label: "그대로 두기", Decision: action.ChoiceReject, Tone: action.ToneNeutral},
		}}
	p, _ := props.Open(proposal.Proposal{Action: action.Action{Kind: "form.move", Input: json.RawMessage(`{"date":"2026-11-15"}`)}, Card: card})

	if code, why := s.decide(context.Background(), p.ID, thread.Answer{Choice: "c9"}); code != http.StatusBadRequest || why == "" {
		t.Fatalf("없는 답: %d %q", code, why)
	}
	// The press says 반려 but the choice runs: the choice decides.
	if code, why := s.decide(context.Background(), p.ID, thread.Answer{Decision: "rejected", Choice: "c1", Edits: map[string]string{"date": "2026-11-29"}}); code != http.StatusNoContent {
		t.Fatalf("%d %s", code, why)
	}
	got, _ := props.Get(p.ID)
	if got.State != proposal.Approved || got.Choice != "c1" || got.Picked() != "22일로 옮기기" {
		t.Fatalf("고른 답으로 승인: %+v", got)
	}
	if string(got.Action.Input) != `{"date":"2026-11-22"}` || got.Card.Title != "캠핑을 옮길까요?" || len(got.Card.Choices) != 2 {
		t.Fatalf("답이 채운 값으로 실행하고, 문구는 그대로: %s %+v", got.Action.Input, got.Card)
	}
}
