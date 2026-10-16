package browsercheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeBrowser is the browser's MCP bridge: tabs, navigate, close. While the
// check waits for its page, something else (a job, the operator) opens a
// tab of its own.
type fakeBrowser struct {
	mu     sync.Mutex
	tabs   []tab
	lists  int
	closed []string
	// lateAfter, when set, holds the navigated tab back until that many tab
	// lists have been made (a cold browser starting up).
	lateAfter int
	late      bool
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
		if f.late && f.lateAfter > 0 && f.lists >= f.lateAfter {
			f.tabs = append(f.tabs, tab{ID: "t-late", URL: "https://shop.example/orders"})
			f.late = false
		}
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
		if f.lateAfter > 0 {
			f.late = true
			break
		}
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

// A cold browser: the tab the check asked for shows up only after the wait.
// The check fails (nothing landed in time) but does not leave the tab open.
func TestLateTabIsStillClosed(t *testing.T) {
	// One list before the navigate and three during the wait; the tab is
	// listed from the fifth - the last look after the wait.
	f := &fakeBrowser{tabs: []tab{{ID: "t-agent", URL: "https://agent.example/working"}}, lateAfter: 5}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	defer srv.Close()
	c := New(srv.URL, "tok")
	c.poll, c.tries = time.Millisecond, 3
	if l, err := c.Open(context.Background(), "https://shop.example/orders", nil); err == nil {
		t.Fatalf("제때 열리지 않았으면 실패로 답해야 합니다: %+v lists=%d tabs=%v", l, f.lists, f.tabs)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.closed) != 1 || f.closed[0] != "t-late" {
		t.Fatalf("늦게 뜬 자기 탭도 닫아야 합니다: %v", f.closed)
	}
}

func TestSiteOf(t *testing.T) {
	for host, want := range map[string]string{
		"mc.coupang.com": "coupang.com", "login.coupang.com": "coupang.com",
		"shop.co.kr": "shop.co.kr", "m.shop.co.kr": "shop.co.kr", "naver.com": "naver.com",
	} {
		if got := siteOf(host); got != want {
			t.Errorf("siteOf(%q) = %q, want %q", host, got, want)
		}
	}
	if !sameSite("https://login.coupang.com/login", "coupang.com") || sameSite("https://notcoupang.com/", "coupang.com") {
		t.Error("sameSite")
	}
}

func TestLoggedOutIsTheLoginPage(t *testing.T) {
	s := Site{ID: "coupang", LoginHost: "login.coupang.com"}
	for _, c := range []struct {
		r    Result
		want bool
	}{
		{Result{URL: "https://login.coupang.com/login/login.pang?rtnUrl=x"}, true},
		{Result{URL: "https://mc.coupang.com/ssr/desktop/order/list"}, false},
		{Result{URL: "https://login.coupang.com/x", Busy: true}, false},
		{Result{Error: "탭을 열지 못함"}, false},
	} {
		if got := LoggedOut(c.r, s); got != c.want {
			t.Errorf("%+v: %v", c.r, got)
		}
	}
}

func TestReloginIsNotTriedAgainSoon(t *testing.T) {
	c := &Checker{ReloginEvery: time.Hour}
	c.init()
	if !c.due("coupang") || c.due("coupang") {
		t.Fatal("한 시간에 한 번만 시도해야 합니다")
	}
	if !c.due("naver") {
		t.Fatal("사이트마다 따로 셉니다")
	}
}
