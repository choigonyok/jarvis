package claudecode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// A stand-in CLI that writes as the real one does with
// --include-partial-messages (2.1.287, seen 2026-10-27): each block's pieces,
// then the block's whole text as an assistant line, then the block's stop.
// It says a line, calls a tool, and answers in two paragraphs.
func streamingClaude(t *testing.T, dir string) string {
	t.Helper()
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s-1"}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"thinking","thinking":""}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"확인해"}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":" 볼게요."}},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"확인해 볼게요."}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"t1","name":"Bash"}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"date\"}"}},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"date"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Tue Oct 27"}]}}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m2"}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"오늘은 "}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"27일이에요.\n\n"}},"parent_tool_use_id":null}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"화요일이고요."}},"parent_tool_use_id":null}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"오늘은 27일이에요.\n\n화요일이고요."}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0},"parent_tool_use_id":null}`,
		`{"type":"result","subtype":"success","result":"ok"}`,
	}
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + dir + "/args\"\nIFS= read -r line\ncat <<'EOF'\n" +
		strings.Join(lines, "\n") + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeTurns stands in for chat-svc: an empty transcript, and a turn posted
// comes back with an id.
func fakeTurns() http.Handler {
	n := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			_, _ = w.Write([]byte(`{"turns":[],"hasMore":false}`))
			return
		}
		var t thread.Turn
		_ = json.NewDecoder(r.Body).Decode(&t)
		n++
		t.ID = fmt.Sprintf("a-%d-%d", time.Now().UnixMilli(), n)
		_ = json.NewEncoder(w).Encode(t)
	})
}

// The answer reaches the screen as it is written: each text block as a
// draft that grows, then the stored turn in its place. A tool call between
// them is no draft.
func TestTurnStreamsItsAnswer(t *testing.T) {
	dir := t.TempDir()
	r := runner(t, streamingClaude(t, dir))
	r.cfg.TurnTimeout = 10 * time.Second
	chat := httptest.NewServer(fakeTurns())
	t.Cleanup(chat.Close)
	b := bus.New()
	events, cancel := b.Subscribe()
	defer cancel()
	r.store = thread.NewStore(chat.URL, "", b)

	if err := r.turn("오늘 며칠이야"); err != nil {
		t.Fatal(err)
	}
	if args := read(t, filepath.Join(dir, "args")); !strings.Contains(args, "--include-partial-messages") {
		t.Fatalf("쓰는 중인 글을 받아야 합니다:\n%s", args)
	}

	type settled struct {
		draft string
		text  string
		shown string
	}
	var got []settled
	shown := map[string]string{}
	for len(got) < 2 {
		var ev bus.Event
		select {
		case ev = <-events:
		case <-time.After(2 * time.Second):
			t.Fatalf("프레임이 끊겼습니다: %+v", got)
		}
		switch ev.Type {
		case "draft":
			d := ev.Draft.(thread.Draft)
			if d.Gone {
				t.Fatalf("말한 초안이 거둬졌습니다: %+v", d)
			}
			// No emoji here, so runes count as UTF-16 units do.
			cur := []rune(shown[d.ID])
			if d.At > len(cur) {
				t.Fatalf("빈틈 있는 프레임: %+v (화면 %q)", d, string(cur))
			}
			shown[d.ID] = string(cur[:d.At]) + d.Text
		case "turn":
			turn := ev.Turn.(thread.Turn)
			if ev.Replaces == "" {
				t.Fatalf("초안 없이 온 턴: %+v", turn)
			}
			got = append(got, settled{ev.Replaces, strings.Join(turn.Paragraphs, "\n\n"), shown[ev.Replaces]})
		}
	}
	if got[0].draft == got[1].draft {
		t.Fatal("블록마다 따로인 초안이어야 합니다")
	}
	if got[0].text != "확인해 볼게요." || got[1].text != "오늘은 27일이에요.\n\n화요일이고요." {
		t.Fatalf("턴: %+v", got)
	}
	for _, g := range got {
		if g.shown != g.text {
			t.Fatalf("화면에 쓰인 글 %q 와 턴 %q 가 다릅니다", g.shown, g.text)
		}
	}
}
