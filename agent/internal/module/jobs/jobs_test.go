package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

func newModule() *Module {
	return New(NewStore("http://127.0.0.1:1", ""), nil, proposal.NewStore(bus.New()), nil, nil)
}

func askAction(t *testing.T, in AskInput) action.Action {
	t.Helper()
	raw, _ := json.Marshal(in)
	return action.Action{Kind: KindAsk, Input: raw}
}

func TestReviseRequiresAndApplies(t *testing.T) {
	m := newModule()
	a := askAction(t, AskInput{JobID: 1, JobTitle: "에어팟 판매", Question: "이대로 올릴까요?", Fields: []action.Field{
		{Key: "title", Label: "제목", Kind: "text", Value: "에어팟 프로 2"},
		{Key: "price", Label: "가격(원)", Kind: "number", Required: true},
		{Key: "shipping", Label: "택배비", Kind: "select", Options: []string{"포함", "별도"}, Value: "포함"},
	}})

	if _, _, err := m.Revise(context.Background(), a, nil); err == nil || !strings.Contains(err.Error(), "가격") {
		t.Fatalf("empty required field: %v", err)
	}
	if _, _, err := m.Revise(context.Background(), a, map[string]string{"price": "1", "shipping": "무료"}); err == nil {
		t.Fatal("value outside the options accepted")
	}
	if _, _, err := m.Revise(context.Background(), a, map[string]string{"price": "1", "photos": "x"}); err == nil {
		t.Fatal("unknown key accepted")
	}
	got, c, err := m.Revise(context.Background(), a, map[string]string{"price": "150000", "title": " 에어팟 프로 2세대 "})
	if err != nil {
		t.Fatal(err)
	}
	var in AskInput
	_ = json.Unmarshal(got.Input, &in)
	if in.Fields[0].Value != "에어팟 프로 2세대" || in.Fields[1].Value != "150000" || c.Fields[1].Value != "150000" {
		t.Fatalf("edits not applied: %+v", in.Fields)
	}
	text := answerText(in, proposal.Approve)
	if !strings.Contains(text, "[승인]") || !strings.Contains(text, "가격(원): 150000") {
		t.Fatalf("answer: %s", text)
	}
	if answerText(in, proposal.Reject) != "[반려] 이대로 올릴까요?" {
		t.Fatal("reject text")
	}
}

func TestRunToolsOnlyForTheRunInFlight(t *testing.T) {
	var r running
	if _, ok := r.get(7); ok {
		t.Fatal("nothing is running")
	}
	r.set(7, Job{ID: 1})
	if _, ok := r.get(8); ok {
		t.Fatal("another run id must not pass")
	}
	if j, ok := r.get(7); !ok || j.ID != 1 {
		t.Fatal("the running run must pass")
	}
	r.clear()
	if _, ok := r.get(7); ok {
		t.Fatal("a finished run must not pass")
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		step claudecode.Step
		want string
	}{
		{claudecode.Step{Tool: "mcp__mcp-browser__browser_navigate", Input: json.RawMessage(`{"url":"https://web.joongna.com/product/form"}`)}, "열기: https://web.joongna.com/product/form"},
		{claudecode.Step{Tool: "mcp__mcp-browser__browser_upload_files", Input: json.RawMessage(`{"paths":["a","b"]}`)}, "사진 2장 넣기"},
		{claudecode.Step{Tool: "mcp__job__schedule_next", Input: json.RawMessage(`{}`)}, ""},
		{claudecode.Step{Text: "판매 글 양식을 확인합니다."}, "판매 글 양식을 확인합니다."},
	}
	for _, c := range cases {
		if _, got := describe(c.step); got != c.want {
			t.Errorf("%+v: %q", c.step, got)
		}
	}
}

func TestPrompt(t *testing.T) {
	d := Detail{
		Job: Job{ID: 3, Title: "에어팟 판매", Goal: "중고나라에 팔기", Instructions: "택배거래만",
			Sites: []string{"web.joongna.com"}, Photos: []string{"a.jpg"}, Attempts: 1},
		Memory:  map[string]string{"posted_url": "https://web.joongna.com/product/1"},
		Inbox:   []Inbox{{Kind: "answer", Text: "[승인] 이대로 올릴까요?", At: time.Now()}},
		Records: []Record{{Key: "1", Title: "에어팟", Status: "판매중"}},
	}
	p := prompt(d, "/uploads", time.Date(2026, 10, 8, 14, 0, 0, 0, time.Local), nil)
	for _, want := range []string{"작업 3: 에어팟 판매", "택배거래만", "/uploads/a.jpg", "[카드 답", "posted_url", "[1] 에어팟 · 판매중", "지난 1번", "schedule_next"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "중간에 멈췄습니다") {
		t.Error("멈춘 실행이 없으면 그 이야기도 없습니다")
	}
	// A run the conversation cut short hands on what it did.
	cut := prompt(d, "/uploads", time.Now(), []string{"열기: https://web.joongna.com/sell", "입력: 에어팟 프로"})
	for _, want := range []string{"중간에 멈췄습니다", "- 열기: https://web.joongna.com/sell", "- 입력: 에어팟 프로"} {
		if !strings.Contains(cut, want) {
			t.Errorf("prompt lacks %q:\n%s", want, cut)
		}
	}
	if got := lastSteps([]string{"a", "b", "c"}, 2); strings.Join(got, ",") != "b,c" {
		t.Errorf("마지막 단계만: %v", got)
	}
}

// Approving a job's request for more sites adds them to the job and tells
// the job; turning it down only tells it.
func TestSitesCardAddsOnlyWhenApproved(t *testing.T) {
	var added []string
	var answers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/jobs/7/sites":
			for _, s := range body["add"].([]any) {
				added = append(added, s.(string))
			}
		case "/jobs/7/answer":
			answers = append(answers, body["text"].(string))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	props := proposal.NewStore(bus.New())
	m := New(NewStore(srv.URL, ""), nil, props, nil, nil)

	in, _ := json.Marshal(SitesInput{JobID: 7, JobTitle: "콘서트 일정", Sites: []string{"ticket.melon.com"}, Reason: "멜론티켓 공지"})
	p, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindSites, Input: in}, Card: sitesCard(SitesInput{JobTitle: "콘서트 일정", Sites: []string{"ticket.melon.com"}})})
	m.decided(*p, proposal.Reject)
	if len(added) != 0 || len(answers) != 1 || !strings.HasPrefix(answers[0], "[반려]") {
		t.Fatalf("반려는 알리기만: %v %v", added, answers)
	}
	m.decided(*p, proposal.Approve)
	if len(added) != 1 || added[0] != "ticket.melon.com" || !strings.HasPrefix(answers[1], "[승인]") {
		t.Fatalf("승인하면 더하고 알림: %v %v", added, answers)
	}
}
