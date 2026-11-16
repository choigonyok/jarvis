package shop

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
)

// The fixtures keep the shape of m.search.naver.com's page - the states its
// shopping section starts from, a list view with the plus store's after it
// (testdata/list.html) and the answer for one model (testdata/catalog.html) -
// with a few made-up products.
func page(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSearchAsksThePhonePage(t *testing.T) {
	var asked *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r
		if !strings.Contains(r.Header.Get("User-Agent"), "iPhone") {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.Write(page(t, "list.html"))
	}))
	defer srv.Close()
	old := SearchURL
	SearchURL = srv.URL + "/search.naver"
	defer func() { SearchURL = old }()

	ps, err := Search(context.Background(), fetch.NewAllowingLocal(), "캠핑 의자")
	if err != nil {
		t.Fatal(err)
	}
	if q := asked.URL.Query(); q.Get("query") != "캠핑 의자" || q.Get("where") != "m" {
		t.Errorf("asked %s", asked.URL.RawQuery)
	}
	if !strings.HasPrefix(asked.Header.Get("Accept-Language"), "ko-KR") {
		t.Errorf("Accept-Language %q", asked.Header.Get("Accept-Language"))
	}
	if len(ps) != 7 {
		t.Fatalf("%d products: %+v", len(ps), ps)
	}
}

func TestTheListReadsAsThePageShowsIt(t *testing.T) {
	ps, err := Parse(page(t, "list.html"))
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, p := range ps {
		titles = append(titles, p.Title)
	}
	// Results in the page's order, the plus store's after; then the ads -
	// the ad that is also a result is the result, once.
	want := []string{
		"다라 경량 캠핑 의자 1+1 & 수납 가방", "마바 몬테라 체어 S", "차카 해외 캠핑 의자", "타파 하이백 체어",
		"가나캠핑 릴렉스 롱 체어", "사아 감성 로우체어 L", "자차 폴딩 체어 2개 세트",
	}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("titles\n got %q\nwant %q", titles, want)
	}

	p := ps[0]
	if p.Price != 19900 || p.ListPrice != 25900 || p.Mall != "다라아웃도어" || p.Shipping == nil || *p.Shipping != 3500 ||
		p.Rating != 4.68 || p.Reviews != 6979 || p.Ad || p.Catalog || p.ID != "90000000002" ||
		p.URL != "https://smartstore.naver.com/main/products/10000000002" ||
		p.Image != "https://shopping-phinf.pstatic.net/main_9000000/90000000002.jpg" {
		t.Errorf("organic = %+v", p)
	}
	c := ps[1]
	if !c.Catalog || c.Mall != "" || c.Malls != 86 || c.Price != 69000 || c.URL != CatalogURL+"50000000003" || *c.Shipping != 3000 {
		t.Errorf("catalog = %+v", c)
	}
	// The ad-click address is not the product's; a smart store product is
	// opened by its own number. Numbers written as text still read.
	o := ps[2]
	if o.URL != "https://smartstore.naver.com/main/products/10000000007" || !o.Overseas || o.Price != 0 ||
		o.Shipping != nil || o.Rating != 4.5 || o.Reviews != 1024 {
		t.Errorf("overseas = %+v", o)
	}
	for _, ad := range ps[4:] {
		if !ad.Ad {
			t.Errorf("not marked an ad: %+v", ad)
		}
	}
	if ps[6].URL == "" || !strings.HasPrefix(ps[6].URL, "https://link.coupang.com/") {
		t.Errorf("coupang ad url = %q", ps[6].URL)
	}
}

func TestTheAnswerForOneModelComesFirstAndSuggestionsLast(t *testing.T) {
	ps, err := Parse(page(t, "catalog.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 {
		t.Fatalf("%d products: %+v", len(ps), ps)
	}
	a := ps[0]
	if !a.Catalog || a.Title != "가나 무선 이어폰 프로 2세대 USB-C" || a.Price != 304000 || a.Malls != 72 ||
		a.Category != "무선이어폰/이어셋" || a.URL != CatalogURL+"40000000001" || a.Shipping == nil || *a.Shipping != 0 {
		t.Errorf("answer = %+v", a)
	}
	if len(a.Offers) != 2 || a.Offers[0].Mall != "가나스토어" || a.Offers[0].URL != "https://smartstore.naver.com/main/products/13000000001" ||
		a.Offers[1].Mall != "다라 전자" || a.Offers[1].Price != 315000 || *a.Offers[1].Shipping != 2500 || a.Offers[1].URL != "" || a.Offers[0].Option != "" {
		t.Errorf("offers = %+v", a.Offers)
	}
	if ps[1].Title != "가나 무선 이어폰 프로 2세대 USB-C 화이트" || ps[1].Related {
		t.Errorf("first result = %+v", ps[1])
	}
	if r := ps[2]; !r.Related || r.Ad || r.Price != 17000 {
		t.Errorf("suggestion = %+v", r)
	}

	// Cheapest first, but a suggestion is not a result: it stays last.
	Sort(ps, PriceAsc)
	if !ps[2].Related {
		t.Errorf("price order put a suggestion among results: %+v", ps)
	}
}

func TestSort(t *testing.T) {
	ps := []Product{
		{Title: "a", Price: 300, Reviews: 1},
		{Title: "unpriced", Reviews: 50},
		{Title: "b", Price: 100, Reviews: 20},
		{Title: "related", Price: 10, Reviews: 999, Related: true},
		{Title: "c", Price: 200, Reviews: 5},
	}
	order := func(by string) string {
		cp := append([]Product(nil), ps...)
		Sort(cp, by)
		var out []string
		for _, p := range cp {
			out = append(out, p.Title)
		}
		return strings.Join(out, ",")
	}
	for by, want := range map[string]string{
		Relevance: "a,unpriced,b,c,related",
		PriceAsc:  "b,c,a,unpriced,related",
		PriceDesc: "a,c,b,unpriced,related",
		Reviews:   "unpriced,b,c,a,related",
	} {
		if got := order(by); got != want {
			t.Errorf("%s: %s, want %s", by, got, want)
		}
	}
	for in, want := range map[string]string{"": Relevance, "PRICE": PriceAsc, "price_asc": PriceAsc, "price_desc": PriceDesc, "review": Reviews} {
		if got, err := Order(in); err != nil || got != want {
			t.Errorf("Order(%q) = %q %v", in, got, err)
		}
	}
	if _, err := Order("cheapest"); err == nil {
		t.Error("want an error for an unknown sort")
	}
}

// A page that is not what was expected says so - never an empty list, as if
// nothing matched.
func TestAPageWithoutProductsSaysSo(t *testing.T) {
	if _, err := Parse([]byte(`<html><title>서울 날씨 : 네이버 검색</title><body>맑음</body></html>`)); !errors.Is(err, ErrNoShopping) {
		t.Errorf("no shopping section: %v", err)
	}
	if _, err := Parse([]byte(`<html><body>잠시 후 다시 시도해 주세요</body></html>`)); err == nil || !strings.Contains(err.Error(), "검색 결과 페이지가 아닌") {
		t.Errorf("not a results page: %v", err)
	}
	changed := `<title>x : 네이버 검색</title><script>naver.search.ext.newshopping["shopping"]._INITIAL_STATE={"initProps":{"items":[{"name":"카드 모양이 바뀜"}]}}</script>`
	if _, err := Parse([]byte(changed)); err == nil || !strings.Contains(err.Error(), "구조") {
		t.Errorf("changed cards: %v", err)
	}
	broken := `<title>x : 네이버 검색</title><script>naver.search.ext.newshopping["shopping"]._INITIAL_STATE={"initProps":</script>`
	if _, err := Parse([]byte(broken)); err == nil || !strings.Contains(err.Error(), "풀지 못했습니다") {
		t.Errorf("broken state: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	defer srv.Close()
	old := SearchURL
	SearchURL = srv.URL
	defer func() { SearchURL = old }()
	if _, err := Search(context.Background(), fetch.NewAllowingLocal(), "x"); err == nil || !strings.Contains(err.Error(), "418") {
		t.Errorf("418: %v", err)
	}
}
