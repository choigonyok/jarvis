package sensor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
)

// What the adapters leave in the stream. mind no longer goes to the calendar,
// the phones or the accounts: each reads its own outside and leaves a record
// when what it read changed (gocommon/watch), and this is the lane that takes
// them - by rule, no model.
const (
	KindCalendarNear = "calendar.near"
	KindCalendarYear = "calendar.year"
	KindVisits       = "context.visits"
	KindHoldings     = "assets.holdings"
	// Those only kept, for whoever asks: where the phones are, the regions,
	// the ways taken.
	KindNow      = "context.now"
	KindNowGuest = "context.now.guest"
	KindRegions  = "context.regions"
	KindTravel   = "context.travel"
	// KindForecast is what is forecast at home and where the plans are.
	KindForecast = "weather.forecast"
	// The last two days' photos, and the days that are over.
	KindPhotos    = "photos.recent"
	KindPhotoDays = "photos.days"
	// KindFX is the dollar's rate, only kept: a card alert in dollars is
	// estimated in won with it.
	KindFX = "assets.fx"
	// KindOutflows is what left the investment accounts and what became of it.
	KindOutflows = "assets.outflows"
	// KindWorkout is what changed in the training log (input-svc).
	KindWorkout = "workout.changes"
)

// Kinds is what the structured lane reads.
var Kinds = []string{KindCalendarNear, KindCalendarYear, KindVisits, KindHoldings, KindNow, KindNowGuest, KindRegions, KindTravel, KindForecast, KindPhotos, KindPhotoDays, KindMessage, KindFX, KindOutflows, KindWorkout, KindTurn}

// calendarRead is one read of the calendar.
type calendarRead struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Events []entry `json:"events"`
}

// visit is a stay as context-svc keeps it.
type visit struct {
	ID        int64      `json:"id"`
	Region    string     `json:"region"`
	Kind      string     `json:"kind"`
	Area      string     `json:"area"`
	EnteredAt time.Time  `json:"enteredAt"`
	LeftAt    *time.Time `json:"leftAt"`
}

// Take is one record of an adapter's: what the source says now, whole. It is
// kept as the latest of its kind and read as what it says.
func (s *Structured) Take(ctx context.Context, m queue.Message) error {
	if m.Kind == KindWorkout {
		var c struct {
			Sessions []session `json:"sessions"`
		}
		if err := json.Unmarshal(m.Payload, &c); err != nil {
			return fmt.Errorf("%w: %s: %v", queue.ErrRefused, m.Kind, err)
		}
		return s.takeWorkout(ctx, c.Sessions)
	}
	if err := s.Store.PutLatest(ctx, m.Kind, m.Payload, m.OccurredAt); err != nil {
		return err
	}
	var err error
	switch m.Kind {
	case KindCalendarNear:
		var c calendarRead
		if err = json.Unmarshal(m.Payload, &c); err == nil {
			return s.takeCalendar(ctx, c)
		}
	case KindCalendarYear:
		var c calendarRead
		if err = json.Unmarshal(m.Payload, &c); err == nil {
			return s.takeCalendarYear(ctx, c)
		}
	case KindVisits:
		var v struct {
			Visits []visit `json:"visits"`
		}
		if err = json.Unmarshal(m.Payload, &v); err == nil {
			return s.takeVisits(ctx, v.Visits)
		}
	case KindHoldings:
		var p portfolio
		if err = json.Unmarshal(m.Payload, &p); err == nil {
			return s.takeAssets(ctx, p)
		}
	case KindPhotos:
		var p struct {
			Items []asset `json:"items"`
		}
		if err = json.Unmarshal(m.Payload, &p); err == nil {
			return s.takePhotos(ctx, p.Items)
		}
	case KindPhotoDays:
		var p struct {
			Days []photoDay `json:"days"`
		}
		if err = json.Unmarshal(m.Payload, &p); err == nil {
			return s.takePhotoDays(ctx, p.Days)
		}
	case KindOutflows:
		if s.Outflows != nil {
			return s.Outflows(ctx, m.Payload)
		}
	case KindForecast:
		var f struct {
			Forecasts map[string]json.RawMessage `json:"forecasts"`
		}
		if err = json.Unmarshal(m.Payload, &f); err == nil {
			for name, raw := range f.Forecasts {
				if err := s.Store.PutLatest(ctx, mind.ForecastKey(name), raw, m.OccurredAt); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %v", queue.ErrRefused, m.Kind, err)
	}
	return nil
}
