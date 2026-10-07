// Package api serves the transcript over HTTP.
//
// There is no SSE here. The live stream stays in the agent, together with the
// "is a run in flight" flag - that flag is deliberately not persisted, because a
// process that just started is not in the middle of a turn whatever it was doing
// when it stopped. Ephemeral run state belongs to the process that runs; the
// transcript belongs here.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/choigonyok/jarvis/chat-svc/internal/store"
	"github.com/choigonyok/jarvis/chat-svc/internal/turn"
)

type Server struct {
	store *store.Store
	token string
	tz    string
	log   *slog.Logger
}

func New(s *store.Store, token, tz string, log *slog.Logger) *Server {
	return &Server{store: s, token: token, tz: tz, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /turns", s.auth(s.list))
	mux.HandleFunc("POST /turns", s.auth(s.append))
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

// threadName is "" (the operator) or a short lowercase name. Anything else is
// refused rather than stored: a typo would quietly start a second, empty
// conversation.
var threadName = regexp.MustCompile(`^[a-z0-9-]{0,32}$`)

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	thread := r.URL.Query().Get("thread")
	if !threadName.MatchString(thread) {
		writeErr(w, http.StatusBadRequest, "thread 이름이 올바르지 않습니다.")
		return
	}
	turns, err := s.store.All(r.Context(), s.tz, thread)
	if err != nil {
		s.log.Error("스레드를 불러오지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "스레드를 불러오지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
}

func (s *Server) append(w http.ResponseWriter, r *http.Request) {
	var in turn.Turn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	// 빈 턴은 화면에 빈 말풍선으로 나타난다. 에이전트도 걸러내지만, 여기서
	// 막아야 다른 호출자가 같은 실수를 반복하지 않는다.
	if !threadName.MatchString(in.Thread) {
		writeErr(w, http.StatusBadRequest, "thread 이름이 올바르지 않습니다.")
		return
	}
	if in.Text == "" && len(in.Paragraphs) == 0 && in.ProposalID == "" && len(in.Images) == 0 {
		writeErr(w, http.StatusBadRequest, "내용이 없는 턴입니다.")
		return
	}

	saved, err := s.store.Append(r.Context(), in, s.tz)
	if err != nil {
		s.log.Error("턴을 저장하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "턴을 저장하지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, saved)
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
