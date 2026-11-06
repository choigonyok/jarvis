package place

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The map's answers for a city (an outline), a station (a building's, too
// small to follow) and a neighbourhood (a point): followed, circled, circled.
func TestFindArea(t *testing.T) {
	maps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != userAgent || r.URL.Query().Get("polygon_geojson") != "1" {
			t.Errorf("지도에 이름만, 누가 묻는지 밝혀서: %v", r.URL)
		}
		switch r.URL.Query().Get("q") {
		case "가상시":
			w.Write([]byte(`[{"name":"가상시","display_name":"가상시, 가상도, 대한민국","lat":"36.08","lon":"128.10",
				"boundingbox":["36.00","36.15","128.00","128.20"],
				"geojson":{"type":"Polygon","coordinates":[[[128.00,36.00],[128.20,36.00],[128.20,36.15],[128.00,36.15],[128.00,36.00]]]}}]`))
		case "가상역":
			w.Write([]byte(`[{"name":"가상역","display_name":"가상역, 924, 역로, 가상시, 가상도, 16622, 대한민국","lat":"36.5656","lon":"128.5000",
				"boundingbox":["36.5643","36.5668","128.4988","128.5012"],
				"geojson":{"type":"Polygon","coordinates":[[[128.4988,36.5643],[128.5012,36.5643],[128.5012,36.5668],[128.4988,36.5643]]]}}]`))
		case "가상동":
			w.Write([]byte(`[{"name":"가상동","display_name":"가상동, 가상구, 가상시, 대한민국","lat":"36.39","lon":"128.39",
				"boundingbox":["36.38","36.40","128.38","128.40"],"geojson":{"type":"Point","coordinates":[128.39,36.39]}}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer maps.Close()
	old := nominatimURL
	nominatimURL = maps.URL + "/search"
	defer func() { nominatimURL = old }()
	m := New("http://unused", "")
	ctx := context.Background()

	city, err := m.FindArea(ctx, "가상시")
	if err != nil || len(city.Rings) != 1 || len(city.Rings[0]) != 5 || city.Radius != 0 || city.Where != "가상시, 가상도" {
		t.Fatalf("도시는 경계로: %+v %v", city, err)
	}
	station, err := m.FindArea(ctx, "가상역")
	if err != nil || len(station.Rings) != 0 || station.Radius != minRadius || station.Lat != 36.5656 || !strings.HasPrefix(station.Where, "가상역") {
		t.Fatalf("역 건물은 작아서 둘레 300m: %+v %v", station, err)
	}
	quarter, err := m.FindArea(ctx, "가상동")
	if err != nil || quarter.Radius < 1000 || quarter.Radius > maxRadius {
		t.Fatalf("점뿐인 동은 상자 크기만큼의 둘레: %+v %v", quarter, err)
	}
	if _, err := m.FindArea(ctx, "없는곳"); err == nil || !strings.Contains(err.Error(), "찾지 못했습니다") {
		t.Fatalf("없는 이름: %v", err)
	}
}

func TestIsRegion(t *testing.T) {
	ctxSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/regions" || r.Header.Get("Authorization") != "Bearer t" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"regions":[{"name":"집","kind":"home"},{"name":"헬스장","kind":""}]}`))
	}))
	defer ctxSvc.Close()
	m := New(ctxSvc.URL, "t")
	for name, want := range map[string]bool{"헬스장": true, "집": true, "home": true, "수원시": false} {
		if got, err := m.IsRegion(context.Background(), name); err != nil || got != want {
			t.Fatalf("%s: %v %v", name, got, err)
		}
	}
	if _, err := New(ctxSvc.URL, "wrong").IsRegion(context.Background(), "수원시"); err == nil {
		t.Fatal("영역을 못 읽으면 모른다고 해야 합니다")
	}
}

func TestThinKeepsAnOutlineSmall(t *testing.T) {
	ring := make([][2]float64, 9000)
	for i := range ring {
		ring[i] = [2]float64{float64(i), 0}
	}
	got := thin([][][2]float64{ring}, 4000)
	if n := len(got[0]); n > 4000 || n < 2000 {
		t.Fatalf("%d", n)
	}
}
