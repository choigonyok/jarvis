package weather

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Service is the weather the loop asks for: at home, or where a plan is.
type Service struct {
	Source Source
	// Home is where the operator lives; an error when it is not known.
	Home func(ctx context.Context) (Point, error)

	mu      sync.Mutex
	at      time.Time // the last forecast read
	err     string    // why the latest one failed
	address string    // where home was taken to be
}

// At is the weather on date where a plan is, found by its neighbourhood or
// its place (Names) - or at home when it names neither - with the hours of
// the next day and a half. ok is false when the place is not on the map or
// the forecast does not reach the day.
func (s *Service) At(ctx context.Context, area, place, date string) (Day, []Hour, bool) {
	if s == nil || s.Source == nil {
		return Day{}, nil, false
	}
	home, err := s.home(ctx)
	if err != nil && area == "" && place == "" {
		return Day{}, nil, false
	}
	p := home
	if area != "" || place != "" {
		found := false
		for _, name := range Names(area, place) {
			if q, err := s.Source.Locate(ctx, name, home); err == nil {
				p, found = q, true
				break
			} else if !errors.Is(err, ErrUnknown) {
				s.failed(err)
				return Day{}, nil, false
			}
		}
		if !found {
			return Day{}, nil, false
		}
	}
	f, err := s.forecast(ctx, p)
	if err != nil {
		return Day{}, nil, false
	}
	d, ok := f.Day(date)
	return d, f.Hours, ok
}

// HomeDays is the days in [from, to) at home, by date: as they turned out
// for the days behind, as forecast for the days ahead.
func (s *Service) HomeDays(ctx context.Context, from, to string) (map[string]Day, error) {
	if s == nil || s.Source == nil {
		return nil, errors.New("날씨가 연결되어 있지 않습니다")
	}
	home, err := s.home(ctx)
	if err != nil {
		return nil, err
	}
	f, err := s.forecast(ctx, home)
	if err != nil {
		return nil, err
	}
	out := map[string]Day{}
	for _, d := range f.Days {
		if d.Date >= from && d.Date < to {
			out[d.Date] = d
		}
	}
	return out, nil
}

// State is how reading the weather went last: when a forecast was last
// read, why the latest try failed, and where home was taken to be.
func (s *Service) State() (time.Time, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at, s.err, s.address
}

func (s *Service) home(ctx context.Context) (Point, error) {
	if s.Home == nil {
		return Point{}, errors.New("집이 어디인지 모릅니다")
	}
	p, err := s.Home(ctx)
	if err != nil {
		s.failed(err)
		return Point{}, err
	}
	s.mu.Lock()
	s.address = p.Name
	s.mu.Unlock()
	return p, nil
}

func (s *Service) forecast(ctx context.Context, p Point) (Forecast, error) {
	f, err := s.Source.Forecast(ctx, p)
	if err != nil {
		s.failed(err)
		return f, err
	}
	s.mu.Lock()
	s.at, s.err = f.At, ""
	s.mu.Unlock()
	return f, nil
}

func (s *Service) failed(err error) {
	s.mu.Lock()
	s.err = err.Error()
	s.mu.Unlock()
}

// Static is fixed weather, for evaluation and tests: the days and hours of
// each place by its name, the operator's home under "".
type Static struct {
	Days  map[string][]Day  `json:"days"`
	Hours map[string][]Hour `json:"hours,omitempty"`
	At    time.Time         `json:"-"`
}

func (s Static) Forecast(_ context.Context, p Point) (Forecast, error) {
	days, ok := s.Days[p.Name]
	if !ok {
		return Forecast{}, ErrUnknown
	}
	return Forecast{Point: p, Days: days, Hours: s.Hours[p.Name], At: s.At}, nil
}

func (s Static) Locate(_ context.Context, name string, _ Point) (Point, error) {
	if _, ok := s.Days[name]; ok && name != "" {
		return Point{Name: name}, nil
	}
	return Point{}, ErrUnknown
}

// StaticHome is home as Static keeps it.
func StaticHome(context.Context) (Point, error) { return Point{Name: ""}, nil }
