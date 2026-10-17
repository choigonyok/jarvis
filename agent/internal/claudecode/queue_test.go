package claudecode

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// A stand-in CLI on stream-json input: it answers every message it reads
// with "got: <message>" - past the time (and any recap) a turn's first
// message carries in front - and gives its result once a message says END,
// then waits for stdin to close, as the real one does.
func echoClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
while IFS= read -r line; do
  text=$(printf '%s' "$line" | sed 's/.*"content":"\([^"]*\)".*/\1/; s/.*\\n\\n//')
  printf '{"type":"assistant","message":{"content":[{"type":"text","text":"got: %s"}]}}\n' "$text"
  case "$text" in *END*) echo '{"type":"result","result":"ok"}' ;; esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// queueRunner is a runner whose transcript is read off the bus (the chat
// service is not there; a turn that cannot be saved is still shown).
func queueRunner(t *testing.T) (*Runner, func() []string) {
	t.Helper()
	b := bus.New()
	events, stop := b.Subscribe()
	t.Cleanup(stop)
	r := runner(t, echoClaude(t))
	r.cfg.TurnTimeout = 10 * time.Second
	r.store = thread.NewStore("http://127.0.0.1:1", "", b)
	var said []string
	read := func() []string {
		for {
			select {
			case e := <-events:
				if e.Type == "turn" {
					turn := e.Turn.(thread.Turn)
					said = append(said, string(turn.Role)+":"+turn.Text+strings.Join(turn.Paragraphs, " "))
				}
			default:
				return said
			}
		}
	}
	return r, read
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal(what)
}

// A message sent while a turn runs waits in the queue instead of being
// refused; steered, the running turn reads it; dropped, it is never sent; and
// what is left runs once the turn is done.
func TestQueueSteerAndDrop(t *testing.T) {
	r, said := queueRunner(t)
	if queued, err := r.Submit("first", nil, nil); err != nil || queued {
		t.Fatalf("한가할 때는 바로 시작: queued=%v err=%v", queued, err)
	}
	waitFor(t, "첫 답이 와야 합니다", func() bool { return slices.Contains(said(), "agent:got: first") })

	for _, text := range []string{"steer me", "drop me", "later END"} {
		if queued, err := r.Submit(text, nil, nil); err != nil || !queued {
			t.Fatalf("%q 는 대기열로: queued=%v err=%v", text, queued, err)
		}
	}
	q := r.Queue()
	if len(q) != 3 || q[0].Text != "steer me" {
		t.Fatalf("대기열: %+v", q)
	}
	if steered, err := r.Steer(q[0].ID); err != nil || !steered {
		t.Fatalf("진행 중인 턴에 끼워 넣어야 합니다: %v %v", steered, err)
	}
	waitFor(t, "끼워 넣은 말을 진행 중인 턴이 읽어야 합니다", func() bool { return slices.Contains(said(), "agent:got: steer me") })
	if err := r.Drop(q[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Drop(q[1].ID); !errors.Is(err, ErrNotQueued) {
		t.Fatalf("두 번 뺄 수는 없습니다: %v", err)
	}

	// END closes the running turn; "later END" then runs as the next one.
	end, _ := r.Submit("now END", nil, nil)
	if !end {
		t.Fatal("대기열로 가야 합니다")
	}
	if steered, _ := r.Steer(r.Queue()[1].ID); !steered {
		t.Fatal("END 를 끼워 넣어야 합니다")
	}
	waitFor(t, "남은 메시지가 다음 턴으로 돌아야 합니다", func() bool { return slices.Contains(said(), "agent:got: later END") })
	waitFor(t, "끝나면 쉬어야 합니다", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return !r.running })

	got := said()
	for _, want := range []string{"user:first", "user:steer me", "user:now END", "user:later END"} {
		if !slices.Contains(got, want) {
			t.Fatalf("대화에 %q 가 있어야 합니다: %q", want, got)
		}
	}
	if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "drop me") }) {
		t.Fatalf("뺀 메시지는 보내지 않습니다: %q", got)
	}
	if len(r.Queue()) != 0 {
		t.Fatalf("대기열이 비어야 합니다: %+v", r.Queue())
	}
}

// A steer that comes after the turn stopped taking messages is not lost: it
// goes first in line.
func TestSteerAfterTheResultGoesFirst(t *testing.T) {
	r, _ := queueRunner(t)
	r.mu.Lock()
	r.running = true // a turn is giving its last answer: no input open
	r.mu.Unlock()
	_, _ = r.Submit("a", nil, nil)
	_, _ = r.Submit("b", nil, nil)
	q := r.Queue()
	if steered, err := r.Steer(q[1].ID); err != nil || steered {
		t.Fatalf("끼워 넣을 턴이 없으면 다음 차례로: %v %v", steered, err)
	}
	if q := r.Queue(); q[0].Text != "b" {
		t.Fatalf("맨 앞으로: %+v", q)
	}
}

// Queued ids never repeat, however close together the messages come.
func TestQueueIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := queueID("q")
		if seen[id] {
			t.Fatalf("id 가 겹쳤습니다: %s", id)
		}
		seen[id] = true
	}
}
