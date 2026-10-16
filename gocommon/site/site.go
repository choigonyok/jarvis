// Package site names the site a login belongs to.
//
// A saved login is bound to a site's registrable domain - coupang.com, not
// login.coupang.com - the way a password manager binds it: it is filled on
// that domain and its subdomains (www., login., m.) and nowhere else. One
// level below a shared suffix (co.kr, co.uk) is where anyone can register a
// name, so a domain is never cut shorter than that.
package site

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// sharedSuffixes are second-level names under which anyone registers.
var sharedSuffixes = map[string]bool{
	"co.kr": true, "or.kr": true, "go.kr": true, "ne.kr": true, "re.kr": true, "pe.kr": true, "ac.kr": true, "ms.kr": true,
	"co.jp": true, "ne.jp": true, "or.jp": true, "co.uk": true, "org.uk": true, "com.au": true, "com.cn": true, "com.tw": true,
}

// Host is the host of a URL or a bare host name, lower-cased, without port.
func Host(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			raw = u.Hostname()
		}
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		raw = h
	}
	return strings.Trim(raw, ".")
}

// Domain is the registrable domain of a host or URL: login.coupang.com →
// coupang.com, www.11st.co.kr → 11st.co.kr. An address, a single-label host
// or a bare shared suffix has none.
func Domain(raw string) (string, error) {
	h := Host(raw)
	if h == "" || net.ParseIP(h) != nil {
		return "", errors.New("사이트 주소가 아닙니다")
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if l == "" {
			return "", errors.New("사이트 주소가 아닙니다")
		}
	}
	if len(labels) < 2 {
		return "", errors.New("사이트 주소가 아닙니다")
	}
	n := 2
	if sharedSuffixes[strings.Join(labels[len(labels)-2:], ".")] {
		n = 3
	}
	if len(labels) < n {
		return "", errors.New("사이트 주소가 아닙니다")
	}
	return strings.Join(labels[len(labels)-n:], "."), nil
}

// Within is whether host is domain or one of its subdomains.
func Within(host, domain string) bool {
	h := Host(host)
	return domain != "" && (h == domain || strings.HasSuffix(h, "."+domain))
}
