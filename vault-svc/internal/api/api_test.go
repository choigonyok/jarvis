package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/vault-svc/internal/seal"
	"github.com/choigonyok/jarvis/vault-svc/internal/store"
)

type fake struct {
	logins   map[string]store.Login
	pins     map[string]store.Pin
	profiles map[string]store.Profile
	uses     []store.Use
}

func (f *fake) Ping(context.Context) error { return nil }
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
// one digit short (2026-10-30).
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
