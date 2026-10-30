package mind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

// Home is where the operator lives, for the weather: the region they made
// for home on the phone (context-svc, kind "home"), else a fixed point.
type Home struct {
	Store store.Store   // what context-svc last said; nil: the fixed point only
	Fixed weather.Point // MIND_WEATHER_HOME, 서울 when not set

	mu    sync.Mutex
	p     weather.Point
	until time.Time
}

// How long home is taken to stay where it was found - and, when context-svc
// did not answer, how long the fixed point stands in before it is asked
// again: a morning asks the weather of every plan and day, and must not wait
// out a timeout for each.
const (
	homeTTL   = time.Hour
	homeRetry = 5 * time.Minute
)

// Point is home now.
func (h *Home) Point(ctx context.Context) (weather.Point, error) {
	h.mu.Lock()
	if time.Now().Before(h.until) {
		p := h.p
		h.mu.Unlock()
		return p, nil
	}
	h.mu.Unlock()
	p, ttl := h.Fixed, homeTTL
	if h.Store != nil {
		var out struct {
			Regions []struct {
				Kind string  `json:"kind"`
				Lat  float64 `json:"lat"`
				Lon  float64 `json:"lon"`
			} `json:"regions"`
		}
		raw, _, err := h.Store.Latest(ctx, sensorRegions)
		if err == nil {
			err = json.Unmarshal(raw, &out)
		}
		if err != nil {
			ttl = homeRetry
		}
		for _, r := range out.Regions {
			if r.Kind == "home" {
				p = weather.Point{Name: "집", Lat: r.Lat, Lon: r.Lon}
				break
			}
		}
	}
	if p.Zero() {
		return p, errors.New("집이 어디인지 모릅니다")
	}
	h.mu.Lock()
	h.p, h.until = p, time.Now().Add(ttl)
	h.mu.Unlock()
	return p, nil
}

// ParsePoint reads "37.5665,126.9780" or "37.5665,126.9780,서울".
func ParsePoint(s string) (weather.Point, error) { return weather.ParsePoint(s) }

// WeatherState is how reading the weather went, for the status screen.
type WeatherState struct {
	Home  string     `json:"home,omitempty"`  // where home was taken to be
	At    *time.Time `json:"at,omitempty"`    // the last forecast read
	Error string     `json:"error,omitempty"` // why the latest try failed
}

// ForecastKey is where the forecast of a name is kept.
func ForecastKey(name string) string { return "weather:" + name }

// homeForecast is the name home's forecast goes by, whatever home is called.
const homeForecast = "집"

// LatestWeather is the weather as context-svc last said it: mind does not go
// out for it. A place it has said nothing about is a place not on the map as
// far as the loop can tell; a plan's place is told on the next round.
type LatestWeather struct{ Store store.Store }

func (l LatestWeather) read(ctx context.Context, name string) (weather.Forecast, error) {
	var f weather.Forecast
	raw, _, err := l.Store.Latest(ctx, ForecastKey(name))
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(raw, &f)
}

// Forecast is what was last said for p.
func (l LatestWeather) Forecast(ctx context.Context, p weather.Point) (weather.Forecast, error) {
	f, err := l.read(ctx, p.Name)
	if errors.Is(err, model.ErrNotFound) {
		f, err = l.read(ctx, homeForecast)
	}
	if err != nil {
		return f, fmt.Errorf("예보가 아직 없습니다: %w", err)
	}
	return f, nil
}

// Locate is a name a forecast was said for.
func (l LatestWeather) Locate(ctx context.Context, name string, _ weather.Point) (weather.Point, error) {
	f, err := l.read(ctx, name)
	if errors.Is(err, model.ErrNotFound) {
		return weather.Point{}, weather.ErrUnknown
	}
	if err != nil {
		return weather.Point{}, err
	}
	return weather.Point{Name: name, Lat: f.Point.Lat, Lon: f.Point.Lon}, nil
}

// WeatherPlaces is where the plans of the next two weeks are: what context-svc
// asks, to know what to read the forecast of.
func (m *Mind) WeatherPlaces(ctx context.Context) ([]WeatherPlace, error) {
	plans, err := m.World.Entities(ctx, model.KindPlan, "", true)
	if err != nil {
		return nil, err
	}
	now := m.Opts.Now().In(m.Opts.Loc)
	from, to := now.Format("2006-01-02"), now.AddDate(0, 0, weatherAhead).Format("2006-01-02")
	seen := map[WeatherPlace]bool{}
	out := []WeatherPlace{}
	for _, p := range plans {
		st := p.State
		if st.Date == "" || st.Date < from || st.Date > to || st.Area == "" && st.Place == "" {
			continue
		}
		w := WeatherPlace{Area: st.Area, Place: st.Place}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out, nil
}

// weatherAhead is how many days ahead a plan's weather is read: as far as the
// rain skill looks, and a little.
const weatherAhead = 14

// WeatherPlace is where a plan is.
type WeatherPlace struct {
	Area  string `json:"area,omitempty"`
	Place string `json:"place,omitempty"`
}
