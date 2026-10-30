package sensor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
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
)

// Kinds is what the structured lane reads.
var Kinds = []string{KindCalendarNear, KindCalendarYear, KindVisits, KindHoldings, KindNow, KindNowGuest, KindRegions, KindTravel, KindMessage}

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
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %v", queue.ErrRefused, m.Kind, err)
	}
	return nil
}
