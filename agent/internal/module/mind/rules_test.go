package mind

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// sent keeps what would go through the stream.
type sent struct{ calls []string }

func (s *sent) Do(_ context.Context, method, path string, in, out any) error {
	body, _ := json.Marshal(in)
	s.calls = append(s.calls, method+" "+path+" "+string(body))
	if path == "/api/rules" && out != nil {
		return json.Unmarshal([]byte(`{"rule":{"id":"note_ab12","text":"렌터카 대여는 일정 카드로 올리지 않는다"},"new":true}`), out)
	}
	return nil
}

// A rule is kept through the stream, as every write to mind; only a rule is
// taken back by rule.end - mind's own notes are answered on the 생활 screen.
func TestRulesAreKeptAndTakenBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"rules": []map[string]any{{"id": "note_ab12", "text": "렌터카 대여는 일정 카드로 올리지 않는다", "since": "2026-11-03T21:00:00+09:00"}}})
	}))
	defer srv.Close()
	stream := &sent{}
	m := New(Client{URL: srv.URL, Send: stream})
	change := tool.New(tool.Change, "고친다.")
	m.Change(change)
	look := tool.New(tool.Look, "읽는다.")
	m.LookRules(look)
	ctx := context.Background()
	out, err := change.Do(ctx, "rule.set", map[string]any{"text": "렌터카 대여는 일정 카드로 올리지 않는다", "said": "대여는 일정 아니야"})
	if err != nil || !strings.Contains(out, "규칙으로 남겼습니다") || !strings.Contains(out, "note_ab12") {
		t.Fatalf("%q %v", out, err)
	}
	if len(stream.calls) != 1 || !strings.HasPrefix(stream.calls[0], "POST /api/rules ") || !strings.Contains(stream.calls[0], "대여는 일정 아니야") {
		t.Fatalf("기록으로 가야 합니다: %v", stream.calls)
	}
	if _, err := change.Do(ctx, "rule.end", map[string]any{"id": "note_other"}); err == nil {
		t.Fatal("규칙이 아닌 노트는 거두지 않습니다")
	}
	if out, err := change.Do(ctx, "rule.end", map[string]any{"id": "note_ab12", "why": "이제 넣어도 돼"}); err != nil || out != "규칙을 거뒀습니다." {
		t.Fatalf("%q %v", out, err)
	}
	if last := stream.calls[len(stream.calls)-1]; !strings.HasPrefix(last, "POST /api/notes/note_ab12 ") || !strings.Contains(last, `"status":"rejected"`) {
		t.Fatalf("노트의 문으로: %s", last)
	}
	if out, err := look.Do(ctx, "rules", nil); err != nil || !strings.Contains(out, "렌터카 대여") || !strings.Contains(out, "note_ab12") {
		t.Fatalf("%q %v", out, err)
	}
}
