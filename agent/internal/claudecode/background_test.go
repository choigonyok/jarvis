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
	out, err := r.Background(context.Background(), Task{Name: "t", Extra: []string{"mcp-browser"}, Timeout: 5 * time.Second})
	if err != nil || out != "done" || !r.Idle() {
		t.Fatalf("out %q err %v idle %v", out, err, r.Idle())
	}
}

// The person speaking stops the task at once, and the task says it gave way
// rather than that it failed.
func TestBackgroundYieldsToTheConversation(t *testing.T) {
	r := runner(t, fakeClaude(t, "10"))
	done := make(chan error, 1)
	go func() {
		_, err := r.Background(context.Background(), Task{Name: "t", Timeout: 30 * time.Second})
		done <- err
	}()
	for r.Idle() {
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	r.mu.Lock()
	r.yieldLocked() // what Send and Enqueue do
	r.mu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, ErrYielded) {
			t.Fatalf("err %v", err)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("양보가 늦습니다")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("멈추지 않았습니다")
	}
}

func TestBackgroundWaitsForTheConversation(t *testing.T) {
	r := runner(t, fakeClaude(t, "0"))
	r.running = true
	if _, err := r.Background(context.Background(), Task{Timeout: time.Second}); !errors.Is(err, ErrBusy) {
		t.Fatalf("대화 중에 시작했습니다: %v", err)
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
	if !r.Idle() {
		t.Fatal("대화 턴이 끝났으면 바로 시작합니다")
	}
	r.running = true
	if r.Idle() {
		t.Fatal("대화 중에는 시작하지 않습니다")
	}
}
