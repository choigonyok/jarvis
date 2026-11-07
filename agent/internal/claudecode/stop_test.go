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

// stoppableClaude stands in for the CLI as 2.1.287 behaves on stream-json
// input when an interrupt comes: a message saying LONG
// starts writing and waits; a control_request to interrupt flushes what was
// written as an assistant message, says the request was interrupted and gives
// an error_during_execution result. One saying DEAF hears nothing and never
// ends - a call stuck where the interrupt does not reach. Anything else is
// echoed and ends.
func stoppableClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
IFS= read -r line
echo '{"type":"system","subtype":"init","session_id":"s-stop"}'
text=$(printf '%s' "$line" | sed 's/.*"content":"\([^"]*\)".*/\1/; s/.*\\n\\n//')
case "$text" in
  *LONG*)
    echo '{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}'
    echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"1 2 3"}}}'
    while IFS= read -r l; do
      case "$l" in *interrupt*)
        echo '{"type":"control_response","response":{"subtype":"success","request_id":"x"}}'
        echo '{"type":"assistant","message":{"content":[{"type":"text","text":"1 2 3"}]}}'
        echo '{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}'
        echo '{"type":"result","subtype":"error_during_execution","is_error":true}'
        cat > /dev/null
        exit 1 ;;
      esac
    done ;;
  *DEAF*)
    echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"..."}}}'
    while IFS= read -r l; do :; done
    sleep 30 ;;
esac
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"got: %s"}]}}\n' "$text"
echo '{"type":"result","result":"ok","duration_ms":12,"duration_api_ms":7}'
cat > /dev/null
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func stopRunner(t *testing.T) (*Runner, func() (said, failed []string)) {
	t.Helper()
	b := bus.New()
	events, stop := b.Subscribe()
	t.Cleanup(stop)
	r := runner(t, stoppableClaude(t))
	r.cfg.TurnTimeout = time.Minute
	r.store = thread.NewStore("http://127.0.0.1:1", "", b)
	var said, failed []string
	read := func() ([]string, []string) {
		for {
			select {
			case e := <-events:
				switch e.Type {
				case "turn":
					turn := e.Turn.(thread.Turn)
					said = append(said, string(turn.Role)+":"+turn.Text+strings.Join(turn.Paragraphs, " "))
				case "error":
					failed = append(failed, e.Message)
				}
			default:
				return said, failed
			}
		}
	}
	return r, read
}

func listening(r *Runner) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.input != nil
}

// Stopped, a turn ends where it is: what it wrote stays, marked as stopped,
// nothing is called a failure, the session goes on, and what was sent
// meanwhile is answered next.
func TestStopEndsTheTurnWhereItIs(t *testing.T) {
	r, read := stopRunner(t)
	if _, err := r.Stop(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("쓰고 있는 답이 없으면 멈출 것도 없습니다: %v", err)
	}
	if _, err := r.Submit("LONG count", nil, nil); err != nil {
		t.Fatal(err)
	}
	if queued, _ := r.Submit("after", nil, nil); !queued {
		t.Fatal("두 번째는 기다려야 합니다")
	}
	waitFor(t, "턴이 듣고 있어야 합니다", func() bool { return listening(r) })
	if stopped, err := r.Stop(); err != nil || !stopped {
		t.Fatalf("멈춰야 합니다: %v %v", stopped, err)
	}
	if stopped, err := r.Stop(); err != nil || !stopped {
		t.Fatalf("두 번 눌러도 같은 멈춤입니다: %v %v", stopped, err)
	}
	waitFor(t, "기다리던 메시지에 답해야 합니다", func() bool {
		said, _ := read()
		return slices.Contains(said, "agent:got: after")
	})
	waitFor(t, "실행기가 쉬어야 합니다", r.Idle)
	said, failed := read()
	if len(failed) != 0 {
		t.Fatalf("멈춤은 실패가 아닙니다: %v", failed)
	}
	if !slices.Contains(said, "agent:1 2 3 "+stoppedMark) {
		t.Fatalf("쓴 데까지 남고 멈춘 자리가 보여야 합니다: %v", said)
	}
	if slices.Contains(said, "agent:"+stoppedLine) {
		t.Fatalf("쓴 말이 있으면 따로 알리지 않습니다: %v", said)
	}
	if r.sessionID != "s-stop" {
		t.Fatalf("멈춘 세션은 이어 갑니다: %q", r.sessionID)
	}
}

// A turn that does not hear the interrupt is killed after the grace, and the
// next message opens a new session.
func TestAStopNobodyHearsKills(t *testing.T) {
	stopGrace = 200 * time.Millisecond
	t.Cleanup(func() { stopGrace = 5 * time.Second })
	r, read := stopRunner(t)
	if _, err := r.Submit("DEAF wait", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "턴이 듣고 있어야 합니다", func() bool { return listening(r) })
	start := time.Now()
	if stopped, err := r.Stop(); err != nil || !stopped {
		t.Fatalf("멈춰야 합니다: %v %v", stopped, err)
	}
	waitFor(t, "실행기가 쉬어야 합니다", r.Idle)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("유예 뒤 바로 끝내야 합니다: %v", time.Since(start))
	}
	said, failed := read()
	if len(failed) != 0 || !slices.Contains(said, "agent:"+stoppedLine) {
		t.Fatalf("멈췄다고만 남겨야 합니다: said=%v failed=%v", said, failed)
	}
	if r.sessionID != "" {
		t.Fatalf("끊어 낸 세션은 이어 가지 않습니다: %q", r.sessionID)
	}
}

// A stop pressed as one turn ended does not stop the one after it.
func TestAStopBetweenTurnsStopsNothingElse(t *testing.T) {
	r, read := stopRunner(t)
	if _, err := r.Submit("first", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "첫 턴이 끝나야 합니다", r.Idle)
	r.mu.Lock()
	r.running = true // the queue's next turn not yet started
	r.mu.Unlock()
	if stopped, err := r.Stop(); err != nil || stopped {
		t.Fatalf("끝난 턴은 멈출 것이 없습니다: %v %v", stopped, err)
	}
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
	if _, err := r.Submit("second", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "다음 턴은 그대로 답해야 합니다", func() bool {
		said, _ := read()
		return slices.Contains(said, "agent:got: second")
	})
	waitFor(t, "실행기가 쉬어야 합니다", r.Idle)
}

// What the operator waits through is in the turn's log line: until the
// session opened, until the first word, until the end.
func TestATurnIsTimed(t *testing.T) {
	var tl tally
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	tl.begin(at)
	tl.clocked(event{Type: "system", Subtype: "init"}, at.Add(2*time.Second))
	tl.clocked(event{Type: "stream_event", Event: []byte(`{"type":"content_block_start","index":0}`)}, at.Add(3*time.Second))
	tl.clocked(event{Type: "stream_event", ParentToolUseID: "t1", Event: []byte(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}`)}, at.Add(3*time.Second))
	tl.clocked(event{Type: "stream_event", Event: []byte(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"안"}}`)}, at.Add(4*time.Second))
	tl.clocked(event{Type: "result", DurationMS: 5100, DurationAPIMS: 3000}, at.Add(6*time.Second))
	got := map[string]any{}
	attrs := tl.attrs()
	for i := 0; i+1 < len(attrs); i += 2 {
		got[attrs[i].(string)] = attrs[i+1]
	}
	want := map[string]int64{"startup_ms": 2000, "first_text_ms": 4000, "total_ms": 6000, "cli_ms": 5100, "api_ms": 3000}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %v, want %d (%v)", k, got[k], v, got)
		}
	}
	var none tally
	none.begin(at)
	for _, a := range none.attrs() {
		if a == "first_text_ms" {
			return
		}
	}
	t.Fatal("first_text_ms 가 없습니다")
}

// A stop that comes as the answer finishes stops nothing: the answer stands
// as written, with no word of a stop under it.
func TestAStopAsTheAnswerEndsSaysNothing(t *testing.T) {
	r, read := stopRunner(t)
	r.mu.Lock()
	r.running, r.gen, r.resulted = true, 7, 7 // the result came; the process is closing
	r.mu.Unlock()
	if stopped, err := r.Stop(); err != nil || stopped {
		t.Fatalf("답이 끝난 턴은 멈출 것이 없습니다: %v %v", stopped, err)
	}
	r.mu.Lock()
	asked := r.stopGen == 7
	r.running = false
	r.mu.Unlock()
	if asked {
		t.Fatal("멈춤으로 적으면 안 됩니다")
	}
	if _, err := r.Submit("plain", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "실행기가 쉬어야 합니다", r.Idle)
	said, _ := read()
	for _, s := range said {
		if strings.Contains(s, stoppedLine) || strings.Contains(s, stoppedMark) {
			t.Fatalf("멈추지 않은 답에 멈춤 표시: %v", said)
		}
	}
}
