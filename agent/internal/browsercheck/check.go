package browsercheck

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Site is an order history whose login is checked by opening it.
type Site struct {
	ID        string `json:"id"`
	Orders    string `json:"orders"`
	LoginHost string `json:"loginHost"`
}

// Result is the last check of one site.
type Result struct {
	URL   string    `json:"url,omitempty"` // where the page landed
	Busy  bool      `json:"busy,omitempty"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

// Checker opens each site now and then, only while the assistant is idle.
//
// It lives in the agent because the agent is the browser's one user: the
// conversation, the jobs and the order lookups all drive it, and only the
// agent knows when none of them is. status-svc used to open tabs on its own
// clock and could land in the middle of a job; now it reads the results here.
type Checker struct {
	Client    *Client
	Sites     []Site
	BusyHosts []string
	Every     time.Duration
	Idle      func() bool
	Log       *slog.Logger

	mu      sync.Mutex
	results map[string]Result
	kick    chan struct{}
}

func (c *Checker) init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.results == nil {
		c.results = map[string]Result{}
		c.kick = make(chan struct{}, 1)
	}
}

// Run checks every site once per Every, waiting for an idle moment.
func (c *Checker) Run(ctx context.Context) {
	c.init()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	forced := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-c.kick:
			forced = true
		}
		if c.Idle != nil && !c.Idle() {
			continue
		}
		for _, s := range c.Sites {
			c.mu.Lock()
			last := c.results[s.ID].At
			c.mu.Unlock()
			if !forced && time.Since(last) < c.Every {
				continue
			}
			r := Result{At: time.Now()}
			l, err := c.Client.Open(ctx, s.Orders, c.BusyHosts)
			if err != nil {
				r.Error = err.Error()
			} else {
				r.URL, r.Busy = l.URL, l.Busy
			}
			c.mu.Lock()
			c.results[s.ID] = r
			c.mu.Unlock()
			if c.Log != nil {
				c.Log.Info("브라우저로 로그인 상태를 확인했습니다", "site", s.ID, "url", r.URL, "busy", r.Busy, "err", r.Error)
			}
		}
		forced = false
	}
}

// Handler serves GET (the latest results) and POST (check again soon).
func (c *Checker) Handler() http.Handler {
	c.init()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			select {
			case c.kick <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		c.mu.Lock()
		out := map[string]any{"sites": c.Sites, "results": c.results, "every": c.Every.String()}
		body, _ := json.Marshal(out)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(body)
	})
}
