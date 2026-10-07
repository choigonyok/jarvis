// Package api serves the calendar over HTTP.
//
// There is no approval gate here, and that is deliberate: this service does
// what it is told. The gate lives in the agent, where the card and the decision
// are - by the time a write reaches this service, either a person typed it or a
// person approved it.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
	"github.com/choigonyok/jarvis/calendar-svc/internal/store"
)

// Backend is where events live. The Postgres store was the only one; uniple
// (a couple's shared calendar, reached through its own Supabase) replaces it
// without the agent or the web noticing - they speak this API, not a table.
type Backend interface {
	Ping(ctx context.Context) error
	Range(ctx context.Context, from, to string) ([]event.Event, error)
	Get(ctx context.Context, id string) (event.Event, error)
	Put(ctx context.Context, e event.Event) (event.Event, error)
	Delete(ctx context.Context, id string) (event.Event, error)
	Conflicts(ctx context.Context, id string, buffer time.Duration) ([]event.Event, error)
}

var _ Backend = (*store.Store)(nil)

type Server struct {
	store Backend
	token string
	log   *slog.Logger
}

func New(s Backend, token string, log *slog.Logger) *Server {
	return &Server{store: s, token: token, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /events", s.auth(s.list))
	mux.HandleFunc("GET /events/{id}", s.auth(s.get))
	// id 없이 PUT 하면 추가, 있으면 수정. 파일 저장소의 Put 과 같은 계약이다.
	mux.HandleFunc("PUT /events", s.auth(s.put))
	mux.HandleFunc("DELETE /events/{id}", s.auth(s.remove))
	// 겹치는 일정. 막지 않고 알려주는 것이 이 경로의 존재 이유다.
	mux.HandleFunc("GET /events/{id}/conflicts", s.auth(s.conflicts))
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

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	events, err := s.store.Range(r.Context(), q.Get("from"), q.Get("to"))
	if err != nil {
		s.log.Error("기간 조회에 실패했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "일정을 불러오지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	e, err := s.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, event.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.log.Error("일정 조회에 실패했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "일정을 불러오지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	var in event.Event
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}

	saved, err := s.store.Put(r.Context(), in)
	if err != nil {
		// 검증 실패는 사람이 읽을 메시지 그대로 400 으로 돌려준다. 모델이
		// 상대 날짜를 보냈을 때 왜 거부됐는지 알아야 고칠 수 있다.
		var invalid event.ValidationError
		if errors.As(err, &invalid) {
			writeErr(w, http.StatusBadRequest, invalid.Error())
			return
		}
		s.log.Error("일정 저장에 실패했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "일정을 저장하지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	gone, err := s.store.Delete(r.Context(), r.PathValue("id"))
	if errors.Is(err, event.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.log.Error("일정 삭제에 실패했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "일정을 삭제하지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, gone)
}

func (s *Server) conflicts(w http.ResponseWriter, r *http.Request) {
	buffer := time.Duration(0)
	if v := r.URL.Query().Get("buffer"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "buffer 는 30m 같은 기간이어야 합니다.")
			return
		}
		buffer = d
	}

	found, err := s.store.Conflicts(r.Context(), r.PathValue("id"), buffer)
	if err != nil {
		s.log.Error("충돌 조회에 실패했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "충돌을 확인하지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": found})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
