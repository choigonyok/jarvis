package transit

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoKey is a feed asked for without its key.
var ErrNoKey = errors.New("실시간 정보 키가 아직 없어요")

// FeedError is a feed's refusal or failure, in words fit to show.
type FeedError struct{ Msg string }

func (e *FeedError) Error() string { return e.Msg }

// Feeds reads the live feeds: data.go.kr's key (DataKey) opens 경기's and
// 서울's buses, data.seoul.go.kr's real-time subway key (SubwayKey) 서울's
// subway - only stations inside 서울; 코레일's stations outside it have no
// public real-time feed. Stations are found in the list kept with the binary
// (stations.go), so also while no train runs. What a feed said is kept a little while, so the screen and the nudges
// asking at once ask it once - the keys have a daily allowance.
type Feeds struct {
	DataKey, SubwayKey string
	HTTP               *http.Client
	Loc                *time.Location
	Now                func() time.Time
	// Endpoints, replaceable in tests.
	GyeonggiArrivals, GyeonggiStations, SeoulStations, SubwayBase string

	mu    sync.Mutex
	cache map[string]kept
}

type kept struct {
	at  time.Time
	val any
	err error
}

// New is the feeds with their keys. data.go.kr shows its key twice - as is
// and already escaped (holding %2B and the like) - and either works here.
func New(dataKey, subwayKey string) *Feeds {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}
	key := strings.TrimSpace(dataKey)
	if strings.Contains(key, "%") {
		if d, err := url.QueryUnescape(key); err == nil {
			key = d
		}
	}
	return &Feeds{
		DataKey: key, SubwayKey: strings.TrimSpace(subwayKey),
		HTTP: &http.Client{Timeout: 8 * time.Second}, Loc: loc, Now: time.Now,
		GyeonggiArrivals: "https://apis.data.go.kr/6410000/busarrivalservice/v2",
		GyeonggiStations: "https://apis.data.go.kr/6410000/busstationservice/v2",
		SeoulStations:    "http://ws.bus.go.kr/api/rest/stationinfo",
		SubwayBase:       "http://swopenAPI.seoul.go.kr/api/subway",
	}
}

// Configured is which feeds have their keys.
func (f *Feeds) Configured() (buses, subway bool) {
	return f.DataKey != "", f.SubwayKey != ""
}

// remember asks get once per key while ttl lasts - a failure for half a
// minute at most, so a failing feed is not asked again at once nor kept
// failing for long.
func (f *Feeds) remember(key string, ttl time.Duration, get func() (any, error)) (any, error) {
	f.mu.Lock()
	if c, ok := f.cache[key]; ok && f.Now().Sub(c.at) < ttl && (c.err == nil || f.Now().Sub(c.at) < 30*time.Second) {
		f.mu.Unlock()
		return c.val, c.err
	}
	f.mu.Unlock()
	v, err := get()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cache == nil {
		f.cache = map[string]kept{}
	}
	if len(f.cache) > 400 {
		for k, c := range f.cache {
			if f.Now().Sub(c.at) > 30*time.Minute {
				delete(f.cache, k)
			}
		}
	}
	f.cache[key] = kept{at: f.Now(), val: v, err: err}
	return v, err
}

// Arrivals is what is coming to a stop now.
func (f *Feeds) Arrivals(ctx context.Context, s Stop) ([]Arrival, error) {
	switch {
	case s.Kind == Subway:
		v, err := f.remember("subway:"+s.ID, 25*time.Second, func() (any, error) { return f.subway(ctx, s.ID) })
		if err != nil {
			return nil, err
		}
		return v.([]Arrival), nil
	case s.Region == Gyeonggi:
		v, err := f.remember("gyeonggi:"+s.ID, 25*time.Second, func() (any, error) { return f.gyeonggiArrivals(ctx, s.ID) })
		if err != nil {
			return nil, err
		}
		return v.([]Arrival), nil
	case s.Region == Seoul:
		at, err := f.seoulStop(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		return at.arrivals, nil
	}
	return nil, &FeedError{"모르는 정류장이에요"}
}

// Lines is what stops at a stop: at a bus stop every route, running now or
// not; at a station each line toward each station next to it - from the list
// kept here, any time, and from the trains coming now when it has none.
func (f *Feeds) Lines(ctx context.Context, s Stop) ([]Line, error) {
	switch {
	case s.Kind == Subway:
		lines := stationLines(s.ID)
		arrivals, err := f.Arrivals(ctx, s)
		if err != nil && len(lines) == 0 {
			return nil, err
		}
		for _, a := range arrivals {
			line, next, _ := strings.Cut(a.Line.ID, ">")
			if next == "" || slices.ContainsFunc(lines, func(l Line) bool {
				on, via, _ := strings.Cut(l.ID, ">")
				return on == line && heading(s.ID, line, via, next)
			}) {
				continue
			}
			lines = append(lines, Line{ID: a.Line.ID, Name: a.Line.Name, Toward: next + " 방면"})
		}
		if len(lines) == 0 {
			return nil, &FeedError{"이 역은 지금 실시간 정보가 없어요 - 서울 밖의 역이거나, 지금 다니는 열차가 없어요"}
		}
		slices.SortStableFunc(lines, func(a, b Line) int { return strings.Compare(a.ID, b.ID) })
		return lines, nil
	case s.Region == Gyeonggi:
		v, err := f.remember("gyeonggi-lines:"+s.ID, time.Hour, func() (any, error) { return f.gyeonggiLines(ctx, s.ID) })
		if err != nil {
			return nil, err
		}
		return v.([]Line), nil
	case s.Region == Seoul:
		at, err := f.seoulStop(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		return at.lines, nil
	}
	return nil, &FeedError{"모르는 정류장이에요"}
}

// Near is the stops around a point: bus stops from both bus feeds (500 m),
// and stations (1.5 km) - nearest first.
func (f *Feeds) Near(ctx context.Context, lat, lon float64) ([]Stop, error) {
	key := fmt.Sprintf("near:%.3f,%.3f", lat, lon)
	v, err := f.remember(key, 5*time.Minute, func() (any, error) {
		out, err := gather(
			lookup{"버스 정류장", func() ([]Stop, error) { return f.gyeonggiNear(ctx, lat, lon) }},
			lookup{"버스 정류장", func() ([]Stop, error) { return f.seoulNear(ctx, lat, lon) }},
			lookup{"역", func() ([]Stop, error) { return stationsNear(lat, lon, 1500), nil }},
		)
		for i := range out {
			if out[i].Lat != 0 || out[i].Lon != 0 {
				out[i].Dist = int(Meters(lat, lon, out[i].Lat, out[i].Lon))
			}
		}
		slices.SortStableFunc(out, func(a, b Stop) int { return a.Dist - b.Dist })
		return out, err
	})
	stops, _ := v.([]Stop)
	return stops, err
}

// Search is stops by name or stop number: bus stops in both bus feeds, and a
// station of that name if the subway feed knows it now.
func (f *Feeds) Search(ctx context.Context, q string) ([]Stop, error) {
	q = strings.TrimSpace(q)
	v, err := f.remember("search:"+q, 10*time.Minute, func() (any, error) {
		out, err := gather(
			lookup{"버스 정류장", func() ([]Stop, error) { return f.gyeonggiSearch(ctx, q) }},
			lookup{"버스 정류장", func() ([]Stop, error) { return f.seoulSearch(ctx, q) }},
			lookup{"역", func() ([]Stop, error) {
				if found := stationsNamed(q); len(found) > 0 {
					return found, nil
				}
				return f.station(ctx, q) // one the list does not have, if the feed knows it now
			}},
		)
		// The station first, then bus stops by name.
		slices.SortStableFunc(out, func(a, b Stop) int {
			if (a.Kind == Subway) != (b.Kind == Subway) {
				if a.Kind == Subway {
					return -1
				}
				return 1
			}
			return 0
		})
		return out, err
	})
	stops, _ := v.([]Stop)
	return stops, err
}

// Partial is stops found while some of the feeds asked for them failed:
// what is missing, and why.
type Partial struct {
	What string
	Err  error
}

func (p *Partial) Error() string { return p.What + "은 빠졌어요: " + p.Err.Error() }
func (p *Partial) Unwrap() error { return p.Err }

// lookup is one feed's stops among several asked at once, and what they are
// - "버스 정류장" or "역" - for saying they are missing.
type lookup struct {
	what string
	get  func() ([]Stop, error)
}

// gather asks the lookups at once. When none finds any, the first failure is
// the answer; when some do, what they found comes with a *Partial naming the
// first that failed.
func gather(lookups ...lookup) ([]Stop, error) {
	found := make([][]Stop, len(lookups))
	failed := make([]error, len(lookups))
	var wg sync.WaitGroup
	for i, l := range lookups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i], failed[i] = l.get()
		}()
	}
	wg.Wait()
	out := slices.Concat(found...)
	for i, err := range failed {
		if err == nil {
			continue
		}
		if len(out) == 0 {
			return nil, err
		}
		return out, &Partial{What: lookups[i].what, Err: err}
	}
	return out, nil
}

// --- the feeds ---------------------------------------------------------------

func (f *Feeds) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, &FeedError{"실시간 정보 서버에 닿지 못했어요"}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &FeedError{"실시간 정보를 다 받지 못했어요"}
	}
	if resp.StatusCode >= 500 {
		return nil, &FeedError{fmt.Sprintf("실시간 정보 서버가 %d 로 답했어요", resp.StatusCode)}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if err := keyTrouble(body, f.service(u)); err != nil {
			return nil, err
		}
		return nil, &FeedError{"실시간 정보 서버가 키를 받아 주지 않았어요"}
	}
	return body, nil
}

// keyTrouble is data.go.kr's word on the key, when a call is refused for it.
// service is the listing the call is under there - the one to apply for
// (활용신청) when the key is not registered for it - or "" if not known.
func keyTrouble(body []byte, service string) error {
	switch t := string(body); {
	// 서울's bus feed says it in Korean, with a 401: "유효하지 않은 서비스키입니다: 등록되지 않은 서비스키".
	case strings.Contains(t, "SERVICE_KEY_IS_NOT_REGISTERED") || strings.Contains(t, "SERVICE KEY IS NOT REGISTERED") || strings.Contains(t, "등록되지 않은 서비스키"):
		if service != "" {
			return &FeedError{"공공데이터포털 키가 아직 ‘" + service + "’에 등록되지 않았어요 - 활용신청하면 한 시간쯤 뒤부터 돼요"}
		}
		return &FeedError{"공공데이터포털 키가 이 정보에 아직 등록되지 않았어요 - 활용신청이 승인되고 한 시간쯤 걸려요"}
	case strings.Contains(t, "LIMITED_NUMBER_OF_SERVICE_REQUESTS"):
		return &FeedError{"오늘 쓸 수 있는 실시간 정보 호출을 다 썼어요"}
	case strings.Contains(t, "SERVICE_ACCESS_DENIED") || strings.Contains(t, "UNREGISTERED_IP"):
		return &FeedError{"공공데이터포털이 이 서버의 요청을 막았어요"}
	}
	return nil
}

// service is the data.go.kr listing a call to u is under, by its name there.
func (f *Feeds) service(u string) string {
	for _, s := range []struct{ base, name string }{
		{f.GyeonggiArrivals, "경기도_버스도착정보 조회"},
		{f.GyeonggiStations, "경기도_정류소 조회"},
		{f.SeoulStations, "서울특별시_정류소정보조회 서비스"},
	} {
		if s.base != "" && strings.HasPrefix(u, s.base) {
			return s.name
		}
	}
	return ""
}

// gbis asks 경기's bus feed: op under base, with params, for the list named
// list in its answer ({"response": {"msgHeader": …, "msgBody": {list: …}}}).
// A single item may come as an object rather than a list.
func (f *Feeds) gbis(ctx context.Context, base, op string, params url.Values, list string) ([]map[string]any, error) {
	if f.DataKey == "" {
		return nil, ErrNoKey
	}
	params.Set("serviceKey", f.DataKey)
	params.Set("format", "json")
	body, err := f.get(ctx, base+"/"+op+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	if err := keyTrouble(body, f.service(base)); err != nil {
		return nil, err
	}
	var env struct {
		Response *gbisPart `json:"response"`
		gbisPart
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, &FeedError{"경기 버스 정보를 읽지 못했어요"}
	}
	part := env.gbisPart
	if env.Response != nil {
		part = *env.Response
	}
	switch code := text(part.MsgHeader["resultCode"]); code {
	case "0", "":
	case "4":
		return nil, nil // nothing found
	default:
		msg := text(part.MsgHeader["resultMessage"])
		if msg == "" {
			msg = "경기 버스 정보가 오류(" + code + ")로 답했어요"
		}
		return nil, &FeedError{msg}
	}
	return items(part.MsgBody[list]), nil
}

type gbisPart struct {
	MsgHeader map[string]any             `json:"msgHeader"`
	MsgBody   map[string]json.RawMessage `json:"msgBody"`
}

// items is a JSON list of objects, or one object alone, as a list.
func items(raw json.RawMessage) []map[string]any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == 'n' {
		return nil
	}
	if raw[0] == '{' {
		var one map[string]any
		if json.Unmarshal(raw, &one) == nil {
			return []map[string]any{one}
		}
		return nil
	}
	var many []map[string]any
	_ = json.Unmarshal(raw, &many)
	return many
}

// text is a JSON value as text: strings as they are, numbers without a
// trailing .0.
func text(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return n, err == nil
	}
	return 0, false
}

func (f *Feeds) gyeonggiArrivals(ctx context.Context, stationID string) ([]Arrival, error) {
	list, err := f.gbis(ctx, f.GyeonggiArrivals, "getBusArrivalListv2", url.Values{"stationId": {stationID}}, "busArrivalList")
	if err != nil {
		return nil, err
	}
	now := f.Now()
	out := []Arrival{}
	for _, it := range list {
		if text(it["flag"]) == "STOP" {
			continue // ended for the day
		}
		line := Line{ID: text(it["routeId"]), Name: text(it["routeName"]), Toward: toward(text(it["routeDestName"]))}
		for _, n := range []string{"1", "2"} {
			sec, ok := number(it["predictTimeSec"+n])
			if !ok {
				if min, okMin := number(it["predictTime"+n]); okMin {
					sec, ok = min*60, true
				}
			}
			if !ok || sec < 0 {
				continue
			}
			a := Arrival{Line: line, At: now.Add(time.Duration(sec) * time.Second), Vehicle: text(it["vehId"+n])}
			if a.Vehicle == "" {
				a.Vehicle = text(it["plateNo"+n])
			}
			if stops, ok := number(it["locationNo"+n]); ok && stops > 0 {
				a.Note = fmt.Sprintf("%d번째 전", int(stops))
			}
			if seats, ok := number(it["remainSeatCnt"+n]); ok && seats >= 0 && text(it["remainSeatCnt"+n]) != "" {
				a.Note = strings.TrimSpace(a.Note + fmt.Sprintf(" · 빈자리 %d", int(seats)))
				a.Note = strings.TrimPrefix(a.Note, "· ")
			}
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *Feeds) gyeonggiLines(ctx context.Context, stationID string) ([]Line, error) {
	list, err := f.gbis(ctx, f.GyeonggiStations, "getBusStationViaRouteListv2", url.Values{"stationId": {stationID}}, "busRouteList")
	if err != nil {
		return nil, err
	}
	out := []Line{}
	for _, it := range list {
		out = append(out, Line{ID: text(it["routeId"]), Name: text(it["routeName"]), Toward: toward(text(it["routeDestName"]))})
	}
	slices.SortFunc(out, func(a, b Line) int { return compareRoute(a.Name, b.Name) })
	return out, nil
}

func (f *Feeds) gyeonggiStops(list []map[string]any) []Stop {
	out := []Stop{}
	for _, it := range list {
		s := Stop{Kind: Bus, Region: Gyeonggi, ID: text(it["stationId"]), Name: text(it["stationName"]), No: text(it["mobileNo"])}
		s.Lon, _ = number(it["x"])
		s.Lat, _ = number(it["y"])
		if d, ok := number(it["distance"]); ok {
			s.Dist = int(d)
		}
		if s.ID != "" {
			out = append(out, s)
		}
	}
	return out
}

func (f *Feeds) gyeonggiNear(ctx context.Context, lat, lon float64) ([]Stop, error) {
	list, err := f.gbis(ctx, f.GyeonggiStations, "getBusStationAroundListv2",
		url.Values{"x": {strconv.FormatFloat(lon, 'f', 6, 64)}, "y": {strconv.FormatFloat(lat, 'f', 6, 64)}}, "busStationAroundList")
	if err != nil {
		return nil, err
	}
	return f.gyeonggiStops(list), nil
}

func (f *Feeds) gyeonggiSearch(ctx context.Context, q string) ([]Stop, error) {
	list, err := f.gbis(ctx, f.GyeonggiStations, "getBusStationListv2", url.Values{"keyword": {q}}, "busStationList")
	if err != nil {
		return nil, err
	}
	stops := f.gyeonggiStops(list)
	return stops[:min(len(stops), 15)], nil
}

// seoulBus asks 서울's bus feed for its itemList: JSON (resultType=json)
// when it answers so, else the XML it answers by default.
func (f *Feeds) seoulBus(ctx context.Context, op string, params url.Values) ([]map[string]string, error) {
	if f.DataKey == "" {
		return nil, ErrNoKey
	}
	params.Set("serviceKey", f.DataKey)
	params.Set("resultType", "json")
	body, err := f.get(ctx, f.SeoulStations+"/"+op+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	if err := keyTrouble(body, f.service(f.SeoulStations)); err != nil {
		return nil, err
	}
	var code, msg string
	var list []map[string]string
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '<' {
		code, msg, list = seoulXML(t)
	} else {
		var env struct {
			MsgHeader map[string]any             `json:"msgHeader"`
			MsgBody   map[string]json.RawMessage `json:"msgBody"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, &FeedError{"서울 버스 정보를 읽지 못했어요"}
		}
		code, msg = text(env.MsgHeader["headerCd"]), text(env.MsgHeader["headerMsg"])
		for _, it := range items(env.MsgBody["itemList"]) {
			row := map[string]string{}
			for k, v := range it {
				row[k] = text(v)
			}
			list = append(list, row)
		}
	}
	switch code {
	case "0", "":
		return list, nil
	case "4":
		return nil, nil // nothing found
	}
	if strings.Contains(msg, "KEY") || strings.Contains(msg, "키") {
		return nil, &FeedError{"공공데이터포털 키가 서울 버스 정보에 아직 등록되지 않았어요"}
	}
	if msg == "" {
		msg = "서울 버스 정보가 오류(" + code + ")로 답했어요"
	}
	return nil, &FeedError{msg}
}

// seoulXML is the default XML answer: its header, and each itemList's fields.
func seoulXML(body []byte) (code, msg string, list []map[string]string) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var path []string
	var row map[string]string
	for {
		tok, err := dec.Token()
		if err != nil {
			return code, msg, list
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			if t.Name.Local == "itemList" {
				row = map[string]string{}
			}
		case xml.EndElement:
			if t.Name.Local == "itemList" && row != nil {
				list = append(list, row)
				row = nil
			}
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			if len(path) == 0 {
				continue
			}
			v := strings.TrimSpace(string(t))
			switch name := path[len(path)-1]; {
			case row != nil && len(path) >= 2 && path[len(path)-2] == "itemList":
				row[name] = v
			case name == "headerCd":
				code = v
			case name == "headerMsg":
				msg = v
			case name == "returnAuthMsg" && v != "" && code == "":
				code, msg = "auth", v
			}
		}
	}
}

// seoulStop is one 서울 stop's routes and what is coming on them - one call
// gives both.
type seoulAnswer struct {
	arrivals []Arrival
	lines    []Line
}

func (f *Feeds) seoulStop(ctx context.Context, arsID string) (seoulAnswer, error) {
	v, err := f.remember("seoul:"+arsID, 25*time.Second, func() (any, error) {
		list, err := f.seoulBus(ctx, "getStationByUid", url.Values{"arsId": {arsID}})
		if err != nil {
			return nil, err
		}
		now := f.Now()
		ans := seoulAnswer{arrivals: []Arrival{}, lines: []Line{}}
		for _, it := range list {
			name := it["busRouteAbrv"]
			if name == "" {
				name = it["rtNm"]
			}
			line := Line{ID: it["busRouteId"], Name: name, Toward: toward(it["adirection"])}
			if line.ID == "" {
				continue
			}
			ans.lines = append(ans.lines, line)
			for _, n := range []string{"1", "2"} {
				msg := it["arrmsg"+n]
				sec, ok := seoulSeconds(msg)
				if !ok {
					continue
				}
				a := Arrival{Line: line, At: now.Add(time.Duration(sec) * time.Second), Vehicle: it["vehId"+n]}
				if a.Vehicle == "" {
					a.Vehicle = it["plainNo"+n]
				}
				if m := bracket.FindStringSubmatch(msg); m != nil {
					a.Note = m[1]
				}
				if it["isLast"+n] == "1" {
					a.Note = strings.TrimSpace(a.Note + " 막차")
				}
				ans.arrivals = append(ans.arrivals, a)
			}
		}
		slices.SortFunc(ans.lines, func(a, b Line) int { return compareRoute(a.Name, b.Name) })
		return ans, nil
	})
	if err != nil {
		return seoulAnswer{}, err
	}
	return v.(seoulAnswer), nil
}

var (
	bracket    = regexp.MustCompile(`\[([^\]]+)\]`)
	minSec     = regexp.MustCompile(`(\d+)\s*분\s*(\d+)\s*초\s*후`)
	minOnly    = regexp.MustCompile(`(\d+)\s*분\s*후`)
	secOnly    = regexp.MustCompile(`(\d+)\s*초\s*후`)
	stationsTo = regexp.MustCompile(`\[(\d+)\]\s*번째\s*전역`)
)

// seoulSeconds is how long until a 서울 bus arrives, from its message: "5분
// 12초후[3번째 전]", "곧 도착". 출발대기, 운행종료 and the like are no time.
func seoulSeconds(msg string) (int, bool) {
	switch {
	case strings.Contains(msg, "곧 도착"):
		return 30, true
	case minSec.MatchString(msg):
		m := minSec.FindStringSubmatch(msg)
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		return a*60 + b, true
	case minOnly.MatchString(msg):
		a, _ := strconv.Atoi(minOnly.FindStringSubmatch(msg)[1])
		return a * 60, true
	case secOnly.MatchString(msg):
		a, _ := strconv.Atoi(secOnly.FindStringSubmatch(msg)[1])
		return a, true
	}
	return 0, false
}

func (f *Feeds) seoulStops(list []map[string]string, lonKey, latKey, nameKey string) []Stop {
	out := []Stop{}
	for _, it := range list {
		s := Stop{Kind: Bus, Region: Seoul, ID: it["arsId"], Name: it[nameKey], No: it["arsId"]}
		s.Lon, _ = strconv.ParseFloat(it[lonKey], 64)
		s.Lat, _ = strconv.ParseFloat(it[latKey], 64)
		if d, err := strconv.ParseFloat(it["dist"], 64); err == nil {
			s.Dist = int(d)
		}
		if s.ID != "" && s.ID != "0" {
			out = append(out, s)
		}
	}
	return out
}

func (f *Feeds) seoulNear(ctx context.Context, lat, lon float64) ([]Stop, error) {
	list, err := f.seoulBus(ctx, "getStationByPos", url.Values{
		"tmX": {strconv.FormatFloat(lon, 'f', 6, 64)}, "tmY": {strconv.FormatFloat(lat, 'f', 6, 64)}, "radius": {"500"},
	})
	if err != nil {
		return nil, err
	}
	return f.seoulStops(list, "gpsX", "gpsY", "stationNm"), nil
}

func (f *Feeds) seoulSearch(ctx context.Context, q string) ([]Stop, error) {
	list, err := f.seoulBus(ctx, "getStationByName", url.Values{"stSrch": {q}})
	if err != nil {
		return nil, err
	}
	stops := f.seoulStops(list, "tmX", "tmY", "stNm")
	return stops[:min(len(stops), 15)], nil
}

// subway is the trains coming to a 서울 station, from its real-time feed. A
// station the feed does not know - outside 서울, or with no train now - has
// none.
func (f *Feeds) subway(ctx context.Context, name string) ([]Arrival, error) {
	if f.SubwayKey == "" {
		return nil, ErrNoKey
	}
	u := fmt.Sprintf("%s/%s/json/realtimeStationArrival/0/40/%s", strings.TrimRight(f.SubwayBase, "/"), url.PathEscape(f.SubwayKey), url.PathEscape(name))
	body, err := f.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var env struct {
		ErrorMessage map[string]any   `json:"errorMessage"`
		Code         string           `json:"code"`
		Message      string           `json:"message"`
		List         []map[string]any `json:"realtimeArrivalList"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, &FeedError{"지하철 정보를 읽지 못했어요"}
	}
	code, msg := env.Code, env.Message
	if env.ErrorMessage != nil {
		code, msg = text(env.ErrorMessage["code"]), text(env.ErrorMessage["message"])
	}
	switch {
	case code == "INFO-000" || code == "":
	case code == "INFO-200":
		return []Arrival{}, nil // no data: not a 서울 station, or no train now
	case code == "INFO-100" || strings.HasPrefix(code, "ERROR-3"):
		return nil, &FeedError{"서울 열린데이터광장 지하철 키를 받아 주지 않았어요 - 실시간 지하철 인증키인지 확인해 주세요"}
	default:
		if msg == "" {
			msg = "지하철 정보가 오류(" + code + ")로 답했어요"
		}
		return nil, &FeedError{msg}
	}
	now := f.Now()
	out := []Arrival{}
	for _, it := range env.List {
		cd := text(it["arvlCd"])
		if cd == "2" {
			continue // left this station already
		}
		id := text(it["subwayId"])
		line := Line{ID: id + ">" + nextStation(text(it["trainLineNm"])), Name: LineName(id), Toward: text(it["trainLineNm"])}
		base := now
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", text(it["recptnDt"]), f.Loc); err == nil && t.Before(now) && now.Sub(t) < 10*time.Minute {
			base = t
		}
		sec, _ := number(it["barvlDt"])
		rough := false
		if sec <= 0 {
			s, ok, guessed := subwaySeconds(text(it["arvlMsg2"]), cd)
			if !ok {
				continue
			}
			sec, rough = float64(s), guessed
		}
		a := Arrival{Line: line, At: base.Add(time.Duration(sec) * time.Second), Vehicle: text(it["btrainNo"]), Rough: rough, Way: text(it["updnLine"])}
		if kind := text(it["btrainSttus"]); kind != "" && kind != "일반" {
			a.Note = kind
		}
		if text(it["lstcarAt"]) == "1" {
			a.Note = strings.TrimSpace(a.Note + " 막차")
		}
		out = append(out, a)
	}
	return out, nil
}

// subwaySeconds is how long until a train arrives when the feed gives no
// seconds: from its message ("3분 후", "[2]번째 전역") or its state - a guess
// unless the message says the time.
func subwaySeconds(msg, cd string) (sec int, ok, guessed bool) {
	switch {
	case minSec.MatchString(msg):
		m := minSec.FindStringSubmatch(msg)
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		return a*60 + b, true, false
	case minOnly.MatchString(msg):
		a, _ := strconv.Atoi(minOnly.FindStringSubmatch(msg)[1])
		return a * 60, true, false
	case secOnly.MatchString(msg):
		a, _ := strconv.Atoi(secOnly.FindStringSubmatch(msg)[1])
		return a, true, false
	case stationsTo.MatchString(msg):
		n, _ := strconv.Atoi(stationsTo.FindStringSubmatch(msg)[1])
		return n * 120, true, true
	}
	switch cd {
	case "0": // 진입
		return 20, true, true
	case "1": // 도착
		return 0, true, true
	case "3": // 전역 출발
		return 90, true, true
	case "4": // 전역 진입
		return 150, true, true
	case "5": // 전역 도착
		return 120, true, true
	}
	return 0, false, false
}

// station is a station of name q, if the subway feed knows it now.
func (f *Feeds) station(ctx context.Context, q string) ([]Stop, error) {
	name := stationName(q)
	if name == "" || f.SubwayKey == "" {
		return nil, nil
	}
	arrivals, err := f.Arrivals(ctx, Stop{Kind: Subway, ID: name})
	if err != nil || len(arrivals) == 0 {
		return nil, err
	}
	return []Stop{{Kind: Subway, ID: name, Name: name + "역"}}, nil
}

// stationName is a station as the subway feed names it: 강남 for 강남역 -
// but 서울역 stays 서울.
func stationName(q string) string {
	q = strings.TrimSpace(q)
	if i := strings.Index(q, "("); i > 0 {
		q = strings.TrimSpace(q[:i])
	}
	if r := []rune(q); len(r) > 2 && strings.HasSuffix(q, "역") {
		q = strings.TrimSuffix(q, "역")
	} else if q == "서울역" {
		q = "서울"
	}
	return q
}

// toward is a destination said as a direction: "강남역" → "강남역 방면".
func toward(dest string) string {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasSuffix(dest, "방면") || strings.HasSuffix(dest, "방향") {
		return dest
	}
	return dest + " 방면"
}

// compareRoute orders route names as riders read them: by number, then by
// name ("7", "11", "7770", "M5107", "마을 3").
func compareRoute(a, b string) int {
	na, ea := strconv.Atoi(strings.TrimLeft(a, "0"))
	nb, eb := strconv.Atoi(strings.TrimLeft(b, "0"))
	switch {
	case ea == nil && eb == nil:
		return na - nb
	case ea == nil:
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}
