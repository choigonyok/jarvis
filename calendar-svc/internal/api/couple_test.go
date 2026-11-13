package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
	"github.com/choigonyok/jarvis/calendar-svc/internal/uniple"
)

// stubCouple answers what the tests ask; anything else would panic.
type stubCouple struct {
	Couple
	checked string
}

func (s *stubCouple) Space(context.Context) (uniple.Space, error) {
	return uniple.Space{Me: uniple.Person{ID: "u-me", Name: "하늘"}, Blocks: []uniple.Block{}}, nil
}

func (s *stubCouple) Check(_ context.Context, block, date, _ string) (uniple.Check, error) {
	if date == "" {
		return uniple.Check{}, event.ValidationError{Msg: "날짜는 YYYY-MM-DD 여야 합니다"}
	}
	if block == "nope" {
		return uniple.Check{}, uniple.ErrBlock
	}
	s.checked = block + " " + date
	return uniple.Check{ID: "c1", Date: date, Who: "me"}, nil
}

func call(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCoupleRoutes(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	stub := &stubCouple{}
	h := New(nil, "owner", "guest", quiet, nil).WithCouple(stub).Handler()

	if rec := call(h, "GET", "/couple", "owner", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "하늘") {
		t.Fatalf("공간 읽기: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "GET", "/couple", "guest", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("손님은 아직: %d", rec.Code)
	}
	if rec := call(h, "GET", "/couple", "nobody", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("토큰 없으면: %d", rec.Code)
	}
	if rec := call(h, "POST", "/couple/blocks/b-h/checks", "owner", `{"date":"2026-11-13"}`); rec.Code != 200 || stub.checked != "b-h 2026-11-13" {
		t.Fatalf("체크: %d %s %q", rec.Code, rec.Body, stub.checked)
	}
	if rec := call(h, "POST", "/couple/blocks/b-h/checks", "owner", `{}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "YYYY-MM-DD") {
		t.Fatalf("고칠 수 있는 입력은 400 과 이유: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "POST", "/couple/blocks/nope/checks", "owner", `{"date":"2026-11-13"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("없는 블록은 404: %d", rec.Code)
	}
	if rec := call(h, "POST", "/couple/blocks/b-h/checks", "owner", `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("깨진 본문: %d", rec.Code)
	}

	// Off uniple there is no space to open.
	plain := New(nil, "owner", "", quiet, nil).Handler()
	if rec := call(plain, "GET", "/couple", "owner", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("uniple 이 아니면 404: %d", rec.Code)
	}
}

// What went wrong in uniple reads as what to do about it, not one sentence for all.
func TestUnipleTroubleReadsAsWhatToDo(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		says   string
	}{
		{event.ValidationError{Msg: "버킷은 1자에서 100자까지예요"}, 400, "100자"},
		{uniple.ErrBlock, 404, "블록"},
		{fmt.Errorf("%w(400). 새 토큰", uniple.ErrSession), 503, "로그인"},
		{fmt.Errorf("%w: 호출: dial", uniple.ErrUnreachable), 503, "닿지 않아요"},
		{&uniple.Refused{Status: 521}, 503, "응답하지 않아요"},
		{&uniple.Refused{Status: 403, Code: "42501"}, 403, "상대가 만든"},
		{&uniple.Refused{Status: 400, Code: "42501"}, 403, "상대가 만든"},
		{&uniple.Refused{Status: 404}, 404, "새로고침"},
		{&uniple.Refused{Status: 409, Code: "23505"}, 409, "이미"},
		{&uniple.Refused{Status: 400, Code: "22P02"}, 400, "값을"},
		{errors.New("뭔가"), 502, "잠시 뒤"},
	} {
		status, says := trouble(c.err)
		if status != c.status || !strings.Contains(says, c.says) || !strings.HasSuffix(strings.TrimSuffix(says, "."), "요") {
			t.Errorf("%v: %d %q", c.err, status, says)
		}
	}
}
