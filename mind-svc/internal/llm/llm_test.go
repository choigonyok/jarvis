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
