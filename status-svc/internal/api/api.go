// Package api serves the probes' latest verdicts. Same bearer scheme as the
// rest of the stack: everything but /health needs the token.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

type Server struct {
	runner *check.Runner
	token  string
}

func New(r *check.Runner, token string) *Server { return &Server{runner: r, token: token} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /status", s.auth(s.status))
	// Runs every probe now (slow ones respect their floor) and answers with
	// the result, so a "다시 확인" press shows fresh verdicts, not cached ones.
	mux.HandleFunc("POST /refresh", s.auth(s.refresh))
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

type response struct {
	check.Snapshot
	Now time.Time `json:"now"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, response{Snapshot: s.runner.Snapshot(), Now: time.Now()})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.runner.Refresh(r.Context())
	s.status(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
