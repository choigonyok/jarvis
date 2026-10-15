package place

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Health is one body measure against the operator's own usual: what the
// last reading was, the median of the days before it, and how far apart.
// "Usual" is the person's own, never a population's - a resting heart rate
// of 70 is unremarkable unless it is usually 58.
type Health struct {
	Kind     string    `json:"kind"` // health.hrv, health.sleep, health.hr_rest, health.steps
	Name     string    `json:"name"`
	Value    float64   `json:"value"`
	Baseline float64   `json:"baseline"`
	Change   float64   `json:"change"` // fraction: -0.16 is 16% below usual
	Days     int       `json:"days"`   // days the baseline rests on
	At       time.Time `json:"at"`
}

// healthNames are the measures a shortcut sends, as they are said.
var healthNames = map[string]string{
	"health.hrv": "심박 변이(HRV)", "health.hr_rest": "안정 시 심박", "health.sleep": "수면 시간", "health.steps": "걸음 수",
}

// HealthName is a measure's name, or its kind.
func HealthName(kind string) string {
	if n, ok := healthNames[kind]; ok {
		return n
	}
	return kind
}

// BaselineDays: a baseline needs this many days of readings to be said.
const BaselineDays = 7

// Reading is one numeric report of a measure.
type Reading struct {
	At    time.Time
	Value float64
}

// Baseline is the latest reading against the median of one reading a day
// (the day's last) over the 28 days before it.
func Baseline(kind string, readings []Reading, loc *time.Location) (Health, bool) {
	if len(readings) == 0 {
		return Health{}, false
	}
	last := readings[len(readings)-1]
	lastDay := last.At.In(loc).Format("2006-01-02")
	byDay := map[string]float64{}
	for _, r := range readings {
		d := r.At.In(loc).Format("2006-01-02")
		if d == lastDay || last.At.Sub(r.At) > 28*24*time.Hour {
			continue
		}
		byDay[d] = r.Value
	}
	if len(byDay) < BaselineDays {
		return Health{}, false
	}
	vals := make([]float64, 0, len(byDay))
	for _, v := range byDay {
		vals = append(vals, v)
	}
	slices.Sort(vals)
	base := vals[len(vals)/2]
	if len(vals)%2 == 0 {
		base = (vals[len(vals)/2-1] + vals[len(vals)/2]) / 2
	}
	h := Health{Kind: kind, Name: HealthName(kind), Value: last.Value, Baseline: base, Days: len(byDay), At: last.At}
	if base != 0 {
		h.Change = math.Round((last.Value-base)/base*100) / 100
	}
	return h, true
}

// Notable is whether a measure is far enough from usual to say: 15%.
func (h Health) Notable() bool { return math.Abs(h.Change) >= 0.15 }

// Line is a measure in words: "심박 변이(HRV) 38 (평소 45보다 16% 낮음)".
func (h Health) Line() string {
	way := "높음"
	if h.Change < 0 {
		way = "낮음"
	}
	return fmt.Sprintf("%s %s (평소 %s보다 %.0f%% %s)", h.Name, num(h.Value), num(h.Baseline), math.Abs(h.Change)*100, way)
}

func num(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}
