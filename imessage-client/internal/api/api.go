// Package api serves the collected messages, read-only, in the same shape as
// the KakaoTalk collector. A bearer token guards everything but /health.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/imessage-client/internal/store"
)

// Status is how collection is going.
type Status struct {
	mu         sync.Mutex
	LastPollAt int64  `json:"last_poll_at,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	Cursor     int64  `json:"cursor"`
	Total      int64  `json:"total"`
}

func (st *Status) Set(cursor, total int64, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.LastPollAt = time.Now().Unix()
	st.Cursor, st.Total = cursor, total
	st.LastError = ""
	if err != nil {
		st.LastError = err.Error()
	}
}

type Server struct {
	store  *store.Store
	status *Status
	token  string
}

func New(s *store.Store, st *Status, token string) *Server {
	return &Server{store: s, status: st, token: token}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/status", s.auth(func(w http.ResponseWriter, r *http.Request) {
		s.status.mu.Lock()
		defer s.status.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"last_poll_at": s.status.LastPollAt, "last_error": s.status.LastError,
			"cursor": s.status.Cursor, "total": s.status.Total,
		})
	}))
	mux.HandleFunc("/chats", s.auth(func(w http.ResponseWriter, r *http.Request) {
		chats, err := s.store.Chats(intQuery(r, "limit", 200))
		reply(w, map[string]any{"chats": chats}, err)
	}))
	mux.HandleFunc("/messages", s.auth(func(w http.ResponseWriter, r *http.Request) {
		chatID, err := strconv.ParseInt(r.URL.Query().Get("chat"), 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat query param (chat_id) required"})
			return
		}
		msgs, err := s.store.ChatMessages(chatID, intQuery(r, "limit", 100))
		reply(w, map[string]any{"messages": msgs}, err)
	}))
	mux.HandleFunc("/messages/recent", s.auth(func(w http.ResponseWriter, r *http.Request) {
		msgs, err := s.store.Recent(intQuery(r, "limit", 50))
		reply(w, map[string]any{"messages": msgs}, err)
	}))
	mux.HandleFunc("/messages/after", s.auth(func(w http.ResponseWriter, r *http.Request) {
		rowid, _ := strconv.ParseInt(r.URL.Query().Get("rowid"), 10, 64)
		limit := intQuery(r, "limit", 500)
		if limit <= 0 || limit > 2000 {
			limit = 500
		}
		msgs, err := s.store.After(rowid, limit)
		reply(w, map[string]any{"messages": msgs}, err)
	}))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func intQuery(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
