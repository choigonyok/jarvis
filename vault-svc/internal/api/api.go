// Package api serves the vault.
//
// Two callers, two tokens, and nothing in common between what they open:
//
//	GET    /logins            the services' token: what is saved (no secret)
//	PUT    /logins/{domain}   the services' token: save a login {name, username, password}
//	DELETE /logins/{domain}   the services' token: forget one
//	GET    /uses              the services' token: every release, newest first
//	POST   /release           the browser's token only: {host, purpose} → the login for that host's domain
//	GET    /pins              the services' token: which payment passwords are saved (no digit)
//	PUT    /pins/{site}       the services' token: save one {name, pin} - site is a payment host (PinSites)
//	DELETE /pins/{site}       the services' token: forget one
//	POST   /release-pin       the browser's token only: {site, host, purpose} → the pin, for a keypad on that site
//	GET    /profiles          the services' token: the saved orderer/shipping profiles, read in full
//	PUT    /profiles/{name}   the services' token: save one {recipient, phone, email, zip, address, detail}
//	DELETE /profiles/{name}   the services' token: forget one
//
// The agent saves and lists but can never read a secret back; the browser
// reads one only for the page it is on, and only the login of that page's
// own domain - or the payment password of the payment host its keypad is on.
// A shipping profile is not a secret but someone's address: the agent reads
// it whole (an order sheet is typed from it), and it is kept sealed all the
// same. Nothing here logs a body.
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
	PutPin(ctx context.Context, p store.Pin) error
	GetPin(ctx context.Context, site string) (store.Pin, error)
	ListPins(ctx context.Context) ([]store.PinMeta, error)
	DeletePin(ctx context.Context, site string) (bool, error)
	UsedPin(ctx context.Context, site string) error
	PutProfile(ctx context.Context, p store.Profile) error
	Profiles(ctx context.Context) ([]store.Profile, error)
	DeleteProfile(ctx context.Context, name string) (bool, error)
}

// PinSites are the payment hosts a payment password may be saved for, each
// the host its keypad is on (and the hosts under it). Not a registrable
// domain like a login's: naver.com is the login's, pay.naver.com the
// password's - a page elsewhere on naver.com (a blog, a cafe, anyone's) never
// gets it.
var PinSites = map[string]string{
	"pay.naver.com": "네이버페이",
	"coupang.com":   "쿠팡(쿠페이)",
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
	mux.HandleFunc("GET /pins", s.with(s.Token, s.listPins))
	mux.HandleFunc("PUT /pins/{site}", s.with(s.Token, s.putPin))
	mux.HandleFunc("DELETE /pins/{site}", s.with(s.Token, s.forgetPin))
	mux.HandleFunc("POST /release-pin", s.with(s.ReleaseToken, s.releasePin))
	mux.HandleFunc("GET /profiles", s.with(s.Token, s.listProfiles))
	mux.HandleFunc("PUT /profiles/{name}", s.with(s.Token, s.putProfile))
	mux.HandleFunc("DELETE /profiles/{name}", s.with(s.Token, s.forgetProfile))
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

// pinSite is a path's payment host, if a payment password may be kept for it.
func pinSite(raw string) (string, bool) {
	h := site.Host(raw)
	_, ok := PinSites[h]
	return h, ok
}

// pinSeal is what a payment password is sealed under: never a login's domain.
func pinSeal(site string) string { return "pin:" + site }

func (s *Server) listPins(w http.ResponseWriter, r *http.Request) {
	pins, err := s.Store.ListPins(r.Context())
	if err != nil {
		s.Log.Error("결제 비밀번호 목록을 읽지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "목록을 읽지 못했습니다.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pins": pins})
}

func (s *Server) putPin(w http.ResponseWriter, r *http.Request) {
	where, ok := pinSite(r.PathValue("site"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "결제 비밀번호를 둘 수 있는 곳이 아닙니다(네이버페이 pay.naver.com, 쿠팡 coupang.com).")
		return
	}
	var in struct {
		Name string `json:"name"`
		Pin  string `json:"pin"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	if !sixDigits(in.Pin) {
		writeErr(w, http.StatusBadRequest, "결제 비밀번호는 숫자 6자리입니다.")
		return
	}
	name := clamp(strings.TrimSpace(in.Name), 60)
	if name == "" {
		name = PinSites[where]
	}
	plain, _ := json.Marshal(map[string]string{"pin": in.Pin})
	nonce, sealed, err := s.Sealer.Seal(pinSeal(where), plain)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "봉인하지 못했습니다.")
		return
	}
	if err := s.Store.PutPin(r.Context(), store.Pin{Site: where, Name: name, Nonce: nonce, Sealed: sealed, KeyID: s.Sealer.ID()}); err != nil {
		s.Log.Error("결제 비밀번호를 저장하지 못했습니다", "site", where, "err", err)
		writeErr(w, http.StatusInternalServerError, "저장하지 못했습니다.")
		return
	}
	s.Log.Info("결제 비밀번호를 저장했습니다", "site", where)
	writeJSON(w, http.StatusOK, map[string]string{"site": where, "name": name})
}

func (s *Server) forgetPin(w http.ResponseWriter, r *http.Request) {
	where, ok := pinSite(r.PathValue("site"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "결제 비밀번호를 둘 수 있는 곳이 아닙니다.")
		return
	}
	gone, err := s.Store.DeletePin(r.Context(), where)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "지우지 못했습니다.")
		return
	}
	if !gone {
		writeErr(w, http.StatusNotFound, "저장된 결제 비밀번호가 없습니다.")
		return
	}
	s.Log.Info("결제 비밀번호를 지웠습니다", "site", where)
	w.WriteHeader(http.StatusNoContent)
}

// releasePin hands a payment password to the browser for a keypad on that
// payment's own host. host is the frame the keypad is in - the browser reads
// it from the page, not from anything the model said - and it must be the
// site or under it.
func (s *Server) releasePin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Site    string `json:"site"`
		Host    string `json:"host"`
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	host := site.Host(in.Host)
	purpose := clamp(strings.TrimSpace(in.Purpose), 80)
	where, ok := pinSite(in.Site)
	record := func(outcome, note string) {
		if err := s.Store.Record(r.Context(), store.Use{Domain: pinSeal(where), Host: host, Purpose: purpose, Outcome: outcome, Note: note}); err != nil {
			s.Log.Warn("사용 기록을 남기지 못했습니다", "err", err)
		}
	}
	if !ok || (host != where && !strings.HasSuffix(host, "."+where)) {
		record("refused", "그 결제의 화면이 아님")
		writeErr(w, http.StatusForbidden, "그 결제의 화면이 아니라서 결제 비밀번호를 내주지 않습니다.")
		return
	}
	p, err := s.Store.GetPin(r.Context(), where)
	if errors.Is(err, store.ErrNotFound) {
		record("none", "")
		writeErr(w, http.StatusNotFound, PinSites[where]+" 결제 비밀번호가 보관함에 없습니다.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "읽지 못했습니다.")
		return
	}
	if p.KeyID != s.Sealer.ID() {
		record("failed", "다른 키로 봉인됨")
		writeErr(w, http.StatusConflict, "다른 키로 저장된 결제 비밀번호라 열 수 없습니다. 다시 저장해 주세요.")
		return
	}
	plain, err := s.Sealer.Open(pinSeal(where), p.Nonce, p.Sealed)
	var got struct {
		Pin string `json:"pin"`
	}
	if err == nil {
		err = json.Unmarshal(plain, &got)
	}
	if err != nil || !sixDigits(got.Pin) {
		record("failed", "봉인을 열지 못함")
		writeErr(w, http.StatusInternalServerError, "저장된 결제 비밀번호를 열지 못했습니다.")
		return
	}
	_ = s.Store.UsedPin(r.Context(), where)
	record("released", "")
	s.Log.Info("결제 비밀번호를 내주었습니다", "site", where, "host", host, "purpose", purpose)
	writeJSON(w, http.StatusOK, map[string]string{"site": where, "pin": got.Pin})
}

// Shipping is an orderer and where an order goes, as an order sheet asks
// for it. Address is the road address up to the building number - the
// address search of an order sheet (카카오 우편번호) finds it by that, and
// without the number a run once picked a building number one digit short.
type Shipping struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
	Phone     string `json:"phone"`
	Email     string `json:"email,omitempty"`
	Zip       string `json:"zip"`
	Address   string `json:"address"`
	Detail    string `json:"detail"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// buildingNumber is the end of a road address: the building's number (19,
// 19-3), with what a person may add after it in parentheses (태평로1가).
var buildingNumber = regexp.MustCompile(`\d+(-\d+)?(\s*\([^()]*\))?$`)

// cleanShipping checks a profile as a person typed it, and puts the phone
// in one form (digits).
func cleanShipping(in Shipping) (Shipping, string) {
	out := Shipping{
		Recipient: strings.Join(strings.Fields(in.Recipient), " "),
		Email:     strings.TrimSpace(in.Email),
		Zip:       strings.TrimSpace(in.Zip),
		Address:   strings.Join(strings.Fields(in.Address), " "),
		Detail:    strings.Join(strings.Fields(in.Detail), " "),
	}
	for _, c := range in.Phone {
		if c >= '0' && c <= '9' {
			out.Phone += string(c)
		}
	}
	switch {
	case out.Recipient == "" || utf8.RuneCountInString(out.Recipient) > 40:
		return out, "받는 분 이름을 넣어 주세요(40자까지)."
	case len(out.Phone) < 10 || len(out.Phone) > 11 || !strings.HasPrefix(out.Phone, "01"):
		return out, "연락처는 휴대폰 번호(010-0000-0000)로 넣어 주세요."
	case out.Email != "" && (len(out.Email) > 100 || !strings.Contains(out.Email, "@")):
		return out, "이메일 주소를 확인해 주세요."
	case len(out.Zip) != 5 || strings.Trim(out.Zip, "0123456789") != "":
		return out, "우편번호는 숫자 5자리입니다."
	case out.Address == "" || utf8.RuneCountInString(out.Address) > 120:
		return out, "주소를 넣어 주세요(120자까지)."
	case !buildingNumber.MatchString(out.Address):
		return out, "주소는 도로명과 건물번호까지 넣어 주세요(예: 서울 중구 세종대로 110)."
	case out.Detail == "" || utf8.RuneCountInString(out.Detail) > 60:
		return out, "상세주소(동·호수)를 넣어 주세요(60자까지)."
	}
	return out, ""
}

// profileName is a path's profile name: '기본' and the like, short.
func profileName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	return name, name != "" && utf8.RuneCountInString(name) <= 20 && !strings.ContainsAny(name, "/\\")
}

// profileSeal is what a profile is sealed under: never a login's or a pin's.
func profileSeal(name string) string { return "profile:" + name }

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Profiles(r.Context())
	if err != nil {
		s.Log.Error("배송지를 읽지 못했습니다", "err", err)
		writeErr(w, http.StatusInternalServerError, "배송지를 읽지 못했습니다.")
		return
	}
	out := []Shipping{}
	for _, p := range rows {
		if p.KeyID != s.Sealer.ID() {
			s.Log.Warn("다른 키로 봉인된 배송지라 건너뜁니다", "name", p.Name)
			continue
		}
		plain, err := s.Sealer.Open(profileSeal(p.Name), p.Nonce, p.Sealed)
		var sh Shipping
		if err == nil {
			err = json.Unmarshal(plain, &sh)
		}
		if err != nil {
			s.Log.Warn("배송지 봉인을 열지 못했습니다", "name", p.Name)
			continue
		}
		sh.Name = p.Name
		sh.UpdatedAt = p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
		out = append(out, sh)
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	name, ok := profileName(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "배송지 이름은 20자까지입니다(예: 기본).")
		return
	}
	var in Shipping
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "본문을 읽을 수 없습니다.")
		return
	}
	sh, bad := cleanShipping(in)
	if bad != "" {
		writeErr(w, http.StatusBadRequest, bad)
		return
	}
	plain, _ := json.Marshal(sh)
	nonce, sealed, err := s.Sealer.Seal(profileSeal(name), plain)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "봉인하지 못했습니다.")
		return
	}
	if err := s.Store.PutProfile(r.Context(), store.Profile{Name: name, Nonce: nonce, Sealed: sealed, KeyID: s.Sealer.ID()}); err != nil {
		s.Log.Error("배송지를 저장하지 못했습니다", "name", name, "err", err)
		writeErr(w, http.StatusInternalServerError, "저장하지 못했습니다.")
		return
	}
	s.Log.Info("배송지를 저장했습니다", "name", name)
	sh.Name = name
	writeJSON(w, http.StatusOK, sh)
}

func (s *Server) forgetProfile(w http.ResponseWriter, r *http.Request) {
	name, ok := profileName(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "배송지 이름은 20자까지입니다.")
		return
	}
	gone, err := s.Store.DeleteProfile(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "지우지 못했습니다.")
		return
	}
	if !gone {
		writeErr(w, http.StatusNotFound, "저장된 배송지가 없습니다.")
		return
	}
	s.Log.Info("배송지를 지웠습니다", "name", name)
	w.WriteHeader(http.StatusNoContent)
}

func sixDigits(v string) bool {
	if len(v) != 6 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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
