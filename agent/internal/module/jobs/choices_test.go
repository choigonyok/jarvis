package jobs

import (
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// A job may offer its own answer buttons: they go on its card as it wrote
// them - a no among them an answer, not a turn-down - and buttons that do not
// hold up (all one way) are sent back for the job to fix.
func TestAJobOffersItsOwnAnswers(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, &transcript{}, quietLog())
	m.current.add(7, Job{ID: 30, Title: "중고나라 판매"}, true)
	s := runSession(t, m, "7")

	text, isErr := callRun(t, s, "ask", map[string]any{"question": "가격을 어떻게 할까요?", "choices": []map[string]any{
		{"label": "모두 같은 값으로 올리기", "run": true},
		{"label": "올리지 않기", "run": false},
	}})
	if isErr {
		t.Fatalf("ask: %q", text)
	}
	card := props.List()[0].Card
	if card.Writer != action.WriterJob || len(card.Choices) != 2 || card.Choices[0].Decision != action.ChoiceApprove ||
		card.Choices[1].Decision != action.ChoiceReject || card.Choices[1].Tone != action.ToneNeutral || card.Choices[1].Why {
		t.Fatalf("작업이 준 답: %+v", card)
	}
	if len(card.Facts) != 1 || card.Facts[0].Value != "중고나라 판매" {
		t.Fatalf("어느 작업의 카드인지는 사실로: %+v", card.Facts)
	}

	text, isErr = callRun(t, s, "ask", map[string]any{"question": "어느 쪽으로 할까요?", "choices": []map[string]any{
		{"label": "A", "run": true}, {"label": "B", "run": true},
	}})
	if !isErr || !strings.Contains(text, "답 버튼을 쓸 수 없습니다") {
		t.Fatalf("한쪽뿐인 답은 돌려보냅니다: %q", text)
	}
}

// The job hears the answer chosen by its label, with the form as it stood.
func TestTheJobHearsTheAnswerChosen(t *testing.T) {
	in := AskInput{Question: "가격을 어떻게 할까요?", Fields: []action.Field{{Key: "price", Label: "가격", Value: "60000"}}}
	if got := answerOf(in, proposal.Approve, "", "모두 같은 값으로 올리기"); got != "[승인] 가격을 어떻게 할까요?\n- 고른 답: 모두 같은 값으로 올리기\n- 가격: 60000" {
		t.Fatalf("%q", got)
	}
	if got := answerOf(in, proposal.Reject, "다음 주에", "올리지 않기"); got != "[반려] 가격을 어떻게 할까요?\n- 고른 답: 올리지 않기\n- 운영자의 말: 다음 주에" {
		t.Fatalf("%q", got)
	}
}
