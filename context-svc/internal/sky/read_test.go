package sky

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/choigonyok/jarvis/gocommon/weather"
)

type fakeSource struct {
	known map[string]weather.Point
	asked []string
}

func (f *fakeSource) Forecast(_ context.Context, p weather.Point) (weather.Forecast, error) {
	f.asked = append(f.asked, p.Name)
	return weather.Forecast{Point: p, Days: []weather.Day{{Date: "2026-10-31", Prob: 80}}}, nil
}

func (f *fakeSource) Locate(_ context.Context, name string, _ weather.Point) (weather.Point, error) {
	if p, ok := f.known[name]; ok {
		return p, nil
	}
	return weather.Point{}, weather.ErrUnknown
}

func forecasts(t *testing.T, v any) map[string]weather.Forecast {
	t.Helper()
	raw, _ := json.Marshal(v)
	var out struct {
		Forecasts map[string]weather.Forecast `json:"forecasts"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.Forecasts
}

// Home's forecast, and each plan's under the name a map knew it by: the first
// of its names that a map knows.
func TestReadHomeAndPlans(t *testing.T) {
	mind := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/weather/places" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("%s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"places":[{"area":"없는동네","place":"자라섬 캠핑장"},{"place":"없는곳"}]}`))
	}))
	defer mind.Close()
	src := &fakeSource{known: map[string]weather.Point{"자라섬": {Name: "자라섬", Lat: 37.8, Lon: 127.5}}}
	r := &Reader{Source: src, Fixed: weather.Point{Name: "서울", Lat: 37.5, Lon: 127}, MindURL: mind.URL, Token: "tok",
		Home: func(context.Context) (weather.Point, bool, error) {
			return weather.Point{Name: HomeKey, Lat: 37.55, Lon: 126.9}, true, nil
		}}
	v, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := forecasts(t, v)
	if len(got) != 2 || got[HomeKey].Point.Lat != 37.55 || got["자라섬"].Point.Name != "자라섬" {
		t.Fatalf("%+v", got)
	}
}

// No region made: the fixed point stands in. mind away: home is still told.
func TestHomeGoesOutWhateverElse(t *testing.T) {
	src := &fakeSource{}
	r := &Reader{Source: src, Fixed: weather.Point{Name: "서울", Lat: 37.5, Lon: 127}, MindURL: "http://127.0.0.1:1"}
	v, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := forecasts(t, v)
	if len(got) != 1 || got[HomeKey].Point.Name != "서울" {
		t.Fatalf("%+v", got)
	}
	if _, err := (&Reader{Source: src}).Read(context.Background()); err == nil {
		t.Fatal("집을 모르면 오류입니다")
	}
	failing := &Reader{Source: src, Home: func(context.Context) (weather.Point, bool, error) {
		return weather.Point{}, false, errors.New("표를 못 읽음")
	}}
	if _, err := failing.Read(context.Background()); err == nil {
		t.Fatal("영역을 못 읽으면 오류입니다")
	}
}
