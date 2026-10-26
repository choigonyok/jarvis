// Package naver calls Naver's search through NAVER API HUB (NCP): the same
// search the Naver site gives a person - blogs, news, cafe posts, 지식iN,
// web documents, the encyclopedia and local businesses - as JSON.
//
// It replaced developers.naver.com's open API on 2026-06-25: another host
// (naverapihub.apigw.ntruss.com) and other headers (X-NCP-APIGW-API-KEY-ID
// and -KEY, an HUB application's Client ID and Secret). Shopping, books and
// specialist documents were not carried over. One key, about 775,000 calls a
// month across the search categories, 50 a second.
package naver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBase is the HUB's API host.
const DefaultBase = "https://naverapihub.apigw.ntruss.com"

// Kind is one search category, as the HUB names its path.
type Kind string

const (
	Blog  Kind = "blog"
	News  Kind = "news"
	Cafe  Kind = "cafearticle"
	Kin   Kind = "kin"
	Web   Kind = "webkr"
	Encyc Kind = "encyc"
	Local Kind = "local"
)

// Sorted is whether a category takes sort=date (webkr and encyc rank only by
// match; local sorts by its own two orders).
func (k Kind) Sorted() bool {
	switch k {
	case Blog, News, Cafe, Kin:
		return true
	}
	return false
}

// MaxDisplay is the most one call returns: local gives five, the rest a
// hundred.
func (k Kind) MaxDisplay() int {
	if k == Local {
		return 5
	}
	return 100
}

// Item is one result, whatever its category: fields a category does not
// have are left empty.
type Item struct {
	Title       string
	Link        string
	Description string
	// Date is when it was written: a blog post's day, a news article's
	// time. Zero when the category does not say (cafe, 지식iN, web).
	Date time.Time
	// Author is the blog's or the cafe's name; AuthorLink its address.
	Author     string
	AuthorLink string
	// Local businesses.
	Category    string
	Address     string
	RoadAddress string
	Lat, Lng    float64
}

// Result is one call's answer.
type Result struct {
	Total int
	Items []Item
}

// Client is the HUB with one application's key.
type Client struct {
	ID, Secret string
	Base       string
	HTTP       *http.Client
}

// Configured is whether there is a key to call with.
func (c *Client) Configured() bool { return c != nil && c.ID != "" && c.Secret != "" }

// ErrNoKey is a search asked of a client without a key.
var ErrNoKey = errors.New("네이버 검색 키(NAVER_API_HUB_ID·SECRET)가 없습니다")

// Search asks one category. display and start are clamped to what the
// category takes; sort is "date" or "" (relevance; for local, "comment"
// orders by review count).
func (c *Client) Search(ctx context.Context, kind Kind, query string, display, start int, sort string) (Result, error) {
	if !c.Configured() {
		return Result{}, ErrNoKey
	}
	q := url.Values{"query": {query}}
	if display <= 0 {
		display = 10
	}
	q.Set("display", strconv.Itoa(min(display, kind.MaxDisplay())))
	if start > 1 && kind != Local {
		q.Set("start", strconv.Itoa(min(start, 1000)))
	}
	switch {
	case sort == "date" && kind.Sorted():
		q.Set("sort", "date")
	case sort == "comment" && kind == Local:
		q.Set("sort", "comment")
	}
	base := c.Base
	if base == "" {
		base = DefaultBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/search/v1/"+string(kind)+"?"+q.Encode(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("X-NCP-APIGW-API-KEY-ID", c.ID)
	req.Header.Set("X-NCP-APIGW-API-KEY", c.Secret)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("네이버 검색에 연결하지 못했습니다: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, statusError(resp.StatusCode, body)
	}
	var raw struct {
		Total int `json:"total"`
		Items []struct {
			Title        string `json:"title"`
			Link         string `json:"link"`
			OriginalLink string `json:"originallink"`
			Description  string `json:"description"`
			PostDate     string `json:"postdate"`
			PubDate      string `json:"pubDate"`
			BloggerName  string `json:"bloggername"`
			BloggerLink  string `json:"bloggerlink"`
			CafeName     string `json:"cafename"`
			CafeURL      string `json:"cafeurl"`
			Category     string `json:"category"`
			Address      string `json:"address"`
			RoadAddress  string `json:"roadAddress"`
			MapX         string `json:"mapx"`
			MapY         string `json:"mapy"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Result{}, fmt.Errorf("네이버 검색의 답을 읽지 못했습니다: %w", err)
	}
	out := Result{Total: raw.Total}
	for _, r := range raw.Items {
		it := Item{
			Title:       Clean(r.Title),
			Link:        r.Link,
			Description: Clean(r.Description),
			Author:      Clean(firstOf(r.BloggerName, r.CafeName)),
			AuthorLink:  firstOf(r.BloggerLink, r.CafeURL),
			Category:    r.Category,
			Address:     r.Address,
			RoadAddress: r.RoadAddress,
		}
		// A news item's link is Naver's copy when it has one; the paper's own
		// page is the one to read when it does not.
		if kind == News && it.Link == "" {
			it.Link = r.OriginalLink
		}
		it.Date = parseDate(r.PostDate, r.PubDate)
		it.Lng, it.Lat = coord(r.MapX), coord(r.MapY)
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// statusError says what a failed call means, in the words the operator
// would act on.
func statusError(status int, body []byte) error {
	var e struct {
		ErrorCode    string `json:"errorCode"`
		ErrorMessage string `json:"errorMessage"`
		Error        struct {
			ErrorCode string `json:"errorCode"`
			Message   string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	detail := strings.TrimSpace(firstOf(e.ErrorMessage, e.Error.Message))
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("네이버 검색 키가 맞지 않거나 이 API 를 신청하지 않았습니다(401 %s)", detail)
	case http.StatusTooManyRequests:
		return errors.New("네이버 검색 호출 한도를 넘었습니다(429)")
	}
	if detail == "" {
		detail = strings.TrimSpace(string(body))
		if len(detail) > 200 {
			detail = detail[:200]
		}
	}
	return fmt.Errorf("네이버 검색이 %d 로 답했습니다: %s", status, detail)
}

var tags = regexp.MustCompile(`<[^>]*>`)

// Clean is a title or snippet as text: the <b> marks around matched words
// gone and the entities decoded.
func Clean(s string) string {
	s = html.UnescapeString(tags.ReplaceAllString(s, ""))
	return strings.Join(strings.Fields(s), " ")
}

// seoul is where a blog's date (a day, no zone) is a day.
var seoul = time.FixedZone("KST", 9*60*60)

func parseDate(postDate, pubDate string) time.Time {
	if postDate != "" {
		if t, err := time.ParseInLocation("20060102", postDate, seoul); err == nil {
			return t
		}
	}
	if pubDate != "" {
		if t, err := time.Parse(time.RFC1123Z, pubDate); err == nil {
			return t
		}
	}
	return time.Time{}
}

// coord reads mapx/mapy: WGS84 degrees, given as integers scaled by 10^7
// ("1270276743" is 127.0276743). A value with a point is taken as degrees.
func coord(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	if !strings.Contains(s, ".") {
		f /= 1e7
	}
	return f
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
