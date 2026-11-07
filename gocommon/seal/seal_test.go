package seal

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
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
	if _, err := other.Open(body); !errors.Is(err, ErrOtherKey) {
		t.Fatalf("다른 키로는 다른 키라고 말합니다: %v", err)
	}
	if _, err := s.Open([]byte("짧음")); err == nil {
		t.Fatal("잘린 것이 열렸습니다")
	}
	if _, err := s.Open(body[:len(body)-1]); err == nil {
		t.Fatal("끝이 잘린 것이 열렸습니다")
	}
	if _, err := FromBase64("abc"); err == nil {
		t.Fatal("짧은 키")
	}
	if _, err := FromBase64(" " + base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		t.Fatalf("앞뒤 공백은 키가 아닙니다: %v", err)
	}
}

// A seal says its form and its key in its first eight bytes, and both are
// bound to it.
func TestASealSaysItsFormAndKey(t *testing.T) {
	s, _ := New(bytes.Repeat([]byte{1}, 32))
	body, err := s.Seal([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body[:4]) != prefix || len(body) != HeadSize+1+16 {
		t.Fatalf("형식: %x (%d바이트)", body[:8], len(body))
	}
	if id := s.ID(); len(id) != 8 || hex.EncodeToString(body[4:8]) != id {
		t.Fatalf("키 id %q, 봉인에는 %x", id, body[4:8])
	}
	same, _ := New(bytes.Repeat([]byte{1}, 32))
	other, _ := New(bytes.Repeat([]byte{2}, 32))
	if same.ID() != s.ID() || other.ID() == s.ID() {
		t.Fatal("키 id 는 키마다 하나입니다")
	}
	forged := append([]byte{}, body...)
	copy(forged[4:8], other.id[:])
	if _, err := other.Open(forged); err == nil {
		t.Fatal("키 id 만 바꾼 봉인이 열렸습니다")
	}
}

// What is sealed for a place opens there only: a login moved under another
// domain stays shut, and so does one read as a message.
func TestASealOpensForItsPlaceOnly(t *testing.T) {
	s, _ := New(bytes.Repeat([]byte{3}, 32))
	body, err := s.SealFor("coupang.com", []byte(`{"u":"me","p":"pw"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.OpenFor("coupang.com", body); err != nil || string(got) != `{"u":"me","p":"pw"}` {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := s.OpenFor("evil.com", body); err == nil {
		t.Fatal("다른 도메인으로 옮긴 봉인이 열렸습니다")
	}
	if _, err := s.Open(body); err == nil {
		t.Fatal("자리 없이 열렸습니다")
	}
	head, rest, err := s.SealApart("pin:pay.example.com", []byte(`{"pin":"123456"}`))
	if err != nil || len(head) != HeadSize || string(head[:4]) != prefix {
		t.Fatalf("머리 %x %v", head, err)
	}
	if got, err := s.OpenApart("pin:pay.example.com", head, rest); err != nil || string(got) != `{"pin":"123456"}` {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := s.OpenApart("pin:evil.com", head, rest); err == nil {
		t.Fatal("다른 자리로 열렸습니다")
	}
}

// legacy is what the two forms without the prefix made (testdata/legacy.json:
// sealed by those implementations themselves, with a test key).
type legacy struct {
	Key      string `json:"key"`
	KeyID    string `json:"keyID"`
	Messages []struct {
		Plain  string `json:"plain"`
		Sealed string `json:"sealed"`
	} `json:"messages"`
	Vault []struct {
		Place  string `json:"place"`
		Plain  string `json:"plain"`
		Nonce  string `json:"nonce"`
		Sealed string `json:"sealed"`
	} `json:"vault"`
}

func readLegacy(t *testing.T) (legacy, *Sealer) {
	t.Helper()
	raw, err := os.ReadFile("testdata/legacy.json")
	if err != nil {
		t.Fatal(err)
	}
	var l legacy
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	s, err := FromBase64(l.Key)
	if err != nil {
		t.Fatal(err)
	}
	return l, s
}

func unb64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Rows in the forms without the prefix open as they were made: messages with
// nothing bound, vault logins bound to their place under the vault's label.
func TestTheFormsWithoutThePrefixStillOpen(t *testing.T) {
	l, s := readLegacy(t)
	if s.ID() != l.KeyID {
		t.Fatalf("키 id %s, 보관함의 행은 %s", s.ID(), l.KeyID)
	}
	for _, m := range l.Messages {
		got, err := s.Open(unb64(t, m.Sealed))
		if err != nil || string(got) != m.Plain {
			t.Fatalf("메시지 %q: %q %v", m.Plain, got, err)
		}
	}
	for _, v := range l.Vault {
		nonce, sealed := unb64(t, v.Nonce), unb64(t, v.Sealed)
		got, err := s.OpenApart(v.Place, nonce, sealed)
		if err != nil || string(got) != v.Plain {
			t.Fatalf("보관함 %s: %q %v", v.Place, got, err)
		}
		if _, err := s.OpenApart("evil.com", nonce, sealed); err == nil {
			t.Fatalf("보관함 %s 가 다른 자리로 열렸습니다", v.Place)
		}
		broken := append([]byte{}, sealed...)
		broken[0] ^= 1
		if _, err := s.OpenApart(v.Place, nonce, broken); err == nil {
			t.Fatalf("보관함 %s: 손상된 봉인이 열렸습니다", v.Place)
		}
	}
	other, _ := New(bytes.Repeat([]byte{9}, 32))
	if _, err := other.Open(unb64(t, l.Messages[0].Sealed)); err == nil {
		t.Fatal("다른 키로 열렸습니다")
	}
}

// A seal without the prefix whose random nonce happens to start with it still
// opens.
func TestABareSealThatStartsLikeThePrefixOpens(t *testing.T) {
	s, _ := New(bytes.Repeat([]byte{4}, 32))
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	_, _ = rand.Read(nonce[len(prefix):])
	bare := s.aead.Seal(append([]byte{}, nonce...), nonce, []byte("우연"), nil)
	if got, err := s.Open(bare); err != nil || string(got) != "우연" {
		t.Fatalf("메시지: %q %v", got, err)
	}
	row := s.aead.Seal(nil, nonce, []byte("로그인"), []byte(vaultLabel+"example.com"))
	if got, err := s.OpenApart("example.com", nonce, row); err != nil || string(got) != "로그인" {
		t.Fatalf("보관함: %q %v", got, err)
	}
}
