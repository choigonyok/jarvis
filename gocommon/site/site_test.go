package site

import "testing"

func TestDomain(t *testing.T) {
	for in, want := range map[string]string{
		"coupang.com":                       "coupang.com",
		"https://login.coupang.com/login?x": "coupang.com",
		"nid.naver.com":                     "naver.com",
		"WWW.11ST.CO.KR":                    "11st.co.kr",
		"m.11st.co.kr:443":                  "11st.co.kr",
		"web.joongna.com/":                  "joongna.com",
		"the-internet.herokuapp.com/login":  "the-internet.herokuapp.com",
		"me.github.io":                      "me.github.io",
	} {
		if got, err := Domain(in); err != nil || got != want {
			t.Errorf("Domain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "localhost", "co.kr", "github.io", "127.0.0.1", "https://[::1]/", "a..b"} {
		if got, err := Domain(bad); err == nil {
			t.Errorf("Domain(%q) = %q; want an error", bad, got)
		}
	}
}

func TestWithin(t *testing.T) {
	if !Within("login.coupang.com", "coupang.com") || !Within("coupang.com", "coupang.com") {
		t.Fatal("같은 도메인과 하위 주소는 안")
	}
	for _, h := range []string{"notcoupang.com", "coupang.com.evil.com", "evil.com", ""} {
		if Within(h, "coupang.com") {
			t.Errorf("%q 는 coupang.com 이 아닙니다", h)
		}
	}
}
