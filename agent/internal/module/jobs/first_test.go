package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

func firstCards(props *proposal.Store) []proposal.Proposal {
	return props.PendingWhere(func(p proposal.Proposal) bool { return p.Action.Kind == KindFirst })
}

// A job the operator just asked for, waiting for the browser behind another
// job's run, is offered the front on one card - not one per look - and a
// job's own clock coming due is not. Its turn coming takes the card down.
func TestARequestedJobIsOfferedTheFront(t *testing.T) {
	props := proposal.NewStore(bus.New())
	chat := &transcript{}
	m := New(NewStore((&jobsSvc{}).serve(t).URL, ""), nil, props, chat, quietLog())
	holder := Job{ID: 1, Title: "중고나라에 올리기", Sites: []string{"web.joongna.com"}}
	asked := Job{ID: 2, Title: "쿠팡 장작 주문", Sites: []string{"www.coupang.com"}, NextReason: "첫 실행"}
	last := time.Now().Add(-time.Hour)
	clock := Job{ID: 3, Title: "가격 지켜보기", Sites: []string{"shop.example"}, NextReason: "다음 확인", LastRunAt: &last}

	m.offerFirst(asked, holder)
	m.offerFirst(asked, holder)
	m.offerFirst(clock, holder)
	cards := firstCards(props)
	if len(cards) != 1 {
		t.Fatalf("먼저 카드는 요청한 작업에 하나만: %+v", cards)
	}
	c := cards[0]
	if c.Card.Title != "'쿠팡 장작 주문' 먼저 할까요?" || !strings.Contains(c.Card.Body, "지금 하는 작업: 중고나라에 올리기") {
		t.Fatalf("카드: %+v", c.Card)
	}
	if len(chat.attached) != 1 || chat.attached[0] != c.ID {
		t.Fatalf("카드가 대화에 남아야 합니다: %v", chat.attached)
	}
	// Instructed again: still the same wait, no second card.
	asked.NextReason = "새 지시"
	m.offerFirst(asked, holder)
	if len(firstCards(props)) != 1 {
		t.Fatal("같은 기다림에 카드가 또 올라왔습니다")
	}

	m.closeOffer(asked.ID, "차례가 와서 시작했습니다.")
	if p, _ := props.Get(c.ID); p.State == proposal.Pending {
		t.Fatalf("차례가 오면 카드가 내려가야 합니다: %+v", p)
	}
	// Its next wait asks again.
	m.offerFirst(asked, holder)
	if len(firstCards(props)) != 1 {
		t.Fatal("다음 기다림에는 다시 물어야 합니다")
	}
	// Gone from the line (called off): its card comes down.
	m.dropOffers([]Job{clock})
	if len(firstCards(props)) != 0 {
		t.Fatal("줄에서 빠진 작업의 카드는 내려가야 합니다")
	}
}

// Approved, the job goes to the front of mind's line and the run on the
// browser is stopped for it, marked to keep its place; turned down, nothing
// moves.
func TestPuttingAJobFirst(t *testing.T) {
	svc := &jobsSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t).URL, ""), nil, props, &transcript{}, quietLog())
	bg := &bgFake{}
	m.bg = bg
	holder := Job{ID: 1, Title: "중고나라에 올리기", Sites: []string{"web.joongna.com"}}
	m.current.add(7, holder, true)

	m.offerFirst(Job{ID: 2, Title: "쿠팡 장작 주문", Sites: []string{"www.coupang.com"}}, holder)
	card := firstCards(props)[0].ID
	if err := props.Decide(card, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "먼저 하기가 mind 에 가지 않았습니다", func() bool { return contains(svc.got(), "/jobs/2/first") })
	waitFor(t, "카드가 처리되지 않았습니다", func() bool {
		p, _ := props.Get(card)
		return p.State == proposal.Executed
	})
	if p, _ := props.Get(card); !strings.Contains(p.Note, "'중고나라에 올리기' 작업을 잠시 멈추고") {
		t.Fatalf("카드 메모: %q", p.Note)
	}
	bg.mu.Lock()
	yields := len(bg.yields)
	bg.mu.Unlock()
	if yields != 1 || m.current.behind(7) != "쿠팡 장작 주문" {
		t.Fatalf("브라우저의 실행을 멈추고 자리를 지키게 해야 합니다: yields=%d behind=%q", yields, m.current.behind(7))
	}
	if !woken(m) {
		t.Fatal("스케줄러를 깨워야 합니다")
	}

	m.offerFirst(Job{ID: 3, Title: "책 주문", Sites: []string{"www.yes24.com"}}, holder)
	other := firstCards(props)[0].ID
	if err := props.Decide(other, proposal.Reject); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "반려가 처리되지 않았습니다", func() bool {
		p, _ := props.Get(other)
		return p.State == proposal.Rejected
	})
	if contains(svc.got(), "/jobs/3/first") {
		t.Fatal("반려하면 순서를 바꾸지 않습니다")
	}
}

// endSvc is mind for one run of job 5 (run 9): it keeps what the run was
// ended with, and answers the end as told.
type endSvc struct {
	mu    sync.Mutex
	ended map[string]any
	reply string
}

func (f *endSvc) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/jobs/"):
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 5, Title: "장작 주문", Goal: "사기", Sites: []string{"shop.example"}}})
		case strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"runId": 9}`))
		case r.URL.Path == "/runs/9/end":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.ended = body
			reply := f.reply
			f.mu.Unlock()
			if reply == "" {
				reply = "{}"
			}
			_, _ = w.Write([]byte(reply))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A run stopped for a job put first ends "deferred", naming that job, so it
// keeps its place in line; the browser was held to its sites only while it
// had the browser, and let go after.
func TestADeferredRunKeepsItsPlace(t *testing.T) {
	svc := &endSvc{}
	m := New(NewStore(svc.serve(t).URL, ""), nil, proposal.NewStore(bus.New()), &transcript{}, quietLog())
	g := &fakeGuard{}
	bg := &bgFake{err: claudecode.ErrYielded, during: func() { m.current.putBehind(9, "쿠팡 장작 주문") }}
	m.runOnce(context.Background(), bg, SchedulerConfig{RunURL: "http://agent/mcp/job-run", Guard: g, Timeout: time.Minute}, Job{ID: 5}, fakePlace{})
	svc.mu.Lock()
	ended := svc.ended
	svc.mu.Unlock()
	if ended["outcome"] != "deferred" || ended["note"] != "쿠팡 장작 주문" {
		t.Fatalf("실행 종료: %v", ended)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.sites) != 1 || g.released != 1 {
		t.Fatalf("사이트 제한은 실행이 브라우저를 가진 동안만: held=%v released=%d", g.sites, g.released)
	}
}

// Every job that is over is told on the phone - done by its run, or stopped
// for failing - and one called off by the operator is not.
func TestEveryEndIsTold(t *testing.T) {
	cases := []struct {
		name   string
		reply  string
		finish bool
		want   *EndNote
	}{
		{"끝냄", `{"over": true}`, true, &EndNote{Run: 9, Done: true, Text: "장작 두 묶음을 주문했습니다"}},
		{"실패", `{"failed": true, "note": "결제 화면이 열리지 않음"}`, false, &EndNote{Run: 9, Text: "결제 화면이 열리지 않음"}},
		{"취소", `{"over": true}`, false, nil},
		{"다음 확인", `{}`, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &endSvc{reply: c.reply}
			m := New(NewStore(svc.serve(t).URL, ""), nil, proposal.NewStore(bus.New()), &transcript{}, quietLog())
			var got []EndNote
			m.OnEnd(func(n EndNote) { got = append(got, n) })
			bg := &bgFake{during: func() {
				if c.finish {
					m.current.finish(9, "장작 두 묶음을 주문했습니다")
				}
			}}
			m.runOnce(context.Background(), bg, SchedulerConfig{RunURL: "http://agent/mcp/job-run", Timeout: time.Minute}, Job{ID: 5}, fakePlace{})
			if c.want == nil {
				if len(got) != 0 {
					t.Fatalf("알리지 않아야 합니다: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Run != c.want.Run || got[0].Done != c.want.Done || got[0].Text != c.want.Text || got[0].Job.Title != "장작 주문" {
				t.Fatalf("알림: %+v, 기대 %+v", got, *c.want)
			}
		})
	}
}

// In the scheduler: while another job's run has the browser, a due job the
// operator asked for waits (nothing starts on the browser) and is offered
// the front once; a job without sites runs meanwhile.
func TestTheSchedulerOffersTheFrontWhileTheBrowserIsBusy(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/due":
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []Job{
				{ID: 2, Title: "쿠팡 장작 주문", Sites: []string{"www.coupang.com"}, NextReason: "첫 실행"},
				{ID: 4, Title: "주간 운동 요약"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/4":
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 4, Title: "주간 운동 요약"}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"runId": 40}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer svc.Close()
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.URL, ""), nil, props, &transcript{}, quietLog())
	m.current.add(7, Job{ID: 1, Title: "중고나라에 올리기", Sites: []string{"web.joongna.com"}}, true)
	bg := &bgFake{canStart: func(browser bool) bool { return !browser }}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	m.Run(ctx, bg, SchedulerConfig{RunURL: "http://agent/mcp/job-run", Interval: 20 * time.Millisecond, Timeout: time.Minute})

	cards := firstCards(props)
	if len(cards) != 1 || !strings.Contains(cards[0].Card.Title, "쿠팡 장작 주문") {
		t.Fatalf("먼저 카드 하나: %+v", cards)
	}
	bg.mu.Lock()
	defer bg.mu.Unlock()
	if len(bg.tasks) == 0 {
		t.Fatal("사이트 없는 작업은 그동안 돌아야 합니다")
	}
	for _, task := range bg.tasks {
		if task.Browser {
			t.Fatalf("브라우저가 다른 작업의 것인데 시작했습니다: %+v", task.Name)
		}
	}
}

// capRunner is a runner with a few places that counts what ran at once.
type capRunner struct {
	mu            sync.Mutex
	free          int
	running, peak int
	ran           []string
	busy          int
}

type capPlace struct {
	c    *capRunner
	done bool
}

func (p *capPlace) Release() {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()
	if !p.done {
		p.done = true
		p.c.free++
	}
}

func (c *capRunner) Reserve(bool) (claudecode.Place, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.free == 0 {
		return nil, false
	}
	c.free--
	return &capPlace{c: c}, true
}

func (c *capRunner) Background(_ context.Context, t claudecode.Task) (string, error) {
	p, _ := t.Place.(*capPlace)
	c.mu.Lock()
	if p == nil || p.done {
		c.busy++
		c.mu.Unlock()
		return "", claudecode.ErrBusy
	}
	p.done = true // the place is the run's now; it comes back when the run ends
	c.running++
	c.peak = max(c.peak, c.running)
	c.ran = append(c.ran, t.Name)
	c.mu.Unlock()
	time.Sleep(80 * time.Millisecond)
	c.mu.Lock()
	c.running--
	c.free++
	c.mu.Unlock()
	return "", nil
}

func (c *capRunner) YieldBrowser(string) bool { return false }

// More jobs due than places: as many start as there are places, the rest
// start as places come free, and none is turned away to wait out a backoff
// (2026-10-28: the fifth of five plain jobs gave way at once into a
// five-minute wait).
func TestMoreDueJobsThanPlacesWaitForAPlace(t *testing.T) {
	var mu sync.Mutex
	done := map[int64]bool{}
	var outcomes []string
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/due":
			var due []Job
			for id := int64(1); id <= 6; id++ {
				if !done[id] {
					due = append(due, Job{ID: id, Title: fmt.Sprintf("요약 %d", id)})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": due})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/jobs/"):
			var id int64
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/jobs/"), "%d", &id)
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: id, Title: fmt.Sprintf("요약 %d", id)}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			var id int64
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/jobs/"), "%d", &id)
			_, _ = fmt.Fprintf(w, `{"runId": %d}`, 100+id)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/end"):
			var body struct {
				Outcome string `json:"outcome"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			outcomes = append(outcomes, body.Outcome)
			var run int64
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/runs/"), "%d", &run)
			done[run-100] = true
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer svc.Close()
	m := New(NewStore(svc.URL, ""), nil, proposal.NewStore(bus.New()), &transcript{}, quietLog())
	bg := &capRunner{free: 4}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	m.Run(ctx, bg, SchedulerConfig{RunURL: "http://agent/mcp/job-run", Interval: 20 * time.Millisecond, Timeout: time.Minute})

	bg.mu.Lock()
	defer bg.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if bg.busy != 0 || bg.peak != 4 || len(bg.ran) != 6 {
		t.Fatalf("자리 넷에 여섯: 거절 %d, 동시 최대 %d, 돈 것 %v", bg.busy, bg.peak, bg.ran)
	}
	for _, o := range outcomes {
		if o != "ok" {
			t.Fatalf("양보로 끝난 실행이 있습니다: %v", outcomes)
		}
	}
}
