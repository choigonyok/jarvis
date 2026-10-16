// Package api serves the training log over HTTP. Same bearer scheme as the
// rest of the stack: everything but /health needs the token.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/choigonyok/jarvis/workout-svc/internal/book"
	"github.com/choigonyok/jarvis/workout-svc/internal/store"
)

type Server struct {
	store *store.Store
	token string
	log   *slog.Logger
}

func New(s *store.Store, token string, log *slog.Logger) *Server {
	return &Server{store: s, token: token, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	// 화면이 쓰는 경로. 계약은 분리 전과 같다 - 책 전체를 주고받는다.
	mux.HandleFunc("GET /workout", s.auth(s.get))
	mux.HandleFunc("PUT /workout", s.auth(s.put))
	// 바뀐 세션만 쓰는 경로. 손님과 운영자가 같은 책을 써도 서로 지우지 않는다.
	mux.HandleFunc("POST /workout/changes", s.auth(s.changes))
	mux.HandleFunc("PUT /workout/sessions/{id}", s.auth(s.putSession))
	mux.HandleFunc("DELETE /workout/sessions/{id}", s.auth(s.deleteSession))
	// 룰 게이트가 쓰는 경로. 제안을 열지 말지 판단하는 데 필요한 것만.
	mux.HandleFunc("GET /stats", s.auth(s.stats))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		// 데이터베이스가 없으면 이 서비스는 아무것도 할 수 없다. healthy 라고
		// 답하면 compose 가 의존하는 서비스를 그 위에 띄운다.
		writeErr(w, http.StatusServiceUnavailable, "데이터베이스에 연결할 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.Load(r.Context())
	if err != nil {
		s.log.Error("기록을 불러오지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "기록을 불러오지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(b.Revision, 10)))
	writeJSON(w, http.StatusOK, b)
}

// expected reads the revision a full replace was built on: If-Match, or
// ?rev= for sendBeacon, which cannot set headers. -1 = 확인하지 않음.
func expected(r *http.Request) (int64, bool) {
	v := strings.Trim(strings.TrimPrefix(r.Header.Get("If-Match"), "W/"), `"`)
	if v == "" {
		v = r.URL.Query().Get("rev")
	}
	if v == "" || v == "*" {
		return -1, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

func (s *Server) written(w http.ResponseWriter, rev int64, err error) {
	if errors.Is(err, store.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "다른 곳에서 기록이 바뀌었습니다. 새로 불러와 주세요.", "revision": rev})
		return
	}
	if err != nil {
		s.log.Error("기록을 저장하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "기록을 저장하지 못했습니다.")
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(rev, 10)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": rev})
}

func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	var c book.Changes
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	rev, err := s.store.Apply(r.Context(), c)
	s.written(w, rev, err)
}

func (s *Server) putSession(w http.ResponseWriter, r *http.Request) {
	var one book.Session
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&one); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	one.ID = r.PathValue("id")
	rev, err := s.store.Apply(r.Context(), book.Changes{Sessions: []book.Session{one}})
	s.written(w, rev, err)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	rev, err := s.store.Apply(r.Context(), book.Changes{Deleted: []string{r.PathValue("id")}})
	s.written(w, rev, err)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	// 헬스장에서 온 sendBeacon 요청도 여기로 들어온다. 1MB 면 몇 년치 기록보다
	// 크고, 그보다 큰 본문은 실수이거나 공격이다.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "본문이 너무 큽니다.")
		return
	}

	var b book.Book
	if err := json.Unmarshal(body, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	// sessions 가 없는 본문은 빈 기록이 아니라 잘못된 요청이다. 빈 배열과
	// 빠진 필드를 구분하지 않으면 파싱 실패 한 번이 기록 전체를 지운다.
	if b.Sessions == nil {
		writeErr(w, http.StatusBadRequest, "sessions 가 없습니다.")
		return
	}

	expect, ok := expected(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "If-Match 를 읽을 수 없습니다.")
		return
	}
	rev, err := s.store.Save(r.Context(), b, expect)
	s.written(w, rev, err)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats(r.Context())
	if err != nil {
		s.log.Error("통계를 계산하지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "통계를 계산하지 못했습니다.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, st)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
