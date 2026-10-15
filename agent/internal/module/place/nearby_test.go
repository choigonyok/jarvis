package place

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Around the last fix, rounded before it leaves; around a region by its
// name; around a place the map knows. Nearest first.
func TestNearby(t *testing.T) {
	var asked string
	maps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/interpreter":
			b, _ := io.ReadAll(r.Body)
			v, _ := url.ParseQuery(string(b))
			asked = v.Get("data")
			w.Write([]byte(`{"elements":[
				{"type":"node","id":2,"lat":37.5571,"lon":126.9101,"tags":{"name":"먼 식당","amenity":"restaurant"}},
				{"type":"way","id":1,"center":{"lat":37.5561,"lon":126.9105},"tags":{"name":"Near","name:ko":"가까운 국숫집","amenity":"restaurant","cuisine":"noodle;korean","opening_hours":"11:00-21:00"}}]}`))
		case "/busy":
			w.WriteHeader(http.StatusGatewayTimeout)
		case "/search":
			w.Write([]byte(`[{"lat":"37.5500","lon":"126.9100","display_name":"망원동"}]`))
		}
	}))
	defer maps.Close()
	overpassURLs, nominatimURL = []string{maps.URL + "/busy", maps.URL + "/interpreter"}, maps.URL+"/search"
	fix := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	ctxsvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/now":
			w.Write([]byte(`{"location":{"lat":37.556123,"lon":126.910456,"acc":10},"fixAt":"` + fix + `","summary":"x"}`))
		case "/regions":
			w.Write([]byte(`{"regions":[{"name":"지수네 집","kind":"other","lat":37.5601,"lon":126.9201,"radiusM":50}]}`))
		}
	}))
	defer ctxsvc.Close()
	m := New(ctxsvc.URL, "")
	ctx := context.Background()

	p, err := m.Where(ctx, "")
	if err != nil || !strings.HasPrefix(p.From, "지금 위치") {
		t.Fatalf("지금 위치: %+v %v", p, err)
	}
	shops, err := m.Nearby(ctx, p, "배달", 0, 0)
	if err != nil || len(shops) != 2 || shops[0].Name != "가까운 국숫집" || shops[0].Kind != "restaurant · noodle, korean" || shops[0].Hours == "" || !strings.Contains(shops[0].Link, "/way/1") {
		t.Fatalf("가까운 순: %+v %v", shops, err)
	}
	if !strings.Contains(asked, "around:800,37.556,126.910") || strings.Contains(asked, "37.556123") || !strings.Contains(asked, "restaurant|fast_food") {
		t.Fatalf("반올림한 좌표와 식당 태그로 물어야 함: %s", asked)
	}
	if p, err := m.Where(ctx, "지수네 집"); err != nil || p.Lat != 37.5601 || !strings.Contains(p.From, "영역") {
		t.Fatalf("영역: %+v %v", p, err)
	}
	if p, err := m.Where(ctx, "망원동"); err != nil || p.Lat != 37.55 || !strings.Contains(p.From, "지도 검색") {
		t.Fatalf("지도 검색: %+v %v", p, err)
	}
	if _, err := m.Nearby(ctx, p, "카페", 0, 0); err != nil || !strings.Contains(asked, `"amenity"="cafe"`) {
		t.Fatalf("카페: %s %v", asked, err)
	}
}

// The real map, when asked: JARVIS_LIVE_MAPS=1 go test ./internal/module/place -run Live -v
func TestNearbyLive(t *testing.T) {
	if os.Getenv("JARVIS_LIVE_MAPS") != "1" {
		t.Skip("JARVIS_LIVE_MAPS=1 일 때만")
	}
	m := New("http://127.0.0.1:1", "")
	ctx := context.Background()
	p, err := m.Where(ctx, "강남역")
	if err != nil {
		t.Fatal(err)
	}
	shops, err := m.Nearby(ctx, p, "식당", 500, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range shops {
		t.Logf("%4dm %s (%s) %s", s.Meters, s.Name, s.Kind, s.Address)
	}
	if len(shops) == 0 {
		t.Fatal("강남역 500m 안에 식당이 하나도 없다고 나옴")
	}
}
