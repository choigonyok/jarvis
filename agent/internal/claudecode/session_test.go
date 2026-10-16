package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// A stand-in CLI that writes down its arguments and the first message it was
// given, reports session "s-new", and finishes.
func recordingClaude(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/args"
IFS= read -r line
printf '%s' "$line" > "` + dir + `/first"
echo '{"type":"system","session_id":"s-new"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}'
echo '{"type":"result","result":"ok"}'
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func sessionRunner(t *testing.T, prompt string, saved session) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	r := runner(t, recordingClaude(t, dir))
	r.cfg.SystemPrompt = prompt
	r.cfg.TurnTimeout = 10 * time.Second
	r.cfg.SessionPath = filepath.Join(dir, "session.json")
	r.store = thread.NewStore("http://127.0.0.1:1", "", bus.New())
	r.sessionID, r.sessionPrompt = saved.ID, saved.Prompt
	return r, dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The CLI keeps a session's first system prompt, so a session is resumed only
// under the instructions it was opened with. Changed instructions open a new
// session - and that is remembered with the new instructions' fingerprint.
// The date rides on every turn's message either way.
func TestSessionFollowsItsInstructions(t *testing.T) {
	same, dir := sessionRunner(t, "규칙 B", session{ID: "s-old", Prompt: fingerprint("규칙 B")})
	if err := same.turn("안녕"); err != nil {
		t.Fatal(err)
	}
	if args := read(t, filepath.Join(dir, "args")); !strings.Contains(args, "--resume\ns-old") {
		t.Fatalf("같은 지시면 이어 갑니다:\n%s", args)
	}
	if first := read(t, filepath.Join(dir, "first")); !strings.Contains(first, "현재 시각") || !strings.Contains(first, "안녕") {
		t.Fatalf("날짜는 메시지에: %s", first)
	}

	changed, dir := sessionRunner(t, "규칙 B", session{ID: "s-old", Prompt: fingerprint("규칙 A")})
	if err := changed.turn("다시 사줘"); err != nil {
		t.Fatal(err)
	}
	if args := read(t, filepath.Join(dir, "args")); strings.Contains(args, "--resume") {
		t.Fatalf("지시가 바뀌면 새 세션: \n%s", args)
	}
	var saved session
	if err := jsonfile.Load(filepath.Join(dir, "session.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID != "s-new" || saved.Prompt != fingerprint("규칙 B") {
		t.Fatalf("새 세션을 새 지시로 기억해야 합니다: %+v", saved)
	}

	// A session saved before fingerprints existed is one of unknown
	// instructions: not resumed.
	unknown, dir := sessionRunner(t, "규칙 B", session{ID: "s-old"})
	if err := unknown.turn("x"); err != nil {
		t.Fatal(err)
	}
	if args := read(t, filepath.Join(dir, "args")); strings.Contains(args, "--resume") {
		t.Fatalf("지문 없는 옛 세션은 이어 가지 않습니다:\n%s", args)
	}
}
