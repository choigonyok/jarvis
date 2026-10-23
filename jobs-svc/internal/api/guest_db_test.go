package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/jobs-svc/internal/store"
)

// Against a throwaway Postgres, as svc_jobs (scripts/dbtest jobs-svc): the
// operator and the guest each see, change and run only their own jobs -
// another's is not found - a guest's job opens no website, and a job made
// wakes its own person's agent.
func TestEachSeesOnlyTheirOwnJobs(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest jobs-svc)")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	srv := New(st, "owner-token", "guest-token", time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var mu sync.Mutex
	var woke []string
	srv.SetWake(func(who string) {
		mu.Lock()
		defer mu.Unlock()
		woke = append(woke, who)
	})
	h := srv.Handler()
	do := func(token, method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	made := func(token, title string) int64 {
		code, body := do(token, "POST", "/jobs", `{"title":"`+title+`","goal":"매일 아침 요약"}`)
		var j store.Job
		if code != http.StatusCreated || json.Unmarshal([]byte(body), &j) != nil {
			t.Fatalf("%s 작업 만들기: %d %s", title, code, body)
		}
		return j.ID
	}

	if code, body := do("guest-token", "POST", "/jobs", `{"title":"손님 쇼핑","goal":"장보기","sites":["coupang.com"]}`); code != http.StatusBadRequest || !strings.Contains(body, "웹사이트") {
		t.Fatalf("손님 작업이 사이트를 열려 함: %d %s", code, body)
	}
	guest, owner := made("guest-token", "dbtest 손님 일"), made("owner-token", "dbtest 운영자 일")
	mu.Lock()
	if len(woke) < 2 || !strings.Contains(strings.Join(woke, ","), "guest") || !strings.Contains(strings.Join(woke, ","), "owner") {
		// the knocks run on their own goroutine; give them a moment
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
	}
	if got := strings.Join(woke, ","); !strings.Contains(got, "guest") || !strings.Contains(got, "owner") {
		t.Fatalf("깨운 에이전트: %s", got)
	}
	mu.Unlock()

	g, o := strconv.FormatInt(guest, 10), strconv.FormatInt(owner, 10)
	if _, body := do("guest-token", "GET", "/jobs", ""); !strings.Contains(body, "dbtest 손님 일") || strings.Contains(body, "dbtest 운영자 일") || !strings.Contains(body, `"who":"guest"`) {
		t.Fatalf("손님 목록: %s", body)
	}
	if _, body := do("owner-token", "GET", "/jobs", ""); strings.Contains(body, "dbtest 손님 일") || !strings.Contains(body, "dbtest 운영자 일") {
		t.Fatalf("운영자 목록: %s", body)
	}
	for _, c := range []struct{ token, method, path string }{
		{"guest-token", "GET", "/jobs/" + o},
		{"guest-token", "POST", "/jobs/" + o + "/pause"},
		{"guest-token", "POST", "/jobs/" + o + "/runs"},
		{"owner-token", "GET", "/jobs/" + g},
		{"owner-token", "POST", "/jobs/" + g + "/cancel"},
	} {
		if code, _ := do(c.token, c.method, c.path, `{}`); code != http.StatusNotFound {
			t.Fatalf("남의 작업 %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _ := do("guest-token", "POST", "/jobs/"+g+"/sites", `{"add":["coupang.com"]}`); code != http.StatusForbidden {
		t.Fatalf("손님 작업에 사이트 더하기: %d", code)
	}

	// The scheduler: each agent's own due jobs, and only their own runs.
	_, body := do("guest-token", "GET", "/due?limit=20", "")
	var due struct{ Jobs []store.Job }
	if json.Unmarshal([]byte(body), &due) != nil || len(due.Jobs) == 0 {
		t.Fatalf("손님의 할 작업: %s", body)
	}
	for _, j := range due.Jobs {
		if j.Who != store.Guest {
			t.Fatalf("손님 스케줄러에 남의 작업: %+v", j)
		}
	}
	code, body := do("guest-token", "POST", "/jobs/"+g+"/runs", `{}`)
	var run struct{ RunID int64 }
	if code != http.StatusCreated || json.Unmarshal([]byte(body), &run) != nil {
		t.Fatalf("손님 실행 시작: %d %s", code, body)
	}
	r := strconv.FormatInt(run.RunID, 10)
	if code, _ := do("owner-token", "POST", "/runs/"+r+"/step", `{"kind":"step","text":"남의 실행"}`); code != http.StatusNotFound {
		t.Fatalf("운영자가 손님 실행에 적음: %d", code)
	}
	if code, _ := do("owner-token", "POST", "/runs/orphans", `{}`); code != http.StatusNoContent {
		t.Fatalf("운영자 남은 실행 정리: %d", code)
	}
	if _, body := do("guest-token", "GET", "/jobs/"+g, ""); !strings.Contains(body, `"running":true`) {
		t.Fatalf("운영자 에이전트가 재시작하며 손님 실행을 닫음: %s", body)
	}
	if code, _ := do("guest-token", "POST", "/runs/"+r+"/finish", `{"summary":"끝"}`); code != http.StatusNoContent {
		t.Fatalf("손님 실행 끝내기: %d", code)
	}
	if code, _ := do("someone", "GET", "/jobs", ""); code != http.StatusUnauthorized {
		t.Fatalf("모르는 토큰: %d", code)
	}
}
