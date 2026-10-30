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
	// reuse: the navigate lands in the about:blank tab already there, on
	// landing, as browser-use does when asked for a new tab.
	reuse   bool
	landing string
	// cold: the navigate starts the browser, which opens a blank tab of its
	// own before the one asked for.
	cold      bool
	current   string
	navigated []string
	// alive is what GET /alive answers: whether a browser is running.
	alive bool
}

func (f *fakeBrowser) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/alive" {
		f.mu.Lock()
		alive := f.alive
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"alive": alive})
		return
	}
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
		u, _ := msg.Params.Arguments["url"].(string)
		f.navigated = append(f.navigated, u)
		switch {
		case u == "about:blank":
			for i := range f.tabs {
				if f.tabs[i].ID == f.current {
					f.tabs[i].URL = u
				}
			}
		case f.lateAfter > 0:
			f.late = true
		case f.reuse:
			for i := range f.tabs {
				if f.tabs[i].URL == "about:blank" {
					f.tabs[i].URL, f.current = f.landing, f.tabs[i].ID
					break
				}
			}
		default:
			if f.cold {
				f.tabs = append(f.tabs, tab{ID: "t-default", URL: "about:blank"})
			}
			f.tabs = append(f.tabs, tab{ID: "t-mine", URL: "about:blank"})
			f.current = "t-mine"
		}
		text = "Navigated"
	case "browser_close_tab":
		f.closed = append(f.closed, msg.Params.Arguments["tab_id"].(string))
		text = "closed"
	case "browser_switch_tab":
		f.current = msg.Params.Arguments["tab_id"].(string)
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

// A browser just started has one blank tab, and the navigate asked for a new
// tab lands in it (browser-use reuses blank tabs). The check follows that tab
// to the login page and blanks it again - left there, every later check took
// it for someone logging in and never looked again (2026-10-17).
func TestOpenFollowsTheBlankTabItReused(t *testing.T) {
	login := "https://login.shop.example/login?rtnUrl=orders"
	f := &fakeBrowser{tabs: []tab{{ID: "t-blank", URL: "about:blank"}}, reuse: true, landing: login}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	defer srv.Close()
	c := New(srv.URL, "tok")
	c.poll, c.tries = time.Millisecond, 10

	l, err := c.Open(context.Background(), "https://shop.example/orders", []string{"login.shop.example"})
	if err != nil {
		t.Fatal(err)
	}
	if l.URL != login || l.Busy {
		t.Fatalf("로그인 화면에 도착했다고 답해야 합니다: %+v", l)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.closed) != 0 {
		t.Fatalf("재사용한 탭은 닫지 않습니다(브라우저의 마지막 탭일 수 있음): %v", f.closed)
	}
	if f.tabs[0].URL != "about:blank" {
		t.Fatalf("재사용한 탭을 빈 페이지로 되돌려야 합니다: %+v (navigate %v)", f.tabs, f.navigated)
	}
}

// A browser started by the navigate opens a blank tab of its own beside the
// one asked for. That one is not ours: ours is the one that reaches the site.
func TestOpenDoesNotTakeTheBrowsersOwnBlankTab(t *testing.T) {
	f := &fakeBrowser{cold: true}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	defer srv.Close()
	c := New(srv.URL, "tok")
	c.poll, c.tries = time.Millisecond, 10

	l, err := c.Open(context.Background(), "https://shop.example/orders", nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.URL != "https://shop.example/orders" {
		t.Fatalf("도착한 주소: %q", l.URL)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.closed) != 1 || f.closed[0] != "t-mine" {
		t.Fatalf("자기 탭만 닫아야 합니다: %v", f.closed)
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

// Signed in is a landing on the site's own page - not the login page, and not
// a check that could not tell (an error, another tab at a login screen).
func TestLoggedInIsTheSitesOwnPage(t *testing.T) {
	s := Site{ID: "coupang", LoginHost: "login.coupang.com"}
	for _, c := range []struct {
		r    Result
		want bool
	}{
		{Result{URL: "https://mc.coupang.com/ssr/desktop/order/list"}, true},
		{Result{URL: "https://login.coupang.com/login/login.pang?rtnUrl=x"}, false},
		{Result{URL: "https://mc.coupang.com/x", Busy: true}, false},
		{Result{Error: "탭을 열지 못함"}, false},
		{Result{}, false},
	} {
		if got := LoggedIn(c.r, s); got != c.want {
			t.Errorf("%+v: %v", c.r, got)
		}
	}
}

// A check that lands on the site's own page says so, and what waited on the
// login (the book's lookups, 2026-10-18) can go on without a person.
func TestACheckThatFindsTheSiteSignedInSaysSo(t *testing.T) {
	f := &fakeBrowser{tabs: []tab{{ID: "t-agent", URL: "https://agent.example/working"}}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	defer srv.Close()
	signed := make(chan string, 1)
	c := &Checker{
		Client: New(srv.URL, "tok"),
		Sites:  []Site{{ID: "shop", Orders: "https://shop.example/orders", LoginHost: "login.shop.example"}},
		Every:  time.Hour,
		Idle:   func() bool { return true },
		SignedIn: func(_ context.Context, s Site) {
			signed <- s.ID
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.init()
	go c.pass(ctx)
	select {
	case id := <-signed:
		if id != "shop" {
			t.Fatalf("사이트: %q", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("로그인된 사이트를 알리지 않았습니다")
	}
}

// While the browser is down, a look would start it for the look alone: the
// check waits WhenDown instead, and looks at once when something else has
// the browser up (48 starts a day for the looks alone, 2026-10-27).
func TestALookDoesNotStartTheBrowser(t *testing.T) {
	f := &fakeBrowser{tabs: []tab{{ID: "t-agent", URL: "https://agent.example/working"}}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	defer srv.Close()
	c := &Checker{
		Client:   New(srv.URL, "tok"),
		Sites:    []Site{{ID: "shop", Orders: "https://shop.example/orders", LoginHost: "login.shop.example"}},
		Every:    30 * time.Minute,
		WhenDown: 6 * time.Hour,
		Idle:     func() bool { return true },
	}
	c.Client.poll, c.Client.tries = time.Millisecond, 3
	c.init()
	c.results["shop"] = Result{At: time.Now().Add(-time.Hour)} // due by Every, not by WhenDown
	ctx := context.Background()

	c.pass(ctx)
	f.mu.Lock()
	n := len(f.navigated)
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("브라우저가 꺼져 있으면 열지 않아야 합니다: %v", f.navigated)
	}

	f.mu.Lock()
	f.alive = true
	f.mu.Unlock()
	c.pass(ctx)
	f.mu.Lock()
	n = len(f.navigated)
	f.mu.Unlock()
	if n == 0 {
		t.Fatal("브라우저가 떠 있으면 바로 열어 봐야 합니다")
	}

	f.mu.Lock()
	f.alive, f.navigated = false, nil
	f.mu.Unlock()
	c.results["shop"] = Result{At: time.Now().Add(-7 * time.Hour)} // past WhenDown
	c.pass(ctx)
	f.mu.Lock()
	n = len(f.navigated)
	f.mu.Unlock()
	if n == 0 {
		t.Fatal("여섯 시간이 지나면 꺼져 있어도 열어 봅니다")
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
