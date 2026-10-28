package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func text(r *mcp.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestTheLedgerKnowsWhatWentPast(t *testing.T) {
	var l Ledger
	l.Saw(`운영자: 이거 봐 https://blog.naver.com/someone/224453338768. 그리고 [위키](https://ko.wikipedia.org/wiki/Foo_(bar))`)
	l.Saw(`{"title":"공지","url":"https:\/\/www.leagueoflegends.com\/ko-kr\/news\/notices\/?a=1\u0026b=2"}`)
	for _, u := range []string{
		"https://blog.naver.com/someone/224453338768",
		"http://BLOG.naver.com/someone/224453338768/",
		"https://ko.wikipedia.org/wiki/Foo_(bar)",
		"https://www.leagueoflegends.com/ko-kr/news/notices/?a=1&b=2",
	} {
		if !l.Has(u) {
			t.Errorf("%s should have been seen", u)
		}
	}
	for _, u := range []string{
		"https://blog.naver.com/someone/224453338768?secret=what-the-model-knows",
		"https://attacker.example/",
		"javascript:alert(1)",
	} {
		if l.Has(u) {
			t.Errorf("%s was never seen", u)
		}
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	l2 := Ledger{now: func() time.Time { return old }}
	l2.Saw("https://example.com/a")
	l2.now = time.Now
	if l2.Has("https://example.com/a") {
		t.Error("a week-old address should be forgotten")
	}
}

func fakeService(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"토큰이 맞지 않습니다"}`))
			return
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/search":
			w.Write([]byte(`{"query":"롤토체스 20판","since":"2026-10-12T22:00:00+09:00","hits":[
			  {"source":"blog","title":"20판 플레이 보상","url":"https://blog.naver.com/a/1","snippet":"보물 토큰 500개","date":"2026-10-25T00:00:00+09:00","author":"기록"},
			  {"source":"cafe","title":"카페 글","url":"https://cafe.naver.com/c/2"}],
			  "checked":[{"source":"blog","total":2772,"shown":1,"older":9},{"source":"cafe","total":97697,"shown":1},{"source":"news","error":"네이버 검색 호출 한도를 넘었습니다(429)"}]}`))
		case "/read":
			if in["url"] != "https://blog.naver.com/a/1" {
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte(`{"error":"unexpected url"}`))
				return
			}
			w.Write([]byte(`{"finalUrl":"https://m.blog.naver.com/a/1","title":"20판 보상","published":"2026-10-25T20:22:00+09:00",
			  "text":"본문\n\n[그림 1 · 그림 속 글자]\n20판 플레이 | 무작위 신화급 전설이\n\n[그림 1 끝]","chars":3000,"offset":0,"next":2500,
			  "pictures":[{"n":1,"url":"https://img.example/a.png","width":479,"height":444,"pieces":1,"read":true,"chars":20}],
			  "links":[{"text":"원문 기사","url":"https://news.example.com/1","inBody":true}],
			  "looks":[{"picture":1,"piece":0,"jpeg":"/9j/AA=="}]}`))
		}
	}))
}

func TestSearchThenReadWhatWasFound(t *testing.T) {
	srv := fakeService(t)
	defer srv.Close()
	m := New(NewClient(srv.URL, "tok"))

	res := m.search(context.Background(), SearchInput{Query: "롤토체스 20판", Since: "2w"})
	if res.IsError {
		t.Fatal(text(res))
	}
	out := text(res)
	for _, want := range []string{"[1] 블로그 · 10/25 · 기록", "20판 플레이 보상", "https://blog.naver.com/a/1", "블로그 2,772건 중 1 (기간 밖 9)", "뉴스 실패", "날짜를 알려 주지 않아"} {
		if !strings.Contains(out, want) {
			t.Errorf("search output lacks %q:\n%s", want, out)
		}
	}

	if r := m.read(context.Background(), ReadInput{URL: "https://news.example.com/1"}); !r.IsError || !strings.Contains(text(r), "나온 적이 없어") {
		t.Errorf("an unseen address was read: %s", text(r))
	}
	r := m.read(context.Background(), ReadInput{URL: "https://blog.naver.com/a/1", Look: true})
	if r.IsError {
		t.Fatal(text(r))
	}
	page := text(r)
	for _, want := range []string{"20판 보상 · 2026-10-25 20:22 · https://m.blog.naver.com/a/1", "이어 읽기: offset 2500", "그림 1장 중 1장을 읽었고 1장에 글자가", "무작위 신화급 전설이", "- 원문 기사 https://news.example.com/1", "그림 1 · 조각 1", "- 그림 1 https://img.example/a.png"} {
		if !strings.Contains(page, want) {
			t.Errorf("read output lacks %q:\n%s", want, page)
		}
	}
	var images int
	for _, c := range r.Content {
		if img, ok := c.(*mcp.ImageContent); ok && img.MIMEType == "image/jpeg" && len(img.Data) > 0 {
			images++
		}
	}
	if images != 1 {
		t.Errorf("images = %d", images)
	}
	// A link of the page read may be read next.
	if !m.seen.Has("https://news.example.com/1") || !m.seen.Has("https://m.blog.naver.com/a/1") {
		t.Error("the page's links and final address should be readable now")
	}
	// And its pictures may be shown (module show asks Seen).
	if !m.Seen("https://img.example/a.png") || m.Seen("https://img.example/b.png") {
		t.Error("the page's pictures, and only they, should be showable now")
	}
}

func TestAnErrorFromTheServiceReachesTheModel(t *testing.T) {
	srv := fakeService(t)
	defer srv.Close()
	m := New(NewClient(srv.URL, "wrong"))
	if r := m.search(context.Background(), SearchInput{Query: "x"}); !r.IsError || !strings.Contains(text(r), "토큰이 맞지 않습니다") {
		t.Errorf("got %s", text(r))
	}
}
