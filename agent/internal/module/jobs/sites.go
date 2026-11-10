package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SiteGuard keeps a running job inside its own sites. A job runs with no one
// watching, in the browser that is logged in to the operator's shops, so
// while it runs the browser's guard refuses any page outside the job's Sites
// (and their sibling subdomains) - whatever a page it reads tells it to open.
type SiteGuard interface {
	// Restrict holds the browser to sites for at most d; a run that dies
	// without Release does not leave the person's own browsing held.
	Restrict(ctx context.Context, sites []string, d time.Duration) error
	Release(ctx context.Context) error
}

// BridgeGuard is the browser container's /job-sites, beside its MCP bridge,
// and its /notices: what a run is told with its next browser tool.
type BridgeGuard struct {
	root   string
	token  string
	client *http.Client
}

// NewBridgeGuard takes the bridge's MCP URL (JARVIS_EXTRA_MCP mcp-browser)
// and its token; /job-sites and /notices are on the same host.
func NewBridgeGuard(mcpURL, token string) (*BridgeGuard, error) {
	u, err := url.Parse(mcpURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("브라우저 주소를 읽지 못했습니다: %q", mcpURL)
	}
	return &BridgeGuard{root: u.Scheme + "://" + u.Host, token: token, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (g *BridgeGuard) Restrict(ctx context.Context, sites []string, d time.Duration) error {
	if sites == nil {
		sites = []string{}
	}
	body, _ := json.Marshal(map[string]any{"sites": sites, "seconds": int(d.Seconds())})
	return g.call(ctx, http.MethodPost, "/job-sites", body)
}

func (g *BridgeGuard) Release(ctx context.Context) error {
	return g.call(ctx, http.MethodDelete, "/job-sites", nil)
}

// Tell hands the run holding the browser text with the result of its next
// browser tool - only while a job holds it: the bridge drops it once the job
// lets go (browser/notices.py).
func (g *BridgeGuard) Tell(ctx context.Context, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	return g.call(ctx, http.MethodPost, "/notices", body)
}

func (g *BridgeGuard) call(ctx context.Context, method, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, method, g.root+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("브라우저 가드가 %d 로 답했습니다", resp.StatusCode)
	}
	return nil
}
