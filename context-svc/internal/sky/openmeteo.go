package sky

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/weather"
)

// OpenMeteo reads forecasts from Open-Meteo and finds places with
// Nominatim. Both are free and keyless; both are asked as little as the
// answers allow: a forecast is kept for half an hour, a place for a month
// (one no map knows, for a day), and Nominatim is never asked more than
// once a second - its rules.
type OpenMeteo struct {
	// Forecasts and Places are the two services' search URLs; empty: the
	// public ones.
	Forecasts string
	Places    string
	// Agent names this program to Nominatim, as its rules ask.
	Agent string
	// Every is the least time between two Nominatim requests; 0: a second
	// and a little.
	Every  time.Duration
	Zone   string // the time zone days are cut in (Asia/Seoul)
	Client *http.Client
	Now    func() time.Time

	mu        sync.Mutex
	forecasts map[string]weather.Forecast
	places    map[string]place
	// down is when a service last failed to answer, and why: for a while
	// after, the answer is that failure, at once - a morning asks about
	// every plan and day and must not wait out a timeout for each.
	down   map[string]place
	lookup sync.Mutex // one Nominatim request at a time
	last   time.Time  // when Nominatim was last asked
}

type place struct {
	p   weather.Point
	err error
	at  time.Time
}

const (
	forecastTTL = 30 * time.Minute
	placeTTL    = 30 * 24 * time.Hour
	missTTL     = 24 * time.Hour
	downTTL     = 5 * time.Minute
	// PastDays is how far back a forecast reaches: the reflection's sixty
	// days, and some.
	PastDays = 92
)

func (o *OpenMeteo) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *OpenMeteo) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Forecast is the days around today at p (PastDays back, sixteen ahead) and
// the next day and a half by the hour.
func (o *OpenMeteo) Forecast(ctx context.Context, p weather.Point) (weather.Forecast, error) {
	key := fmt.Sprintf("%.2f,%.2f", p.Lat, p.Lon)
	o.mu.Lock()
	if f, ok := o.forecasts[key]; ok && o.now().Sub(f.At) < forecastTTL {
		o.mu.Unlock()
		f.Point = p
		return f, nil
	}
	o.mu.Unlock()
	if err := o.stillDown("forecast"); err != nil {
		return weather.Forecast{}, err
	}

	base := o.Forecasts
	if base == "" {
		base = "https://api.open-meteo.com/v1/forecast"
	}
	zone := o.Zone
	if zone == "" {
		zone = "Asia/Seoul"
	}
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(p.Lat, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(p.Lon, 'f', 4, 64))
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,snowfall_sum,precipitation_probability_max")
	q.Set("hourly", "precipitation_probability,precipitation")
	q.Set("forecast_hours", "36")
	q.Set("past_days", strconv.Itoa(PastDays))
	q.Set("forecast_days", "16")
	q.Set("timezone", zone)
	var out struct {
		Daily struct {
			Time   []string   `json:"time"`
			Code   []*int     `json:"weather_code"`
			Max    []*float64 `json:"temperature_2m_max"`
			Min    []*float64 `json:"temperature_2m_min"`
			Precip []*float64 `json:"precipitation_sum"`
			Snow   []*float64 `json:"snowfall_sum"`
			Prob   []*float64 `json:"precipitation_probability_max"`
		} `json:"daily"`
		Hourly struct {
			Time   []string   `json:"time"`
			Prob   []*float64 `json:"precipitation_probability"`
			Precip []*float64 `json:"precipitation"`
		} `json:"hourly"`
		Reason string `json:"reason"`
	}
	if err := o.get(ctx, base+"?"+q.Encode(), "", &out); err != nil {
		o.wentDown("forecast", err)
		return weather.Forecast{}, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}
	f := weather.Forecast{Point: p, At: o.now()}
	d := out.Daily
	for i, date := range d.Time {
		// A day the model has nothing for (the far end of the range) is not a
		// dry day: it is not a day.
		if i >= len(d.Max) || d.Max[i] == nil {
			continue
		}
		f.Days = append(f.Days, weather.Day{Date: date, Code: at(d.Code, i), Precip: num(d.Precip, i), Snow: num(d.Snow, i),
			Prob: int(math.Round(num(d.Prob, i))), Max: num(d.Max, i), Min: num(d.Min, i)})
	}
	h := out.Hourly
	for i, t := range h.Time {
		at, err := time.ParseInLocation("2006-01-02T15:04", t, loc)
		if err != nil {
			continue
		}
		f.Hours = append(f.Hours, weather.Hour{At: at, Prob: int(math.Round(num(h.Prob, i))), Precip: num(h.Precip, i)})
	}
	if len(f.Days) == 0 {
		return weather.Forecast{}, fmt.Errorf("예보에 날이 없습니다%s", suffix(out.Reason))
	}
	o.mu.Lock()
	if o.forecasts == nil {
		o.forecasts = map[string]weather.Forecast{}
	}
	o.forecasts[key] = f
	o.mu.Unlock()
	return f, nil
}

// Locate finds name on the map, preferring what is within a day's drive of
// near: there are two 자라섬, and the campsite is on the one near Seoul.
// What a map files as a road, a bus stop or a stream shares its name with a
// town often enough ("성수동" is a bus stop on Jeju) to be no answer at all.
func (o *OpenMeteo) Locate(ctx context.Context, name string, near weather.Point) (weather.Point, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return weather.Point{}, weather.ErrUnknown
	}
	key := name
	if !near.Zero() {
		key += fmt.Sprintf("@%.0f,%.0f", near.Lat, near.Lon)
	}
	if p, err, ok := o.kept(key); ok {
		return p, err
	}
	if err := o.stillDown("places"); err != nil {
		return weather.Point{}, err
	}
	o.lookup.Lock()
	defer o.lookup.Unlock()
	// Someone else may have looked it up while this waited its turn.
	if p, err, ok := o.kept(key); ok {
		return p, err
	}
	every := o.Every
	if every == 0 {
		every = 1100 * time.Millisecond
	}
	if wait := time.Until(o.last.Add(every)); wait > 0 {
		select {
		case <-ctx.Done():
			return weather.Point{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	base := o.Places
	if base == "" {
		base = "https://nominatim.openstreetmap.org/search"
	}
	q := url.Values{}
	q.Set("q", name)
	q.Set("format", "jsonv2")
	q.Set("limit", "5")
	q.Set("accept-language", "ko")
	if !near.Zero() {
		// Preferred, not required: a trip to 강릉 is still 강릉.
		q.Set("viewbox", fmt.Sprintf("%.2f,%.2f,%.2f,%.2f", near.Lon-1.5, near.Lat+1.5, near.Lon+1.5, near.Lat-1.5))
		q.Set("bounded", "0")
	}
	var hits []struct {
		Lat      string `json:"lat"`
		Lon      string `json:"lon"`
		Category string `json:"category"`
		Name     string `json:"name"`
	}
	err := o.get(ctx, base+"?"+q.Encode(), o.Agent, &hits)
	o.last = time.Now()
	if err != nil {
		o.wentDown("places", err) // the name is not kept: the next try may reach it
		return weather.Point{}, err
	}
	found := weather.Point{}
	err = weather.ErrUnknown
	for _, h := range hits {
		if h.Category == "highway" || h.Category == "waterway" {
			continue
		}
		lat, e1 := strconv.ParseFloat(h.Lat, 64)
		lon, e2 := strconv.ParseFloat(h.Lon, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		found, err = weather.Point{Name: name, Lat: lat, Lon: lon}, nil
		break
	}
	o.mu.Lock()
	if o.places == nil {
		o.places = map[string]place{}
	}
	o.places[key] = place{p: found, err: err, at: o.now()}
	o.mu.Unlock()
	return found, err
}

func (o *OpenMeteo) stillDown(service string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d, ok := o.down[service]; ok && o.now().Sub(d.at) < downTTL {
		return d.err
	}
	return nil
}

func (o *OpenMeteo) wentDown(service string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.down == nil {
		o.down = map[string]place{}
	}
	o.down[service] = place{err: err, at: o.now()}
}

// kept is a place looked up before, while it is still good.
func (o *OpenMeteo) kept(name string) (weather.Point, error, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	c, ok := o.places[name]
	ttl := placeTTL
	if c.err != nil {
		ttl = missTTL
	}
	if !ok || o.now().Sub(c.at) >= ttl {
		return weather.Point{}, nil, false
	}
	return c.p, c.err, true
}

func (o *OpenMeteo) get(ctx context.Context, u, agent string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if agent != "" {
		req.Header.Set("User-Agent", agent)
	}
	res, err := o.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %d %s", hostOf(u), res.StatusCode, strings.TrimSpace(string(body[:min(len(body), 200)])))
	}
	return json.Unmarshal(body, out)
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return u
}

func suffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

func num(xs []*float64, i int) float64 {
	if i < len(xs) && xs[i] != nil {
		return *xs[i]
	}
	return 0
}

func at(xs []*int, i int) int {
	if i < len(xs) && xs[i] != nil {
		return *xs[i]
	}
	return 0
}
