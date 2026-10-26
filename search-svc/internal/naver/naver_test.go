package naver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSearchSendsKeyAndReadsItems(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"total": 2772, "items": [
			{"title": "롤토체스 <b>20판</b> 보상 &amp; 시작일", "link": "https://blog.naver.com/someone/1",
			 "description": "<b>20판</b> 플레이하면", "bloggername": "게임 기록", "bloggerlink": "blog.naver.com/someone",
			 "postdate": "20261025"}]}`))
	}))
	defer srv.Close()

	c := &Client{ID: "id", Secret: "secret", Base: srv.URL}
	res, err := c.Search(context.Background(), Blog, "롤토체스 20판", 500, 1, "date")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/search/v1/blog" {
		t.Errorf("path = %s", got.URL.Path)
	}
	if got.Header.Get("X-NCP-APIGW-API-KEY-ID") != "id" || got.Header.Get("X-NCP-APIGW-API-KEY") != "secret" {
		t.Errorf("key headers not sent: %v", got.Header)
	}
	q := got.URL.Query()
	if q.Get("query") != "롤토체스 20판" || q.Get("display") != "100" || q.Get("sort") != "date" {
		t.Errorf("query = %v", q)
	}
	if res.Total != 2772 || len(res.Items) != 1 {
		t.Fatalf("result = %+v", res)
	}
	it := res.Items[0]
	if it.Title != "롤토체스 20판 보상 & 시작일" || it.Description != "20판 플레이하면" || it.Author != "게임 기록" {
		t.Errorf("item = %+v", it)
	}
	want := time.Date(2026, 10, 25, 0, 0, 0, 0, seoul)
	if !it.Date.Equal(want) {
		t.Errorf("date = %v, want %v", it.Date, want)
	}
}

func TestLocalCoordinatesAndDisplay(t *testing.T) {
	var display, sort string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		display, sort = r.URL.Query().Get("display"), r.URL.Query().Get("sort")
		w.Write([]byte(`{"total": 5, "items": [{"title": "가게", "category": "술집>요리주점",
			"roadAddress": "서울특별시 강남구 어딘가 1", "mapx": "1270276743", "mapy": "374999163"}]}`))
	}))
	defer srv.Close()

	c := &Client{ID: "id", Secret: "secret", Base: srv.URL}
	res, err := c.Search(context.Background(), Local, "강남역 술집", 20, 1, "date")
	if err != nil {
		t.Fatal(err)
	}
	if display != "5" || sort != "" {
		t.Errorf("display = %s sort = %q: local takes five and no date order", display, sort)
	}
	it := res.Items[0]
	if it.Lng < 127.0276 || it.Lng > 127.0277 || it.Lat < 37.4999 || it.Lat > 37.5 {
		t.Errorf("coords = %v, %v", it.Lat, it.Lng)
	}
}

func TestErrorsSayWhatToDo(t *testing.T) {
	for status, want := range map[int]string{401: "키가 맞지 않거나", 429: "한도", 400: "SE02"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"errorCode": "SE02", "errorMessage": "Invalid display value (SE02)"}`))
		}))
		c := &Client{ID: "id", Secret: "secret", Base: srv.URL}
		_, err := c.Search(context.Background(), News, "x", 10, 1, "")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: err = %v, want it to mention %q", status, err, want)
		}
	}
	if _, err := (&Client{}).Search(context.Background(), Blog, "x", 1, 1, ""); err != ErrNoKey {
		t.Errorf("no key: err = %v", err)
	}
}
