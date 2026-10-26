package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
	"github.com/choigonyok/jarvis/search-svc/internal/imgtext"
	"github.com/choigonyok/jarvis/search-svc/internal/naver"
	"github.com/choigonyok/jarvis/search-svc/internal/place"
)

func newServer(t *testing.T, naverBase, ocrURL string) *Server {
	t.Helper()
	f := fetch.NewAllowingLocal()
	return &Server{
		Token:  "tok",
		Naver:  &naver.Client{ID: "id", Secret: "secret", Base: naverBase},
		Fetch:  f,
		Images: &imgtext.Reader{OCRURL: ocrURL, Token: "ocr", Fetch: f},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func post(t *testing.T, h http.Handler, path, token string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestTheTokenGuardsEveryRouteButHealth(t *testing.T) {
	s := newServer(t, "", "")
	h := s.Handler()
	if code, _ := post(t, h, "/search", "wrong", map[string]any{"query": "x"}); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	s.Token = ""
	if code, out := post(t, s.Handler(), "/search", "", map[string]any{"query": "x"}); code != http.StatusServiceUnavailable || !strings.Contains(out["error"].(string), "꺼져") {
		t.Errorf("no token configured: %d %v", code, out)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Errorf("health: %d %s", rec.Code, rec.Body)
	}
}

func TestSearchAsksNaverAndSaysWhatItChecked(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/v1/blog":
			w.Write([]byte(`{"total": 2, "items": [{"title": "<b>20판</b> 보상", "link": "https://blog.naver.com/a/1", "postdate": "20261025", "bloggername": "기록"}]}`))
		default:
			w.Write([]byte(`{"total": 0, "items": []}`))
		}
	}))
	defer hub.Close()
	code, out := post(t, newServer(t, hub.URL, "").Handler(), "/search", "tok", map[string]any{"query": "롤토체스 20판", "sources": []string{"blog", "news"}, "since": "7d"})
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	hits := out["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["title"] != "20판 보상" {
		t.Errorf("hits = %v", hits)
	}
	if len(out["checked"].([]any)) != 2 || out["since"] == nil {
		t.Errorf("checked = %v since = %v", out["checked"], out["since"])
	}
}

func TestSince(t *testing.T) {
	now := time.Date(2026, 10, 26, 22, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"24h": now.Add(-24 * time.Hour), "7d": now.AddDate(0, 0, -7), "2w": now.AddDate(0, 0, -14), "1m": now.AddDate(0, -1, 0),
	} {
		if got, err := Since(in, now); err != nil || !got.Equal(want) {
			t.Errorf("%s → %v %v", in, got, err)
		}
	}
	if got, _ := Since("2026-10-01", now); got.Day() != 1 {
		t.Errorf("a day: %v", got)
	}
	if _, err := Since("지난주", now); err == nil {
		t.Error("want an error")
	}
}

func pngOf(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y += 7 {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.Black)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestReadPutsWhatThePictureSaysWhereItStands(t *testing.T) {
	ocr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"lines":[{"text":"20판 플레이 무작위 신화급 전설이","conf":1},{"text":"1판 플레이 보물 토큰 500개","conf":1}]}`))
	}))
	defer ocr.Close()
	var site *httptest.Server
	site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/card.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngOf(600, 600))
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<html><head><meta property="og:title" content="이벤트 안내"></head><body>
<div class="se-main-container"><p>보상은 아래 그림과 같습니다.</p><img src="` + site.URL + `/card.png" width="600" height="600">
<p>` + strings.Repeat("패치 일정은 바뀔 수 있습니다. ", 40) + `</p><p><a href="https://example.com/notice">공식 공지</a></p></div></body></html>`))
		}
	}))
	defer site.Close()
	h := newServer(t, "", ocr.URL).Handler()

	code, out := post(t, h, "/read", "tok", map[string]any{"url": site.URL + "/post"})
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	text := out["text"].(string)
	i, j := strings.Index(text, "보상은 아래"), strings.Index(text, "[그림 1 · 그림 속 글자]\n20판 플레이")
	if i < 0 || j < 0 || j < i || !strings.Contains(text, "[그림 1 끝]") {
		t.Fatalf("text = %q", text)
	}
	pics := out["pictures"].([]any)
	if len(pics) != 1 || pics[0].(map[string]any)["read"] != true {
		t.Errorf("pictures = %v", pics)
	}
	if links := out["links"].([]any); len(links) != 1 || links[0].(map[string]any)["url"] != "https://example.com/notice" {
		t.Errorf("links = %v", links)
	}

	// A small page budget and a focus: the parts about it, the opening kept.
	_, out = post(t, h, "/read", "tok", map[string]any{"url": site.URL + "/post", "focus": "신화급 전설이", "max": 120, "look": true})
	ft := out["text"].(string)
	if out["focused"] != true || !strings.Contains(ft, "신화급 전설이") || strings.Count(ft, "패치 일정은") > 0 {
		t.Errorf("focused text = %q", ft)
	}
	if looks := out["looks"].([]any); len(looks) != 1 {
		t.Errorf("looks = %d, want the one piece that matched", len(looks))
	}

	// From an offset: the rest, in order.
	_, first := post(t, h, "/read", "tok", map[string]any{"url": site.URL + "/post", "max": 100})
	next := int(first["next"].(float64))
	_, rest := post(t, h, "/read", "tok", map[string]any{"url": site.URL + "/post", "max": 100, "offset": next})
	if int(rest["offset"].(float64)) != next || rest["text"] == first["text"] {
		t.Errorf("offset read: %v", rest)
	}
}

func TestReadRefusesAPrivateAddress(t *testing.T) {
	s := newServer(t, "", "")
	s.Fetch = fetch.New()
	code, out := post(t, s.Handler(), "/read", "tok", map[string]any{"url": "http://agent:8080/thread"})
	if code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "공개 인터넷") {
		t.Errorf("%d %v", code, out)
	}
}

func TestPlacesKeepsWhatIsNearAndOpen(t *testing.T) {
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<script>window.__APOLLO_STATE__ = {"ROOT_QUERY": {"placeList({\"input\":{\"query\":\"술집\"}})": {"businesses": {"total": 3, "items": [
  {"__ref": "P:1"}, {"__ref": "P:2"}, {"__ref": "P:3"}]}}},
 "P:1": {"id": "1", "name": "가까운 집", "x": "127.0276", "y": "37.4985", "newBusinessHours": {"status": "영업 중", "description": "02:00에 영업 종료"}},
 "P:2": {"id": "2", "name": "닫은 집", "x": "127.0276", "y": "37.4990", "newBusinessHours": {"status": "영업 종료", "description": "17:00에 영업 시작"}},
 "P:3": {"id": "3", "name": "먼 집", "x": "127.0800", "y": "37.5500", "newBusinessHours": {"status": "영업 중"}}};</script>`))
	}))
	defer list.Close()
	old := place.ListURL
	place.ListURL = list.URL
	defer func() { place.ListURL = old }()

	code, out := post(t, newServer(t, "", "").Handler(), "/places", "tok", map[string]any{"query": "술집", "lat": 37.4979, "lng": 127.0276, "radius": 1000, "open": true})
	if code != http.StatusOK {
		t.Fatalf("%d %v", code, out)
	}
	places := out["places"].([]any)
	if len(places) != 1 || places[0].(map[string]any)["name"] != "가까운 집" || out["beyond"].(float64) != 1 || out["closed"].(float64) != 1 {
		t.Errorf("out = %v", out)
	}
	if code, _ := post(t, newServer(t, "", "").Handler(), "/places", "tok", map[string]any{"query": "술집"}); code != http.StatusBadRequest {
		t.Errorf("no point: %d", code)
	}
}
