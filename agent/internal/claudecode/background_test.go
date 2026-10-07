package claudecode

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
