package claudecode

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/config"
)

// A stand-in CLI that prints a result line and then takes its time.
func fakeClaude(t *testing.T, sleep string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\necho '{\"type\":\"result\",\"result\":\"done\"}'\nsleep " + sleep + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func runner(t *testing.T, bin string) *Runner {
	return &Runner{
		cfg: config.Config{ClaudeBin: bin, Workspace: t.TempDir(), ExtraMCPURLs: map[string]string{"mcp-browser": "http://b"}},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestBackgroundFinishes(t *testing.T) {
	r := runner(t, fakeClaude(t, "0"))
	out, err := r.Background(context.Background(), Task{Name: "t", Extra: []string{"mcp-browser"}, Browser: true, Timeout: 5 * time.Second})
	if err != nil || out != "done" || !r.Idle() {
		t.Fatalf("out %q err %v idle %v", out, err, r.Idle())
	}
}

// The conversation going to use the browser stops a task on it at once, and
// the task says it gave way rather than that it failed. TakeBrowser comes
// back once the task has let go - its Done (the site guard's release) run
// first - and no task gets the browser again until the turns run out.
func TestTakeBrowserStopsTheTaskOnIt(t *testing.T) {
	r := runner(t, fakeClaude(t, "10"))
	done := make(chan error, 1)
	released := make(chan struct{})
	go func() {
		_, err := r.Background(context.Background(), Task{Name: "t", Browser: true, Timeout: 30 * time.Second,
			Done: func() { time.Sleep(100 * time.Millisecond); close(released) }})
		done <- err
	}()
	for r.Idle() {
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	r.mu.Lock()
	r.running = true // a turn is calling
	r.mu.Unlock()
	if !r.TakeBrowser(5 * time.Second) {
		t.Fatal("작업이 브라우저를 놓지 않았습니다")
	}
	select {
	case <-released:
	default:
		t.Fatal("작업의 사이트 제한이 풀리기 전에 브라우저를 넘겼습니다")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("양보가 늦습니다")
	}
	if err := <-done; !errors.Is(err, ErrYielded) {
		t.Fatalf("err %v", err)
	}
	if r.CanStart(true) {
		t.Fatal("대화가 브라우저를 쓰는 동안 다른 작업이 브라우저를 잡으면 안 됩니다")
	}
	if !r.CanStart(false) {
		t.Fatal("브라우저 없는 작업은 대화가 브라우저를 써도 시작합니다")
	}
}

// A turn that does not use the browser leaves it to the background: a task
// on it starts during the turn and is not stopped by it (2026-10-28: "대화턴은
// 실제 브라우저 도구를 쓸 때만 양도"). Only a turn that took it (TakeBrowser)
// keeps it from them.
func TestBrowserTaskRunsBesideATalkingTurn(t *testing.T) {
	r := runner(t, fakeClaude(t, "0"))
	r.running = true
	if out, err := r.Background(context.Background(), Task{Browser: true, Timeout: 5 * time.Second}); err != nil || out != "done" {
		t.Fatalf("말만 하는 대화 중에도 브라우저 작업은 돌아야 합니다: %q %v", out, err)
	}
	r.mu.Lock()
	r.chatBrowser = true
	r.mu.Unlock()
	if _, err := r.Background(context.Background(), Task{Browser: true, Timeout: time.Second}); !errors.Is(err, ErrBusy) {
		t.Fatalf("대화가 브라우저를 쓰는 중에 브라우저 작업을 시작했습니다: %v", err)
	}
	if out, err := r.Background(context.Background(), Task{Timeout: 5 * time.Second}); err != nil || out != "done" {
		t.Fatalf("브라우저 없는 작업은 대화 중에도 돌아야 합니다: %q %v", out, err)
	}
}

func TestBackgroundWithoutBrowserRunsThroughTheConversation(t *testing.T) {
	r := runner(t, fakeClaude(t, "2"))
	done := make(chan error, 1)
	go func() {
		_, err := r.Background(context.Background(), Task{Name: "t", Timeout: 30 * time.Second})
		done <- err
	}()
	for r.Idle() {
		time.Sleep(10 * time.Millisecond)
	}
	if !r.TakeBrowser(time.Second) {
		t.Fatal("브라우저를 쓰는 작업이 없으니 바로 넘어와야 합니다")
	}
	if !r.CanStart(false) {
		t.Fatal("자리가 남아 있어야 합니다")
	}
	if err := <-done; err != nil {
		t.Fatalf("대화가 브라우저를 써도 브라우저 없는 작업은 멈추지 않습니다: %v", err)
	}
}

// MaxPlain tasks without the browser at once, and the browser's one beside
// them, counted apart.
func TestBackgroundSlots(t *testing.T) {
	r := runner(t, fakeClaude(t, "3"))
	start := func(browser bool) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := r.Background(context.Background(), Task{Browser: browser, Timeout: 30 * time.Second})
			done <- err
		}()
		return done
	}
	var runs []chan error
	for i := 0; i < MaxPlain; i++ {
		runs = append(runs, start(false))
	}
	for r.CanStart(false) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.Background(context.Background(), Task{Timeout: time.Second}); !errors.Is(err, ErrBusy) {
		t.Fatalf("자리를 넘는 작업은 기다려야 합니다: %v", err)
	}
	if !r.CanStart(true) {
		t.Fatal("브라우저 자리는 따로 남습니다")
	}
	runs = append(runs, start(true))
	for r.CanStart(true) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.Background(context.Background(), Task{Browser: true, Timeout: time.Second}); !errors.Is(err, ErrBusy) {
		t.Fatalf("브라우저는 한 번에 하나입니다: %v", err)
	}
	for _, done := range runs {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// The conversation keeps the browser through the turns queued behind the one
// that took it, and gives it back once they have run out.
func TestTheBrowserComesBackAfterTheTurns(t *testing.T) {
	r, _ := failingRunner(t)
	r.mu.Lock()
	r.running = true // what Submit does before the loop starts
	r.mu.Unlock()
	r.TakeBrowser(0)
	if r.CanStart(true) {
		t.Fatal("턴이 브라우저를 쓰는 동안에는 작업이 잡지 못합니다")
	}
	r.loop("hello")
	if !r.CanStart(true) {
		t.Fatal("턴이 끝나면 브라우저를 작업에 돌려줍니다")
	}
	// A call out of any turn takes nothing.
	r.TakeBrowser(0)
	if !r.CanStart(true) {
		t.Fatal("턴 밖에서 온 호출이 브라우저를 붙잡으면 안 됩니다")
	}
}

// A task is held to its sites only once it has the browser, and a Hold that
// fails stops it before it starts - Done still runs, and the place is free.
func TestHoldRunsOnceTheBrowserIsTheTasks(t *testing.T) {
	r := runner(t, fakeClaude(t, "0"))
	var order []string
	_, err := r.Background(context.Background(), Task{Browser: true, Timeout: 5 * time.Second,
		Hold: func(context.Context) error {
			if r.CanStart(true) {
				order = append(order, "브라우저가 비어 있음")
			}
			order = append(order, "hold")
			return nil
		},
		Done: func() { order = append(order, "done") }})
	if err != nil || strings.Join(order, ",") != "hold,done" {
		t.Fatalf("err %v order %v", err, order)
	}
	_, err = r.Background(context.Background(), Task{Browser: true, Timeout: 5 * time.Second,
		Hold: func(context.Context) error { return errors.New("bridge down") },
		Done: func() { order = append(order, "done again") }})
	if !errors.Is(err, ErrHold) || order[len(order)-1] != "done again" || !r.CanStart(true) {
		t.Fatalf("err %v order %v", err, order)
	}
}

// A place reserved counts as taken at once - a scheduler starting several
// tasks cannot take more places than there are - and the task carrying it
// starts in it; one never used goes back on Release, once (2026-10-28: five
// jobs started into four places, and the fifth gave way at once).
func TestReservedPlaces(t *testing.T) {
	r := runner(t, fakeClaude(t, "0"))
	var places []Place
	for i := 0; i < MaxPlain; i++ {
		p, ok := r.Reserve(false)
		if !ok {
			t.Fatalf("%d번째 자리를 잡지 못했습니다", i+1)
		}
		places = append(places, p)
	}
	if _, ok := r.Reserve(false); ok || r.CanStart(false) {
		t.Fatal("자리를 넘겨 잡았습니다")
	}
	if !r.CanStart(true) {
		t.Fatal("브라우저 자리는 따로입니다")
	}
	// The task carrying a place starts in it, full as the runner is.
	if out, err := r.Background(context.Background(), Task{Place: places[0], Timeout: 5 * time.Second}); err != nil || out != "done" {
		t.Fatalf("예약한 자리로 시작해야 합니다: %q %v", out, err)
	}
	places[0].Release() // used: nothing to give back
	if _, ok := r.Reserve(false); !ok {
		t.Fatal("끝난 작업의 자리는 돌아와야 합니다")
	}
	places[1].Release()
	places[1].Release()
	if _, ok := r.Reserve(false); !ok {
		t.Fatal("돌려준 자리를 다시 잡을 수 있어야 합니다")
	}
	if _, ok := r.Reserve(false); ok {
		t.Fatal("두 번 돌려줘도 한 자리입니다")
	}
}

// A place reserved on the browser keeps it from the others, and the
// conversation taking the browser waits for it: the task finds the browser
// taken when it starts, gives its place back, and is never held to its sites.
func TestTakingTheBrowserFromAReservedPlace(t *testing.T) {
	r := runner(t, fakeClaude(t, "0"))
	p, ok := r.Reserve(true)
	if !ok || r.CanStart(true) {
		t.Fatalf("브라우저 자리 예약: ok=%v", ok)
	}
	r.mu.Lock()
	r.running = true
	r.mu.Unlock()
	taken := make(chan bool, 1)
	go func() { taken <- r.TakeBrowser(5 * time.Second) }()
	select {
	case <-taken:
		t.Fatal("예약된 자리를 두고 바로 넘겨받았습니다")
	case <-time.After(150 * time.Millisecond):
	}
	held := false
	_, err := r.Background(context.Background(), Task{Browser: true, Place: p, Timeout: 5 * time.Second,
		Hold: func(context.Context) error { held = true; return nil }})
	if !errors.Is(err, ErrBusy) || held {
		t.Fatalf("대화가 가져간 브라우저로 시작했습니다: err=%v held=%v", err, held)
	}
	if !<-taken {
		t.Fatal("자리가 돌아온 뒤에는 넘겨받아야 합니다")
	}
}

// YieldBrowser stops the task on the browser for another to go first; with
// nothing there it stops nothing.
func TestYieldBrowser(t *testing.T) {
	r := runner(t, fakeClaude(t, "10"))
	if r.YieldBrowser("먼저") {
		t.Fatal("브라우저에 아무것도 없는데 멈췄다고 합니다")
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.Background(context.Background(), Task{Name: "t", Browser: true, Timeout: 30 * time.Second})
		done <- err
	}()
	for r.Idle() {
		time.Sleep(10 * time.Millisecond)
	}
	if !r.YieldBrowser("먼저") {
		t.Fatal("브라우저의 작업을 멈추지 않았습니다")
	}
	if err := <-done; !errors.Is(err, ErrYielded) {
		t.Fatalf("err %v", err)
	}
	if !r.CanStart(true) {
		t.Fatal("양보한 뒤에는 다른 작업이 브라우저를 잡을 수 있어야 합니다")
	}
}

// A task's instructions are the same on every run - the clock rides on its
// prompt - so two runs share their cache; and it runs on its own model.
func TestBackgroundArgs(t *testing.T) {
	r := runner(t, "claude")
	r.cfg.Model = "chat-model"
	args, err := r.backgroundArgs(Task{Name: "t", Prompt: "할 일", System: "규칙", Model: "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--append-system-prompt\n규칙\n") && !strings.HasSuffix(joined, "--append-system-prompt\n규칙") {
		t.Fatalf("지시는 그대로:\n%s", joined)
	}
	if !strings.Contains(joined, "--model\nsonnet") || strings.Contains(joined, "chat-model") {
		t.Fatalf("작업의 모델로:\n%s", joined)
	}
	if !strings.Contains(args[1], "오늘은 ") || !strings.HasSuffix(args[1], "할 일") {
		t.Fatalf("시각은 프롬프트 앞에: %q", args[1])
	}
	args, _ = r.backgroundArgs(Task{Name: "t"})
	if !strings.Contains(strings.Join(args, "\n"), "--model\nchat-model") {
		t.Fatal("모델이 없으면 대화의 모델")
	}
}

// A background task may start the moment the conversation is not running:
// a job asked for a second ago does not sit on "예약됨" (2026-10-18).
func TestIdleRightAfterTheConversation(t *testing.T) {
	r := runner(t, "claude")
	r.lastAt = time.Now()
	if !r.CanStart(true) {
		t.Fatal("대화 턴이 끝났으면 바로 시작합니다")
	}
	r.running = true
	if !r.CanStart(true) {
		t.Fatal("브라우저를 쓰지 않는 대화 중에는 브라우저 작업도 시작합니다")
	}
	r.chatBrowser = true
	if r.CanStart(true) {
		t.Fatal("대화가 브라우저를 쓰는 중에는 브라우저 작업을 시작하지 않습니다")
	}
	if !r.CanStart(false) {
		t.Fatal("대화 중에도 브라우저 없는 작업은 시작합니다")
	}
}

// A background task ending is a place come free: whoever waits for one (a
// job on the browser) hears it then, not on its next clock.
func TestBackgroundEndingFreesAPlace(t *testing.T) {
	r := runner(t, fakeClaude(t, "0"))
	freed := make(chan struct{}, 4)
	r.OnFree(func() { freed <- struct{}{} })
	if _, err := r.Background(context.Background(), Task{Name: "t", Timeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-freed:
	case <-time.After(2 * time.Second):
		t.Fatal("끝난 백그라운드 작업이 자리를 알리지 않았습니다")
	}
}
