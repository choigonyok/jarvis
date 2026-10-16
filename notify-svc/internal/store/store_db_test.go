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
	_, err = conn.Exec(ctx, `delete from notifications where source = 'dbtest'; delete from push_subscriptions where endpoint like 'https://dbtest/%'`)
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
	guest, unread, err := s.List(ctx, Guest, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range guest {
		if n.Title == "집 떠남" {
			t.Fatal("손님 알림함에 운영자의 위치 알림이 보입니다")
		}
	}
	if len(guest) != 1 || unread != 1 || guest[0].Audience != Guest {
		t.Fatalf("손님 알림함: %d개, 안 읽음 %d", len(guest), unread)
	}
	// "All read" is the guest's all: the operator's stays unread.
	if err := s.MarkRead(ctx, Guest, nil); err != nil {
		t.Fatal(err)
	}
	if _, ownerUnread, _ := s.List(ctx, Owner, 50); ownerUnread == 0 {
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
