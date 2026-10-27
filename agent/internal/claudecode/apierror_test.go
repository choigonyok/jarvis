package claudecode

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/usage"
)

// failingClaude stands in for the CLI on stream-json input, as the real one
// behaves past an API error: a message saying FAIL is answered with an API
// error (overloaded); one saying LIMIT first reports the usage limit as
// rejected, resetting in one to two seconds, then the error. Either way it gives its
// result and then waits for stdin to close before it exits - the wait that
// held a turn for two hours (2026-10-27). Any other message is echoed.
func failingClaude(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
IFS= read -r line
text=$(printf '%s' "$line" | sed 's/.*"content":"\([^"]*\)".*/\1/; s/.*\\n\\n//')
case "$text" in
  *LIMIT*)
    reset=$(( $(date +%s) + 2 ))
    printf '{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":%s,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":1.02,"resetsAt":%s}}}}\n' "$reset" "$reset"
    echo '{"type":"assistant","is_api_error_message":true,"error":"rate_limit","message":{"content":[{"type":"text","text":"Claude AI usage limit reached"}]}}'
    echo '{"type":"result","is_error":true,"result":"usage limit"}'
    cat > /dev/null
    exit 1 ;;
  *FAIL*)
    echo '{"type":"assistant","is_api_error_message":true,"error":"overloaded_error","message":{"content":[{"type":"text","text":"API Error: 529 overloaded"}]}}'
    echo '{"type":"result","is_error":true,"result":"API Error: 529"}'
    cat > /dev/null
    exit 1 ;;
esac
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"got: %s"}]}}\n' "$text"
echo '{"type":"result","result":"ok"}'
cat > /dev/null
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// failingRunner is a runner on failingClaude whose transcript and failures
// are read off the bus. Its turn timeout is long: a turn that hangs fails the
// test by the clock, not by the timeout.
func failingRunner(t *testing.T) (*Runner, func() (said, failed []string)) {
	t.Helper()
	b := bus.New()
	events, stop := b.Subscribe()
	t.Cleanup(stop)
	r := runner(t, failingClaude(t))
	r.cfg.TurnTimeout = time.Minute
	r.store = thread.NewStore("http://127.0.0.1:1", "", b)
	r.usage = usage.New("")
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

// An API error ends the turn at once - not two hours later at its timeout -
// and a message sent meanwhile is answered in a turn of its own instead of
// being written into the dead one.
func TestAnAPIErrorEndsTheTurn(t *testing.T) {
	r, read := failingRunner(t)
	if _, err := r.Submit("FAIL first", nil, nil); err != nil {
		t.Fatal(err)
	}
	if queued, err := r.Submit("second", nil, nil); err != nil || !queued {
		t.Fatalf("두 번째는 대기열로: queued=%v err=%v", queued, err)
	}
	waitFor(t, "오류 뒤에 온 메시지가 제 턴에서 답을 받아야 합니다", func() bool {
		said, _ := read()
		return slices.Contains(said, "agent:got: second")
	})
	waitFor(t, "실행기가 쉬어야 합니다", r.Idle)
	if _, failed := read(); len(failed) != 1 {
		t.Fatalf("실패는 한 번 알립니다: %v", failed)
	}
}

// Out of the usage allowance, what was sent meanwhile waits for the window to
// start over and is then answered, and the failure says when.
func TestAUsageLimitIsWaitedOut(t *testing.T) {
	limitSlack = 0
	t.Cleanup(func() { limitSlack = 30 * time.Second })
	r, read := failingRunner(t)
	start := time.Now()
	if _, err := r.Submit("LIMIT first", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Submit("second", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "한도가 풀린 뒤 기다리던 메시지에 답해야 합니다", func() bool {
		said, _ := read()
		return slices.Contains(said, "agent:got: second")
	})
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("한도가 풀리기 전에 돌았습니다: %v", time.Since(start))
	}
	_, failed := read()
	if len(failed) != 1 || !strings.Contains(failed[0], "풀리면") {
		t.Fatalf("언제 풀리는지 알려야 합니다: %v", failed)
	}
	waitFor(t, "실행기가 쉬어야 합니다", r.Idle)
}
