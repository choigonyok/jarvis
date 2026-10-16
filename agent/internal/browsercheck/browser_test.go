package browsercheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeBrowser is the browser's MCP bridge: tabs, navigate, close. While the
// check waits for its page, something else (a job, the operator) opens a
// tab of its own.
type fakeBrowser struct {
	mu     sync.Mutex
	tabs   []tab
	lists  int
	closed []string
}

func (f *fakeBrowser) handle(w http.ResponseWriter, r *http.Request) {
	var msg struct {
		ID     *int   `json:"id"`
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&msg)
	if msg.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	f.mu.Lock()
	text := ""
	switch msg.Params.Name {
	case "browser_get_state":
		text = `{"url":"https://agent.example/working"}`
	case "browser_list_tabs":
		f.lists++
		if f.lists == 3 { // after the check opened its tab
			f.tabs = append(f.tabs, tab{ID: "t-other", URL: "https://other.example/job"})
			for i := range f.tabs {
				if f.tabs[i].ID == "t-mine" {
					f.tabs[i].URL = "https://shop.example/orders"
				}
			}
		}
		b, _ := json.Marshal(f.tabs)
		text = string(b)
	case "browser_navigate":
		f.tabs = append(f.tabs, tab{ID: "t-mine", URL: "about:blank"})
		text = "Opened new tab"
	case "browser_close_tab":
		f.closed = append(f.closed, msg.Params.Arguments["tab_id"].(string))
		text = "closed"
	case "browser_switch_tab":
		text = "switched"
	}
	f.mu.Unlock()
	w.Header().Set("Mcp-Session-Id", "s1")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *msg.ID,
		"result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}})
}

// B-10: the check closes the tab it opened and no other - not the one a job
// opened while it waited.
func TestOpenClosesOnlyItsOwnTab(t *testing.T) {
	f := &fakeBrowser{tabs: []tab{{ID: "t-agent", URL: "https://agent.example/working"}}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	defer srv.Close()

	l, err := New(srv.URL, "tok").Open(context.Background(), "https://shop.example/orders", []string{"login.shop.example"})
	if err != nil {
		t.Fatal(err)
	}
	if l.URL != "https://shop.example/orders" {
		t.Fatalf("도착한 주소: %q", l.URL)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.closed) != 1 || f.closed[0] != "t-mine" {
		t.Fatalf("자기가 연 탭만 닫아야 합니다: %v", f.closed)
	}
}
