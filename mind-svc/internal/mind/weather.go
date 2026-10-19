package mind

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

// Home is where the operator lives, for the weather: the region they made
// for home on the phone (context-svc, kind "home"), else a fixed point.
type Home struct {
	URL, Token string        // context-svc; empty: the fixed point only
	Fixed      weather.Point // MIND_WEATHER_HOME, 서울 when not set

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
	if h.URL != "" {
		var out struct {
			Regions []struct {
				Kind string  `json:"kind"`
				Lat  float64 `json:"lat"`
				Lon  float64 `json:"lon"`
			} `json:"regions"`
		}
		ask, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := getJSON(ask, strings.TrimRight(h.URL, "/")+"/regions", h.Token, &out)
		cancel()
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
func ParsePoint(s string) (weather.Point, error) {
	parts := strings.SplitN(strings.TrimSpace(s), ",", 3)
	if len(parts) < 2 {
		return weather.Point{}, fmt.Errorf("%q: 위도,경도[,이름] 꼴이어야 합니다", s)
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return weather.Point{}, fmt.Errorf("%q: 위도,경도[,이름] 꼴이어야 합니다", s)
	}
	p := weather.Point{Lat: lat, Lon: lon, Name: "집"}
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
		p.Name = strings.TrimSpace(parts[2])
	}
	return p, nil
}

// WeatherState is how reading the weather went, for the status screen.
type WeatherState struct {
	Home  string     `json:"home,omitempty"`  // where home was taken to be
	At    *time.Time `json:"at,omitempty"`    // the last forecast read
	Error string     `json:"error,omitempty"` // why the latest try failed
}
