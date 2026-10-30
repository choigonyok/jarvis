package remind

import (
	"context"
	"encoding/json"
	"github.com/choigonyok/jarvis/agent/internal/tool"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type call struct {
	method, path string
	body         map[string]any
}

// fakeMind answers /api/reminders as mind does and keeps what it was asked.
func fakeMind(t *testing.T, calls *[]call) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c := call{method: r.Method, path: r.URL.RequestURI()}
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		*calls = append(*calls, c)
		line := `{"reminder":{"id":"rem_1","text":"베개 챙기세요","source":"chat","status":"%s","when":"10/24(토) 08:00","line":"[rem_1] 10/24(토) 08:00 · 베개 챙기세요%s"}}`
		switch {
		case r.Method == http.MethodPost && c.body["at"] == "어제":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"시각을 읽지 못했습니다"}`))
		case r.Method == http.MethodPost, r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(strings.ReplaceAll(strings.Replace(line, "%s", "waiting", 1), "%s", "")))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(strings.Replace(strings.Replace(line, "%s", "cancelled", 1), "%s", " (취소함)", 1)))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"reminders":[
				{"id":"rem_1","source":"chat","status":"waiting","line":"[rem_1] 10/24(토) 08:00 · 베개 챙기세요"},
				{"id":"rem_2","source":"rebalance","status":"waiting","line":"[rem_2] 10/20(화) 17:00부터 집에 있거나 도착할 때 · 늦어도 22:00 · 자산 비중이 목표 범위를 벗어났어요"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func session(t *testing.T, m *Module) *tool.Board {
	t.Helper()
	b := tool.New("t", "")
	m.Look(b)
	m.Change(b)
	return b
}

func callTool(t *testing.T, b *tool.Board, name string, args map[string]any) (string, bool) {
	t.Helper()
	out, err := b.Do(context.Background(), name, args)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

// The tools hand mind only what was given, and answer with what mind stored -
// or, when it stored nothing, say so plainly.
func TestTools(t *testing.T) {
	var calls []call
	s := session(t, New(fakeMind(t, &calls).URL+"/", "tok", nil))

	got, isErr := callTool(t, s, "reminder.set", map[string]any{"text": "베개 챙기세요", "at": "2026-10-24T08:00"})
	if isErr || !strings.Contains(got, "저장했습니다: [rem_1] 10/24(토) 08:00 · 베개 챙기세요") {
		t.Fatalf("저장된 때와 문구를 돌려줘야 합니다: %q", got)
	}
	if c := calls[0]; c.method != http.MethodPost || c.path != "/api/reminders" || c.body["source"] != "chat" || c.body["at"] != "2026-10-24T08:00" || c.body["place"] != nil {
		t.Fatalf("준 칸만 보내야 합니다: %+v", c)
	}

	got, isErr = callTool(t, s, "reminder.set", map[string]any{"text": "베개", "at": "어제"})
	if !isErr || !strings.Contains(got, "시각을 읽지 못했습니다") || !strings.Contains(got, "됐다고 말하지 마세요") {
		t.Fatalf("못 했으면 그렇게: %q %v", got, isErr)
	}

	got, isErr = callTool(t, s, "reminder.change", map[string]any{"id": "rem_1", "at": "2026-10-24T07:30", "clear": []string{"place"}})
	if c := calls[len(calls)-1]; isErr || c.method != http.MethodPatch || c.path != "/api/reminders/rem_1" || c.body["at"] != "2026-10-24T07:30" || c.body["text"] != nil || c.body["clear"] == nil {
		t.Fatalf("고칠 칸만: %+v %q", c, got)
	}
	if !strings.HasPrefix(got, "고쳤습니다: [rem_1]") {
		t.Fatalf("%q", got)
	}

	got, _ = callTool(t, s, "reminders", map[string]any{"all": true})
	if calls[len(calls)-1].path != "/api/reminders?all=1" || !strings.Contains(got, "[rem_1] 10/24(토) 08:00") || !strings.Contains(got, "자비스가 그때로 미뤄 둔 알림") {
		t.Fatalf("목록: %q", got)
	}

	got, isErr = callTool(t, s, "reminder.cancel", map[string]any{"id": "rem_1"})
	if isErr || calls[len(calls)-1].method != http.MethodDelete || !strings.Contains(got, "취소했습니다: [rem_1]") {
		t.Fatalf("취소: %q", got)
	}
	if renderList(nil) != "맡아 둔 리마인드가 없습니다." {
		t.Fatal("빈 목록")
	}
}
