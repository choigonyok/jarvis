package place

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/module/web"
)

type fakeNaver struct {
	got []web.PlacesQuery
	res web.Places
	err error
}

func (f *fakeNaver) Places(_ context.Context, q web.PlacesQuery) (web.Places, error) {
	f.got = append(f.got, q)
	return f.res, f.err
}

func resultText(text string, err error) string {
	if err != nil {
		return err.Error()
	}
	return text
}

// 네이버 지도가 먼저: 반올림한 지금 위치, 영업 중만, 지도에서 찾은 곳이면 그 이름도 함께.
// 네이버를 못 읽으면 OpenStreetMap 으로 찾고 그렇다고 말한다.
func TestFindNearbyAsksNaverFirst(t *testing.T) {
	fix := time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339)
	ctxsvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/now" {
			w.Write([]byte(`{"location":{"lat":37.497912,"lon":127.027634,"acc":10},"fixAt":"` + fix + `","summary":"x"}`))
		} else {
			w.Write([]byte(`{"regions":[]}`))
		}
	}))
	defer ctxsvc.Close()
	maps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/interpreter":
			w.Write([]byte(`{"elements":[{"type":"node","id":1,"lat":37.4981,"lon":127.0277,"tags":{"name":"지도에 있는 바","amenity":"bar"}}]}`))
		case "/search":
			w.Write([]byte(`[{"lat":"37.4979","lon":"127.0276","display_name":"강남역"}]`))
		}
	}))
	defer maps.Close()
	oldO, oldN := overpassURLs, nominatimURL
	overpassURLs, nominatimURL = []string{maps.URL + "/interpreter"}, maps.URL+"/search"
	defer func() { overpassURLs, nominatimURL = oldO, oldN }()

	naver := &fakeNaver{res: web.Places{Total: 890, Closed: 3, Source: "naver-place", Places: []web.Place{
		{Name: "첫 집", Category: "요리주점", Meters: 220, Status: "영업 중", Hours: "02:00에 영업 종료", Rating: 4.89, Reviews: 10016,
			Address: "서울 강남구 역삼동 강남대로 1", URL: "https://m.place.naver.com/place/1/home"},
	}}}
	m := New(ctxsvc.URL, "")
	m.UseNaver(naver)
	ctx := context.Background()

	out := resultText(m.findNearby(ctx, "", "술집", true, 0, 0))
	for _, want := range []string{"네이버 지도에서 '술집' 1곳 · 반경 1km · 지금 영업 중만", "목록 890곳 중", "영업 중 아님 3",
		"- 첫 집 · 요리주점 · 220m · 영업 중, 02:00에 영업 종료 · ★4.89(방문자 리뷰 10016)", "https://m.place.naver.com/place/1/home"} {
		if !strings.Contains(out, want) {
			t.Errorf("결과에 %q 가 없음:\n%s", want, out)
		}
	}
	q := naver.got[0]
	if q.Lat != 37.498 || q.Lng != 127.028 || !q.Open || q.Area != "" || q.Radius != 1000 || q.Query != "술집" {
		t.Errorf("반올림한 지금 위치·영업 중으로 물어야 함: %+v", q)
	}

	resultText(m.findNearby(ctx, "강남역", "", false, 0, 0))
	if q := naver.got[1]; q.Area != "강남역" || q.Query != "음식점" {
		t.Errorf("지도에서 찾은 곳이면 이름을 함께, 무엇이 없으면 음식점: %+v", q)
	}

	naver.err = errors.New("네이버 플레이스가 403 로 답했습니다")
	out = resultText(m.findNearby(ctx, "", "술집", true, 0, 0))
	if !strings.Contains(out, "네이버 지도를 읽지 못해") || !strings.Contains(out, "OpenStreetMap 에서 1곳") || !strings.Contains(out, "지도에 있는 바") {
		t.Errorf("OpenStreetMap 으로 내려가야 함:\n%s", out)
	}
}
