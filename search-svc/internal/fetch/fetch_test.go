package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBlockedAddresses(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "10.1.2.3", "172.18.0.5", "192.168.65.254", "169.254.169.254", "100.101.102.103",
		"0.0.0.0", "::1", "fd00::1", "fe80::1", "::ffff:10.0.0.1", "224.0.0.1",
	} {
		if !Blocked(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"223.130.200.107", "8.8.8.8", "2001:4860:4860::8888"} {
		if Blocked(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestAllowedRefusesWhatIsNotAWebAddress(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd", "ftp://example.com/", "http://user:pw@example.com/", "http://agent:8080/mcp",
		"http://localhost/", "http://10.0.0.1/", "http://[::1]:8080/", "http://printer.local/", "http://host.docker.internal:8107/",
	} {
		u, _ := url.Parse(raw)
		if Allowed(u) == nil {
			t.Errorf("%s should be refused", raw)
		}
	}
	u, _ := url.Parse("https://m.blog.naver.com/someone/1")
	if err := Allowed(u); err != nil {
		t.Errorf("a public page was refused: %v", err)
	}
}

func TestTheRealFetcherDialsNoLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer srv.Close()
	if _, err := New().Get(context.Background(), srv.URL, Options{}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestARedirectToAPrivateAddressIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/admin", http.StatusFound)
	}))
	defer srv.Close()
	_, err := NewAllowingLocal().Get(context.Background(), srv.URL, Options{})
	if err == nil || !strings.Contains(err.Error(), "공개 인터넷") {
		t.Fatalf("err = %v", err)
	}
}

func TestBodyIsCutOrRefusedPastTheLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://example.com/post" || !strings.Contains(r.Header.Get("User-Agent"), "iPhone") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(strings.Repeat("가", 100)))
	}))
	defer srv.Close()
	f := NewAllowingLocal()
	res, err := f.Get(context.Background(), srv.URL, Options{MaxBytes: 30, Referer: "https://example.com/post", Mobile: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || !res.Truncated || len(res.Body) != 30 {
		t.Errorf("status %d truncated %v len %d", res.Status, res.Truncated, len(res.Body))
	}
	if _, err := f.Get(context.Background(), srv.URL, Options{MaxBytes: 30, Strict: true, Referer: "https://example.com/post", Mobile: true}); err == nil {
		t.Error("a strict fetch past the limit should fail")
	}
}
