package browsercheck

import (
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
)

// Where the order page landed decides: its own page, the login page, or not
// looked at this time - and a login page someone may be typing in is left open.
func TestWhereTheOrdersLanded(t *testing.T) {
	s := Site{ID: "coupang", Name: "쿠팡 주문 내역", LoginHost: "login.coupang.com"}
	at := time.Now()
	for _, c := range []struct {
		r    Result
		ok   bool
		l    Lookups
		want string
	}{
		{ok: false, want: checks.Unknown},
		{r: Result{URL: "https://mc.coupang.com/ssr/desktop/order/list", At: at}, ok: true, want: checks.OK},
		{r: Result{URL: "https://login.coupang.com/login", At: at}, ok: true, l: Lookups{LoginRequired: 2}, want: checks.Fail},
		{r: Result{URL: "https://login.coupang.com/login", Busy: true, At: at}, ok: true, want: checks.Fail},
		{r: Result{URL: "https://nid.naver.com/login", Busy: true, At: at}, ok: true, want: checks.Unknown},
		{r: Result{Error: "브라우저가 없습니다", At: at}, ok: true, want: checks.Unknown},
		{r: Result{URL: "https://mc.coupang.com/ssr/desktop/order/list", At: at}, ok: true, l: Lookups{Stale: 1}, want: checks.Warn},
	} {
		if v := opened(s, c.r, c.ok, c.l, nil); v.State != c.want || v.Summary == "" || v.ID != "enrich.coupang" {
			t.Fatalf("%+v: %+v", c, v)
		}
	}
}

// A site never opened for a look is judged by what its lookups ran into.
func TestASiteNotOpened(t *testing.T) {
	q := Quiet{ID: "naverpay", Name: "네이버페이 주문 내역"}
	done := time.Now()
	for _, c := range []struct {
		l    Lookups
		want string
	}{
		{Lookups{}, checks.Unknown},
		{Lookups{LoginRequired: 1}, checks.Fail},
		{Lookups{Stale: 2}, checks.Warn},
		{Lookups{LastDone: &done}, checks.OK},
	} {
		if v := unopened(q, c.l, nil); v.State != c.want {
			t.Fatalf("%+v: %+v", c.l, v)
		}
	}
}
