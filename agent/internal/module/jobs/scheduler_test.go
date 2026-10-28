package jobs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// fakeRuns is jobs-svc as far as one run needs it.
func fakeRuns(t *testing.T, attempts int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/jobs/"):
			_ = json.NewEncoder(w).Encode(Detail{Job: Job{ID: 5, Title: "가격 지켜보기", Goal: "내려가면 알리기", Sites: []string{"shop.example"}, Attempts: attempts}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"runId": 9}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
}

// bgFake is the runner: it takes each task, holds it (Hold), plays its
// steps, lets go (Done), and ends as told.
type bgFake struct {
	mu       sync.Mutex
	tasks    []claudecode.Task
	steps    []claudecode.Step
	err      error
	canStart func(browser bool) bool
	yields   []string
	// during runs while the task is in flight: what happens meanwhile.
	during func()
}

func (b *bgFake) YieldBrowser(why string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.yields = append(b.yields, why)
	return true
}

func (b *bgFake) CanStart(browser bool) bool {
	if b.canStart != nil {
		return b.canStart(browser)
	}
	return true
}
func (b *bgFake) Background(ctx context.Context, t claudecode.Task) (string, error) {
	b.mu.Lock()
	b.tasks = append(b.tasks, t)
	b.mu.Unlock()
	if t.Done != nil {
		defer t.Done()
	}
	if t.Hold != nil {
		if err := t.Hold(ctx); err != nil {
			return "", claudecode.ErrHold
		}
	}
	for _, s := range b.steps {
		t.OnStep(s)
	}
	if b.during != nil {
		b.during()
	}
	return "", b.err
}

// A run the conversation cuts short hands what it did to the next run of the
// same job, once; a job tried again after a bad run gets the retry model.
func TestCutShortRunIsPickedUp(t *testing.T) {
	svc := fakeRuns(t, 0)
	defer svc.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := New(NewStore(svc.URL, ""), nil, proposal.NewStore(bus.New()), nil, quiet)
	cfg := SchedulerConfig{RunURL: "http://agent/mcp/job-run", Model: "sonnet", RetryModel: "opus"}
	j := Job{ID: 5, Title: "가격 지켜보기"}

	step := func(url string) claudecode.Step {
		raw, _ := json.Marshal(map[string]string{"url": url})
		return claudecode.Step{Tool: "mcp__mcp-browser__browser_navigate", Input: raw}
	}
	bg := &bgFake{steps: []claudecode.Step{step("https://shop.example/item/1")}, err: claudecode.ErrYielded}
	m.runOnce(context.Background(), bg, cfg, j)
	bg.steps, bg.err = nil, nil
	m.runOnce(context.Background(), bg, cfg, j)
	m.runOnce(context.Background(), bg, cfg, j)

	if len(bg.tasks) != 3 {
		t.Fatalf("실행 %d번", len(bg.tasks))
	}
	if strings.Contains(bg.tasks[0].Prompt, "중간에 멈췄습니다") || bg.tasks[0].Model != "sonnet" {
		t.Fatalf("첫 실행: %q %s", bg.tasks[0].Prompt, bg.tasks[0].Model)
	}
	if !strings.Contains(bg.tasks[1].Prompt, "중간에 멈췄습니다") || !strings.Contains(bg.tasks[1].Prompt, "열기: https://shop.example/item/1") {
		t.Fatalf("이어서 할 실행에 지난 단계가 없습니다:\n%s", bg.tasks[1].Prompt)
	}
	if strings.Contains(bg.tasks[2].Prompt, "중간에 멈췄습니다") {
		t.Fatal("끝까지 간 실행 뒤에는 넘기지 않습니다")
	}

	j.Attempts = 1
	m.runOnce(context.Background(), bg, cfg, j)
	if got := bg.tasks[3].Model; got != "opus" {
		t.Fatalf("다시 하는 실행은 재시도 모델: %s", got)
	}
}
