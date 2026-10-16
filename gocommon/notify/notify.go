// Package notify reports events to events-svc, from which notify-svc decides
// whether they become a push, a line in the evening digest, or only an inbox
// entry - and the agent whether they are worth a suggestion.
//
// An event is written to the outbox table (event_outbox) first: events-svc
// moves it into the log from there, so one sent while events-svc is down or
// restarting is late rather than lost. Without a database it is posted
// directly, as before.
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
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Event is the notification shape events-svc accepts on POST /events.
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
	db    *pgxpool.Pool
	http  *http.Client
	log   *slog.Logger
}

// New returns nil with neither a URL nor a database; a nil client's Send does
// nothing. The database connection is opened lazily, on the first event.
func New(url, dsn, token string, log *slog.Logger) *Client {
	if url == "" && dsn == "" {
		return nil
	}
	c := &Client{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: 5 * time.Second}, log: log}
	if dsn != "" {
		if cfg, err := pgxpool.ParseConfig(dsn); err != nil {
			log.Warn("이벤트 outbox 를 쓰지 않습니다: DATABASE_URL 을 해석하지 못했습니다", "err", err)
		} else {
			cfg.MaxConns = 1
			c.db, _ = pgxpool.NewWithConfig(context.Background(), cfg)
		}
	}
	return c
}

// Send reports a notification-shaped event.
func (c *Client) Send(e Event) { c.Emit(e) }

// Emit reports any event events-svc accepts (the event shape or the
// notification shape).
func (c *Client) Emit(v any) {
	if c == nil {
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if c.db != nil {
			_, err := c.db.Exec(ctx, `insert into event_outbox (payload) values ($1)`, body)
			if err == nil {
				return
			}
			c.log.Warn("이벤트를 outbox 에 쓰지 못해 바로 보냅니다", "err", err)
		}
		c.post(ctx, body)
	}()
}

func (c *Client) post(ctx context.Context, body []byte) {
	if c.url == "" {
		return
	}
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
		c.log.Warn("이벤트를 보내지 못했습니다", "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		c.log.Warn("이벤트 서비스가 거절했습니다", "status", resp.StatusCode)
	}
}
