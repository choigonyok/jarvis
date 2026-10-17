package store

import (
	"context"
	"os"
	"slices"
	"testing"
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
