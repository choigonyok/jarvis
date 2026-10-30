package seal

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestSealAndOpen(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	s, err := FromBase64(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	body, err := s.Seal([]byte("안녕하세요"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("안녕하세요")) {
		t.Fatal("평문이 보입니다")
	}
	if again, _ := s.Seal([]byte("안녕하세요")); bytes.Equal(body, again) {
		t.Fatal("같은 말이 같은 봉인이 됐습니다")
	}
	got, err := s.Open(body)
	if err != nil || string(got) != "안녕하세요" {
		t.Fatalf("%q %v", got, err)
	}
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open(body); err == nil {
		t.Fatal("다른 키로 열렸습니다")
	}
	if _, err := s.Open([]byte("짧음")); err == nil {
		t.Fatal("잘린 것이 열렸습니다")
	}
	if _, err := FromBase64("abc"); err == nil {
		t.Fatal("짧은 키")
	}
}
