// Package place is the arithmetic of where: distance, which regions a fix
// is in, and how the phone should be told to watch - kept free of storage
// so it can be tested by itself.
package place

import (
	"math"
	"slices"
	"strings"
)

// Region is a named circle: home, work, a campsite.
type Region struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"` // home | work | camp | shop | food | other
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	RadiusM int     `json:"radiusM"`
	Area    string  `json:"area,omitempty"`
}

// KindOf guesses a region's kind from its name, for a region made on the
// phone: 집 is home, 회사 is work, and so on.
func KindOf(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	// Home is the operator's own: "지수네 집" is somewhere else.
	switch strings.ReplaceAll(n, " ", "") {
	case "집", "우리집", "home", "myhome", "내집":
		return "home"
	}
	for _, k := range []struct {
		kind  string
		words []string
	}{
		{"work", []string{"회사", "직장", "사무실", "office", "work"}},
		{"camp", []string{"캠핑", "camp"}},
		{"shop", []string{"마트", "편의점", "시장", "mart"}},
		{"food", []string{"식당", "카페", "cafe"}},
	} {
		for _, w := range k.words {
			if strings.Contains(n, w) {
				return k.kind
			}
		}
	}
	return "other"
}

// Meters between two points (haversine).
func Meters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dlat, dlon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// Inside is the regions a fix is in, given its accuracy: a fix whose error
// circle is mostly outside a small region is not counted in it.
func Inside(regions []Region, lat, lon, acc float64) []string {
	var out []string
	for _, r := range regions {
		radius := float64(max(r.RadiusM, 50))
		if Meters(lat, lon, r.Lat, r.Lon) <= radius+math.Min(acc, radius)/2 {
			out = append(out, r.Name)
		}
	}
	slices.Sort(out)
	return out
}

// Monitoring modes of OwnTracks.
const (
	Quiet       = -1
	Manual      = 0
	Significant = 1
	Move        = 2
)

// Mode is how the phone should watch now, and whether that is a change.
// Leaving home or work, follow closely (Move) - that is when where-am-I
// questions come; arriving anywhere known, back to Significant, which costs
// little battery. A low battery overrides both.
func Mode(current int, event, regionKind string, batt int, charging bool) int {
	want := current
	switch {
	case event == "leave" && (regionKind == "home" || regionKind == "work"):
		want = Move
	case event == "enter":
		want = Significant
	}
	if batt > 0 && batt < 20 && !charging && want == Move {
		want = Significant
	}
	if batt > 0 && batt < 8 && !charging {
		want = Quiet
	}
	return want
}

// Command is the reply that sets the phone's monitoring mode.
func Command(mode int) []map[string]any {
	return []map[string]any{{
		"_type": "cmd", "action": "setConfiguration",
		"configuration": map[string]any{"_type": "configuration", "monitoring": mode},
	}}
}
