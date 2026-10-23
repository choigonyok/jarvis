// Package transit is when to leave for the bus or the train one usually
// takes: the vehicles coming to a stop now - from the public real-time feeds,
// 경기's and 서울's buses and 서울's subway - and of them the one a person can
// still make from where they are, by the walk to the stop, with what to say
// about it the way a friend watching the clock would: 여유 있어요, 슬슬 짐 싸야
// 해요, 지금 나가요.
package transit

import (
	"math"
	"slices"
	"time"
)

// Kinds of stop.
const (
	Bus    = "bus"
	Subway = "subway"
)

// Regions of a bus stop: its feed.
const (
	Gyeonggi = "gyeonggi"
	Seoul    = "seoul"
)

// Stop is where one boards.
type Stop struct {
	Kind string `json:"kind"` // bus | subway
	// Region: a bus stop's feed, gyeonggi or seoul.
	Region string `json:"region,omitempty"`
	// ID: a bus stop's id in its feed (경기 stationId, 서울 arsId); a
	// station's name, as the subway feed knows it (강남, not 강남역).
	ID   string `json:"id"`
	Name string `json:"name"`
	// No: the number a bus stop shows (정류소 번호).
	No   string  `json:"no,omitempty"`
	Lat  float64 `json:"lat,omitempty"`
	Lon  float64 `json:"lon,omitempty"`
	Dist int     `json:"dist,omitempty"` // meters from where it was looked for
}

// Line is what stops there: a bus route, or a subway line in one direction.
type Line struct {
	// ID: a bus route's id in its feed; a subway line's id and direction
	// (1002|외선).
	ID     string `json:"id"`
	Name   string `json:"name"`             // 7770, 2호선
	Toward string `json:"toward,omitempty"` // 강남역 방면, 성수행 - 역삼방면
}

// Arrival is one vehicle coming to a stop.
type Arrival struct {
	Line Line      `json:"line"`
	At   time.Time `json:"at"` // when it reaches the stop
	// Vehicle: which one (plate, train number) - so a nudge is said once
	// for it, however its time moves.
	Vehicle string `json:"vehicle,omitempty"`
	Note    string `json:"note,omitempty"` // 급행, 막차, 3번째 전
	// Rough: guessed from how far away it is, not a time the feed gave.
	Rough bool `json:"rough,omitempty"`
}

// Route is a way one usually goes: from a stop, by a line, after a walk to it
// - and when it is gone, for the nudges.
type Route struct {
	ID    string `json:"id"`
	Name  string `json:"name"` // 출근, 퇴근, 학원
	Stop  Stop   `json:"stop"`
	Line  Line   `json:"line"`
	Walk  int    `json:"walk"`  // minutes to walk to the stop
	Spare int    `json:"spare"` // minutes to be there early
	// Days (1 Monday … 7 Sunday; none, every day) and hours ("HH:MM") it is
	// gone in, for the nudges; past midnight when To is before From.
	Days []int  `json:"days,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Push bool   `json:"push"`
}

// Stages of a plan, by how long until one must leave.
const (
	Relax = "relax" // over 20 minutes
	Pack  = "pack"  // 10 to 20: time to pack up
	Soon  = "soon"  // 3 to 10
	Go    = "go"    // 3 or less
	Run   = "run"   // up to two late: hurrying may still make it
	None  = "none"  // nothing coming that can be made
	Off   = "off"   // no live feed for it now (Error says why)
)

// Plan is when to leave for a route, now: the vehicle to make, when to go
// for it, the ones after it, and the line to say.
type Plan struct {
	Route string    `json:"route"`
	Now   time.Time `json:"now"`
	Catch *Arrival  `json:"catch,omitempty"`
	Leave time.Time `json:"leave,omitzero"`
	Next  []Arrival `json:"next"`
	Stage string    `json:"stage"`
	Say   string    `json:"say"`
	Error string    `json:"error,omitempty"`
}

// Make is a route's plan at now, from the arrivals at its stop: the first of
// its line one can still make - leaving in time for the walk and the minutes
// to spare, or at most two minutes late, hurrying.
func Make(r Route, arrivals []Arrival, now time.Time) Plan {
	p := Plan{Route: r.ID, Now: now, Next: []Arrival{}}
	var mine []Arrival
	for _, a := range arrivals {
		if a.Line.ID == r.Line.ID {
			mine = append(mine, a)
		}
	}
	slices.SortFunc(mine, func(a, b Arrival) int { return a.At.Compare(b.At) })
	ahead := time.Duration(max(r.Walk, 0)+max(r.Spare, 0)) * time.Minute
	for i, a := range mine {
		leave := a.At.Add(-ahead)
		if leave.Before(now.Add(-2 * time.Minute)) {
			continue
		}
		p.Catch, p.Leave, p.Next = &mine[i], leave, mine[i+1:]
		break
	}
	if p.Catch == nil {
		p.Stage, p.Say = None, "곧 오는 차가 없어요"
		return p
	}
	switch left := p.Leave.Sub(now); {
	case left < 0:
		p.Stage, p.Say = Run, "뛰면 탈 수 있어요"
	case left <= 3*time.Minute:
		p.Stage, p.Say = Go, "지금 나가요!"
	case left <= 10*time.Minute:
		p.Stage, p.Say = Soon, "곧 나가야 해요"
	case left <= 20*time.Minute:
		p.Stage, p.Say = Pack, "슬슬 짐 싸야 해요"
	default:
		p.Stage, p.Say = Relax, "여유 있어요"
	}
	return p
}

// Failed is a plan with no live feed for it now, and why.
func Failed(r Route, now time.Time, why string) Plan {
	return Plan{Route: r.ID, Now: now, Next: []Arrival{}, Stage: Off, Say: "실시간 정보를 못 받았어요", Error: why}
}

// Active is whether a route is gone at t (in loc): on one of its days, inside
// its hours. A route without hours is never gone - no nudges.
func (r Route) Active(t time.Time, loc *time.Location) bool {
	from, okFrom := minutes(r.From)
	to, okTo := minutes(r.To)
	if !okFrom || !okTo {
		return false
	}
	t = t.In(loc)
	now := t.Hour()*60 + t.Minute()
	day := t
	if to < from && now < to {
		day = t.AddDate(0, 0, -1) // past midnight: the window began the day before
	}
	if len(r.Days) > 0 && !slices.Contains(r.Days, weekday(day)) {
		return false
	}
	if to < from {
		return now >= from || now < to
	}
	return now >= from && now < to
}

// weekday is 1 for Monday … 7 for Sunday.
func weekday(t time.Time) int {
	if t.Weekday() == time.Sunday {
		return 7
	}
	return int(t.Weekday())
}

func minutes(hm string) (int, bool) {
	t, err := time.Parse("15:04", hm)
	if err != nil || len(hm) != 5 {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// Clock reports whether hm is a clock time, "HH:MM".
func Clock(hm string) bool {
	_, ok := minutes(hm)
	return ok
}

// Walk is the minutes a walk of meters (as the crow flies) takes: streets
// are a quarter longer than the line, and a person walks 70 m a minute.
func Walk(meters float64) int {
	return max(1, int(math.Ceil(meters*1.25/70)))
}

// Meters between two points (haversine).
func Meters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dlat, dlon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// lineNames is the subway feed's line ids, by the names riders use.
var lineNames = map[string]string{
	"1001": "1호선", "1002": "2호선", "1003": "3호선", "1004": "4호선", "1005": "5호선",
	"1006": "6호선", "1007": "7호선", "1008": "8호선", "1009": "9호선",
	"1061": "중앙선", "1063": "경의중앙선", "1065": "공항철도", "1067": "경춘선",
	"1075": "수인분당선", "1077": "신분당선", "1092": "우이신설선", "1093": "서해선",
	"1081": "경강선", "1032": "GTX-A",
}

// LineName is a subway line's name from the feed's id.
func LineName(id string) string {
	if n, ok := lineNames[id]; ok {
		return n
	}
	return id + "호선"
}
