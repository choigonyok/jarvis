package mind

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParsePoint(t *testing.T) {
	p, err := ParsePoint("37.5665, 126.9780, 서울")
	if err != nil || p.Name != "서울" || p.Lat != 37.5665 || p.Lon != 126.978 {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := ParsePoint("37.5,127"); err != nil || p.Name != "집" {
		t.Fatalf("no name is home: %+v %v", p, err)
	}
	for _, bad := range []string{"", "서울", "91,127", "37.5,abc"} {
		if _, err := ParsePoint(bad); err == nil {
			t.Errorf("%q should not read", bad)
		}
	}
}

// Home is the 집 region made on the phone; without one - or without
// context-svc - the fixed point, and a context-svc that did not answer is
// not asked again at once.
func TestHome(t *testing.T) {
	var calls atomic.Int32
	answer := `{"regions":[{"name":"회사","kind":"work","lat":37.49,"lon":127.02},{"name":"집","kind":"home","lat":37.556,"lon":126.901}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/regions" || r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("%s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if answer == "" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	seoul, _ := ParsePoint("37.5665,126.9780,서울")
	ctx := context.Background()

	h := &Home{URL: srv.URL, Token: "t", Fixed: seoul}
	p, err := h.Point(ctx)
	if err != nil || p.Name != "집" || p.Lat != 37.556 {
		t.Fatalf("the 집 region: %+v %v", p, err)
	}
	_, _ = h.Point(ctx)
	if calls.Load() != 1 {
		t.Fatalf("home is kept a while: %d calls", calls.Load())
	}

	answer = ""
	h = &Home{URL: srv.URL, Token: "t", Fixed: seoul}
	for range 3 {
		if p, err := h.Point(ctx); err != nil || p.Name != "서울" {
			t.Fatalf("context-svc down: %+v %v", p, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("a context-svc that did not answer is asked once a while: %d calls", calls.Load())
	}
}
