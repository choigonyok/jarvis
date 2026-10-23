package transit

import (
	_ "embed"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// The subway stations the real-time feed may know, kept with the binary
// (stations.py builds stations.json from Wikidata): each by the name the feed
// knows it by, where it is, and on each line - the feed's subwayId - the
// stations next to it. With them a station is found by name or nearness, and
// its directions offered, while no train runs for the feed to tell of; and a
// train is known to go a route's way when the station it stops at next is
// the one the route names, or lies beyond it.
//
//go:embed stations.json
var stationsJSON []byte

// Station is one of them.
type Station struct {
	Name  string              `json:"n"`
	Point [2]float64          `json:"p"` // latitude, longitude
	Lines map[string][]string `json:"l"`
}

var stations = sync.OnceValue(func() map[string]Station {
	var list []Station
	if err := json.Unmarshal(stationsJSON, &list); err != nil {
		panic("transit: stations.json: " + err.Error())
	}
	byName := make(map[string]Station, len(list))
	for _, s := range list {
		byName[s.Name] = s
	}
	return byName
})

func (s Station) stop() Stop {
	return Stop{Kind: Subway, ID: s.Name, Name: s.Name + "역", Lat: s.Point[0], Lon: s.Point[1]}
}

// stationsNear is the stations within radius meters of a point, nearest first.
func stationsNear(lat, lon, radius float64) []Stop {
	var out []Stop
	for _, s := range stations() {
		if d := Meters(lat, lon, s.Point[0], s.Point[1]); d <= radius {
			st := s.stop()
			st.Dist = int(d)
			out = append(out, st)
		}
	}
	slices.SortFunc(out, func(a, b Stop) int { return a.Dist - b.Dist })
	return out
}

// stationsNamed is the station named q (강남역 or 강남), then a few whose
// names begin with it (강남구청).
func stationsNamed(q string) []Stop {
	name := stationName(q)
	if name == "" {
		return nil
	}
	var out, more []Stop
	if s, ok := stations()[name]; ok {
		out = append(out, s.stop())
	}
	for _, s := range stations() {
		if s.Name != name && strings.HasPrefix(s.Name, name) {
			more = append(more, s.stop())
		}
	}
	slices.SortFunc(more, func(a, b Stop) int { return strings.Compare(a.Name, b.Name) })
	return append(out, more[:min(len(more), 4)]...)
}

// stationLines is the ways from a station: on each of its lines, toward each
// station next to it - 2호선, 역삼 방면.
func stationLines(name string) []Line {
	s, ok := stations()[name]
	if !ok {
		return nil
	}
	var out []Line
	for _, id := range slices.Sorted(maps.Keys(s.Lines)) {
		for _, next := range s.Lines[id] {
			out = append(out, Line{ID: id + ">" + next, Name: LineName(id), Toward: next + " 방면"})
		}
	}
	return out
}

// heading is whether a train on line at station, stopping next at next, goes
// by via - the station after this one on a route: next is via, or lies a few
// stations beyond it, as for an express that passes via without stopping. A
// train the other way, or onto another branch, does not.
func heading(station, line, via, next string) bool {
	if next == via {
		return true
	}
	seen := map[string]bool{station: true, via: true}
	frontier := []string{via}
	for range 6 {
		var further []string
		for _, s := range frontier {
			for _, n := range stations()[s].Lines[line] {
				if n == next {
					return true
				}
				if !seen[n] {
					seen[n] = true
					further = append(further, n)
				}
			}
		}
		frontier = further
	}
	return false
}

// takes is the arrivals a route boards. On a bus, its route's. On the subway,
// its line's going its way - Line.ID "1002>역삼", the line and the station
// after this one - judged by where each train stops next; a train whose next
// stop is not a station known here goes by the direction (updnLine) of the
// trains that are known to go that way.
func takes(r Route, arrivals []Arrival) []Arrival {
	line, via, ok := strings.Cut(r.Line.ID, ">")
	var mine []Arrival
	if r.Stop.Kind != Subway || !ok {
		for _, a := range arrivals {
			if a.Line.ID == r.Line.ID {
				mine = append(mine, a)
			}
		}
		return mine
	}
	var unsure []Arrival
	ways := map[string]bool{}
	for _, a := range arrivals {
		on, next, _ := strings.Cut(a.Line.ID, ">")
		if on != line {
			continue
		}
		_, known := stations()[next]
		switch {
		case heading(r.Stop.ID, line, via, next):
			mine = append(mine, a)
			if a.Way != "" {
				ways[a.Way] = true
			}
		case !known:
			unsure = append(unsure, a)
		}
	}
	for _, a := range unsure {
		if a.Way != "" && ways[a.Way] {
			mine = append(mine, a)
		}
	}
	return mine
}

// The feed's trainLineNm, "성수행 - 역삼방면": where a train is bound, and the
// station it stops at next.
var bound = regexp.MustCompile(`-\s*(.+?)\s*방면`)

// nextStation is the station a train stops at next, from its trainLineNm, or
// "" if it does not say.
func nextStation(trainLine string) string {
	if m := bound.FindStringSubmatch(trainLine); m != nil {
		return stationName(m[1])
	}
	return ""
}
