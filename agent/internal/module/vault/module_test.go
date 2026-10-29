package vault

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
)

// fakeVault is vault-svc's payment password routes as the services' token
// sees them: it saves, lists and forgets, and never hands a digit back.
type fakeVault struct {
	mu   sync.Mutex
	pins map[string]string // site → pin, only to check what was sent
}

func (f *fakeVault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	site := strings.TrimPrefix(r.URL.Path, "/pins/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/pins":
		out := []Pin{}
		for s := range f.pins {
			out = append(out, Pin{Site: s, Name: s})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pins": out})
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/pins/"):
		var in struct{ Name, Pin string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.pins[site] = in.Pin
		_ = json.NewEncoder(w).Encode(map[string]string{"site": site, "name": in.Name})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/pins/"):
		delete(f.pins, site)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"없는 경로"}`)
	}
}

func module(t *testing.T) (*Module, *fakeVault) {
	t.Helper()
	f := &fakeVault{pins: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return New(NewClient(srv.URL, "svc"), slog.New(slog.NewTextHandler(io.Discard, nil))), f
}

func pinAction(kind, pay string) action.Action {
	raw, _ := json.Marshal(PinInput{Pay: pay})
	return action.Action{Kind: kind, Input: raw}
}

// 2026-10-30: the PIN tool stopped at 네이버페이's keypad with no password
// to type, and there was no card to give it one - only a shell on the Mac
// mini. Now the card is one secret field, and the digits go from the card to
// vault-svc and nowhere else.
func TestAPaymentPasswordIsACardWithOneSecretField(t *testing.T) {
	m, f := module(t)
	ctx := context.Background()
	card, err := m.Preview(ctx, pinAction(KindSavePin, "네이버페이"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card.Title, "네이버페이") || len(card.Fields) != 1 || !card.Fields[0].Private || card.Fields[0].Kind != "secret" {
		t.Fatalf("비밀 칸 하나짜리 카드여야 합니다: %+v", card)
	}
	if !strings.Contains(card.Body, "pay.naver.com") || !strings.Contains(card.Body, "보이지 않으며") {
		t.Fatalf("어디에 넣고 누가 못 보는지 말해야 합니다: %s", card.Body)
	}
	for _, bad := range []string{"", "12345", "1234567", "12a456", " 12 345"} {
		if err := m.AcceptSecrets(ctx, pinAction(KindSavePin, "네이버페이"), map[string]string{"pin": bad}); err == nil {
			t.Errorf("%q 는 받으면 안 됩니다", bad)
		}
	}
	if len(f.pins) != 0 {
		t.Fatalf("틀린 값은 보관함에 가면 안 됩니다: %v", f.pins)
	}
	if err := m.AcceptSecrets(ctx, pinAction(KindSavePin, "네이버페이"), map[string]string{"pin": "135790"}); err != nil {
		t.Fatal(err)
	}
	if f.pins["pay.naver.com"] != "135790" || len(f.pins) != 1 {
		t.Fatalf("네이버페이 결제 호스트로 저장해야 합니다: %v", f.pins)
	}
	res, err := m.Execute(ctx, pinAction(KindSavePin, "네이버페이"))
	if err != nil || strings.Contains(res.Note, "135790") || !strings.Contains(res.Note, "저장했습니다") {
		t.Fatalf("모델에게는 저장했다는 말만: %q %v", res.Note, err)
	}
	again, _ := m.Preview(ctx, pinAction(KindSavePin, "네이버페이"))
	if !strings.Contains(again.Body, "새로 바꿉니다") {
		t.Fatalf("이미 있으면 바꾼다고 말해야 합니다: %s", again.Body)
	}
	if _, err := m.Execute(ctx, pinAction(KindForgetPin, "네이버페이")); err != nil || len(f.pins) != 0 {
		t.Fatalf("지우기: %v %v", f.pins, err)
	}
	// A login card's secrets are not a payment password's.
	if err := m.AcceptSecrets(ctx, pinAction(KindForgetPin, "네이버페이"), map[string]string{"pin": "135790"}); err == nil {
		t.Fatal("지우는 카드는 비밀 값을 받지 않습니다")
	}
}

func TestWhichPaymentAPasswordIsFor(t *testing.T) {
	for pay, want := range map[string]string{
		"네이버페이": "pay.naver.com", "네이버 페이": "pay.naver.com", "NaverPay": "pay.naver.com", "pay.naver.com": "pay.naver.com",
		"쿠페이": "coupang.com", "쿠팡": "coupang.com", "coupang.com": "coupang.com",
	} {
		raw, _ := json.Marshal(PinInput{Pay: pay})
		if site, _, err := payOf(raw); err != nil || site != want {
			t.Errorf("%q → %q %v, want %q", pay, site, err, want)
		}
	}
	for _, pay := range []string{"", "카카오페이", "토스", "naver.com.evil"} {
		raw, _ := json.Marshal(PinInput{Pay: pay})
		if _, _, err := payOf(raw); err == nil {
			t.Errorf("%q 는 결제 비밀번호를 둘 곳이 아닙니다", pay)
		}
	}
}
