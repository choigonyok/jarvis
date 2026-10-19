// Package weather is the day's weather where the operator lives and where
// their plans are: what fell on the days behind (for the night's reflection),
// what is forecast for the days ahead (for the morning brief and a plan out of
// doors). It reads, it never decides - the skills say what a forecast is
// worth telling.
//
// Forecasts come from Open-Meteo and places from OpenStreetMap's Nominatim;
// neither needs a key. A place is a name as a conversation or the calendar
// says it ("가평", "망원동", "자라섬 캠핑장"), found once and kept.
package weather

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Point is somewhere on the map, with the name it was found by.
type Point struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// Day is one day's weather at a point: what fell (or is forecast to), how
// likely it was, and how warm.
type Day struct {
	Date   string  `json:"date"`           // YYYY-MM-DD
	Code   int     `json:"code"`           // WMO weather code
	Precip float64 `json:"precip"`         // mm, rain and melted snow
	Snow   float64 `json:"snow,omitempty"` // cm
	Prob   int     `json:"prob"`           // %, the day's highest chance of rain or snow
	Max    float64 `json:"max"`            // °C
	Min    float64 `json:"min"`            // °C
}

// Hour is an hour of the forecast: the chance of rain and how much.
type Hour struct {
	At     time.Time `json:"at"`
	Prob   int       `json:"prob"`
	Precip float64   `json:"precip"`
}

// Forecast is the days around today at a point - the past ones as they
// turned out, the coming ones as forecast - and the next day and a half by
// the hour.
type Forecast struct {
	Point Point     `json:"point"`
	Days  []Day     `json:"days"`
	Hours []Hour    `json:"hours"`
	At    time.Time `json:"at"` // when it was read
}

// Day is the forecast's day on date (YYYY-MM-DD).
func (f Forecast) Day(date string) (Day, bool) {
	for _, d := range f.Days {
		if d.Date == date {
			return d, true
		}
	}
	return Day{}, false
}

// Zero is whether p is no point at all.
func (p Point) Zero() bool { return p.Lat == 0 && p.Lon == 0 }

// ErrUnknown is a place no map knows by that name.
var ErrUnknown = errors.New("모르는 장소")

// Source is where forecasts and places come from.
type Source interface {
	Forecast(ctx context.Context, p Point) (Forecast, error)
	// Locate finds a place by name, the one nearest `near` first when there
	// are several of that name (a zero near: anywhere); ErrUnknown when
	// nothing fits.
	Locate(ctx context.Context, name string, near Point) (Point, error)
}

// Wet is whether a day is one to carry an umbrella for: rain likely and
// more than a trace of it, or plenty of it whatever the odds, or snow.
func (d Day) Wet() bool {
	return d.Prob >= 60 && d.Precip >= 1 || d.Precip >= 5 || d.Snow > 0 && d.Prob >= 50
}

// Snowy is whether what falls is mostly snow: a centimetre of snow is
// about 0.7mm of water.
func (d Day) Snowy() bool {
	return d.Snow > 0 && d.Snow*0.7 >= d.Precip*0.5 || d.Code >= 71 && d.Code <= 77 || d.Code == 85 || d.Code == 86
}

// Sky is the WMO code in a word or two.
func Sky(code int) string {
	switch {
	case code == 0:
		return "맑음"
	case code == 1:
		return "대체로 맑음"
	case code == 2:
		return "구름 조금"
	case code == 3:
		return "흐림"
	case code == 45 || code == 48:
		return "안개"
	case code >= 51 && code <= 57:
		return "이슬비"
	case code >= 61 && code <= 67, code >= 80 && code <= 82:
		return "비"
	case code >= 71 && code <= 77, code == 85 || code == 86:
		return "눈"
	case code >= 95:
		return "뇌우"
	}
	return "흐림"
}

// Fall is what a wet day brings, said short: "비 80% · 12mm", "눈 70%".
func (d Day) Fall() string {
	what := "비"
	if d.Snowy() {
		what = "눈"
	}
	s := fmt.Sprintf("%s %d%%", what, d.Prob)
	if mm := math.Round(d.Precip); mm >= 1 && what == "비" {
		s += fmt.Sprintf(" · %.0fmm", mm)
	}
	return s
}

// Temps is "11~18°".
func (d Day) Temps() string {
	return fmt.Sprintf("%.0f~%.0f°", math.Round(d.Min), math.Round(d.Max))
}

// Line is the day in one line: "비 80% · 12mm · 11~18°" when it is wet,
// "맑음 · 11~18°" when it is not.
func (d Day) Line() string {
	if d.Wet() {
		return d.Fall() + " · " + d.Temps()
	}
	return Sky(d.Code) + " · " + d.Temps()
}

// From is when the rain starts within the hours given, from `after` on: the
// first hour whose chance is at least half. ok is false when none is.
func From(hours []Hour, after time.Time) (time.Time, bool) {
	for _, h := range hours {
		if !h.At.Before(after.Truncate(time.Hour)) && h.Prob >= 50 {
			return h.At, true
		}
	}
	return time.Time{}, false
}

// Names are the names worth looking a place up by, best first: the
// neighbourhood when there is one, then the place as said, then a campsite
// without the word for one ("자라섬 캠핑장" → "자라섬") - a map knows the
// island more often than the campsite. Nothing shorter: the first word of
// "카페 하이" is a word, not a place, and the weather of wherever a map
// files "카페" is worse than none.
func Names(area, place string) []string {
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < 2 {
			return
		}
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	add(area)
	add(place)
	bare := strings.TrimSpace(place)
	for _, w := range []string{"오토캠핑장", "캠핑장", "글램핑장", "글램핑", "카라반", "캠프"} {
		bare = strings.TrimSpace(strings.TrimSuffix(bare, w))
	}
	add(bare)
	return out
}
