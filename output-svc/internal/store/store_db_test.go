package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration 054 against a throwaway Postgres, as svc_notify (scripts/dbtest):
// each person reads only their own inbox, devices and settings, and the
// operator's settings moved over from the old one-row table.
func TestEveryoneHasTheirOwn(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	admin := os.Getenv("JARVIS_TEST_ADMIN_DSN")
	if admin == "" {
		admin = dsn
	}
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `delete from output.notifications where source = 'dbtest'; delete from output.push_subscriptions where endpoint like 'https://dbtest/%'`)
	conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool)

	for _, n := range []Notification{
		{Audience: Owner, Source: "dbtest", Kind: "location.leave", Tier: "log", Level: "info", Title: "집 떠남", URL: "/"},
		{Audience: Guest, Source: "dbtest", Kind: "approval.pending", Tier: "now", Level: "warn", Title: "결재 대기", URL: "/"},
	} {
		if _, _, err := s.Insert(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	box, err := s.List(ctx, Guest, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	guest, unread := box.Items, box.Unread
	for _, n := range guest {
		if n.Title == "집 떠남" {
			t.Fatal("손님 알림함에 운영자의 위치 알림이 보입니다")
		}
	}
	if len(guest) != 1 || unread != 1 || box.Total != 1 || guest[0].Audience != Guest {
		t.Fatalf("손님 알림함: %d개, 안 읽음 %d, 모두 %d", len(guest), unread, box.Total)
	}
	// "All read" is the guest's all: the operator's stays unread.
	if err := s.MarkRead(ctx, Guest, nil); err != nil {
		t.Fatal(err)
	}
	if owner, _ := s.List(ctx, Owner, 50, 0); owner.Unread == 0 {
		t.Fatal("손님의 모두 읽음이 운영자 알림까지 읽었습니다")
	}

	// A browser signed in as the other person moves to them.
	sub := Subscription{Audience: Owner, Endpoint: "https://dbtest/1", P256dh: "k", Auth: "a"}
	if err := s.Subscribe(ctx, sub); err != nil {
		t.Fatal(err)
	}
	sub.Audience = Guest
	if err := s.Subscribe(ctx, sub); err != nil {
		t.Fatal(err)
	}
	owners, _ := s.Subscriptions(ctx, Owner)
	guests, _ := s.Subscriptions(ctx, Guest)
	for _, d := range owners {
		if d.Endpoint == sub.Endpoint {
			t.Fatal("손님으로 다시 구독한 기기가 운영자에게도 남았습니다")
		}
	}
	if len(guests) == 0 {
		t.Fatal("손님 기기가 없습니다")
	}

	type set struct{ QuietStart string }
	if err := s.WriteSettings(ctx, Guest, set{QuietStart: "23:00"}); err != nil {
		t.Fatal(err)
	}
	var owner, mine set
	if err := s.Settings(ctx, Owner, &owner); err != nil {
		t.Fatal(err)
	}
	if err := s.Settings(ctx, Guest, &mine); err != nil {
		t.Fatal(err)
	}
	if mine.QuietStart != "23:00" || owner.QuietStart == "23:00" {
		t.Fatalf("설정이 섞였습니다: owner=%+v guest=%+v", owner, mine)
	}
}

// The console reads the inbox a page at a time: pages in a row hold every
// notification once - none on two pages, none on neither - newest first,
// with the count they add up to.
func TestInboxPages(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool)

	before, err := s.List(ctx, Owner, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		n := Notification{Audience: Owner, Source: "dbtest", Kind: "status.page", Tier: "log", Level: "info", Title: "쪽 나누기", URL: "/"}
		if _, _, err := s.Insert(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]bool{}
	var last *Notification
	total := 0
	for offset := 0; ; offset += 2 {
		page, err := s.List(ctx, Owner, 2, offset)
		if err != nil {
			t.Fatal(err)
		}
		total = page.Total
		if len(page.Items) == 0 {
			break
		}
		for i := range page.Items {
			n := page.Items[i]
			if seen[n.ID] {
				t.Fatalf("%d 번 알림이 두 쪽에 나옵니다", n.ID)
			}
			seen[n.ID] = true
			if last != nil && n.CreatedAt.After(last.CreatedAt) {
				t.Fatalf("새것부터가 아닙니다: %v 뒤에 %v", last.CreatedAt, n.CreatedAt)
			}
			last = &n
		}
	}
	if total != before.Total+5 || len(seen) != total {
		t.Fatalf("모두 %d개(전에 %d개 + 5)여야 하는데 쪽을 다 넘기니 %d개, 합계 %d", before.Total+5, before.Total, len(seen), total)
	}
}
