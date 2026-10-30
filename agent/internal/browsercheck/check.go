package browsercheck

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Site is an order history whose login is checked by opening it.
type Site struct {
	ID        string
	Name      string
	Orders    string
	LoginHost string
}

// Result is the last check of one site.
type Result struct {
	URL   string // where the page landed
	Busy  bool
	Error string
	At    time.Time
}

// Checker opens each site now and then, only while the assistant is idle.
//
// It lives in the agent because the agent is the browser's one user: the
// conversation, the jobs and the order lookups all drive it, and only the
// agent knows when none of them is. status-svc used to open tabs on its own
// clock and could land in the middle of a job; now it reads what is said here
// (Say).
type Checker struct {
	Client    *Client
	Sites     []Site
	BusyHosts []string
	Every     time.Duration
	// WhenDown, when set, is how often a site is looked at while the
	// browser is not running: opening it then starts the browser for the
	// look alone, which the idle browser did every half hour, all day
	// (48 starts a day, 2026-10-27). A check that something else brought
	// the browser up for is cheap and stays at Every.
	WhenDown time.Duration
	Idle     func() bool
	Log      *slog.Logger
	// Relogin, when set, signs a logged-out site back in (with the login the
	// vault keeps - see main), and the site is looked at again right after.
	// It is tried at most once per ReloginEvery a site: a login that keeps
	// failing is a locked account waiting to happen.
	Relogin      func(ctx context.Context, s Site) error
	ReloginEvery time.Duration
	// CanRelogin says whether a relogin is possible at all (a login is saved
	// for the site). Without one there is nothing to try - and nothing to
	// tell the operator every few hours beyond what the status tab shows.
	CanRelogin func(ctx context.Context, s Site) bool
	// StillOut hears a site that is logged out after a relogin was tried.
	StillOut func(s Site, why string)
	// SignedIn hears a site found signed in: what waited on its login (the
	// book's order lookups) can go on without anyone pressing 다시 시도.
	SignedIn func(ctx context.Context, s Site)

	mu      sync.Mutex
	results map[string]Result
	tried   map[string]time.Time
}

// LoggedIn is whether a landing is the site's own page: it opened, nothing
// else was on a login screen, and it did not end at the login page.
func LoggedIn(r Result, s Site) bool {
	if r.Error != "" || r.Busy || r.URL == "" || s.LoginHost == "" {
		return false
	}
	u, err := url.Parse(r.URL)
	return err == nil && u.Hostname() != "" && !strings.EqualFold(u.Hostname(), s.LoginHost)
}

// LoggedOut is whether a landing is the site's login page.
func LoggedOut(r Result, s Site) bool {
	if r.Error != "" || r.Busy || r.URL == "" || s.LoginHost == "" {
		return false
	}
	u, err := url.Parse(r.URL)
	return err == nil && strings.EqualFold(u.Hostname(), s.LoginHost)
}

func (c *Checker) init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.results == nil {
		c.results = map[string]Result{}
		c.tried = map[string]time.Time{}
	}
}

// Run checks every site once per Every, waiting for an idle moment.
func (c *Checker) Run(ctx context.Context) {
	c.init()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if c.Idle != nil && !c.Idle() {
			continue
		}
		c.pass(ctx)
	}
}

// pass looks at every site that is due.
func (c *Checker) pass(ctx context.Context) {
	up, asked := true, false
	for _, s := range c.Sites {
		c.mu.Lock()
		last := c.results[s.ID].At
		c.mu.Unlock()
		if time.Since(last) < c.Every {
			continue
		}
		if c.WhenDown > 0 && time.Since(last) < c.WhenDown {
			if !asked {
				up, _ = c.Client.Alive(ctx)
				asked = true
			}
			if !up {
				continue
			}
		}
		r := c.look(ctx, s)
		if LoggedOut(r, s) && c.Relogin != nil && (c.CanRelogin == nil || c.CanRelogin(ctx, s)) && c.due(s.ID) {
			err := c.Relogin(ctx, s)
			if c.Log != nil {
				c.Log.Info("로그인이 풀려 저장된 정보로 다시 로그인해 봤습니다", "site", s.ID, "err", err)
			}
			if err == nil {
				r = c.look(ctx, s)
			}
			if LoggedOut(r, s) && c.StillOut != nil {
				why := "로그인 뒤에도 로그인 화면으로 돌아왔습니다(문자 인증·보안문자일 수 있습니다)"
				if err != nil {
					why = err.Error()
				}
				c.StillOut(s, why)
			}
		}
		if LoggedIn(r, s) && c.SignedIn != nil {
			c.SignedIn(ctx, s)
		}
	}
}

// look opens the site and records where it landed.
func (c *Checker) look(ctx context.Context, s Site) Result {
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
	return r
}

// due is whether a relogin of this site may be tried now, and marks it tried.
func (c *Checker) due(id string) bool {
	every := c.ReloginEvery
	if every <= 0 {
		every = 6 * time.Hour
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.tried[id]) < every {
		return false
	}
	c.tried[id] = time.Now()
	return true
}
