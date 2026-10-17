package claudecode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// A stand-in CLI that writes down its arguments and the first message it was
// given, reports session "s-new" and two requests' usage, and finishes.
func recordingClaude(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/args"
env > "` + dir + `/env"
IFS= read -r line
printf '%s' "$line" > "` + dir + `/first"
echo '{"type":"system","session_id":"s-new"}'
echo '{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"cache_creation_input_tokens":30000,"cache_read_input_tokens":0,"output_tokens":5}}}'
echo '{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":500,"cache_read_input_tokens":30003,"output_tokens":20}}}'
echo '{"type":"result","result":"ok","num_turns":2,"usage":{"input_tokens":5,"cache_creation_input_tokens":30500,"cache_read_input_tokens":30003,"output_tokens":25}}'
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

// instructions is the fingerprint of the system prompt a runner configured
// with prompt opens its sessions with.
func instructions(prompt string) string {
	return fingerprint((&Runner{cfg: config.Config{SystemPrompt: prompt}}).systemPrompt())
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
	same, dir := sessionRunner(t, "규칙 B", session{ID: "s-old", Prompt: instructions("규칙 B")})
	if err := same.turn("안녕"); err != nil {
		t.Fatal(err)
	}
	if args := read(t, filepath.Join(dir, "args")); !strings.Contains(args, "--resume\ns-old") {
		t.Fatalf("같은 지시면 이어 갑니다:\n%s", args)
	}
	if first := read(t, filepath.Join(dir, "first")); !strings.Contains(first, "현재 시각") || !strings.Contains(first, "안녕") {
		t.Fatalf("날짜는 메시지에: %s", first)
	}

	changed, dir := sessionRunner(t, "규칙 B", session{ID: "s-old", Prompt: instructions("규칙 A")})
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
	if saved.ID != "s-new" || saved.Prompt != instructions("규칙 B") {
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

// A turn notes what its requests showed: a new session's first request less
// its message is what every request sends (instructions and tools), and the
// last request's size is the context the next one starts from. Both outlive
// a restart, with when the turn ended.
func TestTurnMeasuresTheSession(t *testing.T) {
	r, dir := sessionRunner(t, "규칙", session{})
	r.cfg.CacheTTL = "5m"
	if err := r.turn("안녕"); err != nil {
		t.Fatal(err)
	}
	opening := read(t, filepath.Join(dir, "first"))
	var msg struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(opening), &msg); err != nil {
		t.Fatal(err)
	}
	wantBase := 30003 - estimateTokens(msg.Message.Content)
	if r.base != wantBase || r.context != 30505 || r.lastAt.IsZero() {
		t.Fatalf("base %d (want %d) context %d lastAt %v", r.base, wantBase, r.context, r.lastAt)
	}
	var saved session
	if err := jsonfile.Load(filepath.Join(dir, "session.json"), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID != "s-new" || saved.Base != wantBase || saved.Context != 30505 || saved.LastAt.IsZero() {
		t.Fatalf("저장: %+v", saved)
	}
	if env := read(t, filepath.Join(dir, "env")); !strings.Contains(env, "CLAUDE_CODE_PROMPT_CACHE_TTL=5m") {
		t.Fatalf("캐시 수명을 넘겨야 합니다:\n%s", env)
	}

	// Resumed, the session keeps its base; only its size moves.
	r.base = 20000
	if err := r.turn("또"); err != nil {
		t.Fatal(err)
	}
	if args := read(t, filepath.Join(dir, "args")); !strings.Contains(args, "--resume\ns-new") {
		t.Fatalf("이어 가야 합니다:\n%s", args)
	}
	if r.base != 20000 || r.context != 30505 {
		t.Fatalf("이어 간 세션의 고정부는 그대로: base %d context %d", r.base, r.context)
	}
}

// A typed message starts over in a new session when the last one went quiet
// or grew large - unless there is little to gain, or a card this
// conversation raised is still open.
func TestRotate(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		idle    time.Duration
		history int
		held    bool
		rotate  bool
	}{
		{"조용해진 큰 세션", 31 * time.Minute, 35000, false, true},
		{"조용해도 작은 세션", 31 * time.Minute, 5000, false, false},
		{"이어지는 아주 큰 세션", time.Minute, 55000, false, true},
		{"이어지는 보통 세션", time.Minute, 35000, false, false},
		{"카드가 남은 세션", 31 * time.Minute, 60000, true, false},
	}
	for _, c := range cases {
		r := runner(t, "claude")
		r.cfg.SessionIdle, r.cfg.SessionMaxTokens, r.cfg.SessionMinTokens = 30*time.Minute, 50000, 20000
		r.sessionID, r.base, r.context, r.lastAt = "s", 25000, 25000+c.history, now.Add(-c.idle)
		r.rotateLocked(now, c.held)
		if got := r.sessionID == ""; got != c.rotate {
			t.Errorf("%s: 새 세션 %v, 기대 %v", c.name, got, c.rotate)
		}
	}
	// No session yet: nothing to do.
	r := runner(t, "claude")
	r.rotateLocked(now, false)
	if r.sessionID != "" {
		t.Fatal("세션이 생겼습니다")
	}
}

// End to end: a message typed after a long pause opens a new session handed
// the recent conversation; a card's decision never does.
func TestTypedMessageAfterAPauseStartsOver(t *testing.T) {
	var posts atomic.Int32
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/turns":
			_, _ = w.Write([]byte(`{"turns":[{"id":"1","role":"user","at":"09:00","text":"캠핑 어디가 좋아?"},{"id":"2","role":"agent","at":"09:01","paragraphs":["가평 어때요?"]},{"id":"3","role":"user","at":"10:00","text":"그걸로 예약해줘"}]}`))
		case r.Method == http.MethodPost:
			posts.Add(1)
			_, _ = w.Write([]byte(`{"id":"x","role":"user","at":"10:00"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer chat.Close()

	r, dir := sessionRunner(t, "규칙", session{ID: "s-old", Prompt: instructions("규칙")})
	r.store = thread.NewStore(chat.URL, "", bus.New())
	r.cfg.SessionIdle, r.cfg.SessionMaxTokens, r.cfg.SessionMinTokens = 30*time.Minute, 50000, 20000
	r.base, r.context, r.lastAt = 25000, 70000, time.Now().Add(-2*time.Hour)

	// A card's decision continues the old session, however long the pause.
	r.Enqueue("[승인됨] 카드 결과")
	waitIdle(t, r)
	if args := read(t, filepath.Join(dir, "args")); !strings.Contains(args, "--resume\ns-old") {
		t.Fatalf("카드 결과는 세션을 바꾸지 않습니다:\n%s", args)
	}

	r.mu.Lock()
	r.sessionID, r.base, r.context, r.lastAt = "s-old", 25000, 70000, time.Now().Add(-2*time.Hour)
	r.mu.Unlock()
	if _, err := r.Submit("그걸로 예약해줘", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, r)
	if args := read(t, filepath.Join(dir, "args")); strings.Contains(args, "--resume") {
		t.Fatalf("오래 쉰 큰 세션은 새로 엽니다:\n%s", args)
	}
	first := read(t, filepath.Join(dir, "first"))
	for _, want := range []string{"최근 대화", "가평 어때요?", "그걸로 예약해줘"} {
		if !strings.Contains(first, want) {
			t.Fatalf("새 세션 첫 메시지에 %q 가 없습니다:\n%s", want, first)
		}
	}

	// With a card of this conversation open, the session stays.
	r.mu.Lock()
	r.sessionID, r.base, r.context, r.lastAt = "s-old", 25000, 70000, time.Now().Add(-2*time.Hour)
	r.mu.Unlock()
	r.SetHold(func(time.Time) bool { return true })
	if _, err := r.Submit("어떻게 됐어?", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, r)
	if args := read(t, filepath.Join(dir, "args")); !strings.Contains(args, "--resume\ns-old") {
		t.Fatalf("카드가 남아 있으면 이어 갑니다:\n%s", args)
	}
}

func waitIdle(t *testing.T, r *Runner) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		time.Sleep(20 * time.Millisecond)
		if r.Idle() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("턴이 끝나지 않았습니다")
		}
	}
}
