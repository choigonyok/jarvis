package moments

import (
	"context"
	"math"
	"time"
)

// Area is a place by its name on the map - a city, a district, a station -
// that is none of the phone's regions (2026-11-06: "수원쪽에 도착하면", kept
// as a region named 수원 that never was, so it could never ring). Where the
// phone is is judged here, by the fixes context-svc passes on as they come
// (Fixed): nothing is sent anywhere to ask, and nothing asks the phone.
type Area struct {
	// Name is the map's own name for it: "수원시".
	Name string `json:"name"`
	// Rings are its outline, outer rings only, as [longitude, latitude]
	// pairs the way the map gives them: a city in two parts has two.
	Rings [][][2]float64 `json:"rings,omitempty"`
	// Lat, Lon and Radius (meters) are a circle, for a place with no
	// outline: a station, a building.
	Lat    float64 `json:"lat,omitempty"`
	Lon    float64 `json:"lon,omitempty"`
	Radius float64 `json:"radius,omitempty"`
}

// maxAreaPoints bounds an outline kept with a reminder.
const maxAreaPoints = 5000

// valid says what is wrong with an area, if anything.
func (a Area) valid() error {
	n := 0
	for _, ring := range a.Rings {
		if len(ring) < 3 {
			return ErrInvalid("지역의 경계가 점 셋보다 적습니다")
		}
		n += len(ring)
	}
	switch {
	case n > maxAreaPoints:
		return ErrInvalid("지역의 경계가 너무 큽니다")
	case n == 0 && (a.Radius <= 0 || a.Lat == 0 && a.Lon == 0):
		return ErrInvalid("지역에 경계도 반경도 없습니다")
	}
	return nil
}

// Contains is whether a point is in the area.
func (a Area) Contains(lat, lon float64) bool {
	if len(a.Rings) == 0 {
		return meters(a.Lat, a.Lon, lat, lon) <= a.Radius
	}
	for _, ring := range a.Rings {
		if inRing(ring, lon, lat) {
			return true
		}
	}
	return false
}

// trusts is the roughest fix that may say whether the phone is in the area:
// a city's outline takes a fix a few hundred metres out, a station's circle
// only one about as fine as itself.
func (a Area) trusts(acc float64) bool {
	limit := 500.0
	if len(a.Rings) == 0 && a.Radius < limit {
		limit = max(100, a.Radius)
	}
	return acc <= limit
}

// inRing is the even-odd rule on [lon, lat] pairs: flat is fine at a city's
// size.
func inRing(ring [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
		xi, yi, xj, yj := ring[i][0], ring[i][1], ring[j][0], ring[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// meters between two points, on a sphere.
func meters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// Fix is where a phone was at a moment, as context-svc passed it on.
type Fix struct {
	Lat float64
	Lon float64
	// Acc is how sure, in metres.
	Acc float64
	At  time.Time
}

// fixFresh is how long the last fix says where someone is: a reminder that
// starts to count ("19시부터 수원에 있으면") goes by it when it is that new.
const fixFresh = 2 * time.Hour

// Fixed is a fix of who's phone: their reminders waiting for an area hear it.
// Arriving is a fix inside after one outside - or, once a reminder counts
// (after), any fix inside: being there then is its moment. Leaving is a fix
// outside after one inside.
func (s *Service) Fixed(ctx context.Context, who string, f Fix) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Now().Sub(f.At) > stale {
		return nil
	}
	if s.fixes == nil {
		s.fixes = map[string]Fix{}
	}
	if last, ok := s.fixes[who]; ok && f.At.Before(last.At) {
		return nil // a late one says nothing of now
	}
	s.fixes[who] = f
	rs, err := s.St.Reminders(ctx, time.Time{})
	if err != nil {
		return err
	}
	for _, r := range rs {
		if r.Status != Waiting || r.Area == nil || r.Person() != who || f.At.Before(r.UpdatedAt) || !r.Area.trusts(f.Acc) {
			continue
		}
		in := r.Area.Contains(f.Lat, f.Lon)
		was := r.Inside
		counting := r.After == nil || !f.At.Before(*r.After)
		switch {
		case r.Move == Arrive && in && counting && r.After != nil:
			by := ByThere
			if was != nil && !*was {
				by = ByArrive
			}
			s.say(ctx, r, by)
			continue
		case r.Move == Arrive && in && counting && was != nil && !*was:
			s.say(ctx, r, ByArrive)
			continue
		case r.Move == Leave && !in && counting && was != nil && *was:
			s.say(ctx, r, ByLeave)
			continue
		}
		if was == nil || *was != in {
			r.Inside = &in
			if err := s.St.PutReminder(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// thereNow is whether who's last fix, if new enough, is in an area - for a
// reminder that starts to count and finds them already there. ok is false
// when no fix says.
func (s *Service) thereNow(who string, a Area) (in, ok bool) {
	f, known := s.fixes[who]
	if !known || s.Now().Sub(f.At) > fixFresh || !a.trusts(f.Acc) {
		return false, false
	}
	return a.Contains(f.Lat, f.Lon), true
}
