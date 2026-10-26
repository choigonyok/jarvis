package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The model thinks before it answers, and the thinking spends max_tokens: a
// call that ran out while thinking, or declined, has no answer to read - it
// says so, with why it stopped, instead of handing back an empty string.
func TestCompleteSaysWhenThereIsNoAnswer(t *testing.T) {
	var asked map[string]any
	reply := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &asked)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	a := NewAnthropic("k", "", 0)
	a.url = srv.URL

	reply = `{"content":[{"type":"thinking","thinking":"…"}],"stop_reason":"max_tokens","usage":{"input_tokens":9000,"output_tokens":8000}}`
	if _, err := a.Complete(context.Background(), "s", "u"); !errors.Is(err, ErrNoAnswer) || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("생각만 하다 한도에 닿으면 답 없음이어야 합니다: %v", err)
	}
	if asked["max_tokens"] != float64(maxTokens) {
		t.Fatalf("생각과 답이 함께 들어갈 한도로 물어야 합니다: %v", asked["max_tokens"])
	}

	reply = `{"content":[{"type":"text","text":"말씀드리기 어렵습니다"}],"stop_reason":"refusal","usage":{"input_tokens":10,"output_tokens":5}}`
	if _, err := a.Complete(context.Background(), "s", "u"); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("거절은 답 없음이어야 합니다: %v", err)
	}

	reply = `{"content":[{"type":"thinking","thinking":"…"},{"type":"text","text":"{\"changes\":[]}"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":50}}`
	got, err := a.Complete(context.Background(), "s", "u")
	if err != nil || got != `{"changes":[]}` {
		t.Fatalf("생각 다음의 답만 돌려줘야 합니다: %q %v", got, err)
	}
}

// A call that may search asks with the search tool, reads only what was
// written after the last result (a cited answer comes in pieces), sends a
// paused turn back to go on, and prices the searches in.
func TestCompleteSearches(t *testing.T) {
	var asked []map[string]any
	replies := []string{
		`{"content":[{"type":"text","text":"찾아볼게요 {"},{"type":"server_tool_use","id":"s1","name":"web_search","input":{"query":"본사 위치"}}],"stop_reason":"pause_turn","usage":{"input_tokens":100,"output_tokens":10}}`,
		`{"content":[{"type":"web_search_tool_result","tool_use_id":"s1","content":[]},{"type":"text","text":"{\"changes\":"},{"type":"text","text":"[]}","citations":[{"type":"web_search_result_location"}]}],"stop_reason":"end_turn","usage":{"input_tokens":15000,"output_tokens":40,"server_tool_use":{"web_search_requests":1}}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		asked = append(asked, body)
		_, _ = w.Write([]byte(replies[len(asked)-1]))
	}))
	defer srv.Close()
	a := NewAnthropic("k", "", 0)
	a.url = srv.URL
	var uses []Use
	a.OnUse = func(u Use) { uses = append(uses, u) }

	got, err := a.Complete(WithSearch(context.Background(), 2), "s", "u")
	if err != nil || got != `{"changes":[]}` {
		t.Fatalf("마지막 검색 결과 뒤의 글을 이어 읽어야 합니다: %q %v", got, err)
	}
	if len(asked) != 2 {
		t.Fatalf("멈춘 차례는 한 번 더 보내 이어야 합니다: %d번 보냄", len(asked))
	}
	tools, _ := asked[0]["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["type"] != webSearch || tools[0].(map[string]any)["max_uses"] != float64(2) {
		t.Fatalf("검색 도구를 횟수 한도와 함께 줘야 합니다: %v", asked[0]["tools"])
	}
	msgs, _ := asked[1]["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["role"] != "assistant" || len(msgs[1].(map[string]any)["content"].([]any)) != 2 {
		t.Fatalf("멈춘 차례의 답을 그대로 돌려보내야 합니다: %v", asked[1]["messages"])
	}
	if len(uses) != 2 || uses[1].Searches != 1 {
		t.Fatalf("검색 횟수를 장부에 남겨야 합니다: %+v", uses)
	}
	if want := 15100*0.10/1e6 + 50*0.50/1e6 + searchPrice; a.Spent() < want-1e-9 || a.Spent() > want+1e-9 {
		t.Fatalf("검색 값까지 셈해야 합니다: %v, %v 여야 함", a.Spent(), want)
	}

	asked, replies = nil, []string{`{"content":[{"type":"text","text":"{}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`}
	if _, err := a.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if _, ok := asked[0]["tools"]; ok {
		t.Fatalf("검색을 허락하지 않은 부름에는 도구가 없어야 합니다: %v", asked[0]["tools"])
	}
}
