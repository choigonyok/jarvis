package claudecode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/config"
)

// With search-svc the instructions say how to find things out, and
// find_nearby is described on Naver's map; without it, as before.
func TestSearchRulesOnlyWithSearch(t *testing.T) {
	off := (&Runner{cfg: config.Config{SystemPrompt: "base", MindURL: "http://mind"}}).systemPrompt()
	if strings.Contains(off, "mcp__web__search") || !strings.Contains(off, "OpenStreetMap") {
		t.Fatalf("검색 서비스가 없을 때의 지시가 바뀌었습니다:\n%s", off)
	}
	on := (&Runner{cfg: config.Config{SystemPrompt: "base", MindURL: "http://mind", SearchURL: "http://search", SearchToken: "s"}}).systemPrompt()
	for _, want := range []string{"mcp__web__search", "mcp__web__read", "인스타에서 봤대", "네이버 지도", "open 을 켜세요"} {
		if !strings.Contains(on, want) {
			t.Fatalf("지시에 %q 가 없습니다:\n%s", want, on)
		}
	}
	if strings.Contains(on, "OpenStreetMap). 후보") {
		t.Fatalf("OpenStreetMap 안내가 남았습니다:\n%s", on)
	}
	guest := (&Runner{cfg: config.Config{SystemPrompt: "guest", Guest: true, SearchURL: "http://search", SearchToken: "s"}}).systemPrompt()
	if !strings.Contains(guest, "mcp__web__search") || strings.Contains(guest, "find_nearby") {
		t.Fatalf("손님 지시:\n%s", guest)
	}
}

// What the tools gave back reaches OnSeen: a string result, text blocks,
// and the CLI's own WebSearch alike.
func TestToolResultsAreSeen(t *testing.T) {
	var seen []string
	r := &Runner{}
	r.OnSeen(func(s string) { seen = append(seen, s) })
	msg := json.RawMessage(`{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"a","content":"Web search results for query: \"x\"\n\nLinks: [{\"title\":\"공지\",\"url\":\"https://example.com/notice\"}]"},
		{"type":"tool_result","tool_use_id":"b","content":[{"type":"text","text":"https://blog.naver.com/a/1"},{"type":"image","source":{}}]},
		{"type":"text","text":"not a result"}]}`)
	if err := r.handle(event{Type: "user", Message: msg}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "https://example.com/notice") || !strings.Contains(seen[0], "https://blog.naver.com/a/1") || strings.Contains(seen[0], "not a result") {
		t.Fatalf("seen = %q", seen)
	}
}
