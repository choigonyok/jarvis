package place

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
)

// Area is a place on the map by its name, for a reminder that waits for it
// (mind's moments): the outline OpenStreetMap has for a city, a district, a
// town, or a circle round a place that has none worth following - a station,
// a neighbourhood's point. What is asked is the name; where the phone is never
// leaves - mind holds its fixes against this.
type Area struct {
	Name   string         `json:"name"`
	Rings  [][][2]float64 `json:"rings,omitempty"`
	Lat    float64        `json:"lat,omitempty"`
	Lon    float64        `json:"lon,omitempty"`
	Radius float64        `json:"radius,omitempty"`
	// Where is the map's own account of it, for the conversation to say what
	// was found: "수원시, 경기도".
	Where string `json:"-"`
}

// The circle a place without an outline gets: half its box, within these.
const (
	minRadius = 300.0
	maxRadius = 2000.0
	// outlineFrom is how big an outline must be to be followed rather than
	// circled: a station's building is smaller than a fix is rough.
	outlineFrom = 1000.0
	// maxPoints bounds an outline kept with a reminder.
	maxPoints = 4000
)

// IsRegion is whether a name is one of the operator's regions, which a
// reminder waits for by the phone's own comings and goings.
func (m *Module) IsRegion(ctx context.Context, name string) (bool, error) {
	var regions struct {
		Regions []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"regions"`
	}
	if err := m.get(ctx, m.url+"/regions", &regions); err != nil {
		return false, fmt.Errorf("등록한 영역 목록을 읽지 못했습니다: %w", err)
	}
	for _, r := range regions.Regions {
		if r.Name == name || r.Kind != "" && r.Kind == name {
			return true, nil
		}
	}
	return false, nil
}

// FindArea finds a place by its name on the map, as it is officially named
// ("수원시", "강남구", "판교동", "수원역"): a bare "수원" is the station there
// to the map, and a village elsewhere to its search for towns.
func (m *Module) FindArea(ctx context.Context, name string) (Area, error) {
	name = strings.TrimSpace(name)
	q := url.Values{"q": {name}, "format": {"jsonv2"}, "limit": {"1"}, "countrycodes": {"kr"}, "accept-language": {"ko"},
		"polygon_geojson": {"1"}, "polygon_threshold": {"0.0005"}}
	var hits []struct {
		Name        string          `json:"name"`
		DisplayName string          `json:"display_name"`
		Lat         string          `json:"lat"`
		Lon         string          `json:"lon"`
		BoundingBox []string        `json:"boundingbox"`
		GeoJSON     json.RawMessage `json:"geojson"`
	}
	if err := m.getPublic(ctx, nominatimURL+"?"+q.Encode(), &hits); err != nil {
		return Area{}, fmt.Errorf("지도(OpenStreetMap)에 묻지 못했습니다: %w", err)
	}
	if len(hits) == 0 {
		return Area{}, fmt.Errorf("지도에서 '%s'를 찾지 못했습니다", name)
	}
	h := hits[0]
	a := Area{Name: firstOf(h.Name, name), Where: where(h.DisplayName)}
	var box [4]float64 // min lat, max lat, min lon, max lon
	for i := range min(4, len(h.BoundingBox)) {
		fmt.Sscan(h.BoundingBox[i], &box[i])
	}
	across := meters(box[0], box[2], box[1], box[3])
	if rings := outline(h.GeoJSON); len(rings) > 0 && across >= outlineFrom {
		a.Rings = thin(rings, maxPoints)
		return a, nil
	}
	fmt.Sscan(h.Lat, &a.Lat)
	fmt.Sscan(h.Lon, &a.Lon)
	if a.Lat == 0 && a.Lon == 0 {
		return Area{}, fmt.Errorf("지도에서 '%s'의 자리를 읽지 못했습니다", name)
	}
	a.Radius = math.Round(math.Min(maxRadius, math.Max(minRadius, across/2)))
	return a, nil
}

// outline is a GeoJSON polygon's outer rings: one for a Polygon, one a part
// for a MultiPolygon. Anything else has none.
func outline(raw json.RawMessage) [][][2]float64 {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if json.Unmarshal(raw, &g) != nil {
		return nil
	}
	switch g.Type {
	case "Polygon":
		var p [][][2]float64
		if json.Unmarshal(g.Coordinates, &p) == nil && len(p) > 0 && len(p[0]) >= 3 {
			return [][][2]float64{p[0]}
		}
	case "MultiPolygon":
		var mp [][][][2]float64
		if json.Unmarshal(g.Coordinates, &mp) != nil {
			return nil
		}
		var out [][][2]float64
		for _, p := range mp {
			if len(p) > 0 && len(p[0]) >= 3 {
				out = append(out, p[0])
			}
		}
		return out
	}
	return nil
}

// thin keeps every nth point of rings over n points in all, so an outline
// stays a size a reminder can carry; a ring keeps three at the least.
func thin(rings [][][2]float64, n int) [][][2]float64 {
	total := 0
	for _, r := range rings {
		total += len(r)
	}
	if total <= n {
		return rings
	}
	step := (total + n - 1) / n
	out := make([][][2]float64, 0, len(rings))
	for _, r := range rings {
		var kept [][2]float64
		for i := 0; i < len(r); i += step {
			kept = append(kept, r[i])
		}
		if len(kept) >= 3 {
			out = append(out, kept)
		}
	}
	return out
}

// where is the map's account of a place cut to what says where it is: the
// place, and the two names above it ("수원시, 경기도").
func where(display string) string {
	parts := strings.Split(display, ", ")
	var kept []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "대한민국" || strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			continue // the country, a house number, a postcode
		}
		kept = append(kept, p)
	}
	if len(kept) > 3 {
		kept = append(kept[:1], kept[len(kept)-2:]...)
	}
	return strings.Join(kept, ", ")
}
