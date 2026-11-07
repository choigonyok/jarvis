// Package api serves the probes' latest verdicts. Same bearer scheme as the
// rest of the stack: everything but /health needs the token.
package api

import (
	"net/http"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/svc"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

type Server struct {
	runner *check.Runner
	token  string
}

func New(r *check.Runner, token string) *Server { return &Server{runner: r, token: token} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// The stack's contract (gocommon/health) like every service's: up, and
	// no loop of its own for anyone to watch - this is the one watching.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		health.Write(w, http.StatusOK, health.Report{})
	})
	mux.HandleFunc("GET /status", s.auth(s.status))
	// Runs every probe now (slow ones respect their floor) and answers with
	// the result, so a "다시 확인" press shows fresh verdicts, not cached ones.
	mux.HandleFunc("POST /refresh", s.auth(s.refresh))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc { return svc.Guard(s.token, next) }

type response struct {
	check.Snapshot
	Now time.Time `json:"now"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	svc.WriteJSON(w, http.StatusOK, response{Snapshot: s.runner.Snapshot(), Now: time.Now()})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.runner.Refresh(r.Context())
	s.status(w, r)
}
