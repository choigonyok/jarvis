package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs against a throwaway database as svc_vault (scripts/dbtest vault-svc).
func TestStoreAsItsRole(t *testing.T) {
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest vault-svc)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	l := Login{Domain: "dbtest.example", Name: "시험", Hint: "ab***", Nonce: []byte("123456789012"), Sealed: []byte("sealed"), KeyID: "k1"}
	if err := s.Put(ctx, l); err != nil {
		t.Fatal(err)
	}
	l.Hint = "cd***"
	if err := s.Put(ctx, l); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "dbtest.example")
	if err != nil || got.Hint != "cd***" || string(got.Sealed) != "sealed" {
		t.Fatalf("다시 저장하면 덮어써야 합니다: %+v %v", got, err)
	}
	if err := s.Used(ctx, "dbtest.example"); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(ctx, Use{Domain: "dbtest.example", Host: "login.dbtest.example", Purpose: "test", Outcome: "released"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) == 0 || list[0].Uses != 1 || list[0].LastUsedAt == nil {
		t.Fatalf("목록: %+v %v", list, err)
	}
	uses, err := s.Uses(ctx, 10)
	if err != nil || len(uses) == 0 || uses[0].Outcome != "released" {
		t.Fatalf("기록: %+v %v", uses, err)
	}
	if gone, err := s.Delete(ctx, "dbtest.example"); err != nil || !gone {
		t.Fatalf("지우기: %v %v", gone, err)
	}
	if _, err := s.Get(ctx, "dbtest.example"); err != ErrNotFound {
		t.Fatalf("지운 뒤: %v", err)
	}

	// 결제 비밀번호: 로그인과 따로, 같은 역할로.
	p := Pin{Site: "pay.dbtest.example", Name: "시험페이", Nonce: []byte("123456789012"), Sealed: []byte("sealed-pin"), KeyID: "k1"}
	if err := s.PutPin(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Sealed = []byte("sealed-pin-2")
	if err := s.PutPin(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPin(ctx, "pay.dbtest.example"); err != nil || string(got.Sealed) != "sealed-pin-2" {
		t.Fatalf("결제 비밀번호를 다시 저장하면 덮어써야 합니다: %+v %v", got, err)
	}
	if err := s.UsedPin(ctx, "pay.dbtest.example"); err != nil {
		t.Fatal(err)
	}
	pins, err := s.ListPins(ctx)
	if err != nil || len(pins) != 1 || pins[0].Uses != 1 || pins[0].LastUsedAt == nil {
		t.Fatalf("결제 비밀번호 목록: %+v %v", pins, err)
	}
	if gone, err := s.DeletePin(ctx, "pay.dbtest.example"); err != nil || !gone {
		t.Fatalf("결제 비밀번호 지우기: %v %v", gone, err)
	}
	if _, err := s.GetPin(ctx, "pay.dbtest.example"); err != ErrNotFound {
		t.Fatalf("지운 뒤: %v", err)
	}
}
