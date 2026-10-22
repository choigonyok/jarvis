// Package api is context-svc's HTTP surface.
//
//	GET    /health
//	POST   /owntracks        the phone's OwnTracks app (HTTP Basic). The reply
//	                         may carry a command that changes how it watches.
//	                         The guest's phone has credentials of its own: its
//	                         fixes are kept apart and move none of the
//	                         operator's visits, state or events.
//	POST   /signals          {source, kind, state, at?} iOS Shortcuts, the Mac
//	                         collector, Home Assistant (bearer: the signal token)
//	GET    /now              where and in what state, now (bearer: API token)
//	GET    /visits?from=&to= stays in regions
//	GET    /travel?days=60   the journeys between regions, from the stays,
//	                         and how long each way usually takes
//	GET    /track?date=&who= one day's fixes, where it began and its stays -
//	                         the 위치 screen draws and replays the day from it
//	                         (who: owner, the default, or guest)
//	GET    /regions          PUT /regions {name, kind, lat, lon, radiusM, area}
//	DELETE /regions/{name}
//	PUT    /owntracks/cmd    {action: status|reportLocation} ask the phone, with
//	                         the next reply (status: it answers with its app
//	                         version and state - is it hearing commands?)
//	PUT    /owntracks/mode   {mode: -1|0|1|2} tell the phone to watch this way
//	                         with the reply to its next report (the server
//	                         cannot wake it), and keep it so for an hour -
//	                         "follow closely now"
//	GET    /owntracks/setup  the phone's setup: an OwnTracks configuration
//	                         (owntracks:///config?inline=…) and what the
//	                         shortcuts post - for the console, behind its login
//	GET    /owntracks/setup/guest  the guest's phone's: its own credentials,
//	                         nothing of the operator's
//
// Only /owntracks and /signals face the phone, through the gateway; each
// has its own credential, so the phone never holds the stack's token.
//
// When the state in one place changes (driving, in a meeting, home, at
// work, focus), an event says so: context.state, for whatever listens -
// notify-svc holds pushes while driving or in a meeting. It is checked on
// every report and every minute, since a signal also ends by going stale.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/context-svc/internal/place"
	"github.com/choigonyok/jarvis/context-svc/internal/store"
)

type Config struct {
	Token       string // the stack's API token: reads and regions
	SignalToken string // what shortcuts and collectors present on /signals
	User, Pass  string // OwnTracks' HTTP Basic credentials
	// The guest's phone's own credentials. Its fixes are kept (as
	// owntracks-guest) for the 위치 screen and for how it is told to watch,
	// and nothing else.
	GuestUser, GuestPass string
	// Public is where the phone reaches this service (the gateway's
	// /context), for the setup it is given.
	Public string
	// Trust: a fix less accurate than this (meters) does not move visits.
	Trust float64
	// RawDays is how long the raw reports are kept (CONTEXT_RAW_DAYS): a
	// day further back has no fixes left to draw.
	RawDays int
}

type Server struct {
	st     *store.Store
	cfg    Config
	events *notify.Client
	log    *slog.Logger
	now    func() time.Time
	loc    *time.Location
	mu     sync.Mutex // one state check at a time
}

func New(st *store.Store, cfg Config, events *notify.Client, log *slog.Logger) *Server {
	if cfg.Trust == 0 {
		cfg.Trust = 300
	}
	if cfg.RawDays == 0 {
		cfg.RawDays = 30
	}
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		loc = time.Local
	}
	return &Server{st: st, cfg: cfg, events: events, log: log, now: time.Now, loc: loc}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	// 폰이 부르는 두 경로는 공개 호스트에서 열려 있다. 토큰을 맞히려는 시도와
	// 고장 난 단축어의 폭주를 둘 다 막도록 경로마다 요청 수를 제한한다.
	mux.HandleFunc("POST /owntracks", limit(newBucket(120, 2), s.owntracks))
	mux.HandleFunc("POST /signals", limit(newBucket(60, 1), s.signals))
	mux.HandleFunc("GET /now", s.bearer(s.current))
	mux.HandleFunc("GET /visits", s.bearer(s.visits))
	mux.HandleFunc("GET /travel", s.bearer(s.travel))
	mux.HandleFunc("GET /track", s.bearer(s.track))
	mux.HandleFunc("GET /regions", s.bearer(s.regions))
	mux.HandleFunc("PUT /regions", s.bearer(s.putRegion))
	mux.HandleFunc("DELETE /regions/{name}", s.bearer(s.deleteRegion))
	mux.HandleFunc("GET /owntracks/setup", s.bearer(s.setup))
	mux.HandleFunc("GET /owntracks/setup/guest", s.bearer(s.setupGuest))
	mux.HandleFunc("PUT /owntracks/mode", s.bearer(s.putMode))
	mux.HandleFunc("PUT /owntracks/cmd", s.bearer(s.putCmd))
	return mux
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Server) bearer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token != "" && !same(r.Header.Get("Authorization"), "Bearer "+s.cfg.Token) {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

// --- OwnTracks ---------------------------------------------------------------

// report is what OwnTracks posts: a location fix, or a region transition.
type report struct {
	Type      string   `json:"_type"`
	Lat       float64  `json:"lat"`
	Lon       float64  `json:"lon"`
	Acc       float64  `json:"acc"`
	Tst       int64    `json:"tst"`
	Batt      int      `json:"batt"`
	BS        int      `json:"bs"` // battery status: 2 charging, 3 full
	InRegions []string `json:"inregions"`
	Event     string   `json:"event"` // enter | leave
	Desc      string   `json:"desc"`  // the region's name
	Rad       int      `json:"rad"`   // a waypoint's radius
	M         *int     `json:"m"`     // the monitoring mode the phone is in
	Motion    []string `json:"motionactivities"`
	// Waypoints: the regions set on the phone, sent when they change.
	Waypoints []report `json:"waypoints"`
}

// Location is the last fix, as /now tells it.
type Location struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Acc      float64 `json:"acc"`
	Batt     int     `json:"batt,omitempty"`
	Charging bool    `json:"charging,omitempty"`
}

// The phones, as the raw reports name them.
const (
	ownerPhone = "owntracks"
	guestPhone = "owntracks-guest"
)

func (s *Server) owntracks(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	source := ""
	switch {
	case ok && s.cfg.User != "" && same(user, s.cfg.User) && same(pass, s.cfg.Pass):
		source = ownerPhone
	case ok && s.cfg.GuestUser != "" && same(user, s.cfg.GuestUser) && same(pass, s.cfg.GuestPass):
		source = guestPhone
	}
	if source == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="jarvis"`)
		writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	var rep report
	if err := json.Unmarshal(raw, &rep); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON 을 읽지 못했습니다.")
		return
	}
	at := time.Unix(rep.Tst, 0)
	if rep.Tst == 0 {
		at = s.now()
	}
	ctx := r.Context()
	if err := s.st.Raw(ctx, source, orDefault(rep.Type, "unknown"), at, raw); err != nil {
		s.fail(w, "원본을 남기지 못했습니다", err)
		return
	}
	if source == guestPhone {
		cmds, err := s.guestWatch(ctx, rep, at)
		if err != nil {
			s.fail(w, "상대 폰의 위치를 반영하지 못했습니다", err)
			return
		}
		writeJSON(w, http.StatusOK, cmds)
		return
	}
	cmds, err := s.ingest(ctx, rep, at)
	if err != nil {
		s.fail(w, "위치를 반영하지 못했습니다", err)
		return
	}
	s.Check(ctx)
	writeJSON(w, http.StatusOK, cmds)
}

// ingest moves visits and the state by one report and says what the phone
// should be told.
func (s *Server) ingest(ctx context.Context, rep report, at time.Time) ([]map[string]any, error) {
	regions, err := s.st.Regions(ctx)
	if err != nil {
		return nil, err
	}
	kindOf := map[string]string{}
	for _, rg := range regions {
		kindOf[rg.Name] = rg.Kind
	}
	type move struct{ event, region string }
	var moves []move
	// Whether the report says where the phone is now, and if so whether it
	// is settled there: a late or rough fix says neither.
	judged, settled := false, false
	switch rep.Type {
	case "transition":
		if rep.Desc == "" || (rep.Event != "enter" && rep.Event != "leave") {
			return []map[string]any{}, nil
		}
		changed := false
		if rep.Event == "enter" {
			changed, err = s.st.Enter(ctx, rep.Desc, at, "transition")
		} else {
			changed, err = s.st.Leave(ctx, rep.Desc, at)
		}
		if err != nil {
			return nil, err
		}
		if changed {
			moves = append(moves, move{rep.Event, rep.Desc})
		}
		// In a region, or out of one and in none: on the way.
		open, err := s.st.Open(ctx)
		if err != nil {
			return nil, err
		}
		judged, settled = true, rep.Event == "enter" || len(open) > 0
	case "location":
		loc := Location{Lat: rep.Lat, Lon: rep.Lon, Acc: rep.Acc, Batt: rep.Batt, Charging: rep.BS == 2 || rep.BS == 3}
		newer, err := s.st.SetIfNewer(ctx, "location", loc, at)
		if err != nil {
			return nil, err
		}
		if !newer {
			break // a fix that arrived late says nothing about where the phone is now
		}
		if rep.Acc > s.cfg.Trust {
			break // too rough to say which region
		}
		// A fix says which regions the phone is in now - by the phone's own
		// list and by the circles kept here - and so corrects any enter or
		// leave that never arrived.
		in := place.Inside(regions, rep.Lat, rep.Lon, rep.Acc)
		for _, n := range rep.InRegions {
			if !slices.Contains(in, n) {
				in = append(in, n)
			}
		}
		open, err := s.st.Open(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range open {
			if !slices.Contains(in, v.Region) {
				if ok, err := s.st.Leave(ctx, v.Region, at); err != nil {
					return nil, err
				} else if ok {
					moves = append(moves, move{"leave", v.Region})
				}
			}
		}
		for _, n := range in {
			if ok, err := s.st.Enter(ctx, n, at, "fix"); err != nil {
				return nil, err
			} else if ok {
				moves = append(moves, move{"enter", n})
			}
		}
		if settled, err = s.settledAt(ctx, ownerPhone, rep, at, len(in) > 0); err != nil {
			return nil, err
		}
		judged = true
	case "waypoints", "waypoint":
		// Regions made on the phone come here as they are: kept by name, a
		// kind guessed from it unless one was set here.
		wps := rep.Waypoints
		if rep.Type == "waypoint" {
			wps = []report{rep}
		}
		for _, wp := range wps {
			name := strings.TrimSpace(wp.Desc)
			if name == "" || (wp.Lat == 0 && wp.Lon == 0) {
				continue
			}
			rg := place.Region{Name: name, Kind: place.KindOf(name), Lat: wp.Lat, Lon: wp.Lon, RadiusM: max(wp.Rad, 50)}
			for _, old := range regions {
				if old.Name == name {
					rg.Kind, rg.Area = old.Kind, old.Area
				}
			}
			if err := s.st.PutRegion(ctx, rg); err != nil {
				return nil, err
			}
		}
		return []map[string]any{}, nil
	default:
		return []map[string]any{}, nil
	}

	var current struct {
		Mode int `json:"mode"`
	}
	current.Mode = place.Significant
	_, _ = s.st.Get(ctx, "owntracks.mode", &current)
	// The phone says which mode it is in: that, not what was last sent,
	// is what a change is judged against - a command it did not take is
	// sent again.
	if rep.M != nil && *rep.M >= place.Quiet && *rep.M <= place.Move {
		current.Mode = *rep.M
	}
	// Questions asked by hand (PUT /owntracks/cmd) go with this reply, once.
	var extra []map[string]any
	var ask struct {
		Action string `json:"action"`
	}
	if _, err := s.st.Get(ctx, "owntracks.cmd", &ask); err == nil && ask.Action != "" {
		if err := s.st.Drop(ctx, "owntracks.cmd"); err != nil {
			return nil, err
		}
		extra = append(extra, map[string]any{"_type": "cmd", "action": ask.Action})
		s.log.Info("폰에 명령을 보냅니다", "action", ask.Action)
	}
	// Asked for by hand (PUT /owntracks/mode): said with this reply, and kept
	// for an hour - the rule below would otherwise undo it at the next report.
	var asked struct {
		Mode *int `json:"mode"`
	}
	if _, err := s.st.Get(ctx, "owntracks.asked", &asked); err == nil && asked.Mode != nil {
		if err := s.st.Drop(ctx, "owntracks.asked"); err != nil {
			return nil, err
		}
		if err := s.st.Set(ctx, "owntracks.mode", map[string]int{"mode": *asked.Mode}, s.now()); err != nil {
			return nil, err
		}
		if err := s.st.Set(ctx, "owntracks.hold", hold{Mode: *asked.Mode, Until: s.now().Add(time.Hour)}, s.now()); err != nil {
			return nil, err
		}
		s.log.Info("폰의 위치 모드를 바꿉니다(요청)", "from", current.Mode, "to", *asked.Mode)
		for _, m := range moves {
			s.announce(m.event, m.region, kindOf[m.region], at)
		}
		return append(place.Command(*asked.Mode), extra...), nil
	}
	for _, m := range moves {
		s.announce(m.event, m.region, kindOf[m.region], at)
	}
	// Settled where it is - in a region, or still for a while - it watches
	// lightly; out and moving, closely. A mode asked for by hand holds for its
	// hour, and the battery has the last word either way.
	charging := rep.BS == 2 || rep.BS == 3
	var held hold
	_, err = s.st.Get(ctx, "owntracks.hold", &held)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	var want int
	switch {
	case err == nil && s.now().Before(held.Until):
		want = place.Battery(held.Mode, rep.Batt, charging)
	case judged:
		want = place.Watch(settled, rep.Batt, charging)
	default:
		want = place.Battery(current.Mode, rep.Batt, charging)
	}
	if want == current.Mode {
		if extra == nil {
			extra = []map[string]any{}
		}
		return extra, nil
	}
	if err := s.st.Set(ctx, "owntracks.mode", map[string]int{"mode": want}, s.now()); err != nil {
		return nil, err
	}
	s.log.Info("폰의 위치 모드를 바꿉니다", "from", current.Mode, "to", want)
	return append(place.Command(want), extra...), nil
}

// How long in one spot, and how small a spot, counts as staying put.
const (
	stillFor    = 15 * time.Minute
	stillRadius = 150.0
)

// hold is a mode asked for by hand (PUT /owntracks/mode), kept until when.
type hold struct {
	Mode  int       `json:"mode"`
	Until time.Time `json:"until"`
}

// settledAt is whether a phone is settled where this fix puts it: inside a
// region (in) or, outside them and not in a car, still in one spot for a
// while - in a car it is moving, however long the jam. source is the phone.
func (s *Server) settledAt(ctx context.Context, source string, rep report, at time.Time, in bool) (bool, error) {
	if in {
		return true, nil
	}
	if place.Activity(rep.Motion) == "automotive" {
		return false, nil
	}
	fixes, err := s.st.Fixes(ctx, source, at.Add(-6*time.Hour), at.Add(time.Second))
	if err != nil {
		return false, err
	}
	spots := make([]place.Spot, 0, len(fixes))
	for _, f := range fixes {
		if f.Acc <= s.cfg.Trust {
			spots = append(spots, place.Spot{Lat: f.Lat, Lon: f.Lon, At: f.At})
		}
	}
	return place.Still(spots, rep.Lat, rep.Lon, at, stillRadius, stillFor), nil
}

// guestWatch is how the guest's phone should watch, said with the reply to
// its report - the operator's rule (ingest): lightly (Significant) while it is
// settled - inside a region, one it set itself (the phone's own inregions) or
// one kept here, or still in one spot for a while - and closely (Move) once it
// is out and moving, so its way is drawn in detail. The phone's own word on
// the mode it is in is what a change is judged against, and a low battery
// keeps it from Move.
func (s *Server) guestWatch(ctx context.Context, rep report, at time.Time) ([]map[string]any, error) {
	none := []map[string]any{}
	if rep.Type != "location" || rep.Acc > s.cfg.Trust {
		return none, nil
	}
	var kept struct {
		Mode int `json:"mode"`
	}
	kept.Mode = place.Significant
	if _, err := s.st.Get(ctx, "owntracks.mode.guest", &kept); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	current := kept.Mode
	if rep.M != nil && *rep.M >= place.Quiet && *rep.M <= place.Move {
		current = *rep.M
	}
	regions, err := s.st.Regions(ctx)
	if err != nil {
		return nil, err
	}
	in := len(rep.InRegions) > 0 || len(place.Inside(regions, rep.Lat, rep.Lon, rep.Acc)) > 0
	settled, err := s.settledAt(ctx, guestPhone, rep, at, in)
	if err != nil {
		return nil, err
	}
	want := place.Watch(settled, rep.Batt, rep.BS == 2 || rep.BS == 3)
	if want == current {
		return none, nil
	}
	if err := s.st.Set(ctx, "owntracks.mode.guest", map[string]int{"mode": want}, s.now()); err != nil {
		return nil, err
	}
	s.log.Info("상대 폰의 위치 모드를 바꿉니다", "from", current, "to", want)
	return place.Command(want), nil
}

// announce reports an arrival or a departure as an event: to the inbox,
// and to whatever listens (location.enter / location.leave).
func (s *Server) announce(event, region, kind string, at time.Time) {
	title := region + " 도착"
	if event == "leave" {
		title = region + "에서 출발"
	}
	day := at.Format("2006-01-02")
	s.events.Emit(map[string]any{
		"source": "context", "type": "location." + event, "subject": region,
		"data":       map[string]string{"region": region, "kind": kind},
		"key":        fmt.Sprintf("location:%s:%s:%s:%d", event, region, day, at.Unix()),
		"occurredAt": at,
		"notify":     map[string]string{"tier": "log", "title": title, "body": at.Format("15:04")},
	})
}

// --- signals ----------------------------------------------------------------

var kindRE = regexp.MustCompile(`^[a-z][a-z0-9_.-]{1,63}$`)

// signals takes one state change from a shortcut, the Mac collector or a
// home hub: {source, kind, state, at?}. A missed one is corrected by the
// next, since each says the state, not the change.
func (s *Server) signals(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !(s.cfg.SignalToken != "" && same(auth, "Bearer "+s.cfg.SignalToken)) && !(s.cfg.Token != "" && same(auth, "Bearer "+s.cfg.Token)) {
		writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	var in struct {
		Source string `json:"source"`
		Kind   string `json:"kind"`
		State  string `json:"state"`
		At     string `json:"at"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON 을 읽지 못했습니다.")
		return
	}
	in.Kind, in.State = strings.TrimSpace(in.Kind), strings.TrimSpace(in.State)
	if !kindRE.MatchString(in.Kind) || len([]rune(in.State)) > 120 {
		writeErr(w, http.StatusBadRequest, "kind 는 소문자 점 표기(device.carplay), state 는 짧은 값입니다.")
		return
	}
	at := s.now()
	if t, err := time.Parse(time.RFC3339, in.At); err == nil {
		at = t
	}
	if err := s.st.Raw(r.Context(), orDefault(in.Source, "unknown"), in.Kind, at, raw); err != nil {
		s.fail(w, "원본을 남기지 못했습니다", err)
		return
	}
	if err := s.st.Set(r.Context(), "signal:"+in.Kind, map[string]string{"state": in.State, "source": in.Source}, at); err != nil {
		s.fail(w, "상태를 남기지 못했습니다", err)
		return
	}
	s.Check(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// --- reads ------------------------------------------------------------------

// Now is the state in one place.
type Now struct {
	Location *Location      `json:"location,omitempty"`
	FixAt    *time.Time     `json:"fixAt,omitempty"`
	Visits   []store.Visit  `json:"visits"` // regions the operator is in now
	Signals  map[string]Sig `json:"signals"`
	State    State          `json:"state"`
	Summary  string         `json:"summary"`
	Mode     int            `json:"mode"`
	// Health is each body measure a shortcut sends, against the operator's
	// own usual (the median of the 28 days before).
	Health []place.Health `json:"health,omitempty"`
}

// State is the current state in one place, derived from the visits and
// the signals: what an answer may need to know before it is given.
type State struct {
	Driving bool `json:"driving"`
	Meeting bool `json:"meeting"`
	Home    bool `json:"home"`
	Work    bool `json:"work"`
	Focus   bool `json:"focus"`
}

// Derive works the state out. A signal older than its kind lasts is not
// believed: a meeting app in front an hour ago says nothing of now.
func Derive(n Now, now time.Time) State {
	fresh := func(kind string, within time.Duration) (string, bool) {
		sg, ok := n.Signals[kind]
		if !ok || now.Sub(sg.At) > within {
			return "", false
		}
		return sg.State, true
	}
	var st State
	if v, ok := fresh("device.carplay", 4*time.Hour); ok && (v == "connected" || v == "on") {
		st.Driving = true
	}
	if v, ok := fresh("mac.app", 15*time.Minute); ok && v == "meeting" {
		st.Meeting = true
	}
	if v, ok := fresh("device.focus", 12*time.Hour); ok && v != "" && v != "off" && v != "끔" {
		st.Focus = true
		if strings.Contains(v, "운전") || strings.Contains(strings.ToLower(v), "driving") {
			st.Driving = true
		}
	}
	for _, v := range n.Visits {
		switch v.Kind {
		case "home":
			st.Home = true
		case "work":
			st.Work = true
		}
	}
	return st
}

type Sig struct {
	State  string    `json:"state"`
	Source string    `json:"source,omitempty"`
	At     time.Time `json:"at"`
}

func (s *Server) current(w http.ResponseWriter, r *http.Request) {
	out, err := s.snapshot(r.Context())
	if err != nil {
		s.fail(w, "상태를 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// snapshot is the state in one place, now.
func (s *Server) snapshot(ctx context.Context) (Now, error) {
	out := Now{Signals: map[string]Sig{}, Mode: place.Significant}
	var loc Location
	if at, err := s.st.Get(ctx, "location", &loc); err == nil {
		out.Location, out.FixAt = &loc, &at
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	var err error
	if out.Visits, err = s.st.Open(ctx); err != nil {
		return out, err
	}
	states, err := s.st.States(ctx)
	if err != nil {
		return out, err
	}
	for _, st := range states {
		if k, ok := strings.CutPrefix(st.Key, "signal:"); ok {
			var v struct{ State, Source string }
			_ = json.Unmarshal(st.Value, &v)
			out.Signals[k] = Sig{State: v.State, Source: v.Source, At: st.At}
		}
		if st.Key == "owntracks.mode" {
			var m struct{ Mode int }
			_ = json.Unmarshal(st.Value, &m)
			out.Mode = m.Mode
		}
	}
	for kind := range out.Signals {
		if !strings.HasPrefix(kind, "health.") {
			continue
		}
		rs, err := s.st.Readings(ctx, kind, s.now().AddDate(0, 0, -30))
		if err != nil {
			return out, err
		}
		if h, ok := place.Baseline(kind, rs, s.loc); ok {
			out.Health = append(out.Health, h)
		}
	}
	slices.SortFunc(out.Health, func(a, b place.Health) int { return strings.Compare(a.Kind, b.Kind) })
	out.State = Derive(out, s.now())
	out.Summary = Summary(out, s.now())
	return out, nil
}

// Check compares the state in one place with the one last announced, and
// announces it when it changed: context.state, with what changed.
func (s *Server) Check(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.snapshot(ctx)
	if err != nil {
		s.log.Warn("상태를 읽지 못했습니다", "err", err)
		return
	}
	var last State
	if _, err := s.st.Get(ctx, "derived", &last); err != nil && !errors.Is(err, store.ErrNotFound) {
		return
	}
	changed := Changed(last, n.State)
	if len(changed) == 0 {
		return
	}
	now := s.now()
	if err := s.st.Set(ctx, "derived", n.State, now); err != nil {
		s.log.Warn("상태를 남기지 못했습니다", "err", err)
		return
	}
	s.events.Emit(map[string]any{
		"source": "context", "type": "context.state", "subject": strings.Join(changed, ", "),
		"data":       map[string]any{"state": n.State, "changed": changed, "summary": n.Summary},
		"key":        fmt.Sprintf("context.state:%d", now.UnixNano()),
		"occurredAt": now,
	})
}

// Watch checks the state every minute: a signal ends by going stale too.
func (s *Server) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Check(ctx)
		}
	}
}

// Changed is what differs between two states, in words: "운전 시작",
// "회의 끝".
func Changed(a, b State) []string {
	var out []string
	for _, f := range []struct {
		was, is bool
		what    string
	}{{a.Driving, b.Driving, "운전"}, {a.Meeting, b.Meeting, "회의"}, {a.Home, b.Home, "집"}, {a.Work, b.Work, "회사"}, {a.Focus, b.Focus, "집중 모드"}} {
		switch {
		case !f.was && f.is:
			out = append(out, f.what+" 시작")
		case f.was && !f.is:
			out = append(out, f.what+" 끝")
		}
	}
	return out
}

// putCmd asks the phone something with the next reply: its status (which
// says whether it hears commands at all, and its version) or a fix now.
// Nothing that would make it send its configuration back (dump: it holds
// the password).
func (s *Server) putCmd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil || (in.Action != "status" && in.Action != "reportLocation" && in.Action != "waypoints") {
		writeErr(w, http.StatusBadRequest, "action 은 status·reportLocation·waypoints 입니다.")
		return
	}
	if err := s.st.Set(r.Context(), "owntracks.cmd", map[string]string{"action": in.Action}, s.now()); err != nil {
		s.fail(w, "명령을 남기지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"action": in.Action, "when": "폰의 다음 보고에 답으로"})
}

// putMode asks the phone to watch another way, with the reply to its next
// report: Move to follow closely for a while, Significant to save battery.
func (s *Server) putMode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode *int `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil || in.Mode == nil || *in.Mode < place.Quiet || *in.Mode > place.Move {
		writeErr(w, http.StatusBadRequest, "mode 는 -1(Quiet)·0(Manual)·1(Significant)·2(Move) 중 하나입니다.")
		return
	}
	if err := s.st.Set(r.Context(), "owntracks.asked", map[string]int{"mode": *in.Mode}, s.now()); err != nil {
		s.fail(w, "모드를 남기지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"mode": *in.Mode, "when": "폰의 다음 보고에 답으로"})
}

// setup is what the phone needs, for the console to hand over: OwnTracks'
// configuration as a link the app imports (HTTP mode to the gateway, Basic
// auth, Significant, commands and remote configuration on so the reply can
// switch modes), and what the shortcuts post. Behind the API token: it
// holds the phone's credentials.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if s.cfg.User == "" || s.cfg.Pass == "" || s.cfg.Public == "" {
		writeErr(w, http.StatusServiceUnavailable, "OWNTRACKS_USER/OWNTRACKS_PASSWORD 와 CONTEXT_PUBLIC_URL 이 있어야 합니다.")
		return
	}
	base := strings.TrimRight(s.cfg.Public, "/")
	out := map[string]any{
		"link": s.configLink(s.cfg.User, s.cfg.Pass, "iphone", "me"),
		"url":  base + "/owntracks",
		"signals": map[string]any{
			"url": base + "/signals", "token": s.cfg.SignalToken,
			"examples": []map[string]string{
				{"when": "CarPlay 연결", "body": `{"source":"shortcut","kind":"device.carplay","state":"connected"}`},
				{"when": "CarPlay 끊김", "body": `{"source":"shortcut","kind":"device.carplay","state":"disconnected"}`},
				{"when": "집중 모드 켬", "body": `{"source":"shortcut","kind":"device.focus","state":"on"}`},
				{"when": "집중 모드 끔", "body": `{"source":"shortcut","kind":"device.focus","state":"off"}`},
				{"when": "충전기 연결", "body": `{"source":"shortcut","kind":"device.charging","state":"on"}`},
				{"when": "충전기 분리", "body": `{"source":"shortcut","kind":"device.charging","state":"off"}`},
				{"when": "매일 아침: 심박 변이(건강 샘플 찾기의 값)", "body": `{"source":"shortcut","kind":"health.hrv","state":"<값>"}`},
				{"when": "매일 아침: 안정 시 심박", "body": `{"source":"shortcut","kind":"health.hr_rest","state":"<값>"}`},
				{"when": "매일 아침: 어젯밤 수면 시간(시간)", "body": `{"source":"shortcut","kind":"health.sleep","state":"<값>"}`},
			},
		},
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// setupGuest is the guest's phone's setup, for the guest's own console to
// hand over: the guest's credentials and nothing of the operator's - no
// signal token, no shortcuts. Commands and remote configuration are on, so
// the reply to a report can switch how it watches (guestWatch).
func (s *Server) setupGuest(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GuestUser == "" || s.cfg.GuestPass == "" || s.cfg.Public == "" {
		writeErr(w, http.StatusServiceUnavailable, "OWNTRACKS_GUEST_USER/OWNTRACKS_GUEST_PASSWORD 와 CONTEXT_PUBLIC_URL 이 있어야 합니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"link": s.configLink(s.cfg.GuestUser, s.cfg.GuestPass, "guest-iphone", "gs"),
		"url":  strings.TrimRight(s.cfg.Public, "/") + "/owntracks",
	})
}

// configLink is a whole OwnTracks configuration as the link the app imports:
// HTTP mode to the gateway, Basic auth, Significant to begin with, commands
// and remote configuration on so the reply can switch modes.
func (s *Server) configLink(user, pass, device, tid string) string {
	cfg := map[string]any{
		"_type": "configuration", "mode": 3, "url": strings.TrimRight(s.cfg.Public, "/") + "/owntracks",
		"auth": true, "username": user, "password": pass,
		"deviceId": device, "tid": tid,
		"monitoring": place.Significant, "cmd": true, "remoteConfiguration": true,
	}
	body, _ := json.Marshal(cfg)
	return "owntracks:///config?inline=" + url.QueryEscape(base64.StdEncoding.EncodeToString(body))
}

// travel is the journeys between regions over the last days, and how long
// each way usually takes - from the stays, not from a map: the time it
// actually took.
func (s *Server) travel(w http.ResponseWriter, r *http.Request) {
	days := 60
	if v := r.URL.Query().Get("days"); v != "" {
		if _, err := fmt.Sscan(v, &days); err != nil || days < 1 || days > 400 {
			writeErr(w, http.StatusBadRequest, "days 는 1~400 입니다.")
			return
		}
	}
	now := s.now()
	vs, err := s.st.Visits(r.Context(), now.AddDate(0, 0, -days), now.Add(time.Minute))
	if err != nil {
		s.fail(w, "체류를 읽지 못했습니다", err)
		return
	}
	stays := make([]place.Stay, len(vs))
	for i, v := range vs {
		stays[i] = place.Stay{Region: v.Region, Area: v.Area, In: v.EnteredAt, Out: v.LeftAt}
	}
	trips := place.Trips(stays)
	if trips == nil {
		trips = []place.Trip{}
	}
	routes := place.Routes(trips, s.loc)
	if routes == nil {
		routes = []place.Route{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"trips": trips, "routes": routes})
}

// track is one day's way about (a day in Seoul), for the 위치 screen to draw
// and replay: every fix of the day, the last trustworthy one before it -
// where the day began - the stays that overlap it, the regions to name places
// by, and how far back fixes go (the screen goes no further). The fixes are
// as the phone sent them, rough ones too: what to smooth or leave out is the
// drawing's call. who picks the phone: the operator's (owner, the default)
// or the guest's, which has no stays here - only its fixes.
func (s *Server) track(w http.ResponseWriter, r *http.Request) {
	who, source := "owner", ownerPhone
	switch r.URL.Query().Get("who") {
	case "", "owner":
	case "guest":
		who, source = "guest", guestPhone
	default:
		writeErr(w, http.StatusBadRequest, "who 는 owner 나 guest 입니다.")
		return
	}
	now := s.now().In(s.loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc)
	if v := r.URL.Query().Get("date"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, s.loc)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "date 는 YYYY-MM-DD 입니다.")
			return
		}
		day = d
	}
	end := day.AddDate(0, 0, 1)
	ctx := r.Context()
	fixes, err := s.st.Fixes(ctx, source, day, end)
	if err != nil {
		s.fail(w, "위치를 읽지 못했습니다", err)
		return
	}
	before, err := s.st.FixBefore(ctx, source, day, s.cfg.Trust)
	if err != nil {
		s.fail(w, "위치를 읽지 못했습니다", err)
		return
	}
	visits := []store.Visit{}
	if who == "owner" {
		if visits, err = s.st.Visits(ctx, day, end); err != nil {
			s.fail(w, "체류를 읽지 못했습니다", err)
			return
		}
	}
	regions, err := s.st.Regions(ctx)
	if err != nil {
		s.fail(w, "영역을 읽지 못했습니다", err)
		return
	}
	first, err := s.st.FirstFix(ctx, source)
	if err != nil {
		s.fail(w, "위치를 읽지 못했습니다", err)
		return
	}
	out := map[string]any{
		"who": who, "date": day.Format("2006-01-02"), "from": day, "to": end,
		"fixes": fixes, "before": before, "visits": visits, "regions": regions,
		"keepDays": s.cfg.RawDays,
	}
	if first != nil {
		out["since"] = first.In(s.loc).Format("2006-01-02")
	}
	writeJSON(w, http.StatusOK, out)
}

// Summary is the state in a line: where (since when), what the devices
// say, and how old the last fix is.
func Summary(n Now, now time.Time) string {
	var parts []string
	for _, v := range n.Visits {
		parts = append(parts, fmt.Sprintf("지금 %s (%s부터)", v.Region, v.EnteredAt.In(now.Location()).Format("15:04")))
	}
	if len(n.Visits) == 0 && n.FixAt != nil {
		parts = append(parts, "등록된 영역 밖")
	}
	for _, f := range []struct {
		on   bool
		what string
	}{{n.State.Driving, "운전 중"}, {n.State.Meeting, "회의 중"}, {n.State.Focus, "집중 모드"}} {
		if f.on {
			parts = append(parts, f.what)
		}
	}
	for _, h := range n.Health {
		if h.Notable() && now.Sub(h.At) < 36*time.Hour {
			parts = append(parts, h.Line())
		}
	}
	keys := make([]string, 0, len(n.Signals))
	for k := range n.Signals {
		if strings.HasPrefix(k, "health.") {
			continue // said against the usual above, or not at all
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		sg := n.Signals[k]
		if now.Sub(sg.At) > 12*time.Hour {
			continue // stale: not "now" any more
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, sg.State))
	}
	if n.FixAt != nil {
		parts = append(parts, fmt.Sprintf("마지막 위치 %s 전", ago(now.Sub(*n.FixAt))))
	} else {
		parts = append(parts, "위치를 받은 적 없음")
	}
	return strings.Join(parts, " · ")
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "방금"
	case d < time.Hour:
		return fmt.Sprintf("%d분", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d시간", int(d.Hours()))
	}
	return fmt.Sprintf("%d일", int(d.Hours()/24))
}

func (s *Server) visits(w http.ResponseWriter, r *http.Request) {
	from, err1 := when(r.URL.Query().Get("from"), s.now().AddDate(0, 0, -7))
	to, err2 := when(r.URL.Query().Get("to"), s.now().Add(time.Minute))
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "from, to 는 RFC 3339 나 YYYY-MM-DD 입니다.")
		return
	}
	vs, err := s.st.Visits(r.Context(), from, to)
	if err != nil {
		s.fail(w, "체류를 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"visits": vs})
}

func when(v string, def time.Time) (time.Time, error) {
	if v == "" {
		return def, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02", v, time.Local)
}

func (s *Server) regions(w http.ResponseWriter, r *http.Request) {
	rs, err := s.st.Regions(r.Context())
	if err != nil {
		s.fail(w, "영역을 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"regions": rs})
}

func (s *Server) putRegion(w http.ResponseWriter, r *http.Request) {
	var rg place.Region
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&rg); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON 을 읽지 못했습니다.")
		return
	}
	rg.Name = strings.TrimSpace(rg.Name)
	if rg.Name == "" || rg.Lat < -90 || rg.Lat > 90 || rg.Lon < -180 || rg.Lon > 180 || (rg.Lat == 0 && rg.Lon == 0) {
		writeErr(w, http.StatusBadRequest, "이름과 좌표가 필요합니다.")
		return
	}
	if rg.RadiusM <= 0 {
		rg.RadiusM = 150
	}
	if rg.Kind == "" {
		rg.Kind = "other"
	}
	if err := s.st.PutRegion(r.Context(), rg); err != nil {
		s.fail(w, "영역을 저장하지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, rg)
}

func (s *Server) deleteRegion(w http.ResponseWriter, r *http.Request) {
	err := s.st.DeleteRegion(r.Context(), r.PathValue("name"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "그런 영역이 없습니다.")
	case err != nil:
		s.fail(w, "영역을 지우지 못했습니다", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- helpers ----------------------------------------------------------------

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	writeErr(w, http.StatusInternalServerError, msg)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// bucket is a token bucket: burst requests at once, refilled at perSec.
type bucket struct {
	mu     sync.Mutex
	tokens float64
	burst  float64
	perSec float64
	last   time.Time
}

func newBucket(burst, perSec float64) *bucket {
	return &bucket{tokens: burst, burst: burst, perSec: perSec, last: time.Now()}
}

func (b *bucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.perSec)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func limit(b *bucket, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !b.take(time.Now()) {
			w.Header().Set("Retry-After", "5")
			writeErr(w, http.StatusTooManyRequests, "요청이 너무 많습니다.")
			return
		}
		next(w, r)
	}
}
