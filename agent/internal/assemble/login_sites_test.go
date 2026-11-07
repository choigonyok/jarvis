package assemble

import (
	"encoding/json"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The login check opens no page the payment guard asks about: the page's own
// writes would stop there and raise cards in front of someone who started
// nothing. Hosts as the guard reads them - the host or one under it - and
// the patterns over the whole address.
func TestTheLoginCheckOpensNoPaymentPage(t *testing.T) {
	raw, err := os.ReadFile("../../../browser/payment-hosts.json")
	if err != nil {
		t.Fatal(err)
	}
	var guard struct {
		Ask      []string `json:"ask"`
		Patterns []string `json:"ask_patterns"`
		Window   []string `json:"window"`
	}
	if err := json.Unmarshal(raw, &guard); err != nil {
		t.Fatal(err)
	}
	if len(guard.Ask) == 0 || len(guard.Patterns) == 0 {
		t.Fatalf("가드의 표를 읽지 못했습니다: %+v", guard)
	}
	for _, s := range loginSites {
		u, err := url.Parse(s.Orders)
		if err != nil {
			t.Fatal(err)
		}
		host := strings.ToLower(u.Hostname())
		for _, h := range append(guard.Ask, guard.Window...) {
			if host == h || strings.HasSuffix(host, "."+h) {
				t.Fatalf("%s 는 결제 감시가 묻는 호스트(%s)의 페이지를 엽니다", s.ID, h)
			}
		}
		for _, p := range guard.Patterns {
			if fnmatch(p).MatchString(strings.ToLower(s.Orders)) {
				t.Fatalf("%s 는 결제 감시가 묻는 주소(%s)를 엽니다", s.ID, p)
			}
		}
	}
	// The patterns read as the guard reads them.
	if !fnmatch("*coupang.com/*payment*").MatchString("https://checkout.coupang.com/x/payment/go") || fnmatch("*coupang.com/*payment*").MatchString("https://mc.coupang.com/ssr/desktop/order/list") {
		t.Fatal("fnmatch")
	}
}

// fnmatch is the guard's pattern (Python fnmatch: * is anything, / included).
func fnmatch(p string) *regexp.Regexp {
	q := regexp.QuoteMeta(p)
	q = strings.ReplaceAll(q, `\*`, ".*")
	q = strings.ReplaceAll(q, `\?`, ".")
	return regexp.MustCompile("^" + q + "$")
}
