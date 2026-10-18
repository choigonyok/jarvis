package jobs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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
	r.add(7, Job{ID: 1}, true)
	if _, ok := r.get(8); ok {
		t.Fatal("another run id must not pass")
	}
	if j, ok := r.get(7); !ok || j.ID != 1 {
		t.Fatal("the running run must pass")
	}
	r.remove(7)
	if _, ok := r.get(7); ok {
		t.Fatal("a finished run must not pass")
	}
}

// Jobs without sites run beside the conversation, without the browser; a
// job with sites waits for it. The scheduler looks past the one it cannot
// start (2026-10-18: everything waited on the conversation, one at a time).
func TestJobsWithoutSitesRunBesideTheConversation(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/due":
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{
				{ID: 1, Title: "가격 지켜보기", Sites: []string{"shop.example"}},
				{ID: 2, Title: "주간 운동 요약"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/1":
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 1, Title: "가격 지켜보기", Sites: []string{"shop.example"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/2":
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 2, Title: "주간 운동 요약"}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			mu.Lock()
			runs++
			id := runs
			mu.Unlock()
			_, _ = w.Write([]byte(`{"runId": ` + strconv.Itoa(id) + `}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer svc.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(NewStore(svc.URL, ""), nil, proposal.NewStore(bus.New()), nil, quiet)
	// The conversation is running: the browser is not to be had.
	bg := &bgFake{canStart: func(browser bool) bool { return !browser }}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	m.Run(ctx, bg, SchedulerConfig{RunURL: "http://agent/mcp/job-run", Interval: 20 * time.Millisecond, Cooldown: time.Hour, Timeout: time.Minute})

	bg.mu.Lock()
	defer bg.mu.Unlock()
	if len(bg.tasks) == 0 {
		t.Fatal("사이트 없는 작업은 대화 중에도 돌아야 합니다")
	}
	for _, task := range bg.tasks {
		if task.Browser || len(task.Extra) != 0 || !strings.Contains(task.Name, "주간 운동 요약") {
			t.Fatalf("브라우저 없는 실행이어야 합니다: %+v", task)
		}
		for _, tool := range task.Allowed {
			if strings.Contains(tool, "browser_") {
				t.Fatalf("브라우저 도구가 실려 있습니다: %s", tool)
			}
		}
		if !strings.Contains(task.Prompt, "이 실행에는 브라우저가 없습니다") {
			t.Fatal("브라우저가 없다는 말이 프롬프트에 없습니다")
		}
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

// A job that names no site and attaches no photo is made without a card; one
// with a site still goes on one (2026-10-19: reminders asked for approval).
func TestReminderJobsNeedNoCard(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/1":
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 1, Title: "개발 할 일 알림"}})
		case "/jobs/2":
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 2, Title: "가격 지켜보기", Sites: []string{"shop.example"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer svc.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(NewStore(svc.URL, ""), nil, proposal.NewStore(bus.New()), nil, quiet)
	create := QualifiedToolName("create_job")
	instruct := QualifiedToolName("instruct_job")
	for _, c := range []struct {
		tool, input string
		free        bool
	}{
		{create, `{"title":"매일 아침 날씨","goal":"아침 8시에 알려 준다"}`, true},
		{create, `{"title":"판매","goal":"올린다","sites":["web.joongna.com"]}`, false},
		{create, `{"title":"판매","goal":"올린다","photos":["a.jpg"]}`, false},
		{instruct, `{"id":1,"text":"내용 추가"}`, true},
		{instruct, `{"id":2,"text":"가격 내려"}`, false},
		{instruct, `{"id":9,"text":"없는 작업"}`, false},
		{"mcp__jobs__list_jobs", `{}`, false},
	} {
		if got := m.CardFree(c.tool, json.RawMessage(c.input)); got != c.free {
			t.Errorf("%s %s: 카드 없이 %v, want %v", c.tool, c.input, got, c.free)
		}
	}
}
