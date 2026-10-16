package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The guard is told the job's sites, with the bridge's token and a time
// limit, and is told again when the run is over.
func TestBridgeGuardHoldsAndReleases(t *testing.T) {
	var got []string
	var sites []string
	var seconds int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job-sites" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		got = append(got, r.Method)
		if r.Method == http.MethodPost {
			var in struct {
				Sites   []string `json:"sites"`
				Seconds int      `json:"seconds"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			sites, seconds = in.Sites, in.Seconds
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	g, err := NewBridgeGuard(srv.URL+"/", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Restrict(context.Background(), []string{"web.joongna.com"}, 31*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := g.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != http.MethodPost || got[1] != http.MethodDelete {
		t.Fatalf("묶고 풀어야 합니다: %v", got)
	}
	if len(sites) != 1 || sites[0] != "web.joongna.com" || seconds != 31*60 {
		t.Fatalf("사이트와 시간: %v %d", sites, seconds)
	}

	// A job with no sites is held to none, not let loose.
	if err := g.Restrict(context.Background(), nil, time.Minute); err != nil || sites == nil || len(sites) != 0 {
		t.Fatalf("사이트 없는 작업은 빈 목록으로 묶어야 합니다: %v %v", sites, err)
	}

	bad, _ := NewBridgeGuard(srv.URL+"/", "wrong")
	if err := bad.Restrict(context.Background(), []string{"x"}, time.Minute); err == nil {
		t.Fatal("거절을 오류로 알려야 합니다(그러면 실행하지 않습니다)")
	}
}
