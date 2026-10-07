package market

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Prices is what Joongna's own search page says about a query: its summary
// (average, highest, lowest of what is listed) and the first listings.
//
// Read from the server-rendered search page rather than through the browser:
// it needs no login, and a lookup the model makes while drafting must not
// raise a card or take the shared browser from a background task.
type Prices struct {
	Query   string            `json:"query"`
	Summary map[string]string `json:"summary"`
	Items   []PriceItem       `json:"items"`
}

type PriceItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Price string `json:"price"`
	Age   string `json:"age,omitempty"`
}

var (
	summaryRe = regexp.MustCompile(`<dt[^>]*>([^<]*)</dt>\s*<dd[^>]*>([^<]*)</dd>`)
	itemRe    = regexp.MustCompile(`(?s)<a[^>]*href="/product/(\d+)"[^>]*>(.*?)</a>`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
	priceRe   = regexp.MustCompile(`^[\d,]+$`)
)

var searchClient = &http.Client{Timeout: 15 * time.Second}

// SearchPrices fetches and parses one search.
func SearchPrices(ctx context.Context, query string, limit int) (Prices, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Prices{}, fmt.Errorf("검색어가 비어 있습니다")
	}
	u := "https://web.joongna.com/search/" + url.PathEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Prices{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept-Language", "ko-KR,ko;q=0.9")
	res, err := searchClient.Do(req)
	if err != nil {
		return Prices{}, fmt.Errorf("중고나라 검색에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Prices{}, fmt.Errorf("중고나라 검색이 %s 를 돌려주었습니다", res.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return Prices{}, err
	}
	p := parsePrices(string(raw), limit)
	p.Query = query
	return p, nil
}

func parsePrices(page string, limit int) Prices {
	p := Prices{Summary: map[string]string{}, Items: []PriceItem{}}
	for _, m := range summaryRe.FindAllStringSubmatch(page, -1) {
		k, v := strings.TrimSpace(html.UnescapeString(m[1])), strings.TrimSpace(html.UnescapeString(m[2]))
		if strings.Contains(k, "가격") && v != "" {
			p.Summary[k] = v
		}
	}
	seen := map[string]bool{}
	for _, m := range itemRe.FindAllStringSubmatch(page, -1) {
		if len(p.Items) >= limit {
			break
		}
		if seen[m[1]] {
			continue
		}
		var parts []string
		for _, s := range strings.Split(tagRe.ReplaceAllString(m[2], "\x00"), "\x00") {
			if s = strings.TrimSpace(html.UnescapeString(s)); s != "" {
				parts = append(parts, s)
			}
		}
		item := PriceItem{ID: m[1]}
		for i, s := range parts {
			switch {
			case item.Title == "" && !priceRe.MatchString(s):
				item.Title = s
			case item.Price == "" && priceRe.MatchString(s):
				item.Price = s + "원"
				if i+2 < len(parts) && parts[i+1] == "원" {
					item.Age = parts[i+2]
				}
			}
		}
		if item.Title == "" || item.Price == "" {
			continue
		}
		seen[m[1]] = true
		p.Items = append(p.Items, item)
	}
	return p
}

func (p Prices) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "중고나라 '%s' 검색 (판매 중인 글 기준)\n", p.Query)
	if len(p.Summary) == 0 && len(p.Items) == 0 {
		b.WriteString("결과가 없습니다. 검색어를 더 짧게, 모델명 위주로 바꿔 보세요.\n")
		return b.String()
	}
	for _, k := range []string{"평균 가격", "가장 낮은 가격", "가장 높은 가격"} {
		if v, ok := p.Summary[k]; ok {
			fmt.Fprintf(&b, "- %s: %s\n", k, v)
		}
	}
	if len(p.Items) > 0 {
		b.WriteString("\n최근 글 (부품·액세서리가 섞여 있을 수 있으니 같은 물건만 골라 보세요)\n")
		for _, it := range p.Items {
			fmt.Fprintf(&b, "- %s · %s", it.Title, it.Price)
			if it.Age != "" {
				fmt.Fprintf(&b, " · %s", it.Age)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
