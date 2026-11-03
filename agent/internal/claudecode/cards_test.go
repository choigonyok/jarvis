package claudecode

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// The cards on the screen still waiting for an answer ride in front of every
// turn: the list in full, the same list again in a line, a list that emptied
// once. A new session sees it in full, and a turn that failed did not see it.
func TestWaitingCardsInFrontOfTheTurn(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := &Runner{cfg: config.Config{SystemPrompt: "base"}, log: quiet}
	if got := r.cards(); got != "" {
		t.Fatalf("목록을 줄 곳이 없으면 아무것도: %q", got)
	}
	lines := []string{"- 결제를 진행합니다: pay.example.com (브라우저가 보내려던 요청을 결제 감시가 멈춘 것, 15:37)"}
	r.SetWaiting(func() []string { return lines })
	full := r.cards()
	if !strings.Contains(full, "[답을 기다리는 카드 - 운영자 화면에") || !strings.Contains(full, lines[0]) {
		t.Fatalf("%s", full)
	}
	r.account(&tally{}, false, "", 0, false)
	if again := r.cards(); again != full {
		t.Fatalf("실패한 턴은 목록을 못 봤습니다:\n%s", again)
	}
	r.account(&tally{}, false, "", 0, true)
	if same := r.cards(); !strings.Contains(same, "앞 메시지 때와 같음") || strings.Contains(same, "pay.example.com") {
		t.Fatalf("%s", same)
	}
	r.account(&tally{}, false, "", 0, true)
	lines = nil
	if none := r.cards(); !strings.Contains(none, "없음") {
		t.Fatalf("비었으면 한 번 말합니다: %q", none)
	}
	r.account(&tally{}, false, "", 0, true)
	if quiet := r.cards(); quiet != "" {
		t.Fatalf("계속 없으면 말하지 않습니다: %q", quiet)
	}
	lines = []string{"- 캠핑 일정 추가할까요? (비서가 먼저 묻는 것, 16:00)"}
	_ = r.cards()
	r.account(&tally{}, false, "", 0, true)
	r.cardsShown = "" // what turn() does for a fresh session
	if fresh := r.cards(); !strings.Contains(fresh, "캠핑 일정") {
		t.Fatalf("새 세션은 목록을 다 봅니다:\n%s", fresh)
	}
	guest := &Runner{cfg: config.Config{SystemPrompt: "base", Guest: true}, log: quiet}
	guest.SetWaiting(func() []string { return lines })
	if g := guest.cards(); !strings.Contains(g, "사용자 화면에") || !strings.Contains(g, "사용자가 화면에서") {
		t.Fatalf("%s", g)
	}
}

// listeningClaude answers every message with one line and keeps what it was
// sent, one message a line, in dir/input.
func listeningClaude(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nIFS= read -r line\nprintf '%s\\n' \"$line\" >> \"" + dir + "/input\"\n" +
		`echo '{"type":"system","subtype":"init","session_id":"s-1"}'` + "\n" +
		`echo '{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"네"}]}}'` + "\n" +
		`echo '{"type":"result","subtype":"success","result":"ok"}'` + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// What a turn is sent: the waiting cards with the message, and the same
// cards in a line on the next.
func TestATurnIsToldTheWaitingCards(t *testing.T) {
	dir := t.TempDir()
	r := runner(t, listeningClaude(t, dir))
	r.cfg.TurnTimeout = 10 * time.Second
	chat := httptest.NewServer(fakeTurns())
	t.Cleanup(chat.Close)
	r.store = thread.NewStore(chat.URL, "", bus.New())
	r.SetWaiting(func() []string {
		return []string{"- 결제를 진행합니다: pay.example.com (브라우저가 보내려던 요청을 결제 감시가 멈춘 것, 15:37)"}
	})
	for _, msg := range []string{"무슨 건이야?", "그거 승인해도 돼?"} {
		if err := r.turn(msg); err != nil {
			t.Fatal(err)
		}
	}
	sent := strings.Split(strings.TrimSpace(read(t, filepath.Join(dir, "input"))), "\n")
	if len(sent) != 2 {
		t.Fatalf("%d 개를 보냈습니다", len(sent))
	}
	if !strings.Contains(sent[0], "[답을 기다리는 카드 - 운영자 화면에") || !strings.Contains(sent[0], "pay.example.com") || !strings.Contains(sent[0], "무슨 건이야?") {
		t.Fatalf("첫 턴:\n%s", sent[0])
	}
	if !strings.Contains(sent[1], "[답을 기다리는 카드] 앞 메시지 때와 같음") || strings.Contains(sent[1], "pay.example.com") {
		t.Fatalf("둘째 턴:\n%s", sent[1])
	}
}
