// Package browsercheck asks the agent's browser whether a site's order history
// opens: it loads the page in a new tab, reads where it landed, and closes
// the tab (or, when the browser reused a blank tab for it, blanks it again). Landing on a login page is the answer "logged out" - the only
// answer that matters, and one a cookie on disk turned out not to give (a
// profile can keep every long-lived Coupang cookie and still be logged out).
//
// It talks to the browser's MCP bridge with the smallest client that works:
// JSON-RPC over HTTP, one session per check.
package browsercheck

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	URL   string
	Token string
	http  *http.Client
	// One check at a time: each one opens and closes a tab, and two at once
	// could close each other's.
	mu sync.Mutex
	// poll and tries pace the wait for the new tab (zero: a second, twenty
	// times); tests shorten them.
	poll  time.Duration
	tries int
}

func New(url, token string) *Client {
	return &Client{URL: strings.TrimRight(url, "/") + "/", Token: token, http: &http.Client{Timeout: 90 * time.Second}}
}

// Landing is where a page ended up.
type Landing struct {
	URL string
	// Busy is set when a tab is already on a login page: someone may be
	// typing a password in it, and a new tab would steal the focus.
	Busy bool
}

type tab struct {
	ID  string `json:"tab_id"`
	URL string `json:"url"`
}

// Open loads target in a new tab and returns where it landed. busyHosts are
// login hosts; if any tab is already on one, nothing is opened.
func (c *Client) Open(ctx context.Context, target string, busyHosts []string) (Landing, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, err := c.session(ctx)
	if err != nil {
		return Landing{}, err
	}
	before, err := s.tabs(ctx)
	if err != nil {
		return Landing{}, err
	}
	for _, t := range before {
		for _, h := range busyHosts {
			if strings.Contains(t.URL, h) {
				return Landing{URL: t.URL, Busy: true}, nil
			}
		}
	}
	// The tab the agent was on, to give the focus back to.
	var active string
	if st, err := s.state(ctx); err == nil {
		active = st
	}

	if _, err := s.call(ctx, "browser_navigate", map[string]any{"url": target, "new_tab": true}); err != nil {
		return Landing{}, err
	}
	// Our tab is the one the navigate used, and its address is the answer,
	// read off the tab list: the "current page" the bridge reports is not
	// reliably it. The bridge (browser-use) asked for a new tab reuses a
	// blank one when there is one - the tab it is on, if blank, or any
	// about:blank tab - and a browser it has just started has nothing else.
	// So a tab that was blank before is watched like a new one. None is
	// taken for ours until it is on the target's site or a login page: a
	// browser starting up opens a blank tab of its own beside ours, and a new
	// tab already on another site is someone else's (a job, the agent).
	// Redirects to a login page happen after the first load, so the address
	// is read until it settles.
	seen, blank := map[string]bool{}, map[string]bool{}
	for _, t := range before {
		seen[t.ID] = true
		blank[t.ID] = isBlank(t.URL)
	}
	site := ""
	if u, err := url.Parse(target); err == nil {
		site = siteOf(u.Hostname())
	}
	ours := func(t tab) bool {
		return (!seen[t.ID] || blank[t.ID]) && (sameSite(t.URL, site) || onHost(t.URL, busyHosts))
	}
	var mine string
	var landed string
	// A freshly started browser took 18 seconds to open a tab; give the page
	// as long again to land before calling it a failure.
	poll, tries := c.poll, c.tries
	if poll == 0 {
		poll = time.Second
	}
	if tries == 0 {
		tries = 20
	}
	for i := 0; i < tries; i++ {
		time.Sleep(poll)
		after, err := s.tabs(ctx)
		if err != nil {
			continue
		}
		var cur *tab
		for k := range after {
			t := &after[k]
			if (mine != "" && t.ID == mine) || (mine == "" && ours(*t)) {
				cur = t
				break
			}
		}
		if cur == nil {
			continue
		}
		mine = cur.ID
		if !isBlank(cur.URL) {
			landed = cur.URL
			if i >= 2 { // at least three seconds, for the redirect
				break
			}
		}
	}
	if mine == "" {
		// A browser that was not running is started by the navigate itself,
		// and the tab can show up after the wait is over. One last look, so
		// that tab is not left behind.
		if after, err := s.tabs(ctx); err == nil {
			for _, t := range after {
				if ours(t) {
					mine = t.ID
					break
				}
			}
		}
	}
	switch {
	case mine == "":
	case seen[mine]:
		// A blank tab the navigate reused is blanked again, not closed: it
		// may be the browser's last, and left on a login page it would read
		// as someone logging in (BusyHosts) at every later check.
		if _, err := s.call(ctx, "browser_switch_tab", map[string]any{"tab_id": mine}); err == nil {
			_, _ = s.call(ctx, "browser_navigate", map[string]any{"url": "about:blank"})
		}
	default:
		_, _ = s.call(ctx, "browser_close_tab", map[string]any{"tab_id": mine})
	}
	if landed == "" {
		return Landing{}, errors.New("새 탭이 페이지를 열지 못했습니다")
	}
	for _, t := range before {
		if t.URL == active {
			_, _ = s.call(ctx, "browser_switch_tab", map[string]any{"tab_id": t.ID})
			break
		}
	}
	return Landing{URL: landed}, nil
}

type session struct {
	c  *Client
	id string
	n  int
}

func (c *Client) session(ctx context.Context) (*session, error) {
	s := &session{c: c}
	if _, err := s.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "status-svc", "version": "1"},
	}); err != nil {
		return nil, fmt.Errorf("브라우저에 연결하지 못했습니다: %w", err)
	}
	_ = s.notify(ctx, "notifications/initialized")
	return s, nil
}

func (s *session) tabs(ctx context.Context) ([]tab, error) {
	text, err := s.call(ctx, "browser_list_tabs", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out []tab
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("탭 목록을 읽지 못했습니다: %w", err)
	}
	return out, nil
}

// state returns the active tab's URL.
func (s *session) state(ctx context.Context) (string, error) {
	text, err := s.call(ctx, "browser_get_state", map[string]any{"include_screenshot": false})
	if err != nil {
		return "", err
	}
	var st struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(text), &st); err != nil {
		return "", fmt.Errorf("페이지 상태를 읽지 못했습니다: %w", err)
	}
	return st.URL, nil
}

func (s *session) call(ctx context.Context, tool string, args map[string]any) (string, error) {
	raw, err := s.rpc(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return "", err
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	if res.IsError {
		return "", fmt.Errorf("%s: %s", tool, b.String())
	}
	return b.String(), nil
}

func (s *session) notify(ctx context.Context, method string) error {
	_, err := s.post(ctx, map[string]any{"jsonrpc": "2.0", "method": method})
	return err
}

func (s *session) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.n++
	id := s.n
	body, err := s.post(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	var msg struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("응답을 읽지 못했습니다: %w", err)
	}
	if msg.Error != nil {
		return nil, errors.New(msg.Error.Message)
	}
	return msg.Result, nil
}

// post sends one message and returns the JSON-RPC reply, which the server may
// send as plain JSON or as a one-event SSE stream.
func (s *session) post(ctx context.Context, msg any) ([]byte, error) {
	b, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.c.URL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if s.c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.c.Token)
	}
	if s.id != "" {
		req.Header.Set("Mcp-Session-Id", s.id)
	}
	res, err := s.c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if id := res.Header.Get("Mcp-Session-Id"); id != "" {
		s.id = id
	}
	if res.StatusCode == http.StatusAccepted {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("응답 코드 %d", res.StatusCode)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		var buf bytes.Buffer
		_, err := buf.ReadFrom(res.Body)
		return buf.Bytes(), err
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data:"); ok {
			data = strings.TrimSpace(data)
			if strings.Contains(data, `"id"`) {
				return []byte(data), nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("응답이 비었습니다")
}

// siteOf is a host's site: its last two labels (coupang.com), or three
// under a second-level domain like co.kr.
func siteOf(host string) string {
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	n := 2
	if len(labels) >= 3 {
		switch labels[len(labels)-2] {
		case "co", "or", "go", "ne", "ac", "re", "pe", "com", "net", "org":
			if len(labels[len(labels)-1]) == 2 { // a country code: shop.co.kr
				n = 3
			}
		}
	}
	if len(labels) < n {
		return host
	}
	return strings.Join(labels[len(labels)-n:], ".")
}

// isBlank is a tab with no page yet: about:blank, the new-tab page, nothing.
func isBlank(raw string) bool {
	return raw == "" || strings.HasPrefix(raw, "about:") || strings.HasPrefix(raw, "chrome://newtab") ||
		strings.HasPrefix(raw, "chrome://new-tab-page")
}

// onHost says whether raw is on one of hosts exactly.
func onHost(raw string, hosts []string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if strings.EqualFold(u.Hostname(), h) {
			return true
		}
	}
	return false
}

// sameSite says whether raw is on site (the site itself or a subdomain).
func sameSite(raw, site string) bool {
	if site == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == site || strings.HasSuffix(h, "."+site)
}
