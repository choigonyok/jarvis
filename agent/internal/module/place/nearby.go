package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Nearby shops come from OpenStreetMap - free, no key, read only: Overpass
// for what is around a point, Nominatim for a place's name when it is not
// one of the operator's regions. What leaves here is a point rounded to
// about a hundred metres (or the name asked about), never the fix itself.
// Korea's map has gaps, so these are candidates with where they came from,
// not a complete list.

// Shop is one place found.
type Shop struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`              // restaurant, cafe, … (and cuisine)
	Meters  int    `json:"meters"`            // from the point searched
	Address string `json:"address,omitempty"` // as the map has it
	Hours   string `json:"hours,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Link    string `json:"link"` // on openstreetmap.org
}

// Point is where a search is around.
type Point struct {
	Lat, Lon float64
	From     string // how it was found: 지금 위치 · 영역 · 지도 검색
}

const userAgent = "jarvis-personal/1 (personal assistant; github.com/choigonyok/jarvis)"

// Endpoints, replaceable in tests. Overpass is run by volunteers and an
// instance is often busy or down (2026-10-15: one of four answered, in 19
// s), so all are asked at once and the first answer is taken; when none
// answers, Nominatim's category search over the same box stands in (less
// detail: no addresses).
var (
	overpassURLs = []string{
		"https://maps.mail.ru/osm/tools/overpass/api/interpreter",
		"https://overpass-api.de/api/interpreter",
		"https://overpass.private.coffee/api/interpreter",
	}
	nominatimURL = "https://nominatim.openstreetmap.org/search"
)

// overpassWait is how long the mirrors get.
const overpassWait = 30 * time.Second

// filters are what is asked for, as OpenStreetMap tags, and as the phrase
// Nominatim's category search knows it by.
func filters(what string) ([]string, string) {
	w := strings.ToLower(what)
	switch {
	case strings.Contains(w, "카페") || strings.Contains(w, "커피"):
		return []string{`nwr["amenity"="cafe"]`}, "cafe"
	case strings.Contains(w, "술") || strings.Contains(w, "바") || strings.Contains(w, "호프"):
		return []string{`nwr["amenity"~"^(bar|pub)$"]`}, "bar"
	case strings.Contains(w, "편의점"):
		return []string{`nwr["shop"="convenience"]`}, "convenience store"
	case strings.Contains(w, "마트") || strings.Contains(w, "장보"):
		return []string{`nwr["shop"~"^(supermarket|greengrocer)$"]`}, "supermarket"
	case strings.Contains(w, "약국"):
		return []string{`nwr["amenity"="pharmacy"]`}, "pharmacy"
	}
	// 식당·배달·밥·저녁…: places that cook.
	return []string{`nwr["amenity"~"^(restaurant|fast_food|food_court)$"]`}, "restaurant"
}

// Where finds the point to search around: here (the last fix, or the
// region the operator is in), one of the operator's regions by name, or a
// place the map knows by name.
func (m *Module) Where(ctx context.Context, near string) (Point, error) {
	near = strings.TrimSpace(near)
	if near == "" || near == "여기" || near == "지금" {
		now, err := m.Now(ctx)
		if err != nil {
			return Point{}, err
		}
		if now.Location != nil && now.FixAt != nil && time.Since(*now.FixAt) < 2*time.Hour {
			return Point{now.Location.Lat, now.Location.Lon, "지금 위치(" + ago(time.Since(*now.FixAt)) + " 전)"}, nil
		}
		return Point{}, errors.New("최근 위치가 없습니다. 어디 근처인지 이름으로 물어보세요")
	}
	var regions struct {
		Regions []struct {
			Name string  `json:"name"`
			Lat  float64 `json:"lat"`
			Lon  float64 `json:"lon"`
		} `json:"regions"`
	}
	if err := m.get(ctx, m.url+"/regions", &regions); err == nil {
		for _, r := range regions.Regions {
			if r.Name == near {
				return Point{r.Lat, r.Lon, "등록된 영역 '" + r.Name + "'"}, nil
			}
		}
	}
	q := url.Values{"q": {near}, "format": {"jsonv2"}, "limit": {"1"}, "countrycodes": {"kr"}, "accept-language": {"ko"}}
	var hits []struct {
		Lat, Lon, DisplayName string
	}
	var raw []map[string]any
	if err := m.getPublic(ctx, nominatimURL+"?"+q.Encode(), &raw); err != nil {
		return Point{}, fmt.Errorf("지도에서 '%s'를 찾지 못했습니다: %w", near, err)
	}
	for _, h := range raw {
		hits = append(hits, struct{ Lat, Lon, DisplayName string }{fmt.Sprint(h["lat"]), fmt.Sprint(h["lon"]), fmt.Sprint(h["display_name"])})
	}
	if len(hits) == 0 {
		return Point{}, fmt.Errorf("지도에서 '%s'를 찾지 못했습니다", near)
	}
	var p Point
	fmt.Sscan(hits[0].Lat, &p.Lat)
	fmt.Sscan(hits[0].Lon, &p.Lon)
	p.From = "지도 검색 '" + near + "'"
	return p, nil
}

// Nearby is what is around a point, nearest first.
func (m *Module) Nearby(ctx context.Context, p Point, what string, radius, limit int) ([]Shop, error) {
	if radius <= 0 || radius > 3000 {
		radius = 800
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	// About a hundred metres: enough to find shops, not the doorstep.
	lat, lon := math.Round(p.Lat*1000)/1000, math.Round(p.Lon*1000)/1000
	tags, phrase := filters(what)
	var parts []string
	for _, f := range tags {
		parts = append(parts, fmt.Sprintf(`%s["name"](around:%d,%.3f,%.3f);`, f, radius, lat, lon))
	}
	query := "[out:json][timeout:25];(" + strings.Join(parts, "") + ");out center 80;"
	out, err := m.overpass(ctx, query)
	if err != nil {
		shops, ferr := m.nominatimNear(ctx, p, lat, lon, phrase, radius)
		if ferr != nil {
			return nil, fmt.Errorf("지도(OpenStreetMap)에 묻지 못했습니다: %w (대신 찾기도 실패: %v)", err, ferr)
		}
		return nearest(shops, limit), nil
	}
	var shops []Shop
	for _, e := range out.Elements {
		la, lo := e.Lat, e.Lon
		if e.Center != nil {
			la, lo = e.Center.Lat, e.Center.Lon
		}
		name := e.Tags["name:ko"]
		if name == "" {
			name = e.Tags["name"]
		}
		kind := firstOf(e.Tags["amenity"], e.Tags["shop"])
		if c := e.Tags["cuisine"]; c != "" {
			kind += " · " + strings.ReplaceAll(c, ";", ", ")
		}
		addr := strings.TrimSpace(strings.Join(nonEmpty(e.Tags["addr:city"], e.Tags["addr:district"], e.Tags["addr:street"], e.Tags["addr:housenumber"]), " "))
		shops = append(shops, Shop{Name: name, Kind: kind, Meters: int(meters(p.Lat, p.Lon, la, lo)), Address: addr,
			Hours: e.Tags["opening_hours"], Phone: firstOf(e.Tags["phone"], e.Tags["contact:phone"]),
			Link: fmt.Sprintf("https://www.openstreetmap.org/%s/%d", e.Type, e.ID)})
	}
	return nearest(shops, limit), nil
}

func nearest(shops []Shop, limit int) []Shop {
	sort.SliceStable(shops, func(i, j int) bool { return shops[i].Meters < shops[j].Meters })
	if len(shops) > limit {
		shops = shops[:limit]
	}
	return shops
}

type overpassOut struct {
	Elements []struct {
		Type   string                      `json:"type"`
		ID     int64                       `json:"id"`
		Lat    float64                     `json:"lat"`
		Lon    float64                     `json:"lon"`
		Center *struct{ Lat, Lon float64 } `json:"center"`
		Tags   map[string]string           `json:"tags"`
	} `json:"elements"`
}

// overpass asks every mirror at once and takes the first answer.
func (m *Module) overpass(ctx context.Context, query string) (overpassOut, error) {
	ctx, cancel := context.WithTimeout(ctx, overpassWait)
	defer cancel()
	type answer struct {
		out overpassOut
		err error
	}
	answers := make(chan answer, len(overpassURLs))
	for _, endpoint := range overpassURLs {
		go func(endpoint string) {
			var a answer
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"data": {query}}.Encode()))
			if err != nil {
				answers <- answer{err: err}
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("User-Agent", userAgent)
			a.err = m.do(req, &a.out)
			answers <- a
		}(endpoint)
	}
	var last error
	for range overpassURLs {
		a := <-answers
		if a.err == nil {
			return a.out, nil
		}
		last = a.err
	}
	return overpassOut{}, last
}

// nominatimNear is Nominatim's category search ("restaurant") bounded to
// the box around the point: the stand-in when no Overpass mirror answers.
func (m *Module) nominatimNear(ctx context.Context, p Point, lat, lon float64, phrase string, radius int) ([]Shop, error) {
	dlat := float64(radius) / 111000
	dlon := dlat / math.Cos(lat*math.Pi/180)
	q := url.Values{"q": {phrase}, "format": {"jsonv2"}, "limit": {"40"}, "bounded": {"1"}, "extratags": {"1"},
		"accept-language": {"ko"}, "viewbox": {fmt.Sprintf("%.4f,%.4f,%.4f,%.4f", lon-dlon, lat+dlat, lon+dlon, lat-dlat)}}
	var hits []struct {
		OSMType   string            `json:"osm_type"`
		OSMID     int64             `json:"osm_id"`
		Lat       string            `json:"lat"`
		Lon       string            `json:"lon"`
		Name      string            `json:"name"`
		Type      string            `json:"type"`
		Extratags map[string]string `json:"extratags"`
	}
	if err := m.getPublic(ctx, nominatimURL+"?"+q.Encode(), &hits); err != nil {
		return nil, err
	}
	var shops []Shop
	for _, h := range hits {
		if h.Name == "" {
			continue
		}
		var la, lo float64
		fmt.Sscan(h.Lat, &la)
		fmt.Sscan(h.Lon, &lo)
		kind := h.Type
		if c := h.Extratags["cuisine"]; c != "" {
			kind += " · " + strings.ReplaceAll(c, ";", ", ")
		}
		shops = append(shops, Shop{Name: h.Name, Kind: kind, Meters: int(meters(p.Lat, p.Lon, la, lo)),
			Hours: h.Extratags["opening_hours"], Phone: h.Extratags["phone"],
			Link: fmt.Sprintf("https://www.openstreetmap.org/%s/%d", h.OSMType, h.OSMID)})
	}
	return shops, nil
}

func (m *Module) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	return m.do(req, out)
}

// getPublic is a request to a public map service: no token, a user agent
// that says who asks (their usage policy).
func (m *Module) getPublic(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	return m.do(req, out)
}

func (m *Module) do(req *http.Request, out any) error {
	client := m.http
	if req.URL.Host != "" && !strings.HasPrefix(req.URL.String(), m.url) {
		client = &http.Client{Timeout: overpassWait} // public services are slower than ours
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s 가 %d 로 응답했습니다", req.URL.Host, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func meters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dlat, dlon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func nonEmpty(vs ...string) []string {
	var out []string
	for _, v := range vs {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func ago(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d분", int(d.Minutes()))
	}
	return fmt.Sprintf("%d시간", int(d.Hours()))
}
