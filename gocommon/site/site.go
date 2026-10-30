// Package site names the site a login belongs to.
//
// A saved login is bound to a site's registrable domain - coupang.com, not
// login.coupang.com - the way a password manager binds it: it is filled on
// that domain and its subdomains (www., login., m.) and nowhere else. A name
// directly under a public suffix (co.kr, seoul.kr, github.io) is where anyone
// can register one, so a domain is never cut shorter than that. The suffixes
// are the Public Suffix List, not a list kept here: what counts as "anyone can
// register" is the one fact every domain check rests on.
package site

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

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
	domain, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil {
		return "", errors.New("사이트 주소가 아닙니다")
	}
	return domain, nil
}

// Within is whether host is domain or one of its subdomains.
func Within(host, domain string) bool {
	h := Host(host)
	return domain != "" && (h == domain || strings.HasSuffix(h, "."+domain))
}
