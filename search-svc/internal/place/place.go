// Package place finds places near a point the way Naver Map lists them for a
// person searching there: open now or not and until when, the visitors'
// stars and how many reviewed, how far.
//
// The list is m.place.naver.com's page for a search - the page a phone
// opens - whose data rides in the HTML as the page's Apollo cache. It is not
// an API Naver offers, so it can change or refuse; when it does, the HUB's
// local search (official, five places for a name, no hours) stands in, and
// the agent keeps OpenStreetMap behind both.
package place

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/choigonyok/jarvis/search-svc/internal/fetch"
	"github.com/choigonyok/jarvis/search-svc/internal/naver"
)

// ListURL is Naver Place's mobile list page; replaceable in tests.
var ListURL = "https://m.place.naver.com/place/list"

// Place is one place found.
type Place struct {
	ID       string  `json:"id,omitempty"`
	Name     string  `json:"name"`
	Category string  `json:"category,omitempty"`
	Meters   int     `json:"meters"`
	Status   string  `json:"status,omitempty"` // 영업 중 · 영업 종료 · 진료 중 · 오늘 휴무 …
	Hours    string  `json:"hours,omitempty"`  // 02:00에 영업 종료 · 09:00에 진료 시작
	Rating   float64 `json:"rating,omitempty"` // visitors' stars; 0 when the place shows none
	Reviews  int     `json:"reviews,omitempty"`
	Blog     int     `json:"blogReviews,omitempty"`
	Address  string  `json:"address,omitempty"`
	Phone    string  `json:"phone,omitempty"`
	Options  string  `json:"options,omitempty"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	URL      string  `json:"url,omitempty"`
}

// Near is Naver Place's list for a search around a point: every place on
// it, in the list's order (nearness and how well the place is liked), with
// its distance from the point, and how many the list has in all.
func Near(ctx context.Context, f *fetch.Fetcher, query string, lat, lng float64) ([]Place, int, error) {
	q := url.Values{"query": {query}, "x": {coordString(lng)}, "y": {coordString(lat)}}
	res, err := f.Get(ctx, ListURL+"?"+q.Encode(), fetch.Options{Mobile: true, MaxBytes: 8 << 20})
	if err != nil {
		return nil, 0, err
	}
	if res.Status != http.StatusOK {
		return nil, 0, fmt.Errorf("네이버 플레이스가 %d 로 답했습니다", res.Status)
	}
	places, total, err := parseList(res.Body)
	if err != nil {
		return nil, 0, err
	}
	for i := range places {
		places[i].Meters = Meters(lat, lng, places[i].Lat, places[i].Lng)
	}
	return places, total, nil
}

// errNoList is a page with no list in it: Naver changed the page, or sent
// something else (a check that we are a person).
var errNoList = errors.New("네이버 플레이스 페이지에서 목록을 찾지 못했습니다")

func parseList(body []byte) ([]Place, int, error) {
	i := bytes.Index(body, []byte("window.__APOLLO_STATE__"))
	if i < 0 {
		return nil, 0, errNoList
	}
	j := bytes.IndexByte(body[i:], '{')
	if j < 0 {
		return nil, 0, errNoList
	}
	var state map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(body[i+j:])).Decode(&state); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", errNoList, err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(state["ROOT_QUERY"], &root); err != nil {
		return nil, 0, errNoList
	}
	type list struct {
		Businesses struct {
			Total int `json:"total"`
			Items []struct {
				Ref string `json:"__ref"`
			} `json:"items"`
		} `json:"businesses"`
	}
	var best *list
	for k, v := range root {
		// The page also carries the same search narrowed to what is open
		// now; the whole list is the one to read (open is a filter here).
		if !strings.HasPrefix(k, "placeList(") || strings.Contains(k, `"filterOpening"`) {
			continue
		}
		var l list
		if json.Unmarshal(v, &l) != nil {
			continue
		}
		if best == nil || len(l.Businesses.Items) > len(best.Businesses.Items) {
			best = &l
		}
	}
	if best == nil {
		return nil, 0, errNoList
	}
	var out []Place
	for _, it := range best.Businesses.Items {
		var raw struct {
			ID                  string `json:"id"`
			Name                string `json:"name"`
			Category            string `json:"category"`
			X                   string `json:"x"`
			Y                   string `json:"y"`
			RoadAddress         string `json:"roadAddress"`
			Address             string `json:"address"`
			CommonAddress       string `json:"commonAddress"`
			Phone               string `json:"phone"`
			VirtualPhone        string `json:"virtualPhone"`
			Options             string `json:"options"`
			VisitorReviewCount  string `json:"visitorReviewCount"`
			VisitorReviewScore  string `json:"visitorReviewScore"`
			BlogCafeReviewCount string `json:"blogCafeReviewCount"`
			Hours               *struct {
				Status      string `json:"status"`
				Description string `json:"description"`
			} `json:"newBusinessHours"`
		}
		if json.Unmarshal(state[it.Ref], &raw) != nil || raw.Name == "" {
			continue
		}
		p := Place{
			ID:       raw.ID,
			Name:     raw.Name,
			Category: raw.Category,
			Reviews:  count(raw.VisitorReviewCount),
			Blog:     count(raw.BlogCafeReviewCount),
			Phone:    firstOf(raw.Phone, raw.VirtualPhone),
			Options:  raw.Options,
		}
		p.Rating, _ = strconv.ParseFloat(raw.VisitorReviewScore, 64)
		p.Lng, _ = strconv.ParseFloat(raw.X, 64)
		p.Lat, _ = strconv.ParseFloat(raw.Y, 64)
		p.Address = strings.TrimSpace(firstOf(raw.CommonAddress+" "+raw.RoadAddress, raw.Address))
		if raw.Hours != nil {
			p.Status, p.Hours = raw.Hours.Status, raw.Hours.Description
		}
		if raw.ID != "" {
			p.URL = "https://m.place.naver.com/place/" + raw.ID + "/home"
		}
		out = append(out, p)
	}
	return out, best.Businesses.Total, nil
}

// FromLocal is the HUB's local search results as places, measured from a
// point (none when lat and lng are 0).
func FromLocal(items []naver.Item, lat, lng float64) []Place {
	var out []Place
	for _, it := range items {
		p := Place{
			Name:     it.Title,
			Category: it.Category,
			Address:  firstOf(it.RoadAddress, it.Address),
			Lat:      it.Lat,
			Lng:      it.Lng,
			Meters:   -1,
		}
		if lat != 0 || lng != 0 {
			p.Meters = Meters(lat, lng, it.Lat, it.Lng)
		}
		if strings.HasPrefix(it.Link, "http") {
			p.URL = it.Link
		}
		out = append(out, p)
	}
	return out
}

// Open is whether a place's status says it is open now. A place about to
// close is still open; one about to open is not.
func Open(status string) bool {
	s := strings.ReplaceAll(status, " ", "")
	switch {
	case s == "":
		return false
	case strings.Contains(s, "24시간"):
		return true
	case strings.HasPrefix(s, "곧"):
		return strings.Contains(s, "종료") || strings.Contains(s, "브레이크") || strings.Contains(s, "마감")
	case strings.HasSuffix(s, "중"):
		return !strings.Contains(s, "휴")
	}
	return false
}

// Meters is the distance between two points on the earth.
func Meters(lat1, lng1, lat2, lng2 float64) int {
	const r = 6371000.0
	rad := math.Pi / 180
	dlat, dlng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dlng/2)*math.Sin(dlng/2)
	return int(2 * r * math.Asin(math.Sqrt(a)))
}

func coordString(f float64) string { return strconv.FormatFloat(f, 'f', 4, 64) }

// count reads "10,016" as 10016.
func count(s string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	return n
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
