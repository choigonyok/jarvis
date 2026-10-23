package api

// The 출발 screen's: each person's routes - the bus or the train they
// usually take, from which stop, after what walk - and for each, now, when to
// leave for it (transit.Make). Each person sees and keeps only their own: the
// guest's routes have paths of their own, which the gateway opens to the
// guest's token alone.
//
//	GET /transit                 the operator's routes, and their plans now
//	GET /transit/guest           the guest's
//	PUT /transit/routes          the operator's routes, the whole list
//	PUT /transit/guest/routes    the guest's
//	GET /transit/stops?near=lat,lon|me&q=   stops to start a route from (me:
//	                             where the operator's phone last was)
//	GET /transit/guest/stops     the same, me being the guest's phone
//	GET /transit/lines?kind=&region=&id=    what stops there
//
// A route with its hours nudges its person while they last (nudge):
// 슬슬 짐 싸야 해요 when one must leave in a quarter of an hour, 지금 나가요
// two minutes before - a push of its own kind (transit.*), which rings
// through quiet hours like a reminder.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/context-svc/internal/store"
	"github.com/choigonyok/jarvis/context-svc/internal/transit"
)

// transitFeed is the live feeds (transit.Feeds; a fake in tests).
type transitFeed interface {
	Arrivals(ctx context.Context, s transit.Stop) ([]transit.Arrival, error)
	Lines(ctx context.Context, s transit.Stop) ([]transit.Line, error)
	Near(ctx context.Context, lat, lon float64) ([]transit.Stop, error)
	Search(ctx context.Context, q string) ([]transit.Stop, error)
	Configured() (buses, subway bool)
}

// nudged is the nudges said already, so each is said once.
type nudged struct {
	mu   sync.Mutex
	said map[string]time.Time
}

func (n *nudged) first(key string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.said == nil {
		n.said = map[string]time.Time{}
	}
	for k, at := range n.said {
		if now.Sub(at) > 6*time.Hour {
			delete(n.said, k)
		}
	}
	if _, ok := n.said[key]; ok {
		return false
	}
	n.said[key] = now
	return true
}

func (s *Server) routes(ctx context.Context, source string) ([]transit.Route, error) {
	var rs []transit.Route
	if _, err := s.st.Get(ctx, stateKey(source, "transit.routes"), &rs); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if rs == nil {
		rs = []transit.Route{}
	}
	return rs, nil
}

// transitNow is a person's routes and, for each, the plan now.
func (s *Server) transitNow(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		rs, err := s.routes(ctx, source)
		if err != nil {
			s.fail(w, "경로를 읽지 못했습니다", err)
			return
		}
		now := s.now()
		plans := make([]transit.Plan, len(rs))
		var wg sync.WaitGroup
		for i, route := range rs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				plans[i] = s.plan(ctx, route, now)
			}()
		}
		wg.Wait()
		buses, subway := s.feed.Configured()
		writeJSON(w, http.StatusOK, map[string]any{
			"now": now, "routes": rs, "plans": plans,
			"feeds": map[string]bool{"buses": buses, "subway": subway},
		})
	}
}

func (s *Server) plan(ctx context.Context, route transit.Route, now time.Time) transit.Plan {
	arrivals, err := s.feed.Arrivals(ctx, route.Stop)
	if err != nil {
		var fe *transit.FeedError
		if !errors.Is(err, transit.ErrNoKey) && !errors.As(err, &fe) {
			s.log.Warn("실시간 정보를 읽지 못했습니다", "stop", route.Stop.ID, "err", err)
		}
		return transit.Failed(route, now, err.Error())
	}
	return transit.Make(route, arrivals, now)
}

// putRoutes keeps a person's routes, the whole list as the screen has it.
func (s *Server) putRoutes(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Routes []transit.Route `json:"routes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "JSON 을 읽지 못했습니다.")
			return
		}
		if len(in.Routes) > 12 {
			writeErr(w, http.StatusBadRequest, "경로는 12개까지입니다.")
			return
		}
		seen := map[string]bool{}
		for i := range in.Routes {
			rt := &in.Routes[i]
			if msg := checkRoute(rt); msg != "" {
				writeErr(w, http.StatusBadRequest, msg)
				return
			}
			if rt.ID == "" || seen[rt.ID] {
				rt.ID = newRid()
			}
			seen[rt.ID] = true
		}
		if in.Routes == nil {
			in.Routes = []transit.Route{}
		}
		if err := s.st.Set(r.Context(), stateKey(source, "transit.routes"), in.Routes, s.now()); err != nil {
			s.fail(w, "경로를 저장하지 못했습니다", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": in.Routes})
	}
}

// checkRoute is what is wrong with a route, if anything, in words to show.
func checkRoute(r *transit.Route) string {
	r.Name = strings.TrimSpace(r.Name)
	switch {
	case r.Name == "" || utf8.RuneCountInString(r.Name) > 20:
		return "경로 이름은 1~20자입니다."
	case r.Stop.Kind != transit.Bus && r.Stop.Kind != transit.Subway:
		return "정류장 종류는 bus 나 subway 입니다."
	case r.Stop.Kind == transit.Bus && r.Stop.Region != transit.Gyeonggi && r.Stop.Region != transit.Seoul:
		return "버스 정류장의 지역은 gyeonggi 나 seoul 입니다."
	case strings.TrimSpace(r.Stop.ID) == "" || strings.TrimSpace(r.Line.ID) == "":
		return "정류장과 노선을 골라 주세요."
	case r.Walk < 0 || r.Walk > 90 || r.Spare < 0 || r.Spare > 30:
		return "걷는 시간은 0~90분, 여유는 0~30분입니다."
	case (r.From == "") != (r.To == ""), r.From != "" && (!transit.Clock(r.From) || !transit.Clock(r.To)):
		return "시간대는 HH:MM 두 개입니다."
	}
	for _, d := range r.Days {
		if d < 1 || d > 7 {
			return "요일은 1(월)~7(일)입니다."
		}
	}
	slices.Sort(r.Days)
	r.Days = slices.Compact(r.Days)
	if r.From == "" {
		r.Push = false // no hours, nothing to nudge in
	}
	return ""
}

// transitStops is stops to start a route from: near a point (or where the
// person's phone last was: near=me), or by name.
func (s *Server) transitStops(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
			if utf8.RuneCountInString(q) > 30 {
				writeErr(w, http.StatusBadRequest, "검색어가 너무 깁니다.")
				return
			}
			stops, err := s.feed.Search(ctx, q)
			s.answerStops(w, stops, err)
			return
		}
		near := r.URL.Query().Get("near")
		var lat, lon float64
		if near == "me" {
			fix, err := s.st.FixBefore(ctx, source, s.now().Add(time.Minute), s.cfg.Trust)
			if err != nil {
				s.fail(w, "위치를 읽지 못했습니다", err)
				return
			}
			if fix == nil {
				writeErr(w, http.StatusNotFound, "폰이 보낸 위치가 아직 없어요.")
				return
			}
			lat, lon = fix.Lat, fix.Lon
		} else {
			a, b, ok := strings.Cut(near, ",")
			var err1, err2 error
			lat, err1 = strconv.ParseFloat(a, 64)
			lon, err2 = strconv.ParseFloat(b, 64)
			if !ok || err1 != nil || err2 != nil || lat < 33 || lat > 39 || lon < 124 || lon > 132 {
				writeErr(w, http.StatusBadRequest, "near 는 '위도,경도' 나 me 입니다.")
				return
			}
		}
		stops, err := s.feed.Near(ctx, lat, lon)
		if stops != nil {
			for i := range stops {
				if stops[i].Dist == 0 && (stops[i].Lat != 0 || stops[i].Lon != 0) {
					stops[i].Dist = int(transit.Meters(lat, lon, stops[i].Lat, stops[i].Lon))
				}
			}
		}
		s.answerStops(w, stops, err, lat, lon)
	}
}

// answerStops is the stops found. Some feeds failing while others found stops
// is said beside them (missed) - bus stops missing for a key not yet
// registered should not read as none there.
func (s *Server) answerStops(w http.ResponseWriter, stops []transit.Stop, err error, from ...float64) {
	var partial *transit.Partial
	if err != nil && !errors.As(err, &partial) {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if stops == nil {
		stops = []transit.Stop{}
	}
	out := map[string]any{"stops": stops}
	if partial != nil {
		out["missed"] = partial.Error()
	}
	if len(from) == 2 {
		out["from"] = map[string]float64{"lat": from[0], "lon": from[1]}
	}
	writeJSON(w, http.StatusOK, out)
}

// transitLines is what stops at a stop.
func (s *Server) transitLines(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stop := transit.Stop{Kind: q.Get("kind"), Region: q.Get("region"), ID: strings.TrimSpace(q.Get("id"))}
	if (stop.Kind != transit.Bus && stop.Kind != transit.Subway) || stop.ID == "" ||
		(stop.Kind == transit.Bus && stop.Region != transit.Gyeonggi && stop.Region != transit.Seoul) {
		writeErr(w, http.StatusBadRequest, "kind(bus|subway), region(bus: gyeonggi|seoul), id 가 필요합니다.")
		return
	}
	lines, err := s.feed.Lines(r.Context(), stop)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if lines == nil {
		lines = []transit.Line{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// nudge says, for each route with its hours on now, when to leave - once per
// vehicle and stage: 슬슬 짐 싸야 해요 a quarter of an hour before one must
// leave, 지금 나가요 two minutes before (up to a minute after).
func (s *Server) nudge(ctx context.Context) {
	now := s.now()
	for _, source := range []string{ownerPhone, guestPhone} {
		rs, err := s.routes(ctx, source)
		if err != nil {
			s.log.Warn("경로를 읽지 못했습니다", "err", err)
			continue
		}
		for _, route := range rs {
			if !route.Push || !route.Active(now, s.loc) {
				continue
			}
			p := s.plan(ctx, route, now)
			if p.Catch == nil {
				continue
			}
			stage := ""
			switch left := p.Leave.Sub(now); {
			case left >= -time.Minute && left <= 2*time.Minute:
				stage = "go"
			case left > 2*time.Minute && left <= 15*time.Minute:
				stage = "pack"
			}
			if stage == "" {
				continue
			}
			vehicle := p.Catch.Vehicle
			if vehicle == "" {
				vehicle = p.Catch.At.In(s.loc).Truncate(5 * time.Minute).Format("0102-1504")
			}
			key := fmt.Sprintf("transit:%s:%s:%s:%s", source, route.ID, vehicle, stage)
			if !s.transitSaid.first(key, now) {
				continue
			}
			s.sayNudge(source, route, p, stage, key)
		}
	}
}

func (s *Server) sayNudge(source string, route transit.Route, p transit.Plan, stage, key string) {
	board := p.Catch.At.In(s.loc).Format("15:04")
	line := p.Catch.Line.Name
	if route.Stop.Kind == transit.Subway && p.Catch.Line.Toward != "" {
		line += "(" + strings.TrimSpace(strings.SplitN(p.Catch.Line.Toward, " - ", 2)[0]) + ")"
	}
	walk := fmt.Sprintf("%s까지 도보 %d분", route.Stop.Name, route.Walk)
	title, body := "슬슬 짐 싸야 해요", fmt.Sprintf("%s %s 타기 · %s · %d분 뒤 나가요", board, line, walk, max(1, int(p.Leave.Sub(p.Now).Minutes()+0.5)))
	if stage == "go" {
		title, body = "지금 나가요", fmt.Sprintf("%s %s 타기 · %s", board, line, walk)
	}
	audience := "owner"
	if source == guestPhone {
		audience = "guest"
	}
	s.events.Emit(map[string]any{
		"source": "context", "type": "transit." + stage, "subject": route.Name,
		"data":       map[string]any{"route": route.ID, "name": route.Name, "board": p.Catch.At, "leave": p.Leave, "who": audience},
		"key":        key,
		"occurredAt": p.Now,
		"notify":     map[string]string{"tier": "now", "title": title, "body": body, "url": "/transit", "audience": audience},
	})
	s.log.Info("출발을 알립니다", "who", audience, "route", route.Name, "stage", stage, "board", board)
}
