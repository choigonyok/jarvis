// Package browser asks the agent's browser whether a site's order history
// opens: it loads the page in a new tab, reads where it landed, and closes
// the tab. Landing on a login page is the answer "logged out" - the only
// answer that matters, and one a cookie on disk turned out not to give (a
// profile can keep every long-lived Coupang cookie and still be logged out).
//
// It talks to the browser's MCP bridge with the smallest client that works:
// JSON-RPC over HTTP, one session per check.
package browser

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	// The new tab is the one that was not there before. Its address is the
	// answer, read off the tab list: the "current page" the bridge reports
	// is not reliably the tab just opened. Redirects to a login page happen
	// after the first load, so wait for it to leave about:blank and settle.
	seen := map[string]bool{}
	for _, t := range before {
		seen[t.ID] = true
	}
	var opened []tab
	var landed string
	// A freshly started browser took 18 seconds to open a tab; give the page
	// as long again to land before calling it a failure.
	for i := 0; i < 20; i++ {
		time.Sleep(time.Second)
		after, err := s.tabs(ctx)
		if err != nil {
			continue
		}
		opened = opened[:0]
		for _, t := range after {
			if !seen[t.ID] {
				opened = append(opened, t)
			}
		}
		if len(opened) > 0 && opened[0].URL != "" && !strings.HasPrefix(opened[0].URL, "about:") {
			landed = opened[0].URL
			if i >= 2 { // at least three seconds, for the redirect
				break
			}
		}
	}
	for _, t := range opened {
		_, _ = s.call(ctx, "browser_close_tab", map[string]any{"tab_id": t.ID})
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
