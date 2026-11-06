package convo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// turns stands in for input-svc's transcript: what the runner appends, and
// the conversation so far for the recap of a new session.
type turns struct {
	mu   sync.Mutex
	list []thread.Turn
	seq  int
}

func (t *turns) add(tr thread.Turn, at time.Time) thread.Turn {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	prefix := "a"
	if tr.Role == thread.RoleUser {
		prefix = "u"
	}
	tr.ID = fmt.Sprintf("%s-%d-%d", prefix, at.UnixMilli(), t.seq)
	tr.At = at.Format("15:04")
	tr.AtISO = at.Format(time.RFC3339Nano)
	t.list = append(t.list, tr)
	return tr
}

// since is the assistant's words after the n-th turn: its answer.
func (t *turns) answerAfter(n int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var parts []string
	for _, tr := range t.list[min(n, len(t.list)):] {
		if tr.Role == thread.RoleAgent {
			parts = append(parts, strings.Join(tr.Paragraphs, "\n\n"))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (t *turns) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.list)
}

func (t *turns) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /turns", func(w http.ResponseWriter, r *http.Request) {
		t.mu.Lock()
		list := append([]thread.Turn(nil), t.list...)
		t.mu.Unlock()
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n < len(list) {
			list = list[len(list)-n:]
		}
		writeJSON(w, map[string]any{"turns": list, "hasMore": false})
	})
	mux.HandleFunc("POST /turns", func(w http.ResponseWriter, r *http.Request) {
		var tr thread.Turn
		if err := json.NewDecoder(r.Body).Decode(&tr); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, t.add(tr, time.Now()))
	})
	mux.HandleFunc("PUT /turns/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Paragraphs []string `json:"paragraphs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		t.mu.Lock()
		defer t.mu.Unlock()
		for i := range t.list {
			if t.list[i].ID == r.PathValue("id") {
				t.list[i].Paragraphs = in.Paragraphs
				writeJSON(w, t.list[i])
				return
			}
		}
		http.NotFound(w, r)
	})
	return mux
}

// mindFake answers the situation in front of every turn (mind's /now, and
// context-svc's, which says nothing here) and what long-term memory holds.
func mindFake(sc Scenario) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /now", func(w http.ResponseWriter, _ *http.Request) {
		rules := sc.Rules
		if rules == nil {
			rules = []string{}
		}
		writeJSON(w, map[string]any{"text": sc.Situation, "prefs": "", "rules": rules})
	})
	// context-svc's regions: home and work, as the reminders' places know them.
	mux.HandleFunc("GET /regions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"regions": []map[string]string{{"name": "집", "kind": "home"}, {"name": "회사", "kind": "work"}}})
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, _ *http.Request) {
		facts := []map[string]any{}
		for _, f := range sc.Memory {
			facts = append(facts, map[string]any{"fact": f, "relation": "RELATES_TO", "score": 0.9})
		}
		writeJSON(w, map[string]any{"facts": facts})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// toolsOf is the tools an MCP server offers, as the CLI would list them.
func toolsOf(ctx context.Context, h http.Handler) ([]*mcp.Tool, error) {
	srv := httptest.NewServer(h)
	defer srv.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "convo", Version: "1"}, nil)
	s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	res, err := s.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// stand is an MCP server offering exactly these tools, as they read, each
// answered by answer instead of its own work.
func stand(name string, tools []*mcp.Tool, answer func(tool string, input json.RawMessage) string) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: name, Version: "1"}, nil)
	for _, t := range tools {
		tool := t.Name
		server.AddTool(t, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var input json.RawMessage
			if req != nil && req.Params != nil {
				input = req.Params.Arguments
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: answer(tool, input)}}}, nil
		})
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// browserTools are the shared browser's tools the conversation is offered (an
// outside server, mcp-browser): enough of them to be the way it might reach
// for, which the instructions say it should not.
func browserTools() []*mcp.Tool {
	obj := map[string]any{"type": "object", "properties": map[string]any{}}
	withURL := map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}, "required": []string{"url"}}
	withIndex := map[string]any{"type": "object", "properties": map[string]any{"index": map[string]any{"type": "integer"}}, "required": []string{"index"}}
	withText := map[string]any{"type": "object", "properties": map[string]any{"index": map[string]any{"type": "integer"}, "text": map[string]any{"type": "string"}}, "required": []string{"index", "text"}}
	return []*mcp.Tool{
		{Name: "browser_navigate", Description: "Navigate the browser to a URL.", InputSchema: withURL},
		{Name: "browser_get_state", Description: "Get the current page's interactive elements and text.", InputSchema: obj},
		{Name: "browser_click", Description: "Click an element by its index.", InputSchema: withIndex},
		{Name: "browser_type", Description: "Type text into an element by its index.", InputSchema: withText},
		{Name: "browser_screenshot", Description: "Take a screenshot of the current page.", InputSchema: obj},
	}
}
