package place

import (
	"slices"
	"sort"
	"time"
)

// Stay is a visit as trips are worked out from it: where, and when.
type Stay struct {
	Region string
	Area   string
	In     time.Time
	Out    *time.Time
}

// Trip is the way between two stays: left one, arrived at the next.
type Trip struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	Left    time.Time `json:"left"`
	Arrived time.Time `json:"arrived"`
	Minutes int       `json:"minutes"`
}

// Route is the trips between two places, summed up: how long the way
// usually takes (the median, so one detour does not move it) and when it
// is usually set out on.
type Route struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Trips   int    `json:"trips"`
	Minutes int    `json:"minutes"` // median
	Leave   string `json:"leave"`   // median departure, HH:MM
}

// MaxTrip is the longest gap still read as a trip: longer, and something
// happened between that no region saw.
const MaxTrip = 3 * time.Hour

// Trips is the actual journeys between regions: a stay left, the next stay
// entered within MaxTrip. A place is named by its area when it has one
// ("강남"), as plans name places, else by the region. Regions inside one
// another (a building in a neighbourhood) are not a trip.
func Trips(stays []Stay) []Trip {
	s := slices.Clone(stays)
	sort.Slice(s, func(i, j int) bool { return s[i].In.Before(s[j].In) })
	var out []Trip
	for i, a := range s {
		if a.Out == nil {
			continue
		}
		// The first stay entered after a was left.
		for _, b := range s[i+1:] {
			if b.In.Before(*a.Out) {
				continue // entered while still in a: nested, not a trip
			}
			gap := b.In.Sub(*a.Out)
			from, to := name(a), name(b)
			if gap <= MaxTrip && from != to {
				out = append(out, Trip{From: from, To: to, Left: *a.Out, Arrived: b.In, Minutes: int(gap.Round(time.Minute).Minutes())})
			}
			break
		}
	}
	return out
}

func name(s Stay) string {
	if s.Area != "" {
		return s.Area
	}
	return s.Region
}

// Routes sums trips up by way, most travelled first.
func Routes(trips []Trip, loc *time.Location) []Route {
	by := map[[2]string][]Trip{}
	for _, t := range trips {
		k := [2]string{t.From, t.To}
		by[k] = append(by[k], t)
	}
	var out []Route
	for k, ts := range by {
		mins := make([]int, len(ts))
		leaves := make([]int, len(ts))
		for i, t := range ts {
			mins[i] = t.Minutes
			l := t.Left.In(loc)
			leaves[i] = l.Hour()*60 + l.Minute()
		}
		leave := median(leaves)
		out = append(out, Route{From: k[0], To: k[1], Trips: len(ts), Minutes: median(mins),
			Leave: time.Date(2000, 1, 1, leave/60, leave%60, 0, 0, time.UTC).Format("15:04")})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Trips != out[j].Trips {
			return out[i].Trips > out[j].Trips
		}
		return out[i].From+out[i].To < out[j].From+out[j].To
	})
	return out
}

func median(xs []int) int {
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
