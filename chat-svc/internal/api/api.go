// Package api serves the transcript over HTTP.
//
// There is no SSE here. The live stream stays in the agent, together with the
// "is a run in flight" flag - that flag is deliberately not persisted, because a
// process that just started is not in the middle of a turn whatever it was doing
// when it stopped. Ephemeral run state belongs to the process that runs; the
// transcript belongs here.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/choigonyok/jarvis/chat-svc/internal/store"
	"github.com/choigonyok/jarvis/chat-svc/internal/turn"
)

type Server struct {
	store *store.Store
	token string
	// guestToken is the guest's agent's: it reads and writes the guest's
	// thread and nothing else.
	guestToken string
	tz         string
	log        *slog.Logger
}

func New(s *store.Store, token, guestToken, tz string, log *slog.Logger) *Server {
	return &Server{store: s, token: token, guestToken: guestToken, tz: tz, log: log}
}

// guestThread is the one thread the guest's token opens.
const guestThread = "guest"

type guestKey struct{}

// asGuest is whether the request came with the guest's token.
func asGuest(r *http.Request) bool {
	v, _ := r.Context().Value(guestKey{}).(bool)
	return v
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /turns", s.auth(s.list))
	mux.HandleFunc("POST /turns", s.auth(s.append))
	// A job's card given its newest word (see turn.Origin).
	mux.HandleFunc("PUT /turns/{id}", s.auth(s.update))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		switch {
		case s.token == "" || same(got, "Bearer "+s.token):
		case s.guestToken != "" && same(got, "Bearer "+s.guestToken):
			r = r.WithContext(context.WithValue(r.Context(), guestKey{}, true))
		default:
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

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

// turnID is the shape of a stored turn's id ("a-<epoch ms>-<seq>", "u-…").
var turnID = regexp.MustCompile(`^[a-z]+-[0-9]+-[0-9]+$`)

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	thread := r.URL.Query().Get("thread")
	if !threadName.MatchString(thread) {
		writeErr(w, http.StatusBadRequest, "thread 이름이 올바르지 않습니다.")
		return
	}
	if asGuest(r) && thread != guestThread {
		writeErr(w, http.StatusForbidden, "손님은 손님 대화만 읽을 수 있습니다.")
		return
	}
	// limit= (with before=) is the console's page: the newest turns, and
	// the ones above them as it scrolls up. Answered with hasMore.
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit <= 0 || limit > 500 {
			writeErr(w, http.StatusBadRequest, "limit 는 1~500 이어야 합니다.")
			return
		}
		// before= is a turn id (the oldest on the page below) or an RFC3339 time.
		var before time.Time
		var beforeID string
		if b := r.URL.Query().Get("before"); b != "" {
			if turnID.MatchString(b) {
				beforeID = b
			} else if t, err := time.Parse(time.RFC3339Nano, b); err == nil {
				before = t
			} else {
				writeErr(w, http.StatusBadRequest, "before 는 턴 id 나 RFC3339 시각이어야 합니다.")
				return
			}
		}
		turns, more, err := s.store.Window(r.Context(), s.tz, thread, before, beforeID, limit)
		if err != nil {
			s.log.Error("스레드를 불러오지 못했습니다", "err", err)
			writeErr(w, http.StatusInternalServerError, "스레드를 불러오지 못했습니다.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"turns": turns, "hasMore": more})
		return
	}
	// since= 는 mind 처럼 새 턴만 원하는 소비자용이다. 없으면 스레드 전체.
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "since 는 RFC3339 시각이어야 합니다.")
			return
		}
		since = t
	}
	turns, err := s.store.All(r.Context(), s.tz, thread, since)
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
	if asGuest(r) && in.Thread != guestThread {
		writeErr(w, http.StatusForbidden, "손님은 손님 대화에만 쓸 수 있습니다.")
		return
	}
	if empty(in) {
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

// empty is a turn with nothing to show: no words, no photos, no card. A
// checklist card is its list (kept by mind, named in the origin) - with or
// without a line above it. Refusing it as empty let it reach the screen
// unsaved and vanish on the next read (2026-10-21, "다른 탭 갔다 오면 사라져").
func empty(t turn.Turn) bool {
	if t.Origin != nil && t.Origin.Checklist != "" {
		return false
	}
	return t.Text == "" && len(t.Paragraphs) == 0 && t.ProposalID == "" && len(t.Images) == 0
}

// update rewrites what a turn with an origin says and moves it to now.
func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Thread     string   `json:"thread"`
		Paragraphs []string `json:"paragraphs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	if !threadName.MatchString(in.Thread) {
		writeErr(w, http.StatusBadRequest, "thread 이름이 올바르지 않습니다.")
		return
	}
	if asGuest(r) && in.Thread != guestThread {
		writeErr(w, http.StatusForbidden, "손님은 손님 대화에만 쓸 수 있습니다.")
		return
	}
	if len(in.Paragraphs) == 0 {
		writeErr(w, http.StatusBadRequest, "내용이 없는 턴입니다.")
		return
	}
	saved, err := s.store.Update(r.Context(), in.Thread, r.PathValue("id"), in.Paragraphs, s.tz)
	if errors.Is(err, store.ErrNoTurn) {
		writeErr(w, http.StatusNotFound, "그런 턴이 없습니다.")
		return
	}
	if err != nil {
		s.log.Error("턴을 갱신하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "턴을 갱신하지 못했습니다.")
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
