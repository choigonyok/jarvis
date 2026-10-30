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

// The subway stations the real-time feed knows, kept with the binary
// (stations.py builds stations.json from the feed's own every-station
// answer, and Wikidata for where each is): each by the exact name the feed
// knows it by - 천호(풍납토성), not 천호 - and on each line (the feed's
// subwayId) the stations next to it, as the feed's trains run. With them a
// station is found by name or nearness, and its directions offered, while no
// train runs for the feed to tell of; and a train is known to go a route's
// way when the station it stops at next is the one the route names, or lies
// beyond it.
//
//go:embed stations.json
var stationsJSON []byte

// Station is one of them.
type Station struct {
	Name  string              `json:"n"`
	Point [2]float64          `json:"p"` // latitude, longitude; zero when not known
	Lines map[string][]string `json:"l"`
}

// stationKeys is the words a station is found by: its name without the
// bracketed part or a closing 역 (천호 for 천호(풍납토성), 서울 for 서울역),
// and the bracketed part itself (이수 for 총신대입구(이수)).
func stationKeys(n string) []string {
	keys := []string{stationName(n)}
	if i := strings.Index(n, "("); i > 0 && strings.HasSuffix(n, ")") {
		if alias := strings.TrimSpace(n[i+1 : len(n)-1]); alias != "" {
			keys = append(keys, alias)
		}
	}
	return keys
}

// displayName is a station as a rider reads it: "천호역 (풍납토성)",
// "신촌역 (경의중앙선)", "강남역".
func displayName(n string) string {
	s := stationName(n) + "역"
	if keys := stationKeys(n); len(keys) > 1 {
		s += " (" + keys[1] + ")"
	}
	return s
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
	return Stop{Kind: Subway, ID: s.Name, Name: displayName(s.Name), Lat: s.Point[0], Lon: s.Point[1]}
}

// stationsNear is the stations within radius meters of a point, nearest first.
func stationsNear(lat, lon, radius float64) []Stop {
	var out []Stop
	for _, s := range stations() {
		if s.Point == [2]float64{} {
			continue
		}
		if d := Meters(lat, lon, s.Point[0], s.Point[1]); d <= radius {
			st := s.stop()
			st.Dist = int(d)
			out = append(out, st)
		}
	}
	slices.SortFunc(out, func(a, b Stop) int { return a.Dist - b.Dist })
	return out
}

// stationsNamed is the stations named q - 강남역 or 강남, 천호 for
// 천호(풍납토성), 이수 for 총신대입구(이수) - then a few whose names begin
// with it (강남구청).
func stationsNamed(q string) []Stop {
	name := stationName(q)
	if name == "" {
		return nil
	}
	var out, more []Stop
	for _, s := range stations() {
		keys := stationKeys(s.Name)
		switch {
		case slices.Contains(keys, name):
			out = append(out, s.stop())
		case slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, name) }):
			more = append(more, s.stop())
		}
	}
	byID := func(a, b Stop) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(out, byID)
	slices.SortFunc(more, byID)
	return append(out, more[:min(len(more), 4)]...)
}

// stationLines is the ways from a station: on each of its lines, toward each
// station next to it - 2호선, 역삼 방면; 5호선, 천호 방면 for 천호(풍납토성).
func stationLines(name string) []Line {
	s, ok := stations()[name]
	if !ok {
		return nil
	}
	var out []Line
	for _, id := range slices.Sorted(maps.Keys(s.Lines)) {
		for _, next := range s.Lines[id] {
			out = append(out, Line{ID: id + ">" + next, Name: LineName(id), Toward: stationName(next) + " 방면"})
		}
	}
	return out
}

// heading is whether a train on line at station, stopping next at next, goes
// by via - the station after this one on a route: next is via, or lies a few
// stations beyond it, as for an express that passes via without stopping. A
// train the other way, onto another branch, or ending here (its next stop
// this station itself: "광교행 - 광교방면" at 광교) does not.
func heading(station, line, via, next string) bool {
	if next == station {
		return false
	}
	if next == via {
		return true
	}
	seen := map[string]bool{station: true, via: true}
	frontier := []string{via}
	for range 6 {
		var further []string
		for _, s := range frontier {
			for _, n := range stations()[s].Lines[line] {
				if seen[n] {
					continue
				}
				if n == next {
					return true
				}
				seen[n] = true
				further = append(further, n)
			}
		}
		frontier = further
	}
	return false
}

// takes is the arrivals a route boards. On a bus, its route's going its way
// (oneWay). On the subway, its line's going its way - Line.ID "1002>역삼", the
// line and the station after this one - judged by where each train stops
// next; a train whose next stop is not a station known here goes by the
// direction (updnLine) of the trains that are known to go that way.
func takes(r Route, arrivals []Arrival) []Arrival {
	line, via, ok := strings.Cut(r.Line.ID, ">")
	var mine []Arrival
	if r.Stop.Kind != Subway || !ok {
		for _, a := range arrivals {
			if a.Line.ID == r.Line.ID {
				mine = append(mine, a)
			}
		}
		return oneWay(r.Line, mine)
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

// oneWay is, of a bus route's entries at a stop, the buses going the way
// wanted: a route that passes a stop both ways is there twice under one id,
// told apart by its order on the route, or - saved before that was kept - by
// where it is going. A direction with nothing on it counts (Idle): that the
// other one has buses is no reason to show those. When neither matches the
// feed has renumbered the route, and every bus stays: one going the other way
// is better seen than a route that went blank.
func oneWay(want Line, entries []Arrival) []Arrival {
	way := entries
	for _, same := range []func(Line) bool{
		func(l Line) bool { return want.Order != 0 && l.Order == want.Order },
		func(l Line) bool { return want.Toward != "" && l.Toward == want.Toward },
	} {
		if !slices.ContainsFunc(entries, func(a Arrival) bool { return same(a.Line) }) {
			continue
		}
		way = slices.DeleteFunc(slices.Clone(entries), func(a Arrival) bool { return !same(a.Line) })
		break
	}
	return slices.DeleteFunc(slices.Clone(way), func(a Arrival) bool { return a.Idle })
}

// The feed's trainLineNm, "성수행 - 역삼방면": where a train is bound, and the
// station it stops at next.
var bound = regexp.MustCompile(`-\s*(.+?)\s*방면`)

// nextStation is the station a train stops at next, from its trainLineNm -
// by the feed's own name, as the stations here are kept - or "" if it does
// not say.
func nextStation(trainLine string) string {
	if m := bound.FindStringSubmatch(trainLine); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
