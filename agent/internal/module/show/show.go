// Package show lets the conversation put a picture in front of the person:
// a photo from a page it read, or one already here (a photo the person sent,
// a job's photo), as a message of its own in the thread - the way a friend
// sends the product's picture instead of describing it (2026-10-28: "대화탭에
// ai가 이미지를 띄울 수 있도록").
//
// A picture from the web is fetched here and kept with the uploads, so the
// thread shows it as long as it shows anything. Only an address the
// conversation has seen is fetched - in a page it read, a search result, the
// person's own words (the web module's ledger) - so a page that tells the
// model to "show" an address of its own making cannot carry anything out in
// it. Only https, never an address inside this network, and only JPEG, PNG
// or WebP of a sane size.
package show

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

const (
	Name       = "show"
	ServerName = "show"
	// MaxPictures is how many go in one message.
	MaxPictures = 4
	// MaxBytes bounds a picture fetched from the web.
	MaxBytes = 10 << 20
)

// Tools is every tool this server has. It needs no card: it only shows the
// person a picture, in their own thread.
var Tools = []string{"mcp__" + ServerName + "__show_image"}

// Thread is where a picture is shown: a message of its own.
type Thread interface {
	AppendAgentImages(images []string, caption string) *thread.Turn
}

type Module struct {
	uploads *uploads.Store
	thread  Thread
	// seen is whether the conversation has seen an address (the web
	// module's ledger); nil: no address is fetched.
	seen   func(string) bool
	client *http.Client
	log    *slog.Logger
}

// New shows pictures in t, keeping them in up. seen, when set, is the
// ledger a web address must be in to be fetched.
func New(up *uploads.Store, t Thread, seen func(string) bool, log *slog.Logger) *Module {
	return &Module{uploads: up, thread: t, seen: seen, client: publicClient(), log: log}
}

func (*Module) Name() string                { return Name }
func (*Module) Start(context.Context) error { return nil }
func (*Module) Stop(context.Context) error  { return nil }

type ShowInput struct {
	URLs    []string `json:"urls,omitempty" jsonschema:"보여 줄 그림의 주소(https). 읽은 페이지의 '그림 주소'·검색 결과·운영자의 말에 나온 주소 그대로. 짐작해 만든 주소는 열지 않는다"`
	Uploads []string `json:"uploads,omitempty" jsonschema:"이미 있는 사진 이름(운영자가 보낸 사진·작업 사진. 예: 3f2a…c9.jpg)"`
	Caption string   `json:"caption,omitempty" jsonschema:"사진과 함께 보일 한 줄(선택). 사진이 무엇인지"`
}

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "show_image",
		Description: fmt.Sprintf("운영자에게 사진을 대화에 띄워 보여 준다(한 번에 %d장까지). 상품·장소·음식처럼 보면 바로 아는 것, 운영자가 '보여줘' 한 것에 쓴다. "+
			"웹 사진은 읽은 페이지의 '그림 주소'나 검색 결과 등 대화에 나온 https 주소만, 아니면 이미 있는 사진 이름(uploads). "+
			"띄운 사진은 대화에 따로 남으니, 답에서 같은 사진을 다시 설명하거나 주소를 늘어놓지 않는다.", MaxPictures),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShowInput) (*mcp.CallToolResult, any, error) {
		return m.show(ctx, in), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

func (m *Module) show(ctx context.Context, in ShowInput) *mcp.CallToolResult {
	n := len(in.URLs) + len(in.Uploads)
	switch {
	case n == 0:
		return fail("보여 줄 사진이 없습니다. urls 나 uploads 를 주세요.")
	case n > MaxPictures:
		return fail(fmt.Sprintf("한 번에 %d장까지 보여 줄 수 있습니다.", MaxPictures))
	}
	var names, problems []string
	for _, name := range in.Uploads {
		name = strings.TrimSpace(name)
		if _, err := m.uploads.Path(name); err != nil {
			problems = append(problems, fmt.Sprintf("%s: 그런 사진이 없습니다", name))
			continue
		}
		names = append(names, name)
	}
	for _, raw := range in.URLs {
		name, err := m.fetch(ctx, strings.TrimSpace(raw))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", clip(raw, 120), err))
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return fail("보여 주지 못했습니다.\n- " + strings.Join(problems, "\n- "))
	}
	m.thread.AppendAgentImages(names, strings.TrimSpace(in.Caption))
	text := fmt.Sprintf("대화에 사진 %d장을 띄웠습니다. 운영자가 지금 보고 있으니 같은 사진을 다시 띄우지 마세요.", len(names))
	if len(problems) > 0 {
		text += "\n띄우지 못한 것:\n- " + strings.Join(problems, "\n- ")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// fetch brings one picture from the web into the uploads, and is its name.
func (m *Module) fetch(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("주소가 아닙니다")
	}
	if u.Scheme != "https" {
		return "", errors.New("https 주소만 엽니다")
	}
	if m.seen == nil || !m.seen(raw) {
		return "", errors.New("대화에 나온 적 없는 주소라 열지 않습니다 - 읽은 페이지의 그림 주소를 그대로 쓰세요")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	// Some image hosts refuse a request that names no browser.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,*/*;q=0.5")
	res, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("가져오지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("가져오지 못했습니다 (%d)", res.StatusCode)
	}
	if res.ContentLength > MaxBytes {
		return "", fmt.Errorf("사진이 너무 큽니다 (%dMB 까지)", MaxBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("가져오지 못했습니다: %w", err)
	}
	if len(data) > MaxBytes {
		return "", fmt.Errorf("사진이 너무 큽니다 (%dMB 까지)", MaxBytes>>20)
	}
	name, err := m.uploads.Save(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if m.log != nil {
		m.log.Info("대화에 웹 사진을 띄웁니다", "host", u.Host, "bytes", len(data), "name", name)
	}
	return name, nil
}

// publicClient fetches only from the public internet: a connection to a
// loopback, private, link-local or otherwise inside address is refused when
// it is dialled - after DNS, so a name that resolves inside is refused too,
// and every redirect is held to the same.
func publicClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			if !public(ip) {
				return errors.New("내부 주소라 열지 않습니다")
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("넘겨주기가 너무 많습니다")
			}
			if req.URL.Scheme != "https" {
				return errors.New("https 가 아닌 곳으로 넘겨줍니다")
			}
			return nil
		},
	}
}

// public is whether an address is out on the internet.
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Carrier-grade NAT (Tailscale's 100.64/10) and the benchmark range are
	// not the internet either.
	for _, p := range []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("0.0.0.0/8")} {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func fail(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
