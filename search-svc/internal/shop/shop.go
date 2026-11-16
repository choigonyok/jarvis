// Package shop lists the products Naver shows for a search: the shopping
// section of Naver's integrated search, as a phone gets the page.
//
// Naver offers no product search a program may call: NAVER API HUB has
// none, and Naver Shopping's own search pages (search.shopping.naver.com,
// msearch.shopping.naver.com) answer a server with 418. The integrated
// search page m.search.naver.com does answer, and its shopping section rides
// in the HTML as the state the page's scripts start from, one per view it
// shows (a list of products, the answer for one model, the plus store's
// few):
//
//	naver.search.ext.newshopping["shopping"]._INITIAL_STATE={...}
//
// Every product in those states is the same card - the one the page draws -
// so cards are found wherever they sit in a state, and a view not seen
// before still gives its products.
//
// It is not an API Naver offers, so it can change or refuse. When the state
// is not on the page, Search says so rather than answering that nothing
// matched.
package shop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
)

// SearchURL is Naver's mobile integrated search; replaceable in tests.
var SearchURL = "https://m.search.naver.com/search.naver"

// CatalogURL is where a catalog (one model, compared across sellers) opens
// in a browser.
const CatalogURL = "https://search.shopping.naver.com/catalog/"

// Product is one product as the page lists it.
type Product struct {
	ID        string `json:"id,omitempty"` // Naver's product number (nvMid)
	Title     string `json:"title"`
	Price     int    `json:"price"`               // won: the price shown, a catalog's lowest
	ListPrice int    `json:"listPrice,omitempty"` // won, before the discount, when there is one
	Mall      string `json:"mall,omitempty"`      // the seller; none for a catalog
	Malls     int    `json:"malls,omitempty"`     // a catalog's sellers
	// Catalog is Naver's price comparison: one model, its lowest price
	// across Malls sellers.
	Catalog bool `json:"catalog,omitempty"`
	// Shipping is the delivery fee in won, 0 free; absent when the page
	// does not say.
	Shipping *int    `json:"shipping,omitempty"`
	Rating   float64 `json:"rating,omitempty"`
	Reviews  int     `json:"reviews,omitempty"`
	// Ad is what the page marks 광고 (paid placements, 슈퍼적립 ones too).
	Ad bool `json:"ad,omitempty"`
	// Related is a suggestion Naver puts beside the answer for one model:
	// other products, not results for the words searched.
	Related  bool    `json:"related,omitempty"`
	Overseas bool    `json:"overseas,omitempty"`
	Category string  `json:"category,omitempty"`
	URL      string  `json:"url,omitempty"`
	Image    string  `json:"image,omitempty"`
	Offers   []Offer `json:"offers,omitempty"` // the answer's cheapest sellers
}

// Offer is one seller of a model, as the answer for that model lists it.
type Offer struct {
	Mall     string `json:"mall"`
	Option   string `json:"option,omitempty"`
	Price    int    `json:"price"`
	Shipping *int   `json:"shipping,omitempty"`
	URL      string `json:"url,omitempty"`
}

var (
	// ErrNoShopping is a search page without the shopping section: the
	// words are not a product's, or Naver changed the page.
	ErrNoShopping = errors.New("네이버 통합검색 페이지에 쇼핑 결과가 없습니다 - 상품을 찾는 검색어가 아니거나, 네이버가 페이지를 바꿔 읽지 못했습니다")
	// errNotSearch is something other than a results page (a check that
	// we are a person, an error page).
	errNotSearch = errors.New("네이버 검색 결과 페이지가 아닌 것이 왔습니다(보안 확인 화면일 수 있습니다)")
	// errNoCards is a shopping section in which no product was found: the
	// page changed shape.
	errNoCards = errors.New("네이버 통합검색의 쇼핑 결과에서 상품을 읽지 못했습니다 - 네이버가 페이지 구조를 바꾼 것 같습니다")
)

// Search is the products Naver's integrated search shows for a query: the
// answer for one model first when there is one, then the results in the
// page's order, then what the page marks 광고, then Naver's suggestions.
func Search(ctx context.Context, f *fetch.Fetcher, query string) ([]Product, error) {
	q := url.Values{"where": {"m"}, "query": {query}}
	res, err := f.Get(ctx, SearchURL+"?"+q.Encode(), fetch.Options{Mobile: true, MaxBytes: 8 << 20})
	if err != nil {
		return nil, err
	}
	if res.Status != http.StatusOK {
		return nil, fmt.Errorf("네이버 검색이 %d 로 답했습니다", res.Status)
	}
	return Parse(res.Body)
}

var stateMark = regexp.MustCompile(`naver\.search\.ext\.newshopping\["([A-Za-z0-9_-]+)"\]\._INITIAL_STATE\s*=\s*`)

// Parse reads the products out of an integrated search page.
func Parse(body []byte) ([]Product, error) {
	marks := stateMark.FindAllSubmatchIndex(body, -1)
	if len(marks) == 0 {
		if !bytes.Contains(body, []byte("네이버 검색")) {
			return nil, errNotSearch
		}
		return nil, ErrNoShopping
	}
	var c collector
	read := 0
	for _, m := range marks {
		var state map[string]json.RawMessage
		if err := json.NewDecoder(bytes.NewReader(body[m[1]:])).Decode(&state); err != nil {
			continue
		}
		read++
		var props map[string]json.RawMessage
		if json.Unmarshal(state["initProps"], &props) != nil || props == nil {
			props = state
		}
		c.view(props)
	}
	if read == 0 {
		return nil, fmt.Errorf("%w (쇼핑 결과의 데이터를 풀지 못했습니다)", errNoCards)
	}
	out := c.products()
	if len(out) == 0 {
		return nil, errNoCards
	}
	return out, nil
}

// collector gathers a page's products by where they belong in the answer.
type collector struct {
	answers, results, ads, related []Product
}

// first are a view's keys read before the rest, in this order: the answer
// for one model's own first result, then the lists.
var first = []string{"firstSasProduct", "pagedSlot", "exploreContents", "products"}

func (c *collector) view(props map[string]json.RawMessage) {
	if raw, ok := props["catalogAnswer"]; ok {
		if p, ok := answer(raw); ok {
			c.answers = append(c.answers, p)
		}
	}
	var keys []string
	for k := range props {
		if k != "catalogAnswer" {
			keys = append(keys, k)
		}
	}
	rank := func(k string) int {
		if i := slices.Index(first, k); i >= 0 {
			return i
		}
		return len(first)
	}
	sort.Slice(keys, func(a, b int) bool {
		ra, rb := rank(keys[a]), rank(keys[b])
		if ra != rb {
			return ra < rb
		}
		return keys[a] < keys[b]
	})
	for _, k := range keys {
		c.walk(props[k], suggests(k), 0)
	}
}

// suggests is whether a key holds Naver's suggestions (catalogRecommend)
// rather than results.
func suggests(key string) bool { return strings.Contains(strings.ToLower(key), "recommend") }

// walk finds the cards in a piece of state: objects with a product's name
// and number. Arrays keep their order; an object's keys go in name order.
func (c *collector) walk(raw json.RawMessage, related bool, depth int) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || depth > 12 {
		return
	}
	switch raw[0] {
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return
		}
		for _, it := range items {
			c.walk(it, related, depth+1)
		}
	case '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return
		}
		_, named := obj["productName"]
		_, numbered := obj["nvMid"]
		if named && numbered {
			c.card(raw, related)
			return
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			c.walk(obj[k], related || suggests(k), depth+1)
		}
	}
}

// cardJSON is the part of a product card read here.
type cardJSON struct {
	CardType    string          `json:"cardType"`
	SourceType  string          `json:"sourceType"`
	ID          text            `json:"nvMid"`
	Name        string          `json:"productName"`
	ProductURL  links           `json:"productUrl"`
	MallName    string          `json:"mallName"`
	MallID      string          `json:"mallId"`
	ChannelID   text            `json:"channelProductId"`
	SmartStore  bool            `json:"isSmartStoreProduct"`
	Overseas    bool            `json:"isOverseaProduct"`
	Rating      num             `json:"averageReviewScore"`
	Reviews     num             `json:"totalReviewCount"`
	SalePrice   num             `json:"salePrice"`
	Discounted  num             `json:"discountedSalePrice"`
	AdExtraInfo json.RawMessage `json:"adExtraInfo"`
	Images      []struct {
		URL string `json:"imageUrl"`
	} `json:"images"`
	Delivery *struct {
		Fee num `json:"deliveryFee"`
	} `json:"productDeliveryInfo"`
	Extra struct {
		KRW       num    `json:"discountedKRWSalePrice"`
		Currency  string `json:"priceCurrency"`
		MallCount num    `json:"mallCount"`
	} `json:"shoppingExtraInfo"`
}

type links struct {
	PC     string `json:"pcUrl"`
	Mobile string `json:"mobileUrl"`
}

func (c *collector) card(raw json.RawMessage, related bool) {
	var j cardJSON
	if json.Unmarshal(raw, &j) != nil {
		return
	}
	p := Product{
		ID:       string(j.ID),
		Title:    clean(j.Name),
		Mall:     clean(j.MallName),
		Rating:   float64(j.Rating),
		Reviews:  j.Reviews.Int(),
		Overseas: j.Overseas,
		Related:  related || strings.HasPrefix(j.SourceType, "AITEMS"),
	}
	if p.Title == "" {
		return
	}
	adInfo := len(j.AdExtraInfo) > 0 && string(j.AdExtraInfo) != "null"
	p.Ad = !p.Related && (j.SourceType == "AD" || j.SourceType == "SUPER_POINT" || j.CardType == "AD_CARD" || adInfo)
	p.Price = firstPositive(j.Extra.KRW.Int(), j.Discounted.Int(), j.SalePrice.Int())
	if (j.Extra.Currency == "" || j.Extra.Currency == "KRW") && j.SalePrice.Int() > p.Price {
		p.ListPrice = j.SalePrice.Int()
	}
	p.Catalog = j.CardType == "CATALOG_CARD" || j.MallID == "naver_model" || (p.Mall == "" && j.Extra.MallCount.Int() > 0)
	if p.Catalog {
		p.Mall, p.Malls = "", j.Extra.MallCount.Int()
		if p.ID != "" {
			p.URL = CatalogURL + p.ID
		}
	} else {
		p.URL = productURL(j.ProductURL, j.SmartStore, string(j.ChannelID))
	}
	if j.Delivery != nil {
		fee := j.Delivery.Fee.Int()
		p.Shipping = &fee
	}
	if len(j.Images) > 0 && strings.HasPrefix(j.Images[0].URL, "https://") {
		p.Image = j.Images[0].URL
	}
	switch {
	case p.Related:
		c.related = append(c.related, p)
	case p.Ad:
		c.ads = append(c.ads, p)
	default:
		c.results = append(c.results, p)
	}
}

// answerJSON is the answer for one model (viewType CATALOG): the model, its
// lowest price across sellers, and the cheapest sellers per option.
type answerJSON struct {
	ID      text `json:"nvMid"`
	Summary struct {
		Name     string `json:"modelName"`
		Lowest   num    `json:"lowestPrice"`
		Fee      *num   `json:"deliveryFee"`
		Category string `json:"categoryName"`
		Sellers  num    `json:"productCount"`
		Rating   num    `json:"reviewScore"`
		Reviews  num    `json:"reviewCount"`
		Image    string `json:"productImageUrl"`
		Overseas bool   `json:"isOverseaProduct"`
	} `json:"summaryInfo"`
	Options struct {
		List []struct {
			Name     string `json:"name"`
			Products []struct {
				Mall       string `json:"mallName"`
				Fee        *num   `json:"deliveryFee"`
				Discounted num    `json:"discountedSalePrice"`
				SalePrice  num    `json:"salePrice"`
				ChannelID  text   `json:"channelProductId"`
				SmartStore bool   `json:"isSmartStoreProduct"`
			} `json:"products"`
		} `json:"purchaseOptions"`
	} `json:"purchaseOptionInfo"`
}

// maxOffers is how many of the answer's sellers are kept.
const maxOffers = 8

func answer(raw json.RawMessage) (Product, bool) {
	var j answerJSON
	if json.Unmarshal(raw, &j) != nil || strings.TrimSpace(j.Summary.Name) == "" {
		return Product{}, false
	}
	s := j.Summary
	p := Product{
		ID:       string(j.ID),
		Title:    clean(s.Name),
		Price:    s.Lowest.Int(),
		Malls:    s.Sellers.Int(),
		Catalog:  true,
		Rating:   float64(s.Rating),
		Reviews:  s.Reviews.Int(),
		Overseas: s.Overseas,
		Category: html.UnescapeString(s.Category),
	}
	if p.ID != "" {
		p.URL = CatalogURL + p.ID
	}
	if s.Fee != nil {
		fee := s.Fee.Int()
		p.Shipping = &fee
	}
	if strings.HasPrefix(s.Image, "https://") {
		p.Image = s.Image
	}
	many := len(j.Options.List) > 1
	for _, o := range j.Options.List {
		for _, sp := range o.Products {
			mall := clean(sp.Mall)
			if len(p.Offers) >= maxOffers || mall == "" {
				continue
			}
			of := Offer{Mall: mall, Price: firstPositive(sp.Discounted.Int(), sp.SalePrice.Int())}
			if many {
				of.Option = o.Name
			}
			if sp.Fee != nil {
				fee := sp.Fee.Int()
				of.Shipping = &fee
			}
			if sp.SmartStore && sp.ChannelID != "" {
				of.URL = smartStore + string(sp.ChannelID)
			}
			p.Offers = append(p.Offers, of)
		}
	}
	return p, true
}

const smartStore = "https://smartstore.naver.com/main/products/"

// productURL is where a product opens: the shop's own address, never the
// page's ad-click counter in front of it.
func productURL(l links, smart bool, channel string) string {
	for _, u := range []string{l.PC, l.Mobile} {
		if ok(u) {
			return u
		}
	}
	if smart && channel != "" {
		return smartStore + channel
	}
	return ""
}

// ok is an https address that is not an ad-click counter.
func ok(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	switch u.Hostname() {
	case "ader.naver.com", "adcr.naver.com":
		return false
	}
	return true
}

// products is the page's products in answer order, each once: a product
// seen as a result and again as an ad or a suggestion is the result.
func (c *collector) products() []Product {
	seen := map[string]bool{}
	var out []Product
	for _, group := range [][]Product{c.answers, c.results, c.ads, c.related} {
		for _, p := range group {
			k := key(p)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

func key(p Product) string {
	switch {
	case p.Catalog && p.ID != "":
		return "catalog:" + p.ID
	case p.URL != "":
		return p.URL
	case p.ID != "":
		return "id:" + p.ID
	}
	return p.Title + "\x00" + p.Mall
}

// The orders Sort knows.
const (
	Relevance = "relevance"
	PriceAsc  = "price"
	PriceDesc = "price_desc"
	Reviews   = "reviews"
)

// Order reads a request's sort: empty is the page's own order.
func Order(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "relevance", "rel":
		return Relevance, nil
	case "price", "price_asc", "low", "asc":
		return PriceAsc, nil
	case "price_desc", "high", "desc":
		return PriceDesc, nil
	case "reviews", "review":
		return Reviews, nil
	}
	return "", fmt.Errorf("sort %q 를 모릅니다 - relevance, price, price_desc, reviews 중 하나로 주세요", s)
}

// Sort orders products in place. Naver's suggestions stay last whatever the
// order; a product without a price goes after the priced ones.
func Sort(ps []Product, by string) {
	less := func(a, b Product) bool { return false }
	switch by {
	case PriceAsc:
		less = func(a, b Product) bool { return priced(a) && (!priced(b) || a.Price < b.Price) }
	case PriceDesc:
		less = func(a, b Product) bool { return priced(a) && (!priced(b) || a.Price > b.Price) }
	case Reviews:
		less = func(a, b Product) bool { return a.Reviews > b.Reviews }
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Related != ps[j].Related {
			return !ps[i].Related
		}
		return less(ps[i], ps[j])
	})
}

func priced(p Product) bool { return p.Price > 0 }

var tags = regexp.MustCompile(`<[^>]*>`)

// clean is a product's name as a person reads it: the page's <mark> around
// the words searched and its entities gone, spaces single.
func clean(s string) string {
	s = html.UnescapeString(tags.ReplaceAllString(s, ""))
	return strings.Join(strings.Fields(s), " ")
}

func firstPositive(vs ...int) int {
	for _, v := range vs {
		if v > 0 {
			return v
		}
	}
	return 0
}

// num is a number the page writes as a number or as text ("1,234").
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	s = strings.ReplaceAll(s, ",", "")
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*n = 0
		return nil
	}
	*n = num(f)
	return nil
}

func (n num) Int() int { return int(math.Round(float64(n))) }

// text is an id the page writes as a number or as a string.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*t = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*t = text(v)
		return nil
	}
	*t = text(s)
	return nil
}
