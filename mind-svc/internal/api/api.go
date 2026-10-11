// Package api is mind-svc's HTTP surface.
//
//	GET  /health
//	POST /consume                     events-svc delivers an observation (token)
//	GET  /                            the console: simulator, world model, intents
//	GET  /api/snapshot                everything the console shows
//	POST /api/simulate                {room, roomName, sender, mine, text, at?}
//	POST /api/read                    {room?} interpret now instead of waiting for quiet
//	POST /api/settle                  hand settling changes to the skills now
//	POST /api/tick                    {name: "morning"}
//	POST /api/clock                   {advance: "25h"} move a memory-mode clock (tests the timers)
//	POST /api/intents/{id}/{approve|reject|undo}
//	GET  /api/entities/{id}/changes   an entity's history, with evidence
//	POST /api/replay                  {after, limit} the log through a fresh loop, in memory
//	GET  /api/recall?q=&person=       what is known: world model + long-term memory
//	POST /api/facts/flush             send the ledger's pending facts to memory now
//
// /api and the console need the token unless open: in the stack the port is
// bound to the host's loopback, and a laptop run has nothing to protect.
package api

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
)

//go:embed ui/index.html
var ui embed.FS

type Server struct {
	m     *mind.Mind
	token string
	open  bool
	clock *clock.Clock // nil unless the clock can be moved (memory mode)
	log   *slog.Logger
}

func New(m *mind.Mind, token string, open bool, c *clock.Clock, log *slog.Logger) *Server {
	return &Server{m: m, token: token, open: open, clock: c, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "mode": s.m.Opts.Mode, "llm": s.m.Opts.LLM.Ready()})
	})
	mux.HandleFunc("POST /consume", s.auth(true, s.consume))
	mux.HandleFunc("GET /{$}", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		page, _ := ui.ReadFile("ui/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	}))
	mux.HandleFunc("GET /api/snapshot", s.auth(false, s.snapshot))
	mux.HandleFunc("POST /api/simulate", s.auth(false, s.simulate))
	mux.HandleFunc("POST /api/read", s.auth(false, s.read))
	mux.HandleFunc("POST /api/settle", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		s.m.Attn.Settle(r.Context())
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("POST /api/tick", s.auth(false, s.tick))
	mux.HandleFunc("POST /api/clock", s.auth(false, s.moveClock))
	mux.HandleFunc("POST /api/intents/{id}/{action}", s.auth(false, s.decide))
	mux.HandleFunc("GET /api/entities/{id}/changes", s.auth(false, s.changes))
	mux.HandleFunc("POST /api/replay", s.auth(false, s.replay))
	mux.HandleFunc("GET /api/recall", s.auth(false, s.recall))
	mux.HandleFunc("POST /api/facts/flush", s.auth(false, s.flush))
	return mux
}

// auth guards a route with the token; the console's routes are let through
// when the server is open. /consume always needs it.
func (s *Server) auth(always bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if (always || !s.open) && s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "인증이 필요합니다."})
			return
		}
		next(w, r)
	}
}

// consume takes one observation from events-svc. A failure is a 500, so
// events-svc tries it again; what can never be handled is skipped.
func (s *Server) consume(w http.ResponseWriter, r *http.Request) {
	var o obs.Observation
	if !readJSON(w, r, &o) {
		return
	}
	if err := s.m.Handle(r.Context(), o); err != nil {
		s.log.Warn("관측을 처리하지 못했습니다", "type", o.Type, "id", o.ID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) recall(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	people := r.URL.Query()["person"]
	if q == "" && len(people) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q 나 person 이 필요합니다."})
		return
	}
	rec, err := s.m.Recall(r.Context(), q, people, 10)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) flush(w http.ResponseWriter, r *http.Request) {
	if s.m.Remember == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "장기 기억이 연결되어 있지 않습니다."})
		return
	}
	n, err := s.m.Remember.Flush(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"sent": n, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.m.Snapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Room     string `json:"room"`
		RoomName string `json:"roomName"`
		Sender   string `json:"sender"`
		Mine     bool   `json:"mine"`
		Text     string `json:"text"`
		At       string `json:"at"` // optional, RFC 3339 or "2006-01-02T15:04" in local time
	}
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Text) == "" || strings.TrimSpace(in.Room) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "room 과 text 가 필요합니다."})
		return
	}
	var at time.Time
	if in.At != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, in.At); err != nil {
			if at, err = time.ParseInLocation("2006-01-02T15:04", in.At, s.m.Opts.Loc); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at 은 RFC 3339 나 2006-01-02T15:04 입니다."})
				return
			}
		}
	}
	if in.RoomName == "" {
		in.RoomName = in.Room
	}
	msg, err := s.m.Simulate(r.Context(), "sim:"+in.Room, in.RoomName, in.Sender, in.Mine, in.Text, at)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Room string `json:"room"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	var err error
	if in.Room == "" {
		err = s.m.ReadAll(r.Context())
	} else {
		err = s.m.Conv.Read(r.Context(), in.Room)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) tick(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	s.m.Attn.Tick(r.Context(), in.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) moveClock(w http.ResponseWriter, r *http.Request) {
	if s.clock == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "실제 시계로 도는 중이라 시간을 옮길 수 없습니다."})
		return
	}
	var in struct {
		Advance string `json:"advance"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	d, err := time.ParseDuration(in.Advance)
	if err != nil || d <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "advance 는 25h 같은 양의 기간입니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]time.Time{"now": s.clock.Advance(d)})
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in model.Intent
	var err error
	switch r.PathValue("action") {
	case "approve":
		in, err = s.m.Gate.Approve(r.Context(), id)
	case "reject":
		in, err = s.m.Gate.Reject(r.Context(), id)
	case "undo":
		in, err = s.m.Gate.Undo(r.Context(), id)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "approve, reject, undo 중 하나입니다."})
		return
	}
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 제안이 없습니다."})
	case err != nil:
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, in)
	}
}

func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	cs, err := s.m.Opts.Store.Changes(r.Context(), r.PathValue("id"), 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": cs})
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		After int64 `json:"after"`
		Limit int   `json:"limit"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
	if in.Limit <= 0 || in.Limit > 5000 {
		in.Limit = 1000
	}
	rep, err := s.m.Replay(r.Context(), s.m.Opts.LLM, in.After, in.Limit)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, v)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON 을 읽지 못했습니다."})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
