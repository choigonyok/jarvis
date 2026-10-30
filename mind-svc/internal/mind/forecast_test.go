package mind

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

// The loop asks the weather as before, and gets what context-svc last said:
// at home, at a place it said something about, and nothing elsewhere.
func TestLatestWeather(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	put := func(name string, f weather.Forecast) {
		t.Helper()
		raw, _ := json.Marshal(f)
		if err := st.PutLatest(ctx, ForecastKey(name), raw, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	day := "2026-10-31"
	put("집", weather.Forecast{Point: weather.Point{Name: "집", Lat: 37.5, Lon: 127}, Days: []weather.Day{{Date: day, Code: 0, Max: 20}}})
	put("가평", weather.Forecast{Point: weather.Point{Name: "가평", Lat: 37.8, Lon: 127.5}, Days: []weather.Day{{Date: day, Code: 63, Prob: 90, Precip: 20}}})
	home := func(context.Context) (weather.Point, error) {
		return weather.Point{Name: "서울", Lat: 37.5, Lon: 127}, nil
	}
	s := &weather.Service{Source: LatestWeather{Store: st}, Home: home}
	if d, _, ok := s.At(ctx, "", "", day); !ok || d.Max != 20 {
		t.Fatalf("집: %+v %v", d, ok)
	}
	if d, _, ok := s.At(ctx, "", "가평 캠핑장", day); !ok || !d.Wet() {
		t.Fatalf("캠핑장은 동네로 찾습니다: %+v %v", d, ok)
	}
	if _, _, ok := s.At(ctx, "", "어딘가 캠핑장", day); ok {
		t.Fatal("말한 적 없는 곳에는 날씨가 없습니다")
	}
	if _, err := (LatestWeather{Store: st}).Locate(ctx, "없는곳", weather.Point{}); !errors.Is(err, weather.ErrUnknown) {
		t.Fatalf("%v", err)
	}
}
