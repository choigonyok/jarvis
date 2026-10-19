package weather

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWet(t *testing.T) {
	for _, c := range []struct {
		d    Day
		want bool
	}{
		{Day{Prob: 80, Precip: 12}, true},
		{Day{Prob: 80, Precip: 0.2}, false}, // likely, but a trace
		{Day{Prob: 40, Precip: 2}, false},   // a little, unlikely
		{Day{Prob: 30, Precip: 8}, true},    // plenty, whatever the odds
		{Day{Prob: 60, Snow: 1.4, Precip: 1, Code: 73}, true},
		{Day{Prob: 0, Precip: 0, Code: 0}, false},
	} {
		if got := c.d.Wet(); got != c.want {
			t.Errorf("%+v: Wet() = %v, want %v", c.d, got, c.want)
		}
	}
}

func TestLine(t *testing.T) {
	if got := (Day{Code: 61, Prob: 80, Precip: 12.4, Min: 10.6, Max: 17.5}).Line(); got != "비 80% · 12mm · 11~18°" {
		t.Errorf("wet line %q", got)
	}
	if got := (Day{Code: 0, Prob: 5, Min: 3, Max: 14}).Line(); got != "맑음 · 3~14°" {
		t.Errorf("dry line %q", got)
	}
	if got := (Day{Code: 73, Prob: 70, Precip: 2, Snow: 3}).Fall(); got != "눈 70%" {
		t.Errorf("snow %q", got)
	}
}

func TestNames(t *testing.T) {
	got := Names("", "자라섬 캠핑장")
	want := []string{"자라섬 캠핑장", "자라섬"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %v, want %v", got, want)
	}
	got = Names("망원동", "카페 하이")
	want = []string{"망원동", "카페 하이"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a first word is not a place: Names = %v, want %v", got, want)
	}
	if got := Names("", "집"); len(got) != 0 {
		t.Errorf("a one-letter place is no name to look up: %v", got)
	}
}

func TestFrom(t *testing.T) {
	loc := time.FixedZone("KST", 9*3600)
	day := time.Date(2026, 10, 18, 0, 0, 0, 0, loc)
	hours := []Hour{
		{At: day.Add(7 * time.Hour), Prob: 70},
		{At: day.Add(8 * time.Hour), Prob: 20},
		{At: day.Add(15 * time.Hour), Prob: 60},
	}
	at, ok := From(hours, day.Add(8*time.Hour+5*time.Minute))
	if !ok || at.Hour() != 15 {
		t.Errorf("From = %v %v, want 15:00", at, ok)
	}
	if _, ok := From(hours[:2], day.Add(8*time.Hour)); ok {
		t.Error("no rain after 8 o'clock")
	}
}

const forecastJSON = `{
 "latitude": 37.55, "longitude": 126.875, "timezone": "Asia/Seoul",
 "daily": {
  "time": ["2026-10-17","2026-10-18","2026-10-19"],
  "weather_code": [3, 65, null],
  "temperature_2m_max": [24.8, 21.3, null],
  "temperature_2m_min": [13.7, 14.7, null],
  "precipitation_sum": [0.0, 39.8, null],
  "snowfall_sum": [0.0, 0.0, null],
  "precipitation_probability_max": [2, 100, null]
 },
 "hourly": {
  "time": ["2026-10-18T08:00","2026-10-18T09:00"],
  "precipitation_probability": [40, 85],
  "precipitation": [0.0, 3.1]
 }
}`

func TestOpenMeteoForecast(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if q.Get("past_days") != "92" || q.Get("timezone") != "Asia/Seoul" || !strings.Contains(q.Get("daily"), "precipitation_probability_max") {
			t.Errorf("query %v", q)
		}
		_, _ = w.Write([]byte(forecastJSON))
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC)
	o := &OpenMeteo{Forecasts: srv.URL, Now: func() time.Time { return now }}
	f, err := o.Forecast(context.Background(), Point{Name: "집", Lat: 37.556, Lon: 126.901})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Days) != 2 {
		t.Fatalf("a day with nothing in it is no day: %d days", len(f.Days))
	}
	d, ok := f.Day("2026-10-18")
	if !ok || !d.Wet() || d.Prob != 100 || d.Code != 65 {
		t.Errorf("2026-10-18 = %+v", d)
	}
	if len(f.Hours) != 2 || f.Hours[1].Prob != 85 || f.Hours[1].At.Hour() != 9 {
		t.Errorf("hours %+v", f.Hours)
	}
	if _, err := o.Forecast(context.Background(), Point{Name: "집", Lat: 37.556, Lon: 126.901}); err != nil || calls.Load() != 1 {
		t.Errorf("a forecast read a moment ago is read again: %d calls", calls.Load())
	}
	now = now.Add(time.Hour)
	if _, err := o.Forecast(context.Background(), Point{Lat: 37.556, Lon: 126.901}); err != nil || calls.Load() != 2 {
		t.Errorf("an hour-old forecast is not read again: %d calls", calls.Load())
	}
}

func TestOpenMeteoLocate(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "jarvis") {
			t.Errorf("Nominatim wants to know who asks: %q", r.Header.Get("User-Agent"))
		}
		if r.URL.Query().Get("q") == "자라섬" && r.URL.Query().Get("viewbox") == "" {
			t.Error("a name is looked up near home")
		}
		switch r.URL.Query().Get("q") {
		case "성수동":
			_, _ = w.Write([]byte(`[{"lat":"33.481","lon":"126.413","category":"highway","name":"성수동"}]`))
		case "가평":
			_, _ = w.Write([]byte(`[{"lat":"37.815","lon":"127.511","category":"railway","name":"가평"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()
	o := &OpenMeteo{Places: srv.URL, Agent: "jarvis-test", Every: time.Millisecond}
	ctx := context.Background()
	p, err := o.Locate(ctx, "가평", Point{})
	if err != nil || p.Lat < 37.8 || p.Name != "가평" {
		t.Fatalf("가평 = %+v %v", p, err)
	}
	if _, err := o.Locate(ctx, "성수동", Point{}); !errors.Is(err, ErrUnknown) {
		t.Errorf("a bus stop is no 성수동: %v", err)
	}
	_, _ = o.Locate(ctx, "자라섬", Point{Lat: 37.56, Lon: 126.97})
	before := calls.Load()
	_, _ = o.Locate(ctx, "가평", Point{})
	_, _ = o.Locate(ctx, "성수동", Point{})
	if calls.Load() != before {
		t.Errorf("places found (and not) are kept: %d more calls", calls.Load()-before)
	}
}

func TestServiceAt(t *testing.T) {
	s := &Service{Source: Static{Days: map[string][]Day{
		"":   {{Date: "2026-10-18", Code: 0, Max: 20, Min: 10}},
		"가평": {{Date: "2026-10-18", Code: 63, Prob: 90, Precip: 20, Max: 15, Min: 9}},
	}}, Home: StaticHome}
	ctx := context.Background()
	if d, _, ok := s.At(ctx, "", "", "2026-10-18"); !ok || d.Wet() {
		t.Errorf("home %+v %v", d, ok)
	}
	if d, _, ok := s.At(ctx, "", "가평 캠핑장", "2026-10-18"); !ok || !d.Wet() {
		t.Errorf("a campsite is found by its town: %+v %v", d, ok)
	}
	if _, _, ok := s.At(ctx, "", "어딘가 캠핑장", "2026-10-18"); ok {
		t.Error("a place no map knows has no weather")
	}
	if _, _, ok := s.At(ctx, "", "가평", "2026-10-25"); ok {
		t.Error("a day the forecast does not reach has no weather")
	}
	days, err := s.HomeDays(ctx, "2026-10-01", "2026-10-31")
	if err != nil || len(days) != 1 {
		t.Errorf("HomeDays = %v %v", days, err)
	}
}

// A service that did not answer is not asked again for a while: the answer
// is its failure, at once.
func TestOpenMeteoDown(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "upstream", http.StatusBadGateway)
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC)
	o := &OpenMeteo{Forecasts: srv.URL, Places: srv.URL, Agent: "jarvis-test", Every: time.Millisecond, Now: func() time.Time { return now }}
	ctx := context.Background()
	for range 3 {
		if _, err := o.Forecast(ctx, Point{Lat: 37.5, Lon: 127}); err == nil {
			t.Fatal("a failed forecast is an error")
		}
		if _, err := o.Locate(ctx, "가평", Point{}); err == nil || errors.Is(err, ErrUnknown) {
			t.Fatalf("an unreachable map is not an unknown place: %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("asked %d times while down, want one each", calls.Load())
	}
	now = now.Add(6 * time.Minute)
	_, _ = o.Forecast(ctx, Point{Lat: 37.5, Lon: 127})
	if calls.Load() != 3 {
		t.Fatalf("asked again after a while: %d", calls.Load())
	}
}
