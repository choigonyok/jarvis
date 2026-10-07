// Package api serves the Joongna listings. Same bearer scheme as the rest of
// the stack: everything but /health needs the token.
//
// Two callers: the console's tab (list, queue a change, ask for a sync) and
// the agent's background worker (take the next change, report how it went,
// report what the shop page showed).
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/choigonyok/jarvis/market-svc/internal/listing"
	"github.com/choigonyok/jarvis/market-svc/internal/store"
)

type Server struct {
	store     *store.Store
	token     string
	syncEvery time.Duration
	purgeAt   time.Duration
	log       *slog.Logger
}

func New(s *store.Store, token string, syncEvery, purgeAfter time.Duration, log *slog.Logger) *Server {
	return &Server{store: s, token: token, syncEvery: syncEvery, purgeAt: purgeAfter, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	// 탭
	mux.HandleFunc("GET /listings", s.auth(s.list))
	mux.HandleFunc("POST /listings/{id}/tasks", s.auth(s.addTask))
	mux.HandleFunc("DELETE /tasks/{id}", s.auth(s.cancelTask))
	mux.HandleFunc("POST /sync/request", s.auth(s.requestSync))
	mux.HandleFunc("POST /login/resolved", s.auth(s.loginResolved))
	// 에이전트: 승인된 초안을 넣고, 백그라운드에서 일을 받아 결과를 적는다.
	mux.HandleFunc("POST /listings", s.auth(s.create))
	mux.HandleFunc("GET /tasks/next", s.auth(s.nextTask))
	mux.HandleFunc("POST /tasks/{id}/report", s.auth(s.reportTask))
	mux.HandleFunc("GET /sync/due", s.auth(s.syncDue))
	mux.HandleFunc("POST /sync", s.auth(s.reportSync))
	mux.HandleFunc("GET /purge/due", s.auth(s.purgeDue))
	mux.HandleFunc("POST /listings/{id}/purged", s.auth(s.markPurged))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "데이터베이스에 연결할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type listResponse struct {
	Listings []store.Listing `json:"listings"`
	State    store.State     `json:"state"`
	// SyncEveryMinutes lets the tab say when the next look is.
	SyncEveryMinutes int `json:"syncEveryMinutes"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	ls, err := s.store.List(r.Context())
	if err != nil {
		s.fail(w, "목록을 읽지 못했습니다", err)
		return
	}
	st, err := s.store.State(r.Context())
	if err != nil {
		s.fail(w, "상태를 읽지 못했습니다", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, listResponse{Listings: ls, State: st, SyncEveryMinutes: int(s.syncEvery / time.Minute)})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in listing.New
	if !decode(w, r, &in) {
		return
	}
	if err := in.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	l, err := s.store.Create(r.Context(), in)
	if err != nil {
		s.fail(w, "글을 저장하지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

func (s *Server) addTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in listing.TaskInput
	if !decode(w, r, &in) {
		return
	}
	t, err := s.store.AddTask(r.Context(), id, in)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusCreated, t)
	}
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.CancelTask(r.Context(), id); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requestSync(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RequestSync(r.Context()); err != nil {
		s.fail(w, "갱신을 요청하지 못했습니다", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loginResolved(w http.ResponseWriter, r *http.Request) {
	if err := s.store.LoginResolved(r.Context()); err != nil {
		s.fail(w, "상태를 바꾸지 못했습니다", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) nextTask(w http.ResponseWriter, r *http.Request) {
	work, err := s.store.NextTask(r.Context())
	if err != nil {
		s.fail(w, "작업을 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"work": work})
}

func (s *Server) reportTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.Report
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.ReportTask(r.Context(), id, in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) syncDue(w http.ResponseWriter, r *http.Request) {
	ls, err := s.store.SyncDue(r.Context(), s.syncEvery)
	if err != nil {
		s.fail(w, "동기화 대상을 읽지 못했습니다", err)
		return
	}
	if ls == nil {
		ls = []store.Listing{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": ls})
}

func (s *Server) reportSync(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items         []store.SyncItem `json:"items"`
		Note          string           `json:"note"`
		LoginRequired bool             `json:"loginRequired"`
	}
	if !decode(w, r, &in) {
		return
	}
	n, err := s.store.ReportSync(r.Context(), in.Items, in.Note, in.LoginRequired)
	if err != nil {
		s.fail(w, "동기화 결과를 적지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

func (s *Server) purgeDue(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.PurgeDue(r.Context(), s.purgeAt)
	if err != nil {
		s.fail(w, "정리 대상을 읽지 못했습니다", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": ps})
}

func (s *Server) markPurged(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.MarkPurged(r.Context(), id); err != nil {
		s.fail(w, "정리 기록을 남기지 못했습니다", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) fail(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "err", err)
	writeErr(w, http.StatusInternalServerError, msg+".")
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "id 가 올바르지 않습니다.")
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil || json.Unmarshal(body, v) != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
