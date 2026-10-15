// Command mac-collector is the one process on the Mac that reads what the
// Mac is doing, the way kakaotalk-client is the one that reads KakaoTalk.
// It reports states to context-svc's /signals, never content:
//
//   - mac.app        what kind of app is in front: meeting | code | chat |
//     browser | other (the app's name stays here)
//   - mac.clipboard  what kind of thing was just copied: tracking | address
//     | date (the text stays here; nothing is sent while a
//     password manager is in front)
//   - mac.download, mac.screenshot  a new file of that kind appeared (the
//     file extension only) - off unless -files, since macOS
//     asks for permission to read those folders
//   - mac.notification  a delivery app's notification reached this Mac
//     (iPhone Mirroring forwards the phone's): delivery.baemin,
//     delivery.coupangeats … - which app only, read from the
//     unified log; the text stays on the phone and in the
//     notification store
//
// A state is sent when it changes, and again every half hour, so a report
// lost on the way is corrected by the next.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	url := flag.String("url", os.Getenv("CONTEXT_SIGNALS_URL"), "context-svc /signals (the gateway's /context/signals)")
	tokenFile := flag.String("token-file", os.ExpandEnv("$HOME/.config/jarvis/signal-token"), "file holding the signal token")
	every := flag.Duration("every", 5*time.Second, "how often to look")
	files := flag.Bool("files", false, "also report new downloads and screenshots (macOS asks for folder access)")
	notes := flag.Bool("notifications", true, "report delivery apps' notifications forwarded from the iPhone")
	flag.Parse()
	token := os.Getenv("SIGNAL_TOKEN")
	if b, err := os.ReadFile(*tokenFile); token == "" && err == nil {
		token = strings.TrimSpace(string(b))
	}
	if *url == "" || token == "" {
		log.Fatal("주소(-url 또는 CONTEXT_SIGNALS_URL)와 신호 토큰(-token-file 또는 SIGNAL_TOKEN)이 필요합니다")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := &collector{url: *url, token: token, http: &http.Client{Timeout: 10 * time.Second}, last: map[string]sent{}}
	if *files {
		c.dirs = map[string]string{"mac.download": os.ExpandEnv("$HOME/Downloads"), "mac.screenshot": os.ExpandEnv("$HOME/Desktop")}
		c.seen = map[string]bool{}
		c.scan(true)
	}
	if *notes {
		go c.watchNotifications(ctx)
	}
	t := time.NewTicker(*every)
	defer t.Stop()
	for {
		c.step()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// deliveryApps are the delivery apps by their iPhone bundle (App Store
// lookup, 2026-10-15).
var deliveryApps = map[string]string{
	"com.jawebs.baedal": "baemin", "com.coupang.coupang-eats": "coupangeats",
	"com.yogiyo.yogiyoapp": "yogiyo", "com.shinhan.o2o": "ddangyo",
}

var stored = regexp.MustCompile(`Adding notification to storage: ([A-Za-z0-9._-]+):([0-9A-Fa-f-]+)`)

// Delivery is which delivery app a notification-centre log line is about,
// with the notification's id, if any.
func Delivery(line string) (app, id string) {
	m := stored.FindStringSubmatch(line)
	if m == nil {
		return "", ""
	}
	return deliveryApps[m[1]], m[1] + ":" + m[2]
}

// watchNotifications follows the notification centre's log: a delivery
// app's notification stored here (the same one is re-added several times)
// is reported once. The log says which app, not what it said.
func (c *collector) watchNotifications(ctx context.Context) {
	seen := map[string]bool{}
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "log", "stream", "--style", "compact",
			"--predicate", `subsystem == "com.apple.unc" AND category == "application"`)
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			log.Printf("알림 로그를 읽지 못했습니다: %v", err)
		} else {
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				app, id := Delivery(sc.Text())
				if app == "" || seen[id] {
					continue
				}
				if len(seen) > 1000 {
					seen = map[string]bool{}
				}
				seen[id] = true
				c.report("mac.notification", "delivery."+app, true)
			}
			_ = cmd.Wait()
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Minute):
		}
	}
}

type sent struct {
	state string
	at    time.Time
}

type collector struct {
	mu         sync.Mutex // report is called from the loop and the log watcher
	url, token string
	http       *http.Client
	last       map[string]sent
	clip       string
	dirs       map[string]string
	seen       map[string]bool
}

func (c *collector) step() {
	app := front()
	c.report("mac.app", Kind(app), false)
	if text, err := exec.Command("pbpaste").Output(); err == nil && string(text) != c.clip {
		c.clip = string(text)
		if !Secret(app) {
			if k := Pattern(c.clip); k != "" {
				c.report("mac.clipboard", k, true)
			}
		}
	}
	if c.dirs != nil {
		c.scan(false)
	}
}

// report sends a state when it changed (or always, for events), and again
// every half hour.
func (c *collector) report(kind, state string, event bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, ok := c.last[kind]
	if !event && ok && prev.state == state && time.Since(prev.at) < 30*time.Minute {
		return
	}
	body, _ := json.Marshal(map[string]string{"source": "mac", "kind": kind, "state": state, "at": time.Now().Format(time.RFC3339)})
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		log.Printf("보내지 못했습니다 (%s): %v", kind, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("거절됐습니다 (%s): %d", kind, resp.StatusCode)
		return
	}
	c.last[kind] = sent{state, time.Now()}
}

// front is the name of the app in front.
func front() string {
	asn, err := exec.Command("lsappinfo", "front").Output()
	if err != nil {
		return ""
	}
	out, err := exec.Command("lsappinfo", "info", "-only", "name", strings.TrimSpace(string(asn))).Output()
	if err != nil {
		return ""
	}
	s := string(out)
	if i := strings.Index(s, "="); i >= 0 {
		return strings.Trim(strings.TrimSpace(s[i+1:]), `"`)
	}
	return ""
}

// Kind is what sort of app it is - all that leaves the Mac.
func Kind(app string) string {
	a := strings.ToLower(app)
	switch {
	case a == "":
		return "other"
	case containsAny(a, "zoom", "teams", "webex", "facetime", "meet", "around", "gather"):
		return "meeting"
	case containsAny(a, "code", "xcode", "terminal", "iterm", "cursor", "goland", "intellij", "warp", "ghostty", "zed"):
		return "code"
	case containsAny(a, "kakaotalk", "카카오톡", "slack", "messages", "메시지", "discord", "telegram", "line"):
		return "chat"
	case containsAny(a, "safari", "chrome", "firefox", "arc", "edge", "whale", "brave"):
		return "browser"
	}
	return "other"
}

// Secret is an app whose clipboard is not even looked at.
func Secret(app string) bool {
	return containsAny(strings.ToLower(app), "1password", "bitwarden", "keychain", "키체인", "passwords", "암호", "lastpass", "dashlane")
}

var (
	tracking = regexp.MustCompile(`^\s*(\d{10,14}|\d{4}-\d{4}-\d{4})\s*$`)
	address  = regexp.MustCompile(`(시|도)\s*\S+(구|군)\s*\S+(로|길|동)\s*\d+`)
	date     = regexp.MustCompile(`(\d{4}[./-]\d{1,2}[./-]\d{1,2})|(\d{1,2}월\s*\d{1,2}일)`)
)

// Pattern is what kind of thing a copied text is, if any of interest: a
// tracking number, an address, a date. The text itself goes nowhere.
func Pattern(text string) string {
	if len(text) > 500 {
		return ""
	}
	switch {
	case tracking.MatchString(text):
		return "tracking"
	case address.MatchString(text):
		return "address"
	case date.MatchString(text):
		return "date"
	}
	return ""
}

// scan reports files new in the watched folders, by their extension.
func (c *collector) scan(first bool) {
	for kind, dir := range c.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			key := kind + "/" + name
			if c.seen[key] {
				continue
			}
			c.seen[key] = true
			if first || strings.HasPrefix(name, ".") {
				continue
			}
			if kind == "mac.screenshot" && !strings.Contains(name, "스크린샷") && !strings.HasPrefix(name, "Screenshot") {
				continue
			}
			c.report(kind, strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."), true)
		}
	}
}

func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
