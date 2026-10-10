// Package api is events-svc's HTTP surface. Same bearer scheme as the rest
// of the stack: everything but /health needs the token.
//
//	POST /events                      a service reports something
//	PUT  /subscriptions/{consumer}    a consumer registers {url, types, fromStart}
//	GET  /subscriptions               who is subscribed and how far along
//	GET  /events?after=&limit=        the log, for looking back
package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/events-svc/internal/store"
)

type Server struct {
	store *store.Store
	wake  func()
	sync  func()
	token string
	log   *slog.Logger
}

func New(s *store.Store, wake, sync func(), token string, log *slog.Logger) *Server {
	return &Server{store: s, wake: wake, sync: sync, token: token, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /events", s.auth(s.append))
	mux.HandleFunc("PUT /subscriptions/{consumer}", s.auth(s.subscribe))
	mux.HandleFunc("GET /subscriptions", s.auth(s.subscriptions))
	mux.HandleFunc("GET /events", s.auth(s.list))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "인증이 필요합니다."})
			return
		}
		next(w, r)
	}
}

// incoming is the event as sent. Besides the event's own fields it accepts
// the notification shape services sent before there was an event log
// (kind, tier, title...): that becomes an event of type kind, carrying its
// wording as notify. Senders move to the event shape one at a time.
type incoming struct {
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject"`
	Data       json.RawMessage `json:"data"`
	Notify     *store.Notify   `json:"notify"`
	Key        string          `json:"key"`
	OccurredAt *time.Time      `json:"occurredAt"`

	Kind  string `json:"kind"`
	Tier  string `json:"tier"`
	Level string `json:"level"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

func (in incoming) event() (store.Event, string) {
	e := store.Event{Source: in.Source, Type: in.Type, Subject: in.Subject, Data: in.Data, Notify: in.Notify, Key: in.Key}
	if in.OccurredAt != nil {
		e.OccurredAt = *in.OccurredAt
	}
	if e.Type == "" && in.Kind != "" {
		e.Type = in.Kind
		if e.Notify == nil && in.Title != "" {
			e.Notify = &store.Notify{Tier: in.Tier, Level: in.Level, Title: in.Title, Body: in.Body, URL: in.URL}
		}
	}
	switch {
	case e.Source == "" || e.Type == "":
		return e, "source 와 type 이 필요합니다"
	case len(e.Type) > 80 || len(e.Source) > 40 || len(e.Subject) > 200 || len(e.Key) > 200:
		return e, "source, type, subject, key 가 너무 깁니다"
	case len(e.Data) > 0 && !json.Valid(e.Data):
		return e, "data 는 JSON 이어야 합니다"
	case e.Notify != nil && (e.Notify.Title == "" || !validTier(e.Notify.Tier)):
		return e, "notify 에는 title 과 tier(now, digest, log)가 필요합니다"
	}
	return e, ""
}

func validTier(t string) bool { return t == "now" || t == "digest" || t == "log" }

func (s *Server) append(w http.ResponseWriter, r *http.Request) {
	var in incoming
	if !readJSON(w, r, &in) {
		return
	}
	e, problem := in.event()
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	saved, dup, err := s.store.Append(r.Context(), e)
	switch {
	case err != nil:
		s.log.Error("이벤트를 저장하지 못했습니다", "err", err, "type", e.Type)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "이벤트를 저장하지 못했습니다."})
	case dup:
		writeJSON(w, http.StatusOK, map[string]any{"duplicate": true})
	default:
		s.wake()
		writeJSON(w, http.StatusCreated, saved)
	}
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL       string   `json:"url"`
		Types     []string `json:"types"`
		FromStart bool     `json:"fromStart"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	consumer := r.PathValue("consumer")
	if consumer == "" || !strings.HasPrefix(body.URL, "http") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "consumer 와 http 주소가 필요합니다."})
		return
	}
	if len(body.Types) == 0 {
		body.Types = []string{"*"}
	}
	sub, err := s.store.Subscribe(r.Context(), consumer, body.URL, body.Types, body.FromStart)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "구독을 저장하지 못했습니다."})
		return
	}
	s.sync()
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) subscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.store.Subscriptions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "구독을 읽지 못했습니다."})
		return
	}
	latest, _ := s.store.Latest(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"latest": latest, "subscriptions": subs})
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	events, err := s.store.After(r.Context(), after, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "이벤트를 읽지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
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
