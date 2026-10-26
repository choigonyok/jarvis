package place

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
	"github.com/choigonyok/jarvis/search-svc/internal/naver"
)

// listPage is the shape of m.place.naver.com's list (2026-10): the data in
// window.__APOLLO_STATE__, the list under ROOT_QUERY, each place by reference.
const listPage = `<html><head><script>
window.__APOLLO_STATE__ = {"ROOT_QUERY": {
  "__typename": "Query",
  "placeList({\"input\":{\"display\":9,\"filterOpening\":\"true\",\"query\":\"술집\",\"x\":\"127.0276\",\"y\":\"37.4979\"}})": {"businesses": {"total": 1, "items": [{"__ref": "PlaceListBusinessesItem:2"}]}},
  "placeList({\"input\":{\"display\":70,\"query\":\"술집\",\"x\":\"127.0276\",\"y\":\"37.4979\"}})": {"businesses": {"total": 890, "items": [{"__ref": "PlaceListBusinessesItem:1"}, {"__ref": "PlaceListBusinessesItem:2"}]}}
},
"PlaceListBusinessesItem:1": {"id": "1", "name": "첫 집", "category": "요리주점", "x": "127.0276743", "y": "37.4999163",
  "roadAddress": "강남대로 1 B1", "commonAddress": "서울 강남구 역삼동", "virtualPhone": "0507-0000-0000",
  "visitorReviewCount": "10,016", "visitorReviewScore": "4.89", "blogCafeReviewCount": "1,024", "options": "예약,주차",
  "newBusinessHours": {"status": "영업 중", "description": "02:00에 영업 종료"}},
"PlaceListBusinessesItem:2": {"id": "2", "name": "둘째 집", "category": "이자카야", "x": "127.0353", "y": "37.4927",
  "visitorReviewCount": "434", "visitorReviewScore": null, "newBusinessHours": {"status": "영업 종료", "description": "17:00에 영업 시작"}}
};
window.__PLACE_STATE__ = {};
</script></head><body></body></html>`

func TestNearReadsTheListAndMeasuresFromThePoint(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery
		if !strings.Contains(r.Header.Get("User-Agent"), "iPhone") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(listPage))
	}))
	defer srv.Close()
	old := ListURL
	ListURL = srv.URL + "/place/list"
	defer func() { ListURL = old }()

	places, total, err := Near(context.Background(), fetch.NewAllowingLocal(), "술집", 37.4979, 127.0276)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "x=127.0276") || !strings.Contains(asked, "y=37.4979") {
		t.Errorf("asked %s", asked)
	}
	if total != 890 || len(places) != 2 {
		t.Fatalf("total %d, %d places - the whole list, not the open-now one", total, len(places))
	}
	p := places[0]
	if p.Name != "첫 집" || p.Status != "영업 중" || p.Hours != "02:00에 영업 종료" || p.Rating != 4.89 ||
		p.Reviews != 10016 || p.Blog != 1024 || p.Address != "서울 강남구 역삼동 강남대로 1 B1" ||
		p.URL != "https://m.place.naver.com/place/1/home" || p.Phone != "0507-0000-0000" {
		t.Errorf("first = %+v", p)
	}
	if p.Meters < 200 || p.Meters > 250 {
		t.Errorf("first is %d m away, want about 220", p.Meters)
	}
	if places[1].Rating != 0 {
		t.Errorf("a place without stars got %v", places[1].Rating)
	}
}

func TestAPageWithoutAListSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>잠시 후 다시 시도해 주세요</body></html>"))
	}))
	defer srv.Close()
	old := ListURL
	ListURL = srv.URL
	defer func() { ListURL = old }()
	if _, _, err := Near(context.Background(), fetch.NewAllowingLocal(), "술집", 37.5, 127); err == nil {
		t.Error("want an error")
	}
}

func TestOpen(t *testing.T) {
	for status, want := range map[string]bool{
		"영업 중": true, "진료 중": true, "곧 영업 종료": true, "24시간 영업": true,
		"영업 종료": false, "진료 종료": false, "오늘 휴무": false, "곧 영업 시작": false, "브레이크타임": false, "": false,
	} {
		if Open(status) != want {
			t.Errorf("Open(%q) = %v", status, !want)
		}
	}
}

func TestFromLocal(t *testing.T) {
	got := FromLocal([]naver.Item{{Title: "가게", Category: "술집>요리주점", RoadAddress: "서울 강남구 어딘가 1", Lat: 37.5016, Lng: 127.0273, Link: "https://example.com"}}, 37.4979, 127.0276)
	if len(got) != 1 || got[0].Meters < 380 || got[0].Meters > 440 || got[0].URL != "https://example.com" {
		t.Errorf("got %+v", got)
	}
}
