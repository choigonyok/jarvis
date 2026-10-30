package jobs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// transcript stands in for the conversation: what was put in it.
type transcript struct {
	mu       sync.Mutex
	attached []string
}

func (t *transcript) JobNews(thread.Origin, []string) *thread.Turn { return nil }
func (t *transcript) AttachProposal(id string) {
	t.mu.Lock()
	t.attached = append(t.attached, id)
	t.mu.Unlock()
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func woken(m *Module) bool {
	select {
	case <-m.kick:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A run that asked waits on the card, never on a clock of its own: asking
// and then scheduling a look at the answer is refused (job 30, 2026-10-21 -
// six runs from 18:21 to 20:43 found "카드 답 대기 중"), the card goes into
// the conversation, and its decision wakes the job at once.
func TestAskedRunWaitsForTheDecision(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	chat := &transcript{}
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, chat, quietLog())
	m.current.add(7, Job{ID: 30, Title: "캠핑 소모품 구매"}, true)
	s := runSession(t, m, "7")

	text, isErr := callRun(t, s, "ask", map[string]any{"question": "이소가스 2개 묶음을 사도 될까요?"})
	if isErr || !strings.Contains(text, "곧바로 다시 실행") {
		t.Fatalf("ask: %q", text)
	}
	cards := props.List()
	if len(cards) != 1 {
		t.Fatalf("카드 %d장", len(cards))
	}
	card := cards[0].ID
	if !contains(svc.got(), "/runs/7/wait "+card) {
		t.Fatalf("작업을 카드에 걸어야 합니다: %v", svc.got())
	}
	if len(chat.attached) != 1 || chat.attached[0] != card {
		t.Fatalf("카드가 대화에 남아야 합니다: %v", chat.attached)
	}

	text, isErr = callRun(t, s, "schedule_next", map[string]any{"afterMinutes": 20, "reason": "카드 답 확인 후 주문 진행"})
	if !isErr || !strings.Contains(text, "기다리는 중") {
		t.Fatalf("카드를 기다리는 동안 답을 보러 올 시각은 잡지 않습니다: %q", text)
	}
	for _, c := range svc.got() {
		if strings.HasSuffix(c, "/schedule") {
			t.Fatalf("시계가 잡혔습니다: %v", svc.got())
		}
	}

	if err := props.Decide(card, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "결정이 작업에 가지 않았습니다", func() bool { return contains(svc.got(), "/jobs/30/answer "+card) })
	if !woken(m) {
		t.Fatal("결정이 스케줄러를 깨우지 않았습니다")
	}
}

// A run that says nothing of what comes next while its question is still up
// - one woken for an instruction, with nothing new to ask - puts the job back
// to waiting on that card instead of counting as a failed run, and its
// prompt said the card was still waiting.
func TestUndecidedRunWaitsOnItsCardAgain(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		call := r.Method + " " + r.URL.Path
		if c, ok := body["card"].(string); ok {
			call += " " + c
		}
		calls = append(calls, call)
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/30":
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 30, Title: "캠핑 소모품 구매", Sites: []string{"www.coupang.com"}},
				Inbox: []Inbox{{Kind: "instruction", Text: "밀키트는 다른 걸로", At: time.Now()}}})
		case strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"runId": 9}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer svc.Close()
	props := proposal.NewStore(bus.New())
	raw, _ := json.Marshal(AskInput{JobID: 30, JobTitle: "캠핑 소모품 구매", Question: "밀키트 후보 중 어떤 걸로 할까요?"})
	card, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindAsk, Input: raw}, Card: askCard(AskInput{Question: "밀키트 후보 중 어떤 걸로 할까요?"})})
	m := New(NewStore(svc.URL, ""), nil, props, nil, quietLog())
	bg := &bgFake{}
	m.runOnce(context.Background(), bg, SchedulerConfig{RunURL: "http://agent/mcp/job-run"}, Job{ID: 30, Title: "캠핑 소모품 구매"}, fakePlace{})

	if len(bg.tasks) != 1 || !strings.Contains(bg.tasks[0].Prompt, "아직 운영자의 답을 기다리는 카드") || !strings.Contains(bg.tasks[0].Prompt, card.ID) {
		t.Fatalf("기다리는 카드가 프롬프트에 없습니다:\n%s", bg.tasks[0].Prompt)
	}
	mu.Lock()
	defer mu.Unlock()
	wait, end := -1, -1
	for i, c := range calls {
		switch c {
		case "POST /runs/9/wait " + card.ID:
			wait = i
		case "POST /runs/9/end":
			end = i
		}
	}
	if wait < 0 || end < 0 || wait > end {
		t.Fatalf("실행을 닫기 전에 남은 카드에 다시 걸어야 합니다: %v", calls)
	}
}

// The long check behind the events: a job left waiting on a card decided
// while it was not told (the agent restarted between the two), or on a card
// that is gone, is given what became of it - and nothing is asked of a model
// for that. A card still open is left alone.
func TestSweepTellsWhatAJobNeverHeard(t *testing.T) {
	props := proposal.NewStore(bus.New())
	ask := func(job int64, q string) string {
		raw, _ := json.Marshal(AskInput{JobID: job, JobTitle: "작업", Question: q})
		p, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindAsk, Input: raw}, Card: askCard(AskInput{Question: q})})
		return p.ID
	}
	decided, open := ask(31, "올릴까요?"), ask(33, "내릴까요?")
	// Decided before this process could hear it: no module had registered.
	if err := props.Decide(decided, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	answers := map[string]string{}
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/jobs":
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{
				{ID: 31, State: "waiting", WaitingCard: decided},
				{ID: 32, State: "waiting", WaitingCard: "p-gone"},
				{ID: 33, State: "waiting", WaitingCard: open},
				{ID: 34, State: "active", WaitingCard: ""},
			}})
		case strings.HasSuffix(r.URL.Path, "/answer"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			answers[r.URL.Path] = body["text"]
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer svc.Close()
	m := New(NewStore(svc.URL, ""), nil, props, nil, quietLog())
	m.sweep(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(answers) != 2 {
		t.Fatalf("두 작업에만 전해야 합니다: %v", answers)
	}
	if a := answers["/jobs/31/answer"]; !strings.HasPrefix(a, "[승인] 올릴까요?") || !strings.Contains(a, "늦게") {
		t.Fatalf("결정된 카드: %q", a)
	}
	if a := answers["/jobs/32/answer"]; !strings.HasPrefix(a, "[카드 없음]") {
		t.Fatalf("없어진 카드: %q", a)
	}
}

// A login card turned down no longer leaves the site blocked and its jobs
// silent for good (job 27, 2026-10-20): the site opens, and every live job
// on it is told and woken.
func TestRejectedLoginTellsTheSitesJobs(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		switch {
		case r.URL.Path == "/sites/login":
			calls = append(calls, "login required="+map[bool]string{true: "true", false: "false"}[body["required"] == true])
		case strings.HasSuffix(r.URL.Path, "/answer"):
			calls = append(calls, r.URL.Path+" "+body["text"].(string)[:len("[로그인 반려]")])
		}
		mu.Unlock()
		if r.Method == http.MethodGet && r.URL.Path == "/jobs" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{
				{ID: 27, State: "active", Sites: []string{"search.shopping.naver.com"}},
				{ID: 28, State: "done", Sites: []string{"search.shopping.naver.com"}},
				{ID: 29, State: "waiting", Sites: []string{"www.coupang.com"}},
			}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer svc.Close()
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.URL, ""), nil, props, nil, quietLog())
	raw, _ := json.Marshal(LoginInput{Site: "search.shopping.naver.com", Reason: "보안문자"})
	p, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindLogin, Input: raw}, Card: loginCard(LoginInput{Site: "search.shopping.naver.com"})})
	m.decided(*p, proposal.Reject)

	mu.Lock()
	got := strings.Join(calls, " | ")
	mu.Unlock()
	if got != "login required=false | /jobs/27/answer [로그인 반려]" {
		t.Fatalf("사이트를 열고 그 사이트의 산 작업에만 전해야 합니다: %s", got)
	}
	if after, _ := props.Get(p.ID); after.State != proposal.Rejected || !strings.Contains(after.Note, "1개") {
		t.Fatalf("카드: %+v", after)
	}
	if !woken(m) {
		t.Fatal("반려가 스케줄러를 깨우지 않았습니다")
	}
}

// The scheduler sleeps on no short clock: with nothing due and none on a
// clock it waits for its long net, yet a knock (Wake) starts a job at once,
// and a job on a clock starts when the clock comes due.
func TestSchedulerWakesOnEventsAndClocks(t *testing.T) {
	var due atomic.Int64     // the job id /due hands out, 0 for none
	var nextAt atomic.Int64  // unix ms of the next clock, 0 for none
	var started atomic.Int64 // runs opened
	var asked atomic.Int64   // times /due was read
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/due":
			asked.Add(1)
			body := map[string]any{"jobs": []Job{}, "next": nil}
			if id := due.Swap(0); id != 0 {
				body["jobs"] = []Job{{ID: id, Title: "요약"}}
			}
			if ms := nextAt.Load(); ms != 0 {
				if time.Now().UnixMilli() >= ms {
					body["jobs"] = []Job{{ID: 42, Title: "시계"}}
					nextAt.Store(0)
				} else {
					body["next"] = time.UnixMilli(ms)
				}
			}
			_ = json.NewEncoder(w).Encode(body)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/jobs/"):
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 1, Title: "요약"}})
		case r.Method == http.MethodGet && r.URL.Path == "/jobs":
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{}})
		case strings.HasSuffix(r.URL.Path, "/runs"):
			started.Add(1)
			_, _ = w.Write([]byte(`{"runId": 1}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer svc.Close()
	m := New(NewStore(svc.URL, ""), nil, proposal.NewStore(bus.New()), nil, quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, &bgFake{}, SchedulerConfig{RunURL: "http://agent/mcp/job-run", Interval: time.Hour, Timeout: time.Minute})

	waitFor(t, "처음에 한 번은 봐야 합니다", func() bool { return asked.Load() >= 1 })
	time.Sleep(150 * time.Millisecond)
	if n := asked.Load(); n > 2 {
		t.Fatalf("할 일 없이 %d번 물었습니다 - 짧은 주기로 보고 있습니다", n)
	}
	due.Store(1)
	m.Wake()
	waitFor(t, "깨웠는데 작업이 시작되지 않았습니다", func() bool { return started.Load() == 1 })

	nextAt.Store(time.Now().Add(200 * time.Millisecond).UnixMilli())
	m.Wake() // the scheduler learns of the clock when it next looks
	waitFor(t, "시계가 된 작업이 시작되지 않았습니다", func() bool { return started.Load() == 2 })
}

// A decision that could not reach its job (mind down a moment) stays a
// decision on the card - not "failed", which would later be told as a
// rejection - and is handed on by the sweep once the delivery window is past.
func TestUndeliveredDecisionIsHandedOnSoon(t *testing.T) {
	window := deliveryWindow
	deliveryWindow = 50 * time.Millisecond
	t.Cleanup(func() { deliveryWindow = window })

	var mu sync.Mutex
	down := true
	var answers []string
	props := proposal.NewStore(bus.New())
	raw, _ := json.Marshal(AskInput{JobID: 40, JobTitle: "판매", Question: "올릴까요?"})
	p, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindAsk, Input: raw}, Card: askCard(AskInput{Question: "올릴까요?"})})
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/jobs":
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{{ID: 40, State: "waiting", WaitingCard: p.ID}}})
		case strings.HasSuffix(r.URL.Path, "/answer"):
			if down {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			answers = append(answers, body["text"])
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer svc.Close()
	m := New(NewStore(svc.URL, ""), nil, props, nil, quietLog())

	m.decided(*p, proposal.Approve)
	if got, _ := props.Get(p.ID); got.State != proposal.Approved || !strings.Contains(got.Note, "곧 다시") {
		t.Fatalf("전하지 못해도 승인은 승인으로 남아야 합니다: %+v", got)
	}
	m.sweep(context.Background()) // within the window: left to the delivery
	mu.Lock()
	down = false
	n := len(answers)
	mu.Unlock()
	if n != 0 {
		t.Fatal("전하는 중인 결정을 안전장치가 또 전했습니다")
	}
	time.Sleep(80 * time.Millisecond)
	if !m.retryDue(time.Now().Add(10 * time.Second)) {
		t.Fatal("곧 다시 보자는 표시가 없습니다")
	}
	m.sweep(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(answers) != 1 || !strings.HasPrefix(answers[0], "[승인] 올릴까요?") {
		t.Fatalf("늦게라도 승인으로 전해야 합니다: %v", answers)
	}
}

// With the web module, every run - with a browser or not - can search and
// read without adding a site, and is told so.
func TestRunsGetTheWebTools(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"runId": 9}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer svc.Close()
	m := New(NewStore(svc.URL, ""), nil, proposal.NewStore(bus.New()), nil, quietLog())
	bg := &bgFake{}
	web := &WebTools{Name: "web", URL: "http://agent/mcp/web", Tools: []string{"mcp__web__search", "mcp__web__read"}}
	m.runOnce(context.Background(), bg, SchedulerConfig{RunURL: "http://agent/mcp/job-run", Web: web}, Job{ID: 31, Title: "서레페 정보 조사"}, fakePlace{})

	if len(bg.tasks) != 1 {
		t.Fatalf("실행 %d번", len(bg.tasks))
	}
	task := bg.tasks[0]
	if task.Servers["web"] != "http://agent/mcp/web" || !slices.Contains(task.Allowed, "mcp__web__read") {
		t.Fatalf("web 서버·도구가 없습니다: %v %v", task.Servers, task.Allowed)
	}
	if !strings.Contains(task.System, "찾아보기") || !strings.Contains(task.Prompt, "web 도구로 브라우저 없이") {
		t.Fatalf("지시에 web 도구 안내가 없습니다:\n%s\n---\n%s", task.System, task.Prompt)
	}

	// Without it, as before.
	bg2 := &bgFake{}
	m.runOnce(context.Background(), bg2, SchedulerConfig{RunURL: "http://agent/mcp/job-run"}, Job{ID: 32, Title: "x"}, fakePlace{})
	if _, ok := bg2.tasks[0].Servers["web"]; ok || strings.Contains(bg2.tasks[0].System, "찾아보기") {
		t.Fatalf("web 이 없는데 붙었습니다: %v", bg2.tasks[0].Servers)
	}
}
