package jobs

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// chatSession is the conversation's own MCP connection to the jobs.
func chatSession(t *testing.T, m *Module) *mcp.ClientSession {
	t.Helper()
	server := httptest.NewServer(m.Handler())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// paymentFor is a payment card a job's run stopped at.
func paymentFor(props *proposal.Store, job int64) string {
	raw, _ := json.Marshal(map[string]any{"jobId": job, "host": "pay.example"})
	p, _ := props.Open(proposal.Proposal{Origin: proposal.OriginIntercept, Action: action.Action{Kind: "payment", Input: raw},
		Card: action.Card{Title: "결제 승인"}})
	return p.ID
}

func stateOf(props *proposal.Store, id string) proposal.Proposal {
	p, _ := props.Get(id)
	return p
}

// The operator's instruction answers what the job asked: its questions come
// down, and the conversation is told so. A payment waiting on the operator
// and another job's question stay.
func TestAnInstructionAnswersTheJobsQuestions(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, &transcript{}, quietLog())
	first := askFor(props, 12, "장바구니에 3개만 보입니다. 어떻게 할까요?")
	second := askFor(props, 12, "다시 봐도 3개뿐입니다. 어떻게 할까요?")
	pay := paymentFor(props, 12)
	other := askFor(props, 13, "이 가격으로 올릴까요?")

	text, isErr := callRun(t, chatSession(t, m), "instruct_job", map[string]any{"id": 12, "text": "스크롤 내리면 다 있어. 다시 봐"})
	if isErr || !strings.Contains(text, "질문 카드 2장") {
		t.Fatalf("instruct_job: %q", text)
	}
	if !contains(svc.got(), "/jobs/12/instruct") {
		t.Fatalf("지시가 작업에 가야 합니다: %v", svc.got())
	}
	for _, id := range []string{first, second} {
		if p := stateOf(props, id); p.State != proposal.Rejected || !strings.Contains(p.Note, "지시를 답으로") {
			t.Fatalf("지시로 답한 질문은 내려가야 합니다: %+v", p)
		}
	}
	if p := stateOf(props, pay); p.State != proposal.Pending {
		t.Fatalf("결제 카드는 지시로 내려가지 않습니다: %+v", p)
	}
	if p := stateOf(props, other); p.State != proposal.Pending {
		t.Fatalf("다른 작업의 질문은 그대로: %+v", p)
	}
	for _, c := range svc.got() {
		if strings.HasSuffix(c, "/answer "+first) || strings.HasSuffix(c, "/answer "+second) {
			t.Fatalf("내린 질문을 작업에 반려로 전하지 않습니다: %v", svc.got())
		}
	}
}

// One question at a time: a job asking again takes down what it asked
// before and still waits on.
func TestANewQuestionTakesTheOldOnesPlace(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, &transcript{}, quietLog())
	old := askFor(props, 30, "장바구니에 3개만 보입니다. 어떻게 할까요?")
	m.current.add(7, Job{ID: 30, Title: "장바구니 가격 기록"}, true)

	if _, isErr := callRun(t, runSession(t, m, "7"), "ask", map[string]any{"question": "새로고침해도 3개뿐입니다. 어떻게 할까요?"}); isErr {
		t.Fatal("ask")
	}
	if p := stateOf(props, old); p.State != proposal.Rejected || !strings.Contains(p.Note, "새로 물어") {
		t.Fatalf("앞선 질문은 내려가야 합니다: %+v", p)
	}
	if n := len(props.PendingWhere(func(proposal.Proposal) bool { return true })); n != 1 {
		t.Fatalf("기다리는 질문은 하나: %d", n)
	}
}

// A run that found the answer itself takes its question down, and then it
// may set its next clock - a question left up would refuse it.
func TestARunWithdrawsAQuestionItAnswered(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, &transcript{}, quietLog())
	q := askFor(props, 30, "장바구니에 3개만 보입니다. 어떻게 할까요?")
	pay := paymentFor(props, 30)
	m.current.add(7, Job{ID: 30, Title: "장바구니 가격 기록"}, true)
	s := runSession(t, m, "7")

	text, isErr := callRun(t, s, "withdraw_question", map[string]any{"why": "스크롤해서 7개를 모두 찾았습니다"})
	if isErr || !strings.Contains(text, "1장") {
		t.Fatalf("withdraw_question: %q", text)
	}
	if p := stateOf(props, q); p.State != proposal.Rejected || !strings.Contains(p.Note, "7개를 모두 찾았습니다") {
		t.Fatalf("질문이 내려가야 합니다: %+v", p)
	}
	if p := stateOf(props, pay); p.State != proposal.Pending {
		t.Fatalf("결제 카드는 작업이 내리지 않습니다: %+v", p)
	}
	// The payment still waits, so the clock still waits on it.
	if _, isErr := callRun(t, s, "schedule_next", map[string]any{"at": "2026-11-09 19:30", "reason": "세일 뒤 가격 재확인"}); !isErr {
		t.Fatal("결제 카드가 기다리는 동안은 시각을 잡지 않습니다")
	}
	props.Abandon(pay, "시험")
	if text, isErr := callRun(t, s, "schedule_next", map[string]any{"at": "2026-11-09 19:30", "reason": "세일 뒤 가격 재확인"}); isErr {
		t.Fatalf("기다리는 카드가 없으면 시각을 잡습니다: %q", text)
	}
}

// The operator's words on a card go to the job with the decision: "이미
// 해결됐다, 월요일로 잡아라" turned down a question, and the job heard only
// that it was turned down.
func TestAnAnswerCarriesWhatTheOperatorSaid(t *testing.T) {
	in := AskInput{Question: "어떻게 할까요?", Fields: []action.Field{{Label: "진행 방법", Value: "보이는 것만"}}}
	if got := answerText(in, proposal.Reject, "이미 해결됐다. 월요일 19:30 으로 잡아라"); got != "[반려] 어떻게 할까요?\n- 운영자의 말: 이미 해결됐다. 월요일 19:30 으로 잡아라" {
		t.Fatalf("반려: %q", got)
	}
	if got := answerText(in, proposal.Approve, ""); got != "[승인] 어떻게 할까요?\n- 진행 방법: 보이는 것만" {
		t.Fatalf("승인: %q", got)
	}
}

// The job row says when the job runs next as the store has it, what it
// recorded, and what it said last in full - not the 200 characters of a log
// line, which cut a report of seven prices at the third.
func TestTheJobRowSaysWhatItRecordedAndWhenItRunsNext(t *testing.T) {
	m := newModule()
	next := time.Date(2026, 11, 9, 19, 30, 0, 0, time.Local)
	amount := int64(360456)
	long := "11/7 기준가: " + strings.Repeat("상품 하나 360,456/410,000 12% / ", 12) + "마지막 상품 66,300/78,000 15%"
	out := m.renderDetail(Detail{
		Job: Job{ID: 117, Title: "장바구니 할인율 비교", State: "active", NextAt: &next, NextReason: "세일 뒤 가격 재확인"},
		Records: []Record{
			{Key: "cart-1", Title: "체스터필드 코트 (L)", Status: "11/7 기록", Amount: &amount, Note: "정가 410,000 · 12%"},
			{Key: "cart-2", Title: "니트 머플러 (FREE)", Status: "11/7 기록"},
		},
		Events: []Event{
			{Kind: "note", Text: "실행을 마쳤습니다", At: next.Add(-50 * time.Hour)},
			{Kind: "report", Text: long, At: next.Add(-49 * time.Hour)},
		},
	})
	for _, want := range []string{"다음 실행: 11/9(월) 19:30 - 세일 뒤 가격 재확인", "기록한 값 2개:", "체스터필드 코트 (L) · 11/7 기록 · 360,456원 · 정가 410,000 · 12%", "마지막 상품 66,300/78,000 15%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q 이 없습니다:\n%s", want, out)
		}
	}

	props := proposal.NewStore(bus.New())
	m = New(NewStore("http://127.0.0.1:1", ""), nil, props, nil, nil)
	card := askFor(props, 117, "장바구니에 3개만 보입니다. 어떻게 할까요?")
	out = m.renderDetail(Detail{Job: Job{ID: 117, Title: "장바구니 할인율 비교", State: "waiting", WaitingCard: card}})
	if !strings.Contains(out, "다음 실행: 없음 - 카드 '장바구니에 3개만 보입니다. 어떻게 할까요?'의 답을 기다립니다") {
		t.Fatalf("카드를 기다리는 작업은 다음 실행이 없다고 말해야 합니다:\n%s", out)
	}
}

// A run that set its next look and then ends the job is turned back once:
// told "이번 실행을 마쳐라", one did, and the Monday look was gone. Called
// again, the finish stands.
func TestAFinishAfterAClockIsTurnedBackOnce(t *testing.T) {
	svc := &jobsSvc{}
	m := New(NewStore(svc.serve(t).URL, ""), nil, proposal.NewStore(bus.New()), &transcript{}, quietLog())
	m.current.add(7, Job{ID: 117, Title: "장바구니 할인율 비교"}, true)
	s := runSession(t, m, "7")

	if text, isErr := callRun(t, s, "schedule_next", map[string]any{"at": "2026-11-09 19:30", "reason": "세일 뒤 가격 재확인"}); isErr || !strings.Contains(text, "finish 없이") {
		t.Fatalf("schedule_next: %q", text)
	}
	text, isErr := callRun(t, s, "finish", map[string]any{"summary": "기준을 기억에 남겼습니다"})
	if !isErr || !strings.Contains(text, "finish 를 부르지 말고") {
		t.Fatalf("예약한 실행의 finish 는 한 번 막아야 합니다: %q", text)
	}
	for _, c := range svc.got() {
		if strings.HasSuffix(c, "/finish") {
			t.Fatalf("작업이 끝났습니다: %v", svc.got())
		}
	}
	if text, isErr := callRun(t, s, "finish", map[string]any{"summary": "정말 끝났습니다"}); isErr {
		t.Fatalf("두 번째 finish 는 받아들입니다: %q", text)
	}
}
