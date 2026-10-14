// Package api is context-svc's HTTP surface.
//
//	GET    /health
//	POST   /owntracks        the phone's OwnTracks app (HTTP Basic). The reply
//	                         may carry a command that changes how it watches.
//	POST   /signals          {source, kind, state, at?} iOS Shortcuts, the Mac
//	                         collector, Home Assistant (bearer: the signal token)
//	GET    /now              where and in what state, now (bearer: API token)
//	GET    /visits?from=&to= stays in regions
//	GET    /regions          PUT /regions {name, kind, lat, lon, radiusM, area}
//	DELETE /regions/{name}
//
// Only /owntracks and /signals face the phone, through the gateway; each
// has its own credential, so the phone never holds the stack's token.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/notify"
	"github.com/choigonyok/jarvis/context-svc/internal/place"
	"github.com/choigonyok/jarvis/context-svc/internal/store"
)

type Config struct {
	Token       string // the stack's API token: reads and regions
	SignalToken string // what shortcuts and collectors present on /signals
	User, Pass  string // OwnTracks' HTTP Basic credentials
	// Trust: a fix less accurate than this (meters) does not move visits.
	Trust float64
}

type Server struct {
	st     *store.Store
	cfg    Config
	events *notify.Client
	log    *slog.Logger
	now    func() time.Time
}

func New(st *store.Store, cfg Config, events *notify.Client, log *slog.Logger) *Server {
	if cfg.Trust == 0 {
		cfg.Trust = 300
	}
	return &Server{st: st, cfg: cfg, events: events, log: log, now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /owntracks", s.owntracks)
	mux.HandleFunc("POST /signals", s.signals)
	mux.HandleFunc("GET /now", s.bearer(s.current))
	mux.HandleFunc("GET /visits", s.bearer(s.visits))
	mux.HandleFunc("GET /regions", s.bearer(s.regions))
	mux.HandleFunc("PUT /regions", s.bearer(s.putRegion))
	mux.HandleFunc("DELETE /regions/{name}", s.bearer(s.deleteRegion))
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
}

// Location is the last fix, as /now tells it.
type Location struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Acc      float64 `json:"acc"`
	Batt     int     `json:"batt,omitempty"`
	Charging bool    `json:"charging,omitempty"`
}

func (s *Server) owntracks(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if s.cfg.User == "" || !ok || !same(user, s.cfg.User) || !same(pass, s.cfg.Pass) {
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
	if err := s.st.Raw(ctx, "owntracks", orDefault(rep.Type, "unknown"), at, raw); err != nil {
		s.fail(w, "원본을 남기지 못했습니다", err)
		return
	}
	cmds, err := s.ingest(ctx, rep, at)
	if err != nil {
		s.fail(w, "위치를 반영하지 못했습니다", err)
		return
	}
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
	case "location":
		loc := Location{Lat: rep.Lat, Lon: rep.Lon, Acc: rep.Acc, Batt: rep.Batt, Charging: rep.BS == 2 || rep.BS == 3}
		if err := s.st.Set(ctx, "location", loc, at); err != nil {
			return nil, err
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
	default:
		return []map[string]any{}, nil
	}

	var current struct {
		Mode int `json:"mode"`
	}
	current.Mode = place.Significant
	_, _ = s.st.Get(ctx, "owntracks.mode", &current)
	want := current.Mode
	for _, m := range moves {
		s.announce(m.event, m.region, kindOf[m.region], at)
		want = place.Mode(want, m.event, kindOf[m.region], rep.Batt, rep.BS == 2 || rep.BS == 3)
	}
	if len(moves) == 0 {
		want = place.Mode(want, "", "", rep.Batt, rep.BS == 2 || rep.BS == 3)
	}
	if want == current.Mode {
		return []map[string]any{}, nil
	}
	if err := s.st.Set(ctx, "owntracks.mode", map[string]int{"mode": want}, s.now()); err != nil {
		return nil, err
	}
	s.log.Info("폰의 위치 모드를 바꿉니다", "from", current.Mode, "to", want)
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
	w.WriteHeader(http.StatusNoContent)
}

// --- reads ------------------------------------------------------------------

// Now is the state in one place.
type Now struct {
	Location *Location      `json:"location,omitempty"`
	FixAt    *time.Time     `json:"fixAt,omitempty"`
	Visits   []store.Visit  `json:"visits"` // regions the operator is in now
	Signals  map[string]Sig `json:"signals"`
	Summary  string         `json:"summary"`
	Mode     int            `json:"mode"`
}

type Sig struct {
	State  string    `json:"state"`
	Source string    `json:"source,omitempty"`
	At     time.Time `json:"at"`
}

func (s *Server) current(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := Now{Signals: map[string]Sig{}, Mode: place.Significant}
	var loc Location
	if at, err := s.st.Get(ctx, "location", &loc); err == nil {
		out.Location, out.FixAt = &loc, &at
	} else if !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "상태를 읽지 못했습니다", err)
		return
	}
	var err error
	if out.Visits, err = s.st.Open(ctx); err != nil {
		s.fail(w, "체류를 읽지 못했습니다", err)
		return
	}
	states, err := s.st.States(ctx)
	if err != nil {
		s.fail(w, "상태를 읽지 못했습니다", err)
		return
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
	out.Summary = Summary(out, s.now())
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
	keys := make([]string, 0, len(n.Signals))
	for k := range n.Signals {
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
