// Package api serves the vault.
//
// Two callers, two tokens, and nothing in common between what they open:
//
//	GET    /logins            the services' token: what is saved (no secret)
//	PUT    /logins/{domain}   the services' token: save a login {name, username, password}
//	DELETE /logins/{domain}   the services' token: forget one
//	GET    /uses              the services' token: every release, newest first
//	POST   /release           the browser's token only: {host, purpose} → the login for that host's domain
//
// The agent saves and lists but can never read a secret back; the browser
// reads one only for the page it is on, and only the login of that page's
// own domain. Nothing here logs a body.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/gocommon/site"
	"github.com/choigonyok/jarvis/vault-svc/internal/seal"
	"github.com/choigonyok/jarvis/vault-svc/internal/store"
)

type Store interface {
	Ping(ctx context.Context) error
	Put(ctx context.Context, l store.Login) error
	Get(ctx context.Context, domain string) (store.Login, error)
	List(ctx context.Context) ([]store.Meta, error)
	Delete(ctx context.Context, domain string) (bool, error)
	Used(ctx context.Context, domain string) error
	Record(ctx context.Context, u store.Use) error
	Uses(ctx context.Context, limit int) ([]store.Use, error)
}

type Server struct {
	Store  Store
	Sealer *seal.Sealer
	// Token is the services' (the agent saves, lists and forgets with it).
	Token string
	// ReleaseToken is the browser container's: it reads a login back, and
	// that is all it can do.
	ReleaseToken string
	Log          *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.Ping(r.Context()); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "데이터베이스에 연결할 수 없습니다.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "key": s.Sealer.ID()})
	})
	mux.HandleFunc("GET /logins", s.with(s.Token, s.list))
	mux.HandleFunc("PUT /logins/{domain}", s.with(s.Token, s.put))
	mux.HandleFunc("DELETE /logins/{domain}", s.with(s.Token, s.forget))
	mux.HandleFunc("GET /uses", s.with(s.Token, s.uses))
	mux.HandleFunc("POST /release", s.with(s.ReleaseToken, s.release))
	return mux
}

// with lets a request through only with this route's own token. An empty
// token opens nothing - not even on a laptop: a vault that runs open is not
// worth having.
func (s *Server) with(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	logins, err := s.Store.List(r.Context())
	if err != nil {
		s.Log.Error("목록을 읽지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "목록을 읽지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logins": logins})
}

type secret struct {
	U string `json:"u"`
	P string `json:"p"`
}

func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	domain, err := site.Domain(r.PathValue("domain"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var in struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	in.Name, in.Username = strings.TrimSpace(in.Name), strings.TrimSpace(in.Username)
	switch {
	case in.Username == "" || in.Password == "":
		writeErr(w, http.StatusBadRequest, "아이디와 비밀번호가 모두 필요합니다.")
		return
	case utf8.RuneCountInString(in.Username) > 200 || utf8.RuneCountInString(in.Password) > 500 || utf8.RuneCountInString(in.Name) > 60:
		writeErr(w, http.StatusBadRequest, "너무 깁니다.")
		return
	}
	if in.Name == "" {
		in.Name = domain
	}
	plain, _ := json.Marshal(secret{U: in.Username, P: in.Password})
	nonce, sealed, err := s.Sealer.Seal(domain, plain)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "봉인하지 못했습니다.")
		return
	}
	if err := s.Store.Put(r.Context(), store.Login{Domain: domain, Name: in.Name, Hint: Hint(in.Username), Nonce: nonce, Sealed: sealed, KeyID: s.Sealer.ID()}); err != nil {
		s.Log.Error("저장하지 못했습니다", "domain", domain, "err", err)
		writeErr(w, http.StatusInternalServerError, "저장하지 못했습니다.")
		return
	}
	s.Log.Info("로그인 정보를 저장했습니다", "domain", domain)
	writeJSON(w, http.StatusOK, map[string]string{"domain": domain, "name": in.Name, "hint": Hint(in.Username)})
}

func (s *Server) forget(w http.ResponseWriter, r *http.Request) {
	domain, err := site.Domain(r.PathValue("domain"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	gone, err := s.Store.Delete(r.Context(), domain)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "지우지 못했습니다.")
		return
	}
	if !gone {
		writeErr(w, http.StatusNotFound, store.ErrNotFound.Error())
		return
	}
	s.Log.Info("로그인 정보를 지웠습니다", "domain", domain)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uses(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	uses, err := s.Store.Uses(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "기록을 읽지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uses": uses})
}

// release hands the login of the page's own domain to the browser, which
// types it there. host is the page the browser is on - it reads that from
// the tab, not from anything the model said.
func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host    string `json:"host"`
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	host := site.Host(in.Host)
	purpose := clamp(strings.TrimSpace(in.Purpose), 80)
	record := func(domain, outcome, note string) {
		if err := s.Store.Record(r.Context(), store.Use{Domain: domain, Host: host, Purpose: purpose, Outcome: outcome, Note: note}); err != nil {
			s.Log.Warn("사용 기록을 남기지 못했습니다", "err", err)
		}
	}
	domain, err := site.Domain(host)
	if err != nil {
		record("", "refused", "사이트 주소가 아님")
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	l, err := s.Store.Get(r.Context(), domain)
	if errors.Is(err, store.ErrNotFound) {
		record(domain, "none", "")
		writeErr(w, http.StatusNotFound, "이 사이트("+domain+")에 저장된 로그인 정보가 없습니다.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "읽지 못했습니다.")
		return
	}
	if l.KeyID != s.Sealer.ID() {
		record(domain, "failed", "다른 키로 봉인됨")
		writeErr(w, http.StatusConflict, "다른 키로 저장된 정보라 열 수 없습니다. 다시 저장해 주세요.")
		return
	}
	plain, err := s.Sealer.Open(domain, l.Nonce, l.Sealed)
	var sec secret
	if err == nil {
		err = json.Unmarshal(plain, &sec)
	}
	if err != nil {
		record(domain, "failed", "봉인을 열지 못함")
		writeErr(w, http.StatusInternalServerError, "저장된 정보를 열지 못했습니다.")
		return
	}
	_ = s.Store.Used(r.Context(), domain)
	record(domain, "released", "")
	s.Log.Info("로그인 정보를 내주었습니다", "domain", domain, "host", host, "purpose", purpose)
	writeJSON(w, http.StatusOK, map[string]string{"domain": domain, "name": l.Name, "username": sec.U, "password": sec.P, "hint": l.Hint})
}

// Hint shows enough of a username to tell two apart: ab***, ab***@g***.
func Hint(u string) string {
	local, at, found := strings.Cut(u, "@")
	head := func(s string, n int) string {
		r := []rune(s)
		if len(r) <= n {
			if len(r) <= 1 {
				return "*"
			}
			return string(r[:1]) + "***"
		}
		return string(r[:n]) + "***"
	}
	if found {
		return head(local, 2) + "@" + head(at, 1)
	}
	return head(local, 2)
}

func clamp(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
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
