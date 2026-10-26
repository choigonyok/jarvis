package page

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/korean"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSourceReadsANaverBlogPostFromItsMobilePage(t *testing.T) {
	for in, want := range map[string]string{
		"https://blog.naver.com/someone/224453338768":                             "https://m.blog.naver.com/someone/224453338768",
		"https://blog.naver.com/PostView.naver?blogId=someone&logNo=224453338768": "https://m.blog.naver.com/someone/224453338768",
		"https://m.blog.naver.com/someone/224453338768":                           "https://m.blog.naver.com/someone/224453338768",
	} {
		got, mobile := Source(mustURL(t, in))
		if got.String() != want || !mobile {
			t.Errorf("%s → %s (mobile %v), want %s", in, got, mobile, want)
		}
	}
	if got, mobile := Source(mustURL(t, "https://www.example.com/a")); got.String() != "https://www.example.com/a" || mobile {
		t.Errorf("an ordinary page changed: %s %v", got, mobile)
	}
}

const blogPost = `<html><head>
<meta property="og:title" content="무료 보상 20판 정리">
<title>네이버 블로그</title></head><body>
<header><a href="/menu">메뉴</a></header>
<div class="blog_author"><a href="/PostList.naver?blogId=someone">작성자</a></div>
<p class="blog_date"><span class="txt">2026. 10. 25. 20:22</span></p>
<div class="se-main-container">
  <div class="se-component se-text"><p>라이엇 게임즈가 <b>무료 보상</b>을 발표했습니다.</p><p>시작은 19.1 패치부터입니다.</p></div>
  <div class="se-module-image"><img src="https://mblogthumb-phinf.pstatic.net/a.png?type=w80_blur"
       data-lazy-src="https://mblogthumb-phinf.pstatic.net/a.png?type=w800" data-width="479" data-height="444" alt=""></div>
  <img src="https://storep-phinf.pstatic.net/sticker/1.png" width="185" height="160">
  <img src="/icon.png" width="16" height="16">
  <p>보상 표는 위 그림과 같습니다. <a href="https://www.example.com/news/1">원문 기사</a></p>
</div>
<div class="related"><a href="/someone/224453272454">다음 글: 시작일 정리</a></div>
<footer><a href="/terms">약관</a></footer>
</body></html>`

func TestANaverBlogPostIsReadByItsRule(t *testing.T) {
	d, err := Parse([]byte(blogPost), "text/html; charset=utf-8", mustURL(t, "https://m.blog.naver.com/someone/224453338768"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "무료 보상 20판 정리" {
		t.Errorf("title = %q", d.Title)
	}
	if want := time.Date(2026, 10, 25, 20, 22, 0, 0, seoul); !d.Published.Equal(want) {
		t.Errorf("published = %v", d.Published)
	}
	var texts []string
	var images []*Image
	for _, b := range d.Blocks {
		if b.Image != nil {
			images = append(images, b.Image)
		} else {
			texts = append(texts, b.Text)
		}
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "라이엇 게임즈가 무료 보상을 발표했습니다.") || strings.Contains(joined, "작성자") || strings.Contains(joined, "메뉴") {
		t.Errorf("text = %q", joined)
	}
	if len(images) != 1 || images[0].URL != "https://mblogthumb-phinf.pstatic.net/a.png?type=w800" || images[0].Width != 479 {
		t.Fatalf("images = %+v (the sticker and the icon should be dropped, the lazy source taken)", images)
	}
	// The picture stands between the second paragraph and the third.
	order := ""
	for _, b := range d.Blocks {
		if b.Image != nil {
			order += "I"
		} else {
			order += "T"
		}
	}
	if order != "TTIT" {
		t.Errorf("block order = %s, want TTIT", order)
	}
	var inBody, around []string
	for _, l := range d.Links {
		if l.InBody {
			inBody = append(inBody, l.URL)
		} else {
			around = append(around, l.URL)
		}
	}
	if len(inBody) != 1 || inBody[0] != "https://www.example.com/news/1" {
		t.Errorf("links in the body = %v", inBody)
	}
	if !contains(around, "https://m.blog.naver.com/someone/224453272454") || contains(around, "https://m.blog.naver.com/menu") || contains(around, "https://m.blog.naver.com/terms") {
		t.Errorf("links around = %v (the next post yes, header and footer no)", around)
	}
}

func TestAnArticleAnywhereIsFoundByReadability(t *testing.T) {
	// Long enough to be an article (minArticle): a short one is read whole.
	para := strings.Repeat("전략적 팀 전투의 새 미션은 게임을 한 판 할 때마다 진행도가 쌓이는 방식이다. ", 10)
	page := `<html><head><title>새 미션 안내 | 게임 뉴스</title>
<meta property="article:published_time" content="2026-10-24T13:20:00+09:00"></head><body>
<nav><a href="/">홈</a><a href="/news">뉴스</a></nav>
<div class="sidebar"><a href="/ad">광고</a></div>
<article><h1>새 미션 안내</h1><p>` + para + `</p><p>` + para + `</p><p>` + para + `</p>
<figure><img src="https://cdn.example.com/reward-table.jpg" alt="보상 표"></figure></article>
</body></html>`
	d, err := Parse([]byte(page), "text/html", mustURL(t, "https://news.example.com/a/1"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Published.IsZero() || d.Published.Day() != 24 {
		t.Errorf("published = %v", d.Published)
	}
	text, hasImage := "", false
	for _, b := range d.Blocks {
		text += b.Text
		if b.Image != nil && b.Image.URL == "https://cdn.example.com/reward-table.jpg" {
			hasImage = true
		}
	}
	if !strings.Contains(text, "진행도가 쌓이는") || strings.Contains(text, "광고") || !hasImage {
		t.Errorf("text %q, image %v", text, hasImage)
	}
}

func TestAListIsReadWholeWithoutItsChrome(t *testing.T) {
	page := `<html><body><header><a href="/login">로그인</a></header>
<ul class="notices"><li><a href="/notice/3">10월 이벤트 안내</a> 2026.10.21</li><li><a href="/notice/2">18.5 패치</a></li></ul>
<footer>회사 정보 <a href="/privacy">개인정보</a></footer></body></html>`
	d, err := Parse([]byte(page), "text/html", mustURL(t, "https://game.example.com/news/"))
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, l := range d.Links {
		urls = append(urls, l.URL)
	}
	if !contains(urls, "https://game.example.com/notice/3") || contains(urls, "https://game.example.com/login") || contains(urls, "https://game.example.com/privacy") {
		t.Errorf("links = %v", urls)
	}
	if len(d.Blocks) == 0 || !strings.Contains(d.Blocks[0].Text, "10월 이벤트 안내") {
		t.Errorf("blocks = %+v", d.Blocks)
	}
}

func TestEUCKRIsDecoded(t *testing.T) {
	enc, _ := korean.EUCKR.NewEncoder().String(`<html><head><meta charset="euc-kr"></head><body><div class="se-main-container"><p>오래된 한국 사이트</p></div></body></html>`)
	d, err := Parse([]byte(enc), "text/html", mustURL(t, "http://old.example.co.kr/"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 1 || d.Blocks[0].Text != "오래된 한국 사이트" {
		t.Errorf("blocks = %+v", d.Blocks)
	}
}

func TestAPictureIsAPageOfOnePicture(t *testing.T) {
	d, err := Parse(nil, "image/jpeg", mustURL(t, "https://cdn.example.com/poster.jpg"))
	if err != nil || len(d.Blocks) != 1 || d.Blocks[0].Image == nil {
		t.Fatalf("doc = %+v, err = %v", d, err)
	}
	if _, err := Parse(nil, "application/pdf", mustURL(t, "https://example.com/a.pdf")); err == nil {
		t.Error("a PDF should say it is not read yet")
	}
}

func TestParseTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2026-10-24T13:20:00+09:00": time.Date(2026, 10, 24, 13, 20, 0, 0, seoul),
		"2026. 10. 25. 20:22":       time.Date(2026, 10, 25, 20, 22, 0, 0, seoul),
		"2026-10-24 13:20:01":       time.Date(2026, 10, 24, 13, 20, 1, 0, seoul),
		"2026.10.21":                time.Date(2026, 10, 21, 0, 0, 0, 0, seoul),
	} {
		if got := ParseTime(in); !got.Equal(want) {
			t.Errorf("%q → %v, want %v", in, got, want)
		}
	}
	if !ParseTime("어제").IsZero() {
		t.Error("an unreadable time should be zero")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestAShopPageKeepsItsDetailPictureAndSpecs(t *testing.T) {
	page := `<html><body><div id="gnb"><a href="/company">회사소개</a><a href="/brand">브랜드 소개</a></div>
<div class="prd_top_view"><h2>필드 나이프</h2><p class="info_txt">두꺼운 두께로 장작 등을 손쉽게 절단할 수 있습니다. 손잡이는 원목입니다.</p></div>
<div class="prd_pic_info"><img src="https://cdn.example.com/shop_image/K01.jpg" alt=""></div>
<ul class="spec_txt"><li><strong>중량 :</strong> <span>370g</span></li><li><strong>규격 :</strong> <span>4.25x29cm/5T</span></li></ul>
<div class="footer">사업자등록번호 000-00-00000</div></body></html>`
	d, err := Parse([]byte(page), "text/html", mustURL(t, "https://shop.example.co.kr/item?id=K01"))
	if err != nil {
		t.Fatal(err)
	}
	text, picture := "", false
	for _, b := range d.Blocks {
		text += b.Text + "\n"
		if b.Image != nil && b.Image.URL == "https://cdn.example.com/shop_image/K01.jpg" {
			picture = true
		}
	}
	if !picture || !strings.Contains(text, "370g") || !strings.Contains(text, "장작") {
		t.Errorf("picture %v, text %q", picture, text)
	}
	if strings.Contains(text, "회사소개") || strings.Contains(text, "사업자등록번호") {
		t.Errorf("the site's menu or footer was read: %q", text)
	}
}

func TestATableIsReadRowByRow(t *testing.T) {
	page := `<html><body><div class="se-main-container"><p>보상은 이렇습니다.</p>
<table><tr><td><div><p>미션 및 조건</p></div></td><td><p>보상</p></td></tr>
<tr><td><p>롤토체스 20판 플레이</p></td><td><p>무작위 신화급 전설이 1개</p></td></tr></table>
<p>끝.</p></div></body></html>`
	d, err := Parse([]byte(page), "text/html", mustURL(t, "https://m.blog.naver.com/a/1"))
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, b := range d.Blocks {
		texts = append(texts, b.Text)
	}
	want := []string{"보상은 이렇습니다.", "미션 및 조건 | 보상\n롤토체스 20판 플레이 | 무작위 신화급 전설이 1개", "끝."}
	if strings.Join(texts, "§") != strings.Join(want, "§") {
		t.Errorf("blocks = %q", texts)
	}
}
