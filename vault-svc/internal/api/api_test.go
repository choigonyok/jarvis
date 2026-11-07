package api

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/vault-svc/internal/store"
)

type fake struct {
	logins   map[string]store.Login
	pins     map[string]store.Pin
	profiles map[string]store.Profile
	uses     []store.Use
	down     bool
}

func (f *fake) Ping(context.Context) error {
	if f.down {
		return errors.New("연결 거부")
	}
	return nil
}
func (f *fake) Put(_ context.Context, l store.Login) error {
	f.logins[l.Domain] = l
	return nil
}
func (f *fake) Get(_ context.Context, d string) (store.Login, error) {
	l, ok := f.logins[d]
	if !ok {
		return l, store.ErrNotFound
	}
	return l, nil
}
func (f *fake) List(context.Context) ([]store.Meta, error) {
	out := []store.Meta{}
	for _, l := range f.logins {
		out = append(out, store.Meta{Domain: l.Domain, Name: l.Name, Hint: l.Hint})
	}
	return out, nil
}
func (f *fake) Delete(_ context.Context, d string) (bool, error) {
	_, ok := f.logins[d]
	delete(f.logins, d)
	return ok, nil
}
func (f *fake) Used(context.Context, string) error { return nil }
func (f *fake) Record(_ context.Context, u store.Use) error {
	f.uses = append(f.uses, u)
	return nil
}
func (f *fake) Uses(context.Context, int) ([]store.Use, error) { return f.uses, nil }
func (f *fake) PutPin(_ context.Context, p store.Pin) error {
	f.pins[p.Site] = p
	return nil
}
func (f *fake) GetPin(_ context.Context, site string) (store.Pin, error) {
	p, ok := f.pins[site]
	if !ok {
		return p, store.ErrNotFound
	}
	return p, nil
}
func (f *fake) ListPins(context.Context) ([]store.PinMeta, error) {
	out := []store.PinMeta{}
	for _, p := range f.pins {
		out = append(out, store.PinMeta{Site: p.Site, Name: p.Name})
	}
	return out, nil
}
func (f *fake) DeletePin(_ context.Context, site string) (bool, error) {
	_, ok := f.pins[site]
	delete(f.pins, site)
	return ok, nil
}
func (f *fake) UsedPin(context.Context, string) error { return nil }
func (f *fake) PutProfile(_ context.Context, p store.Profile) error {
	f.profiles[p.Name] = p
	return nil
}
func (f *fake) Profiles(context.Context) ([]store.Profile, error) {
	out := []store.Profile{}
	for _, p := range f.profiles {
		out = append(out, p)
	}
	return out, nil
}
func (f *fake) DeleteProfile(_ context.Context, name string) (bool, error) {
	_, ok := f.profiles[name]
	delete(f.profiles, name)
	return ok, nil
}

func server(t *testing.T) (http.Handler, *fake) {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := seal.New(key)
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{logins: map[string]store.Login{}, pins: map[string]store.Pin{}, profiles: map[string]store.Profile{}}
	return (&Server{Store: f, Sealer: s, Token: "svc", ReleaseToken: "browser", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler(), f
}

func call(h http.Handler, method, path, token, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The agent saves and lists but never reads a password back; the browser
// reads one back for a page on that login's own domain, and does nothing else.
func TestTheTwoTokensOpenDifferentThings(t *testing.T) {
	h, f := server(t)
	if code, _ := call(h, http.MethodPut, "/logins/login.coupang.com", "svc", `{"name":"쿠팡","username":"abcdef@gmail.com","password":"s3cret!"}`); code != http.StatusOK {
		t.Fatalf("저장: %d", code)
	}
	l := f.logins["coupang.com"]
	if l.Domain != "coupang.com" || bytes.Contains(l.Sealed, []byte("s3cret")) || l.Hint != "ab***@g***" {
		t.Fatalf("도메인으로 묶고 봉인해서 저장해야 합니다: %+v", l)
	}
	code, body := call(h, http.MethodGet, "/logins", "svc", "")
	if code != http.StatusOK || strings.Contains(body, "s3cret") || strings.Contains(body, "abcdef") {
		t.Fatalf("목록에 비밀이 보이면 안 됩니다: %d %s", code, body)
	}

	// The services' token cannot read a login back.
	if code, _ := call(h, http.MethodPost, "/release", "svc", `{"host":"login.coupang.com"}`); code != http.StatusUnauthorized {
		t.Fatalf("서비스 토큰으로 꺼내기: %d", code)
	}
	// The browser's token reads it back for the page's own domain only.
	code, body = call(h, http.MethodPost, "/release", "browser", `{"host":"login.coupang.com","purpose":"chat"}`)
	var got map[string]string
	_ = json.Unmarshal([]byte(body), &got)
	if code != http.StatusOK || got["username"] != "abcdef@gmail.com" || got["password"] != "s3cret!" {
		t.Fatalf("브라우저 토큰으로 꺼내기: %d %s", code, body)
	}
	if code, _ := call(h, http.MethodPost, "/release", "browser", `{"host":"coupang.com.evil.com"}`); code != http.StatusNotFound {
		t.Fatalf("다른 도메인 페이지에는 내주면 안 됩니다: %d", code)
	}
	// The browser's token saves, lists and forgets nothing.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/logins", ""},
		{http.MethodPut, "/logins/evil.com", `{"username":"a","password":"b"}`},
		{http.MethodDelete, "/logins/coupang.com", ""},
		{http.MethodGet, "/uses", ""},
	} {
		if code, _ := call(h, c.method, c.path, "browser", c.body); code != http.StatusUnauthorized {
			t.Errorf("브라우저 토큰으로 %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _ := call(h, http.MethodPost, "/release", "", `{"host":"login.coupang.com"}`); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이 꺼내기: %d", code)
	}

	outcomes := []string{}
	for _, u := range f.uses {
		outcomes = append(outcomes, u.Outcome)
	}
	if strings.Join(outcomes, ",") != "released,none" {
		t.Fatalf("내준 것과 못 내준 것이 기록돼야 합니다: %v", outcomes)
	}
	if code, _ := call(h, http.MethodDelete, "/logins/www.coupang.com", "svc", ""); code != http.StatusNoContent {
		t.Fatalf("지우기: %d", code)
	}
	if code, _ := call(h, http.MethodPost, "/release", "browser", `{"host":"login.coupang.com"}`); code != http.StatusNotFound {
		t.Fatalf("지운 뒤 꺼내기: %d", code)
	}
}

func TestSaveNeedsBoth(t *testing.T) {
	h, _ := server(t)
	for _, body := range []string{`{"username":"a"}`, `{"password":"b"}`, `{"username":" ","password":"b"}`} {
		if code, _ := call(h, http.MethodPut, "/logins/coupang.com", "svc", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, code)
		}
	}
	if code, _ := call(h, http.MethodPut, "/logins/localhost", "svc", `{"username":"a","password":"b"}`); code != http.StatusBadRequest {
		t.Errorf("사이트 주소가 아닌 곳: %d", code)
	}
}

func TestHint(t *testing.T) {
	for in, want := range map[string]string{"abcdef": "ab***", "ab": "a***", "a": "*", "abc@naver.com": "ab***@n***", "01012345678": "01***"} {
		if got := Hint(in); got != want {
			t.Errorf("Hint(%q) = %q, want %q", in, got, want)
		}
	}
}

// A payment password is saved by the agent and read back by the browser only
// for a keypad on that payment's own host - pay.naver.com and under it, not
// the rest of naver.com - and it is kept apart from the naver.com login.
func TestAPaymentPasswordIsForItsKeypadOnly(t *testing.T) {
	h, f := server(t)
	if code, _ := call(h, http.MethodPut, "/logins/nid.naver.com", "svc", `{"name":"네이버","username":"abcdef","password":"login-pw"}`); code != http.StatusOK {
		t.Fatalf("네이버 로그인 저장: %d", code)
	}
	if code, body := call(h, http.MethodPut, "/pins/pay.naver.com", "svc", `{"pin":"135790"}`); code != http.StatusOK || !strings.Contains(body, "네이버페이") {
		t.Fatalf("결제 비밀번호 저장: %d %s", code, body)
	}
	if p := f.pins["pay.naver.com"]; bytes.Contains(p.Sealed, []byte("135790")) || p.Name != "네이버페이" {
		t.Fatalf("봉인해서 저장해야 합니다: %+v", p)
	}
	for _, c := range []struct{ path, body string }{
		{"/pins/naver.com", `{"pin":"135790"}`}, {"/pins/blog.naver.com", `{"pin":"135790"}`},
		{"/pins/pay.naver.com", `{"pin":"12345"}`}, {"/pins/pay.naver.com", `{"pin":"12a456"}`}, {"/pins/pay.naver.com", `{}`},
	} {
		if code, _ := call(h, http.MethodPut, c.path, "svc", c.body); code != http.StatusBadRequest {
			t.Errorf("저장하면 안 됩니다 %s %s: %d", c.path, c.body, code)
		}
	}
	if code, body := call(h, http.MethodGet, "/pins", "svc", ""); code != http.StatusOK || strings.Contains(body, "135790") || !strings.Contains(body, "pay.naver.com") {
		t.Fatalf("목록에 숫자가 보이면 안 됩니다: %d %s", code, body)
	}

	if code, _ := call(h, http.MethodPost, "/release-pin", "svc", `{"site":"pay.naver.com","host":"pay.naver.com"}`); code != http.StatusUnauthorized {
		t.Fatalf("서비스 토큰으로 꺼내기: %d", code)
	}
	for _, host := range []string{"pay.naver.com", "https://new-m.pay.naver.com/x", "orders.pay.naver.com"} {
		code, body := call(h, http.MethodPost, "/release-pin", "browser", `{"site":"pay.naver.com","host":"`+host+`","purpose":"결제 비밀번호"}`)
		var got map[string]string
		_ = json.Unmarshal([]byte(body), &got)
		if code != http.StatusOK || got["pin"] != "135790" {
			t.Fatalf("%s 의 키패드에는 내줍니다: %d %s", host, code, body)
		}
	}
	for _, c := range []string{
		`{"site":"pay.naver.com","host":"blog.naver.com"}`, `{"site":"pay.naver.com","host":"pay.naver.com.evil.com"}`,
		`{"site":"naver.com","host":"pay.naver.com"}`, `{"site":"pay.naver.com","host":"nid.naver.com"}`,
	} {
		if code, body := call(h, http.MethodPost, "/release-pin", "browser", c); code != http.StatusForbidden || strings.Contains(body, "135790") {
			t.Errorf("그 결제의 화면이 아니면 내주지 않습니다 %s: %d %s", c, code, body)
		}
	}
	// The login of naver.com is its own: saving the password did not touch it.
	code, body := call(h, http.MethodPost, "/release", "browser", `{"host":"nid.naver.com"}`)
	if code != http.StatusOK || !strings.Contains(body, "login-pw") || strings.Contains(body, "135790") {
		t.Fatalf("네이버 로그인은 그대로여야 합니다: %d %s", code, body)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/pins", ""}, {http.MethodPut, "/pins/pay.naver.com", `{"pin":"000000"}`}, {http.MethodDelete, "/pins/pay.naver.com", ""},
	} {
		if code, _ := call(h, c.method, c.path, "browser", c.body); code != http.StatusUnauthorized {
			t.Errorf("브라우저 토큰으로 %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _ := call(h, http.MethodDelete, "/pins/pay.naver.com", "svc", ""); code != http.StatusNoContent {
		t.Fatalf("지우기: %d", code)
	}
	if code, _ := call(h, http.MethodPost, "/release-pin", "browser", `{"site":"pay.naver.com","host":"pay.naver.com"}`); code != http.StatusNotFound {
		t.Fatalf("지운 뒤 꺼내기: %d", code)
	}
	outcomes := map[string]int{}
	for _, u := range f.uses {
		if strings.HasPrefix(u.Domain, "pin:") {
			outcomes[u.Outcome]++
		}
	}
	if outcomes["released"] != 3 || outcomes["refused"] != 4 || outcomes["none"] != 1 {
		t.Fatalf("결제 비밀번호를 내준 것·거절한 것·없던 것이 기록돼야 합니다: %v", outcomes)
	}
}

// A shipping profile is saved sealed and read back whole with the services'
// token - an order sheet is typed from it - and only with that token. An
// address without its building number is refused: the address search of an
// order sheet finds a road by it, and a run once picked a building number
// one digit short.
func TestAShippingProfileIsReadWholeAndNeedsItsBuildingNumber(t *testing.T) {
	h, f := server(t)
	good := `{"recipient":"홍길동","phone":"010-1234-5678","email":"a@example.com","zip":"04524","address":"서울 중구 세종대로 110","detail":"101동 1001호"}`
	for _, bad := range []struct{ body, want string }{
		{strings.Replace(good, "세종대로 110", "세종대로", 1), "건물번호"},
		{strings.Replace(good, "010-1234-5678", "02-123-4567", 1), "휴대폰"},
		{strings.Replace(good, `"04524"`, `"1641"`, 1), "우편번호"},
		{strings.Replace(good, "101동 1001호", "", 1), "상세주소"},
	} {
		if code, body := call(h, http.MethodPut, "/profiles/기본", "svc", bad.body); code != http.StatusBadRequest || !strings.Contains(body, bad.want) {
			t.Errorf("%s 이 틀린 배송지는 거절해야 합니다: %d %s", bad.want, code, body)
		}
	}
	if code, body := call(h, http.MethodPut, "/profiles/기본", "svc", good); code != http.StatusOK || !strings.Contains(body, `"phone":"01012345678"`) {
		t.Fatalf("저장: %d %s", code, body)
	}
	if p := f.profiles["기본"]; bytes.Contains(p.Sealed, []byte("세종대로")) || bytes.Contains(p.Sealed, []byte("01012345678")) {
		t.Fatalf("배송지는 봉인해서 저장해야 합니다")
	}
	code, body := call(h, http.MethodGet, "/profiles", "svc", "")
	var got struct {
		Profiles []Shipping `json:"profiles"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil || len(got.Profiles) != 1 {
		t.Fatalf("목록: %d %s", code, body)
	}
	if p := got.Profiles[0]; p.Name != "기본" || p.Address != "서울 중구 세종대로 110" || p.Detail != "101동 1001호" || p.Zip != "04524" || p.Recipient != "홍길동" {
		t.Fatalf("배송지를 그대로 돌려줘야 합니다: %+v", p)
	}
	// A parenthesized dong after the number is still a building number.
	if code, _ := call(h, http.MethodPut, "/profiles/회사", "svc", strings.Replace(good, "세종대로 110", "테헤란로 152 (역삼동)", 1)); code != http.StatusOK {
		t.Fatalf("괄호의 동 이름 앞에 건물번호가 있으면 받아야 합니다: %d", code)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/profiles", ""}, {http.MethodPut, "/profiles/기본", good}, {http.MethodDelete, "/profiles/기본", ""},
	} {
		if code, _ := call(h, c.method, c.path, "browser", c.body); code != http.StatusUnauthorized {
			t.Errorf("브라우저 토큰으로 %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _ := call(h, http.MethodDelete, "/profiles/기본", "svc", ""); code != http.StatusNoContent {
		t.Fatalf("지우기: %d", code)
	}
	if code, _ := call(h, http.MethodDelete, "/profiles/기본", "svc", ""); code != http.StatusNotFound {
		t.Fatalf("없는 배송지 지우기: %d", code)
	}
}

// legacyKey is the test key gocommon/seal's fixtures are sealed with (bytes
// 0..31); the bare rows below are two of those fixtures, as the vault keeps
// them.
func legacyKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gcm(t *testing.T, key []byte) cipher.AEAD {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return aead
}

// headedRow is a row in the headed form, made here by hand: "jsl2", the key
// id and the nonce in the nonce column, the sealed text in sealed, bound to
// those first eight bytes and the place.
func headedRow(t *testing.T, key []byte, keyID, place, plain string) (head, rest []byte) {
	t.Helper()
	id, err := hex.DecodeString(keyID)
	if err != nil {
		t.Fatal(err)
	}
	head = append(append([]byte("jsl2"), id...), make([]byte, 12)...)
	if _, err := rand.Read(head[8:]); err != nil {
		t.Fatal(err)
	}
	return head, gcm(t, key).Seal(nil, head[8:], []byte(plain), append(append([]byte{}, head[:8]...), place...))
}

// bareOpen opens a row as a vault that knows only the bare form does: the
// nonce from its own column, bound to "jarvis-vault/v1/" + the place.
func bareOpen(t *testing.T, key []byte, place string, nonce, sealed []byte) string {
	t.Helper()
	if len(nonce) != 12 {
		t.Fatalf("%s: nonce 열은 nonce 12바이트여야 합니다: %x", place, nonce)
	}
	plain, err := gcm(t, key).Open(nil, nonce, sealed, []byte("jarvis-vault/v1/"+place))
	if err != nil {
		t.Fatalf("%s: 머리 없는 형식만 아는 보관함이 열지 못합니다: %v", place, err)
	}
	return string(plain)
}

// The vault reads rows with either head - the bare form's 12-byte nonce and
// the headed form's 20 bytes - and writes the bare form, which a vault that
// knows only that form still opens.
func TestRowsOfBothHeadsOpenAndSavedRowsAreBare(t *testing.T) {
	key := legacyKey()
	s, err := seal.New(key)
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{logins: map[string]store.Login{}, pins: map[string]store.Pin{}, profiles: map[string]store.Profile{}}
	h := (&Server{Store: f, Sealer: s, Token: "svc", ReleaseToken: "browser", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler()
	f.logins["example.com"] = store.Login{Domain: "example.com", Name: "예시", Hint: "me***@e***", KeyID: "257335f4",
		Nonce: b64(t, "AwwalwJ61+a0j+l4"), Sealed: b64(t, "//CLReJoBi/V4HOGIklNUK9O4xeSYbZpKfiz3LNY2skHaFdmRFpIDwixgP8zPdvWI3rxCwweLdBq")}
	f.profiles["기본"] = store.Profile{Name: "기본", KeyID: "257335f4", Nonce: b64(t, "omcITuSdejeNDrjN"),
		Sealed: b64(t, "iwhHviKIIOpLTcqa4DE1kV8x9z0k/XNwW9UrHsGcSMvd4R6tgwaqJRkwQdxjAAdiJbtBSRYDQIFvWfBuDRVFQMaKXub9iy9hkcvk/zJtDnECpOgCLL0ehHjcs3BiNS4bMUkeuxVP+ie+wn8VqsdGYNoqnCypRSY+ArogHkPEmkbReAi/apJMaIa+rQXRhkMSUVdyHlQiQsqmg+KNPSjZ")}
	head, rest := headedRow(t, key, s.ID(), "example.org", `{"u":"you@example.org","p":"pw-test-5678"}`)
	f.logins["example.org"] = store.Login{Domain: "example.org", Name: "예시 2", Hint: "yo***@e***", KeyID: s.ID(), Nonce: head, Sealed: rest}
	head, rest = headedRow(t, key, s.ID(), "pin:pay.naver.com", `{"pin":"135790"}`)
	f.pins["pay.naver.com"] = store.Pin{Site: "pay.naver.com", Name: "네이버페이", KeyID: s.ID(), Nonce: head, Sealed: rest}
	head, rest = headedRow(t, key, s.ID(), "profile:회사",
		`{"name":"","recipient":"김시험","phone":"01000000001","zip":"00001","address":"테스트시 시험구 예시로 2","detail":"202동 202호"}`)
	f.profiles["회사"] = store.Profile{Name: "회사", KeyID: s.ID(), Nonce: head, Sealed: rest}

	release := func(host string) map[string]string {
		t.Helper()
		code, body := call(h, http.MethodPost, "/release", "browser", `{"host":"`+host+`","purpose":"chat"}`)
		var got map[string]string
		if code != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil {
			t.Fatalf("%s 꺼내기: %d %s", host, code, body)
		}
		return got
	}
	if got := release("www.example.com"); got["username"] != "me@example.com" || got["password"] != "pw-test-1234" {
		t.Fatalf("머리 없는 행: %v", got)
	}
	if got := release("www.example.org"); got["username"] != "you@example.org" || got["password"] != "pw-test-5678" {
		t.Fatalf("머리 있는 행: %v", got)
	}
	if code, body := call(h, http.MethodPost, "/release-pin", "browser", `{"site":"pay.naver.com","host":"pay.naver.com"}`); code != http.StatusOK || !strings.Contains(body, "135790") {
		t.Fatalf("머리 있는 결제 비밀번호: %d %s", code, body)
	}
	code, body := call(h, http.MethodGet, "/profiles", "svc", "")
	for _, want := range []string{"홍길동", "101동 101호", "김시험", "202동 202호"} {
		if code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("배송지에 %s 가 없습니다: %d %s", want, code, body)
		}
	}

	// Saved again, each row is bare, under the same key id.
	for _, c := range []struct{ path, body string }{
		{"/logins/example.org", `{"username":"you@example.org","password":"pw-new-5678"}`},
		{"/pins/pay.naver.com", `{"pin":"246802"}`},
		{"/profiles/회사", `{"recipient":"김시험","phone":"010-0000-0001","zip":"00001","address":"테스트시 시험구 예시로 2","detail":"203동 203호"}`},
	} {
		if code, body := call(h, http.MethodPut, c.path, "svc", c.body); code != http.StatusOK {
			t.Fatalf("%s 다시 저장: %d %s", c.path, code, body)
		}
	}
	l := f.logins["example.org"]
	if l.KeyID != s.ID() || !strings.Contains(bareOpen(t, key, "example.org", l.Nonce, l.Sealed), "pw-new-5678") {
		t.Fatalf("다시 쓴 로그인: %x %s", l.Nonce, l.KeyID)
	}
	if p := f.pins["pay.naver.com"]; p.KeyID != s.ID() || !strings.Contains(bareOpen(t, key, "pin:pay.naver.com", p.Nonce, p.Sealed), "246802") {
		t.Fatalf("다시 쓴 결제 비밀번호: %x %s", p.Nonce, p.KeyID)
	}
	if p := f.profiles["회사"]; p.KeyID != s.ID() || !strings.Contains(bareOpen(t, key, "profile:회사", p.Nonce, p.Sealed), "203동 203호") {
		t.Fatalf("다시 쓴 배송지: %x %s", p.Nonce, p.KeyID)
	}
	if got := release("www.example.org"); got["password"] != "pw-new-5678" {
		t.Fatalf("%v", got)
	}
}

// /health is the stack's contract, with the key the vault seals with.
func TestHealthSaysTheKey(t *testing.T) {
	h, f := server(t)
	code, body := call(h, http.MethodGet, "/health", "", "")
	var rep struct {
		Status string         `json:"status"`
		Error  string         `json:"error"`
		About  map[string]any `json:"about"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &rep) != nil || rep.Status != "ok" {
		t.Fatalf("%d %s", code, body)
	}
	if key, _ := rep.About["key"].(string); len(key) != 8 {
		t.Fatalf("키 id: %s", body)
	}
	f.down = true
	code, body = call(h, http.MethodGet, "/health", "", "")
	rep.Status, rep.Error = "", ""
	if code != http.StatusServiceUnavailable || json.Unmarshal([]byte(body), &rep) != nil || rep.Status != "down" || rep.Error == "" {
		t.Fatalf("%d %s", code, body)
	}
}
