package seal

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestSealOpensOnlyUnderItsDomainAndKey(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce, sealed, err := s.Seal("coupang.com", []byte(`{"u":"me","p":"pw"}`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("pw")) {
		t.Fatal("봉인에 평문이 보입니다")
	}
	got, err := s.Open("coupang.com", nonce, sealed)
	if err != nil || string(got) != `{"u":"me","p":"pw"}` {
		t.Fatalf("열기: %q %v", got, err)
	}
	if _, err := s.Open("evil.com", nonce, sealed); err == nil {
		t.Fatal("다른 도메인으로 옮긴 봉인이 열렸습니다")
	}
	other := make([]byte, 32)
	_, _ = rand.Read(other)
	o, _ := New(other)
	if _, err := o.Open("coupang.com", nonce, sealed); err == nil {
		t.Fatal("다른 키로 열렸습니다")
	}
	if o.ID() == s.ID() {
		t.Fatal("키가 다르면 id 도 달라야 합니다")
	}
	n2, _, _ := s.Seal("coupang.com", []byte("x"))
	if bytes.Equal(n2, nonce) {
		t.Fatal("쓸 때마다 새 nonce")
	}
}

func TestFromBase64(t *testing.T) {
	if _, err := FromBase64("short"); err == nil {
		t.Fatal("짧은 키는 거절")
	}
	if _, err := FromBase64("q3fKJ8Yp1xqv0n0Yk4cJ7m7cVQm0m3e2bQ2nYJpQZ8o="); err != nil {
		t.Fatal(err)
	}
}
