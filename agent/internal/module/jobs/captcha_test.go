package jobs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// A 보안문자 is the operator's to solve, not a site to skip: the run raises a
// card that says so - the page left on the screen, what to do if it is gone -
// rests the site's jobs, and ends deciding. Another run stopped at the same
// site raises no second card, and a login card for the site stays its own.
func TestCaptchaAsksTheOperatorToSolveIt(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, nil, nil)
	m.current.add(9, Job{ID: 177, Title: "벨락 수저 최저가", Sites: []string{"search.shopping.naver.com", "www.danawa.com"}}, true)
	s := runSession(t, m, "9")

	text, isErr := callRun(t, s, "request_captcha", map[string]any{"site": "https://search.shopping.naver.com/search/all?query=x", "seen": "네이버쇼핑 검색에 영수증 보안문자"})
	if isErr || !strings.Contains(text, "탭은 그대로") || !strings.Contains(text, "실행을 마치세요") {
		t.Fatalf("카드를 올리고 탭을 둔 채 마치라고 해야 합니다: %q", text)
	}
	cards := props.List()
	if len(cards) != 1 {
		t.Fatalf("카드 %d 장", len(cards))
	}
	c := cards[0]
	if c.Action.Kind != KindLogin || c.Card.Title != "보안문자를 풀어 주세요: search.shopping.naver.com" {
		t.Fatalf("카드: %+v", c.Card)
	}
	if !strings.HasPrefix(c.Card.Body, "네이버쇼핑 검색에 영수증 보안문자") || !strings.Contains(c.Card.Body, "화면에 보안문자가 없으면 그냥 승인") {
		t.Fatalf("카드 본문은 뜬 것과, 화면에서 사라졌을 때 할 일을 말해야 합니다: %q", c.Card.Body)
	}
	var in LoginInput
	if json.Unmarshal(c.Action.Input, &in) != nil || !in.Captcha || in.Site != "search.shopping.naver.com" {
		t.Fatalf("입력: %s", c.Action.Input)
	}
	if got := svc.got(); !contains(got, "/sites/login") || !contains(got, "/runs/9/schedule") {
		t.Fatalf("사이트를 쉬게 하고 실행을 결정된 것으로 쳐야 합니다: %v", got)
	}

	m.current.add(10, Job{ID: 178, Title: "티타늄 소주잔", Sites: []string{"search.shopping.naver.com"}}, true)
	s = runSession(t, m, "10")
	if _, isErr := callRun(t, s, "request_captcha", map[string]any{"site": "search.shopping.naver.com", "seen": "보안 확인 화면"}); isErr {
		t.Fatal("두 번째 요청도 실행은 마칠 수 있어야 합니다")
	}
	if n := len(props.List()); n != 1 {
		t.Fatalf("같은 사이트의 보안문자 카드는 하나여야 합니다: %d 장", n)
	}
	if _, isErr := callRun(t, s, "request_login", map[string]any{"site": "search.shopping.naver.com", "reason": "로그인 화면"}); isErr {
		t.Fatal("request_login")
	}
	if n := len(props.List()); n != 2 || !strings.HasPrefix(props.List()[1].Card.Title, "다시 로그인해 주세요") {
		t.Fatalf("로그인 카드는 따로 올라가야 합니다: %d 장", n)
	}

	if _, isErr := callRun(t, s, "request_captcha", map[string]any{"site": " "}); !isErr {
		t.Fatal("사이트가 없으면 거절해야 합니다")
	}
}

// A 보안문자 card turned down is told as such - the site's jobs go on
// without the site and say they did not see it - not as a refused login.
func TestRejectedCaptchaTellsTheSitesJobs(t *testing.T) {
	var mu sync.Mutex
	answers := map[string]string{}
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.HasSuffix(r.URL.Path, "/answer") {
			mu.Lock()
			answers[r.URL.Path] = body["text"].(string)
			mu.Unlock()
		}
		if r.Method == http.MethodGet && r.URL.Path == "/jobs" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{
				{ID: 177, State: "active", Sites: []string{"search.shopping.naver.com"}},
				{ID: 29, State: "waiting", Sites: []string{"www.coupang.com"}},
			}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer svc.Close()
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.URL, ""), nil, props, nil, quietLog())
	in := LoginInput{Site: "search.shopping.naver.com", Reason: "영수증 보안문자", Captcha: true}
	raw, _ := json.Marshal(in)
	p, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindLogin, Input: raw}, Card: loginCard(in)})
	m.decided(*p, proposal.Reject)

	mu.Lock()
	got := answers
	mu.Unlock()
	if a := got["/jobs/177/answer"]; !strings.HasPrefix(a, "[보안문자 반려] search.shopping.naver.com") || !strings.Contains(a, "이 사이트 없이") {
		t.Fatalf("보안문자 반려로 전해야 합니다: %q", a)
	}
	if _, told := got["/jobs/29/answer"]; told || len(got) != 1 {
		t.Fatalf("그 사이트의 작업에만 전합니다: %v", got)
	}
	if !woken(m) {
		t.Fatal("반려가 스케줄러를 깨우지 않았습니다")
	}
}
