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
	logins map[string]store.Login
	uses   []store.Use
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

func server(t *testing.T) (http.Handler, *fake) {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := seal.New(key)
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{logins: map[string]store.Login{}}
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
