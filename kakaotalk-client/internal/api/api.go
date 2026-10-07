// Package api exposes the collected KakaoTalk data over HTTP for the web
// service. All routes are read-only. A bearer token (when configured) guards
// everything except /health.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/choigonyok/jarvis/kakaotalk-client/internal/poller"
	"github.com/choigonyok/jarvis/kakaotalk-client/internal/store"
)

type Server struct {
	store  *store.Store
	poller *poller.Poller
	token  string
}

func New(s *store.Store, p *poller.Poller, token string) *Server {
	return &Server{store: s, poller: p, token: token}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/status", s.auth(s.status))
	mux.HandleFunc("/chats", s.auth(s.chats))
	mux.HandleFunc("/messages", s.auth(s.messages))
	mux.HandleFunc("/messages/recent", s.auth(s.recent))
	mux.HandleFunc("/messages/after", s.auth(s.after))
	mux.HandleFunc("/search", s.auth(s.search))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			if r.Header.Get("Authorization") != "Bearer "+s.token {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.poller.Status())
}

func (s *Server) chats(w http.ResponseWriter, r *http.Request) {
	limit := intQuery(r, "limit", 200)
	chats, err := s.store.Chats(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	chatID, err := strconv.ParseInt(r.URL.Query().Get("chat"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "chat query param (chat_id) required")
		return
	}
	since := int64(intQuery(r, "since", 0))
	limit := intQuery(r, "limit", 100)
	msgs, err := s.store.ChatMessages(chatID, since, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *Server) recent(w http.ResponseWriter, r *http.Request) {
	limit := intQuery(r, "limit", 50)
	msgs, err := s.store.RecentMessages(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// after serves the incremental feed: ?rowid=<last seen>&limit=<n>, oldest first.
func (s *Server) after(w http.ResponseWriter, r *http.Request) {
	rowid, _ := strconv.ParseInt(r.URL.Query().Get("rowid"), 10, 64)
	limit := intQuery(r, "limit", 500)
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	msgs, err := s.store.MessagesAfter(rowid, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q query param required")
		return
	}
	limit := intQuery(r, "limit", 50)
	msgs, err := s.store.Search(q, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
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
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
