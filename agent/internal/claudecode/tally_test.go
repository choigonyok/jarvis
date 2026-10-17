package claudecode

import (
	"strings"
	"testing"
)

// Each request is counted once, however many lines its response came in;
// the totals are the result line's.
func TestTally(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"id":"a","content":[{"type":"thinking"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":1000,"cache_read_input_tokens":20000,"output_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":1000,"ephemeral_1h_input_tokens":0}}}}`,
		`{"type":"assistant","message":{"id":"a","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":1000,"cache_read_input_tokens":20000,"output_tokens":40,"cache_creation":{"ephemeral_5m_input_tokens":1000,"ephemeral_1h_input_tokens":0}}}}`,
		`{"type":"assistant","message":{"id":"b","content":[{"type":"text","text":"y"}],"usage":{"input_tokens":3,"cache_creation_input_tokens":200,"cache_read_input_tokens":21000,"output_tokens":10}}}`,
		`{"type":"assistant","is_api_error_message":true,"message":{"content":[{"type":"text","text":"err"}]}}`,
		`{"type":"result","num_turns":2,"total_cost_usd":0.05,"usage":{"input_tokens":5,"cache_creation_input_tokens":1200,"cache_read_input_tokens":41000,"output_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":1200,"ephemeral_1h_input_tokens":0}},"modelUsage":{"claude-b":{},"claude-a":{}}}`,
	}
	var tl tally
	if err := scan(strings.NewReader(strings.Join(lines, "\n")), tl.observe); err != nil {
		t.Fatal(err)
	}
	if tl.requests() != 2 {
		t.Fatalf("요청 %d", tl.requests())
	}
	if f := tl.first(); f.Output != 40 || f.context() != 21002 {
		t.Fatalf("첫 요청 %+v", f)
	}
	if l := tl.last(); l.context() != 21203 {
		t.Fatalf("마지막 요청 %+v", l)
	}
	if s := tl.sum(); s.CacheRead != 41000 || s.Output != 50 || s.Creation.FiveMin != 1200 {
		t.Fatalf("합계는 result 줄에서 %+v", s)
	}
	attrs := map[string]any{}
	a := tl.attrs()
	for i := 0; i+1 < len(a); i += 2 {
		attrs[a[i].(string)] = a[i+1]
	}
	if attrs["requests"] != 2 || attrs["context"] != 21203 || attrs["turns"] != 2 || strings.Join(attrs["models"].([]string), ",") != "claude-a,claude-b" {
		t.Fatalf("로그 %v", attrs)
	}

	// Without a result line (cut short), the requests add up.
	var cut tally
	_ = scan(strings.NewReader(strings.Join(lines[:3], "\n")), cut.observe)
	if s := cut.sum(); s.CacheRead != 41000 || s.Output != 50 || s.Input != 5 {
		t.Fatalf("result 없이 %+v", s)
	}
}

func TestEstimateTokens(t *testing.T) {
	if n := estimateTokens("안녕하세요"); n != 5 {
		t.Fatalf("한글 %d", n)
	}
	if n := estimateTokens("abcdefgh"); n != 2 {
		t.Fatalf("ASCII %d", n)
	}
}
