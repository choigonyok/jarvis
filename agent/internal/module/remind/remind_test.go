package remind

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func session(t *testing.T, m *Module) *mcp.ClientSession {
	t.Helper()
	server := httptest.NewServer(m.Handler())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func callTool(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out.WriteString(tc.Text)
		}
	}
	return out.String(), res.IsError
}

// The tools hand mind only what was given, and answer with what mind stored -
// or, when it stored nothing, say so plainly.
func TestTools(t *testing.T) {
	var calls []call
	s := session(t, New(fakeMind(t, &calls).URL+"/", "tok", nil))

	got, isErr := callTool(t, s, "set_reminder", map[string]any{"text": "베개 챙기세요", "at": "2026-10-24T08:00"})
	if isErr || !strings.Contains(got, "저장했습니다: [rem_1] 10/24(토) 08:00 · 베개 챙기세요") {
		t.Fatalf("저장된 때와 문구를 돌려줘야 합니다: %q", got)
	}
	if c := calls[0]; c.method != http.MethodPost || c.path != "/api/reminders" || c.body["source"] != "chat" || c.body["at"] != "2026-10-24T08:00" || c.body["place"] != nil {
		t.Fatalf("준 칸만 보내야 합니다: %+v", c)
	}

	got, isErr = callTool(t, s, "set_reminder", map[string]any{"text": "베개", "at": "어제"})
	if !isErr || !strings.Contains(got, "시각을 읽지 못했습니다") || !strings.Contains(got, "됐다고 말하지 마세요") {
		t.Fatalf("못 했으면 그렇게: %q %v", got, isErr)
	}

	got, isErr = callTool(t, s, "change_reminder", map[string]any{"id": "rem_1", "at": "2026-10-24T07:30", "clear": []string{"place"}})
	if c := calls[len(calls)-1]; isErr || c.method != http.MethodPatch || c.path != "/api/reminders/rem_1" || c.body["at"] != "2026-10-24T07:30" || c.body["text"] != nil || c.body["clear"] == nil {
		t.Fatalf("고칠 칸만: %+v %q", c, got)
	}
	if !strings.HasPrefix(got, "고쳤습니다: [rem_1]") {
		t.Fatalf("%q", got)
	}

	got, _ = callTool(t, s, "list_reminders", map[string]any{"all": true})
	if calls[len(calls)-1].path != "/api/reminders?all=1" || !strings.Contains(got, "[rem_1] 10/24(토) 08:00") || !strings.Contains(got, "자비스가 그때로 미뤄 둔 알림") {
		t.Fatalf("목록: %q", got)
	}

	got, isErr = callTool(t, s, "cancel_reminder", map[string]any{"id": "rem_1"})
	if isErr || calls[len(calls)-1].method != http.MethodDelete || !strings.Contains(got, "취소했습니다: [rem_1]") {
		t.Fatalf("취소: %q", got)
	}
	if renderList(nil) != "맡아 둔 리마인드가 없습니다." {
		t.Fatal("빈 목록")
	}
}

// The console's list is mind's own answer for this person, waiting ones and
// the last HistoryDays days', passed through as it is; only GET.
func TestConsoleListIsMindsHistory(t *testing.T) {
	var calls []call
	mind := fakeMind(t, &calls)
	h := New(mind.URL, "tok", nil).ConsoleHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/reminders", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reminders"`) {
		t.Fatalf("목록: %d %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || calls[0].path != "/api/reminders?days=30" {
		t.Fatalf("mind 에 물은 것: %+v", calls)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/reminders", nil))
	if rec.Code != http.StatusMethodNotAllowed || len(calls) != 1 {
		t.Fatalf("GET 만: %d %+v", rec.Code, calls)
	}
	// mind not answering is the console's error, not an empty list.
	down := New("http://127.0.0.1:1", "tok", nil).ConsoleHandler()
	rec = httptest.NewRecorder()
	down.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/reminders", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("mind 가 없을 때: %d", rec.Code)
	}
}
