// Package fetch gets a page or a picture from the web - and from nowhere
// else.
//
// What it is asked to open comes from a model that has read other pages, so
// an address can be anyone's choice: http://agent:8080/..., the Docker host,
// a router on the operator's wifi. The check is on the address actually
// dialled, after DNS and after every redirect, so a name that resolves to a
// private address (or one that changes its answer between a check and the
// dial) is refused the same way a literal one is.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is an address that is not on the public internet.
var ErrBlocked = errors.New("공개 인터넷 주소가 아니라서 열지 않습니다")

const (
	desktopUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	// Naver's mobile pages carry their content in the HTML; the desktop ones
	// often load it after.
	mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
)

// Options shape one request.
type Options struct {
	Referer string
	Accept  string
	Mobile  bool
	// MaxBytes is how much of the body is read; a page longer than that is
	// cut (Truncated), and Strict makes it an error instead (a picture cut
	// in half is no picture).
	MaxBytes int64
	Strict   bool
}

// Response is what came back from the last hop.
type Response struct {
	URL         *url.URL
	Status      int
	ContentType string
	Body        []byte
	Truncated   bool
}

// Fetcher is an HTTP client that dials public addresses only.
type Fetcher struct {
	client *http.Client
	local  bool
}

// New is the fetcher for the open web.
func New() *Fetcher { return newFetcher(false) }

// NewAllowingLocal also dials loopback - for tests, whose servers listen
// there. Never for the service itself.
func NewAllowingLocal() *Fetcher { return newFetcher(true) }

func newFetcher(local bool) *Fetcher {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return ErrBlocked
			}
			if local && ip.IsLoopback() {
				return nil
			}
			if Blocked(ip) {
				return ErrBlocked
			}
			return nil
		},
	}
	transport := &http.Transport{
		// No proxy from the environment: the dial above is the only gate.
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	f := &Fetcher{local: local}
	f.client = &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("주소가 다섯 번 넘게 넘겨져서 멈췄습니다")
			}
			return f.Check(req.URL)
		},
	}
	return f
}

// Check is Allowed, with the test fetcher's loopback let through.
func (f *Fetcher) Check(u *url.URL) error {
	if f.local && u != nil && (u.Scheme == "http" || u.Scheme == "https") {
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return Allowed(u)
}

// Allowed is the check on an address before any dial: a web address, with
// no credentials in it, whose host is not a private address written out.
// (A name is checked when it is dialled.)
func Allowed(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("http·https 주소만 엽니다")
	}
	if u.User != nil {
		return errors.New("아이디·비밀번호가 든 주소는 열지 않습니다")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("주소에 호스트가 없습니다")
	}
	if ip := net.ParseIP(host); ip != nil && Blocked(ip) {
		return ErrBlocked
	}
	if !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return ErrBlocked
	}
	return nil
}

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",     // this network
		"100.64.0.0/10", // carrier NAT - and Tailscale's addresses
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
		"240.0.0.0/4",   // reserved
		"64:ff9b::/96",  // NAT64: a v4 address in v6 clothes
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// Blocked is an address that is not somewhere on the public internet.
func Blocked(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Get fetches one address.
func (f *Fetcher) Get(ctx context.Context, raw string, o Options) (*Response, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("주소를 읽지 못했습니다: %w", err)
	}
	if err := f.Check(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	ua := desktopUA
	if o.Mobile {
		ua = mobileUA
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "ko-KR,ko;q=0.9,en-US;q=0.6,en;q=0.5")
	accept := o.Accept
	if accept == "" {
		accept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	}
	req.Header.Set("Accept", accept)
	if o.Referer != "" {
		req.Header.Set("Referer", o.Referer)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		return nil, fmt.Errorf("%s 를 열지 못했습니다: %w", u.Host, unwrapURL(err))
	}
	defer resp.Body.Close()
	limit := o.MaxBytes
	if limit <= 0 {
		limit = 6 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s 의 내용을 다 받지 못했습니다: %w", u.Host, err)
	}
	out := &Response{URL: resp.Request.URL, Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: body}
	if int64(len(body)) > limit {
		if o.Strict {
			return nil, fmt.Errorf("%s 의 내용이 너무 큽니다(%d MB 넘음)", u.Host, limit>>20)
		}
		out.Body, out.Truncated = body[:limit], true
	}
	return out, nil
}

// unwrapURL drops net/http's "Get \"…\":" prefix: the host is said already.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
