// Package sky is the weather as far as the assistant knows it: read here, once
// and from one place, and passed on as a record. Nothing past this package
// calls a forecast service - the loop reads what was last said (비 스킬 reads
// the forecast record, not the sky).
package sky

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/weather"
)

// HomeKey is the name home's forecast goes by.
const HomeKey = "집"

// Reader reads what is forecast at home and where the plans are.
type Reader struct {
	Source weather.Source
	// Home is the home region, if one was made on the phone; ok is false
	// when there is none, and Fixed stands in.
	Home  func(ctx context.Context) (weather.Point, bool, error)
	Fixed weather.Point
	// MindURL is mind's read API, which knows the plans.
	MindURL string
	Token   string
	HTTP    *http.Client
}

// plan is where a plan is.
type plan struct {
	Area  string `json:"area"`
	Place string `json:"place"`
}

// Read is a forecast by name: home's under HomeKey, a plan's under the name a
// map knew it by.
func (r *Reader) Read(ctx context.Context) (any, error) {
	home := r.Fixed
	if r.Home != nil {
		p, ok, err := r.Home(ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			home = p
		}
	}
	if home.Zero() {
		return nil, errors.New("집이 어디인지 모릅니다")
	}
	f, err := r.Source.Forecast(ctx, home)
	if err != nil {
		return nil, err
	}
	out := map[string]weather.Forecast{HomeKey: f}
	plans, err := r.plans(ctx)
	if err != nil {
		// Home is told even when the plans cannot be asked for.
		return map[string]any{"forecasts": out}, nil
	}
	for _, p := range plans {
		for _, name := range weather.Names(p.Area, p.Place) {
			if _, done := out[name]; done {
				break
			}
			q, err := r.Source.Locate(ctx, name, home)
			if errors.Is(err, weather.ErrUnknown) {
				continue
			}
			if err != nil {
				break
			}
			if f, err := r.Source.Forecast(ctx, q); err == nil {
				f.Point.Name = name
				out[name] = f
			}
			break
		}
	}
	return map[string]any{"forecasts": out}, nil
}

func (r *Reader) plans(ctx context.Context) ([]plan, error) {
	if r.MindURL == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.MindURL, "/")+"/api/weather/places", nil)
	if err != nil {
		return nil, err
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mind: %d", resp.StatusCode)
	}
	var out struct {
		Places []plan `json:"places"`
	}
	return out.Places, json.NewDecoder(resp.Body).Decode(&out)
}
