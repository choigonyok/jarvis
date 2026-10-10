// Package notify sends events to notify-svc, which decides whether they
// become a push, a line in the evening digest, or only an inbox entry.
//
// Fire and forget: a notification that cannot be delivered must never hold up
// the work it is about.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Event is notify-svc's POST /events body.
type Event struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Tier   string `json:"tier"` // now | digest | log
	Level  string `json:"level,omitempty"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	URL    string `json:"url,omitempty"`
	Key    string `json:"key,omitempty"`
}

type Client struct {
	url   string
	token string
	http  *http.Client
	log   *slog.Logger
}

// New returns nil without a URL; a nil client's Send does nothing.
func New(url, token string, log *slog.Logger) *Client {
	if url == "" {
		return nil
	}
	return &Client{url: url, token: token, http: &http.Client{Timeout: 5 * time.Second}, log: log}
}

func (c *Client) Send(e Event) {
	if c == nil {
		return
	}
	go func() {
		body, _ := json.Marshal(e)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/events", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			c.log.Warn("알림을 보내지 못했습니다", "kind", e.Kind, "err", err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			c.log.Warn("알림 서비스가 거절했습니다", "kind", e.Kind, "status", resp.StatusCode)
		}
	}()
}
