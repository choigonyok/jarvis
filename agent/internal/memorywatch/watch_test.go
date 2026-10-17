package memorywatch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const note = `---
name: propose-with-card
description: "상태 바꾸는 일을 제안할 때 \"할까요?\"로 묻지 말고 바로 승인 카드를 올린다"
metadata:
  node_type: memory
  type: feedback
  originSessionId: 9fe71079
  modified: 2026-10-17T02:51:49.086Z
---

할 일이 분명하면 바로 승인 카드를 올린다.

**Why:** 운영자가 지적함.
`

const indexLine = "- [propose-with-card](propose-with-card.md) — 상태 바꾸는 일을 제안할 때 \"할까요?\"로 묻지 말고 바로 승인 카드를 올린다\n"

// A note saved with its index line is one notification, the index first,
// carrying the words themselves.
func TestANewNoteIsToldWithItsWords(t *testing.T) {
	title, body := Message(Compare(Notes{}, Notes{index: indexLine, "propose-with-card.md": note}))
	if title != "자비스가 새로 기억했어요" {
		t.Fatalf("제목: %q", title)
	}
	want := []string{
		`목록에 추가: propose-with-card — 상태 바꾸는 일을 제안할 때 "할까요?"로 묻지 말고 바로 승인 카드를 올린다`,
		"새 기억 propose-with-card: 할 일이 분명하면 바로 승인 카드를 올린다. Why: 운영자가 지적함.",
	}
	if body != strings.Join(want, "\n") {
		t.Fatalf("본문:\n%s", body)
	}
}

// Saving a note again rewrites its bookkeeping; that alone is not a change.
func TestBookkeepingIsNotAChange(t *testing.T) {
	again := strings.Replace(note, "02:51:49.086Z", "05:10:00.000Z", 1)
	again = strings.Replace(again, "9fe71079", "0a1b2c3d", 1)
	if got := Compare(Notes{"propose-with-card.md": note}, Notes{"propose-with-card.md": again}); len(got) != 0 {
		t.Fatalf("바뀐 게 없어야 합니다: %+v", got)
	}
}

// An edit shows the lines that came and the lines that went.
func TestAnEditShowsBothSides(t *testing.T) {
	edited := strings.Replace(note, "바로 승인 카드를 올린다.", "결제도 묻지 말고 바로 한다.", 1)
	title, body := Message(Compare(Notes{"propose-with-card.md": note}, Notes{"propose-with-card.md": edited}))
	if title != "자비스가 기억을 고쳤어요" {
		t.Fatalf("제목: %q", title)
	}
	if body != "고친 기억 propose-with-card: 추가 - 할 일이 분명하면 결제도 묻지 말고 바로 한다. / 삭제 - 할 일이 분명하면 바로 승인 카드를 올린다." {
		t.Fatalf("본문: %q", body)
	}
}

func TestARemovalIsToldAsOne(t *testing.T) {
	title, body := Message(Compare(Notes{index: indexLine, "propose-with-card.md": note}, Notes{index: ""}))
	if title != "자비스가 기억을 지웠어요" {
		t.Fatalf("제목: %q", title)
	}
	if !strings.HasPrefix(body, "목록에서 삭제: propose-with-card — ") || !strings.HasSuffix(body, "\n지운 기억 propose-with-card") {
		t.Fatalf("본문:\n%s", body)
	}
}

// However much is written at once, the body stays inside what the inbox keeps.
func TestAFloodStaysShort(t *testing.T) {
	after := Notes{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		after[n+".md"] = strings.Repeat("길게 쓴 기억 ", 60)
	}
	_, body := Message(Compare(Notes{}, after))
	lines := strings.Split(body, "\n")
	if len(lines) != linesMax || lines[linesMax-1] != fmt.Sprintf("외 %d건", 7-linesMax+1) {
		t.Fatalf("줄: %q", lines)
	}
	if n := len([]rune(body)); n > 600 {
		t.Fatalf("본문이 %d자입니다", n)
	}
}

// The watcher tells a change once it settles, not what was there before it
// began, and not again after a restart - but a change made while it was
// stopped is still told.
func TestWatcherTellsEachChangeOnce(t *testing.T) {
	dir, state := t.TempDir(), filepath.Join(t.TempDir(), "memory-seen.json")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(index, "")
	told := make(chan []Entry, 10)
	start := func() context.CancelFunc {
		ctx, stop := context.WithCancel(context.Background())
		w := &Watcher{Dir: dir, SeenPath: state, Every: 10 * time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Tell: func(e []Entry) { told <- e }}
		go w.Run(ctx)
		return stop
	}
	quiet := func(why string) {
		t.Helper()
		select {
		case e := <-told:
			t.Fatalf("%s: %+v", why, e)
		case <-time.After(100 * time.Millisecond):
		}
	}
	next := func() []Entry {
		t.Helper()
		select {
		case e := <-told:
			return e
		case <-time.After(2 * time.Second):
			t.Fatal("알림이 와야 합니다")
		}
		return nil
	}

	stop := start()
	quiet("처음 본 상태는 알리지 않습니다")
	write("propose-with-card.md", note)
	write(index, indexLine)
	if e := next(); len(e) != 2 || e[0].Path != index || !e[1].New {
		t.Fatalf("목록 줄과 새 기억이 한 번에: %+v", e)
	}
	quiet("한 번만 알립니다")
	stop()

	stop = start()
	quiet("재시작해도 다시 알리지 않습니다")
	stop()

	if err := os.Remove(filepath.Join(dir, "propose-with-card.md")); err != nil {
		t.Fatal(err)
	}
	stop = start()
	defer stop()
	if e := next(); len(e) != 1 || !e[0].Gone {
		t.Fatalf("멈춰 있던 동안 지운 것도 알립니다: %+v", e)
	}
}
