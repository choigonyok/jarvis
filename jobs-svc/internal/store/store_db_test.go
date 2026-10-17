package store

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/jobs-svc/internal/rules"
)

// Runs against a throwaway database as svc_jobs (scripts/dbtest jobs-svc).
func TestAddSitesKeepsEachOnce(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest jobs-svc)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Create(ctx, NewJob{Title: "시험", Goal: "사이트 더하기", Sites: []string{"www.yes24.com"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AddSites(ctx, j.ID, []string{"https://ticket.melon.com/search", "WWW.YES24.COM", " "})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Sites, []string{"ticket.melon.com", "www.yes24.com"}) {
		t.Fatalf("사이트: %v", got.Sites)
	}
	if _, err := s.AddSites(ctx, j.ID, []string{" "}); err == nil {
		t.Fatal("빈 사이트만 오면 오류")
	}
	if _, err := s.AddSites(ctx, 999999, []string{"a.com"}); err != ErrNotFound {
		t.Fatalf("없는 작업: %v", err)
	}
}

// Against a throwaway Postgres, as svc_jobs (scripts/dbtest): each run in a
// row that gave way to the conversation pushes the next one further out, and
// a run that went through starts the count over.
func TestYieldedRunsBackOff(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	j, err := s.Create(ctx, NewJob{Title: "dbtest 양보", Goal: "기다리기", Sites: []string{"shop.example"}})
	if err != nil {
		t.Fatal(err)
	}

	end := func(outcome string) time.Duration {
		t.Helper()
		run, err := s.StartRun(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if outcome == rules.OK {
			if _, err := s.Schedule(ctx, run, time.Now().Add(time.Hour), "다음"); err != nil {
				t.Fatal(err)
			}
		}
		before := time.Now()
		if _, err := s.EndRun(ctx, run, outcome, ""); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, j.ID)
		if err != nil || got.NextAt == nil {
			t.Fatalf("다음 실행: %+v %v", got, err)
		}
		return got.NextAt.Sub(before).Round(time.Minute)
	}
	for i, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute} {
		if got := end(rules.Yielded); got != want {
			t.Fatalf("%d번째 양보 뒤 %v, 기대 %v", i+1, got, want)
		}
	}
	if got := end(rules.OK); got != time.Hour {
		t.Fatalf("스스로 정한 다음 실행: %v", got)
	}
	if got := end(rules.Yielded); got != 5*time.Minute {
		t.Fatalf("끝까지 간 실행 뒤로는 다시 5분부터: %v", got)
	}
	d, err := s.Detail(ctx, j.ID, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Events) == 0 || !strings.Contains(d.Events[0].Text, "5분 뒤에 다시") {
		t.Fatalf("기록: %+v", d.Events)
	}
}
