package api

// The two phones' upkeep, the same for each: how it is told to watch and why
// (kept for the 위치 screen), its own list of regions by the ids the app gives
// them, once a day its settings and every region put back (place.Daily, 50 m),
// and a region made in the console sent to it with its replies until its own
// list has it.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/context-svc/internal/place"
	"github.com/choigonyok/jarvis/context-svc/internal/store"
)

// stateKey is a phone's key in context_state: the operator's as it is, the
// guest's with .guest after it.
func stateKey(source, key string) string {
	if source == guestPhone {
		return key + ".guest"
	}
	return key
}

// watching is how a phone was last told to watch, and why - the 위치 screen
// says it.
type watching struct {
	At   time.Time `json:"at"`   // the report it was judged by
	Mode int       `json:"mode"` // the mode it is in now: told, or as it was
	// Why: region (in one - Region), still (in one spot outside them for a
	// while), car, moving, left (just out of one, in none) or held (a mode
	// asked for by hand, for its hour).
	Why      string `json:"why"`
	Region   string `json:"region,omitempty"`
	Batt     int    `json:"batt,omitempty"`
	Charging bool   `json:"charging,omitempty"`
	// Downgraded: it would be in Move but for the battery (below place.Low,
	// not charging), so its way is drawn sparsely until it is charged.
	Downgraded bool `json:"downgraded,omitempty"`
}

// decide is the mode a phone should be in, by why and by its battery.
func (w *watching) decide(batt int, charging bool) int {
	settled := w.Why == "region" || w.Why == "still"
	w.Batt, w.Charging = batt, charging
	w.Downgraded = !settled && place.Downgraded(place.Move, batt, charging)
	w.Mode = place.Watch(settled, batt, charging)
	return w.Mode
}

func (s *Server) lastWatching(ctx context.Context, source string) (watching, error) {
	var w watching
	if _, err := s.st.Get(ctx, stateKey(source, "owntracks.watch"), &w); err != nil && !errors.Is(err, store.ErrNotFound) {
		return w, err
	}
	return w, nil
}

// settle is why a phone is settled where a fix puts it, or why not: in a
// region (in), or outside them in one spot for a while (still) - counted
// only from when it was last in one, by its word (since: it left) or by the
// circles here, so the hours at home do not make it still the moment it
// leaves; else in a car (car: moving, however long the jam) or moving.
func (s *Server) settle(ctx context.Context, source string, rep report, at time.Time, in []string, regions []place.Region, since time.Time) (watching, error) {
	if len(in) > 0 {
		return watching{Why: "region", Region: in[0]}, nil
	}
	if place.Activity(rep.Motion) == "automotive" {
		return watching{Why: "car"}, nil
	}
	from := at.Add(-6 * time.Hour)
	if since.After(from) {
		from = since
	}
	fixes, err := s.st.Fixes(ctx, source, from, at.Add(time.Second))
	if err != nil {
		return watching{}, err
	}
	spots := make([]place.Spot, 0, len(fixes))
	for _, f := range fixes {
		if f.Acc > s.cfg.Trust {
			continue
		}
		if len(place.Inside(regions, f.Lat, f.Lon, f.Acc)) > 0 {
			spots = spots[:0] // in a region then: the stay outside begins after it
			continue
		}
		spots = append(spots, place.Spot{Lat: f.Lat, Lon: f.Lon, At: f.At})
	}
	if place.Still(spots, rep.Lat, rep.Lon, at, stillRadius, stillFor) {
		return watching{Why: "still"}, nil
	}
	return watching{Why: "moving"}, nil
}

// keptRegion is a region as a phone has it: by the id the app gives it -
// renamed, it is the same region - and the time it was made, which the app
// wants back with any change to it.
type keptRegion struct {
	Rid string `json:"rid,omitempty"`
	Tst int64  `json:"tst,omitempty"`
	place.Region
}

// phoneRegion is a region as a phone sends it, if it is a place: named (the
// app calls a new point Here-… until it is named), not a beacon
// (name:uuid…), somewhere. A point (radius 0) is a place too: it is made a
// region (phoneRegions).
func phoneRegion(wp report) (keptRegion, bool) {
	name := strings.TrimSpace(wp.Desc)
	if name == "" || strings.HasPrefix(name, "Here-") || strings.Contains(name, ":") || wp.Rad < 0 || (wp.Lat == 0 && wp.Lon == 0) {
		return keptRegion{}, false
	}
	return keptRegion{Rid: wp.Rid, Tst: wp.Tst, Region: place.Region{
		Name: name, Kind: place.KindOf(name), Lat: wp.Lat, Lon: wp.Lon, RadiusM: max(wp.Rad, place.RegionRadius),
	}}, true
}

// listKey is where a phone's own list of regions is kept: the guest's is its
// regions here as well; the operator's sits beside context_regions, for the
// ids.
func listKey(source string) string {
	if source == guestPhone {
		return "guest.regions"
	}
	return "owntracks.regions"
}

func (s *Server) phoneList(ctx context.Context, source string) ([]keptRegion, error) {
	var kept []keptRegion
	if _, err := s.st.Get(ctx, listKey(source), &kept); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return kept, nil
}

func (s *Server) savePhoneList(ctx context.Context, source string, kept []keptRegion) error {
	if kept == nil {
		kept = []keptRegion{}
	}
	slices.SortFunc(kept, func(a, b keptRegion) int { return strings.Compare(a.Name, b.Name) })
	return s.st.Set(ctx, listKey(source), kept, s.now())
}

// phoneRegions takes the regions a phone sends - one made or changed on it
// (waypoint), or all it has, asked for once a day (waypoints) - and says back
// what it should change: a point (radius 0) becomes a 50 m region at once, so
// the phone watches it; with the whole list every region goes back to 50 m,
// and one made in the console goes to it - again if the list lacks it.
func (s *Server) phoneRegions(ctx context.Context, source string, rep report) ([]map[string]any, error) {
	whole := rep.Type == "waypoints"
	wps := rep.Waypoints
	if !whole {
		wps = []report{rep}
	}
	var got, fix []keptRegion
	for _, wp := range wps {
		r, ok := phoneRegion(wp)
		if !ok {
			continue
		}
		if wp.Rad == 0 || (whole && wp.Rad != place.RegionRadius) {
			r.RadiusM = place.RegionRadius
			fix = append(fix, r)
		}
		got = append(got, r)
	}
	pending, err := s.pendingRegions(ctx, source)
	if err != nil {
		return nil, err
	}
	var send []keptRegion
	if whole {
		if pending, send, err = s.listed(ctx, source, got, pending); err != nil {
			return nil, err
		}
	} else if rep.Rid != "" && slices.ContainsFunc(pending, func(p pushed) bool { return p.Rid == rep.Rid }) {
		// Changed on the phone: it has it, as it is there now.
		pending = slices.DeleteFunc(pending, func(p pushed) bool { return p.Rid == rep.Rid })
		if err := s.savePending(ctx, source, pending); err != nil {
			return nil, err
		}
	}
	// One on its way from the console is that, until the phone's list has it.
	onWay := func(r keptRegion) bool {
		return whole && slices.ContainsFunc(pending, func(p pushed) bool { return p.Rid == r.Rid })
	}
	fix = slices.DeleteFunc(fix, onWay)
	before, err := s.phoneList(ctx, source)
	if err != nil {
		return nil, err
	}
	kept := []keptRegion{}
	for _, r := range before {
		switch {
		case whole:
			if onWay(r) {
				kept = append(kept, r)
			}
		case rep.Rid != "" && r.Rid != rep.Rid, rep.Rid == "" && r.Name != strings.TrimSpace(rep.Desc):
			kept = append(kept, r)
		}
	}
	fresh := slices.DeleteFunc(slices.Clone(got), onWay)
	kept = append(kept, fresh...)
	if err := s.savePhoneList(ctx, source, kept); err != nil {
		return nil, err
	}
	if source == guestPhone {
		err = s.guestRegionsMoved(ctx, before, kept)
	} else {
		err = s.ownerRegions(ctx, fresh)
	}
	if err != nil {
		return nil, err
	}
	cmds := []map[string]any{}
	if send = append(fix, send...); len(send) > 0 {
		cmds = append(cmds, setWaypoints(send))
	}
	return cmds, nil
}

// ownerRegions keeps regions the operator's phone has in context_regions:
// by name, a kind guessed from it unless one was set here.
func (s *Server) ownerRegions(ctx context.Context, got []keptRegion) error {
	regions, err := s.st.Regions(ctx)
	if err != nil {
		return err
	}
	for _, r := range got {
		rg := r.Region
		for _, old := range regions {
			if old.Name == rg.Name {
				rg.Kind, rg.Area = old.Kind, old.Area
			}
		}
		if err := s.st.PutRegion(ctx, rg); err != nil {
			return err
		}
	}
	return nil
}

// pushed is a region made in the console, on its way to its phone. It goes
// with a reply and a question for the phone's whole list, which confirms it,
// and again every so often until the list has it - for a week at most. Ask:
// the phone's list was never heard when it was made, so the list comes first,
// in case the phone has one of the same name (then that one is moved).
type pushed struct {
	keptRegion
	At   time.Time `json:"at"`
	Sent time.Time `json:"sent,omitempty"`
	Ask  bool      `json:"ask,omitempty"`
}

func (s *Server) pendingRegions(ctx context.Context, source string) ([]pushed, error) {
	var ps []pushed
	if _, err := s.st.Get(ctx, stateKey(source, "owntracks.push"), &ps); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return slices.DeleteFunc(ps, func(p pushed) bool { return s.now().Sub(p.At) > 7*24*time.Hour }), nil
}

func (s *Server) savePending(ctx context.Context, source string, ps []pushed) error {
	if ps == nil {
		ps = []pushed{}
	}
	return s.st.Set(ctx, stateKey(source, "owntracks.push"), ps, s.now())
}

// listed is what a phone's whole list says of the regions on their way to it:
// one it has is there (by id) and done with; one asked about first goes now,
// as the phone's own of that name if it has one; one sent before that it
// still lacks goes again. It returns those still on their way, and those to
// send.
func (s *Server) listed(ctx context.Context, source string, got []keptRegion, pending []pushed) ([]pushed, []keptRegion, error) {
	if err := s.st.Set(ctx, stateKey(source, "owntracks.listed"), struct{}{}, s.now()); err != nil {
		return nil, nil, err
	}
	var left []pushed
	var send []keptRegion
	for _, p := range pending {
		if p.Ask {
			if i := slices.IndexFunc(got, func(r keptRegion) bool { return r.Name == p.Name }); i >= 0 {
				if err := s.renumber(ctx, source, p.Rid, got[i].Rid, got[i].Tst); err != nil {
					return nil, nil, err
				}
				p.Rid, p.Tst = got[i].Rid, got[i].Tst
			}
			p.Ask = false
		} else if slices.ContainsFunc(got, func(r keptRegion) bool { return r.Rid == p.Rid }) {
			continue
		} else if p.Sent.IsZero() {
			left = append(left, p) // not sent yet: it goes with the next reply
			continue
		}
		p.Sent = s.now()
		send = append(send, p.keptRegion)
		left = append(left, p)
	}
	return left, send, s.savePending(ctx, source, left)
}

// renumber gives a region made in the console the id of the phone's own of the
// same name, so the phone moves that one rather than gaining a second.
func (s *Server) renumber(ctx context.Context, source, from, to string, tst int64) error {
	kept, err := s.phoneList(ctx, source)
	if err != nil {
		return err
	}
	for i := range kept {
		if kept[i].Rid == from {
			kept[i].Rid, kept[i].Tst = to, tst
		}
	}
	return s.savePhoneList(ctx, source, kept)
}

// pushes is what a reply carries of the regions on their way to the phone:
// those due (never sent, or not for ten minutes) and a question for its whole
// list - which alone, while any waits to be asked about first.
func (s *Server) pushes(ctx context.Context, source string) ([]map[string]any, error) {
	ps, err := s.pendingRegions(ctx, source)
	if err != nil || len(ps) == 0 {
		return nil, err
	}
	var send []keptRegion
	ask := false
	for i := range ps {
		if s.now().Sub(ps[i].Sent) < 10*time.Minute {
			continue
		}
		ask = true
		ps[i].Sent = s.now()
		if !ps[i].Ask {
			send = append(send, ps[i].keptRegion)
		}
	}
	if !ask {
		return nil, nil
	}
	if err := s.savePending(ctx, source, ps); err != nil {
		return nil, err
	}
	var cmds []map[string]any
	if len(send) > 0 {
		cmds = append(cmds, setWaypoints(send))
	}
	return append(cmds, map[string]any{"_type": "cmd", "action": "waypoints"}), nil
}

// daily is what a phone's first report of a day (in Seoul) is answered with:
// its settings put back (place.Daily) and a question for all its regions,
// which phoneRegions puts back to 50 m. The regions kept here go to 50 m now.
func (s *Server) daily(ctx context.Context, source string) ([]map[string]any, error) {
	var last struct {
		Day string `json:"day"`
	}
	if _, err := s.st.Get(ctx, stateKey(source, "owntracks.daily"), &last); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	today := s.now().In(s.loc).Format("2006-01-02")
	if last.Day == today {
		return nil, nil
	}
	kept, err := s.phoneList(ctx, source)
	if err != nil {
		return nil, err
	}
	for i := range kept {
		kept[i].RadiusM = place.RegionRadius
	}
	if err := s.savePhoneList(ctx, source, kept); err != nil {
		return nil, err
	}
	if source == ownerPhone {
		if err := s.st.SetRadius(ctx, place.RegionRadius); err != nil {
			return nil, err
		}
	}
	if err := s.st.Set(ctx, stateKey(source, "owntracks.daily"), map[string]string{"day": today}, s.now()); err != nil {
		return nil, err
	}
	s.log.Info("폰의 설정과 영역 반경을 되돌립니다", "phone", source)
	return []map[string]any{place.Daily(), {"_type": "cmd", "action": "waypoints"}}, nil
}

// waypoint is a region as the app takes it.
func waypoint(r keptRegion) map[string]any {
	tst := r.Tst
	if tst == 0 {
		tst = time.Now().Unix()
	}
	return map[string]any{"_type": "waypoint", "desc": r.Name, "lat": r.Lat, "lon": r.Lon, "rad": r.RadiusM, "tst": tst, "rid": r.Rid}
}

// setWaypoints is the command that puts regions on a phone, each by its id:
// one it has is changed in place, one it lacks is added.
func setWaypoints(rs []keptRegion) map[string]any {
	wps := make([]map[string]any, len(rs))
	for i, r := range rs {
		wps[i] = waypoint(r)
	}
	return map[string]any{"_type": "cmd", "action": "setWaypoints", "waypoints": map[string]any{"_type": "waypoints", "waypoints": wps}}
}

// regionLink is the OwnTracks link that puts one region on the phone at once:
// the app opens it, shows the change and asks to take it.
func regionLink(r keptRegion) string {
	body, _ := json.Marshal(map[string]any{"_type": "configuration", "waypoints": []map[string]any{waypoint(r)}})
	return "owntracks:///config?inline=" + url.QueryEscape(base64.StdEncoding.EncodeToString(body))
}

// newRid is an id for a region made here, like the app's own.
func newRid() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// registerRegion makes where a person is now one of their phone's regions:
// 50 m, by the name given - one of that name already there moves here, keeping
// its id. It counts here at once; the phone gets it with the reply to its next
// report, or now through the link when its list is known. Made where the phone
// is, it is no arrival.
func (s *Server) registerRegion(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string  `json:"name"`
			Lat  float64 `json:"lat"`
			Lon  float64 `json:"lon"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, "JSON 을 읽지 못했습니다.")
			return
		}
		name := strings.TrimSpace(in.Name)
		switch {
		case name == "" || utf8.RuneCountInString(name) > 30:
			writeErr(w, http.StatusBadRequest, "이름은 1~30자입니다.")
			return
		case strings.ContainsAny(name, ":|") || strings.HasPrefix(name, "Here-"):
			writeErr(w, http.StatusBadRequest, "이름에 : 나 | 는 쓸 수 없습니다.")
			return
		case in.Lat < -90 || in.Lat > 90 || in.Lon < -180 || in.Lon > 180 || (in.Lat == 0 && in.Lon == 0):
			writeErr(w, http.StatusBadRequest, "위치가 올바르지 않습니다.")
			return
		}
		mu := &s.ownerMu
		if source == guestPhone {
			mu = &s.guestMu
		}
		mu.Lock()
		defer mu.Unlock()
		ctx := r.Context()
		region, link, err := s.register(ctx, source, name, in.Lat, in.Lon)
		if err != nil {
			s.fail(w, "영역을 등록하지 못했습니다", err)
			return
		}
		out := map[string]any{"region": region.Region}
		if link != "" {
			out["link"] = link
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) register(ctx context.Context, source, name string, lat, lon float64) (keptRegion, string, error) {
	before, err := s.phoneList(ctx, source)
	if err != nil {
		return keptRegion{}, "", err
	}
	_, err = s.st.Get(ctx, stateKey(source, "owntracks.listed"), &struct{}{})
	known := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return keptRegion{}, "", err
	}
	region := keptRegion{Rid: newRid(), Tst: s.now().Unix(), Region: place.Region{
		Name: name, Kind: place.KindOf(name), Lat: lat, Lon: lon, RadiusM: place.RegionRadius,
	}}
	if i := slices.IndexFunc(before, func(r keptRegion) bool { return r.Name == name }); i >= 0 && before[i].Rid != "" {
		region.Rid, region.Tst = before[i].Rid, before[i].Tst
	}
	kept := slices.DeleteFunc(slices.Clone(before), func(r keptRegion) bool { return r.Rid == region.Rid || r.Name == name })
	if err := s.savePhoneList(ctx, source, append(kept, region)); err != nil {
		return keptRegion{}, "", err
	}
	if source == guestPhone {
		err = s.guestRegionsMoved(ctx, before, append(kept, region))
	} else {
		err = s.ownerRegistered(ctx, region.Region)
	}
	if err != nil {
		return keptRegion{}, "", err
	}
	ps, err := s.pendingRegions(ctx, source)
	if err != nil {
		return keptRegion{}, "", err
	}
	ps = slices.DeleteFunc(ps, func(p pushed) bool { return p.Rid == region.Rid || p.Name == name })
	ps = append(ps, pushed{keptRegion: region, At: s.now(), Ask: !known})
	if err := s.savePending(ctx, source, ps); err != nil {
		return keptRegion{}, "", err
	}
	s.log.Info("영역을 등록합니다", "phone", source, "region", name)
	if !known {
		return region, "", nil
	}
	return region, regionLink(region), nil
}

// ownerRegistered keeps a region made in the console for the operator: in
// context_regions (its kind and area kept if the name was there), and - where
// the phone last was inside it - as a stay begun now, without a word: it is
// where they already are, not an arrival.
func (s *Server) ownerRegistered(ctx context.Context, rg place.Region) error {
	if err := s.ownerRegions(ctx, []keptRegion{{Region: rg}}); err != nil {
		return err
	}
	var loc Location
	if _, err := s.st.Get(ctx, "location", &loc); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	if len(place.Inside([]place.Region{rg}, loc.Lat, loc.Lon, loc.Acc)) == 0 {
		return nil
	}
	_, err := s.st.Enter(ctx, rg.Name, s.now(), "console")
	return err
}

func (s *Server) saveWatching(ctx context.Context, source string, w watching) error {
	return s.st.Set(ctx, stateKey(source, "owntracks.watch"), w, w.At)
}
