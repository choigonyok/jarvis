// Package seal keeps what is written down from lying about in the clear: what
// is sealed with a key can be read only with it. The machine has no disk
// encryption, so this is what stands between a copy of the database and the
// conversations and logins in it. A message's text is sealed where it comes
// in (the collectors) and stays sealed in the records and in mind's store;
// vault-svc seals each login for its domain.
//
// A seal is AES-256-GCM with a fresh nonce, in one form that says what it is:
//
//	"jsl2" | key id (4 bytes) | nonce (12) | sealed text and tag
//
// with its first eight bytes and its place bound to it as associated data.
// The place is where it is kept - a login's domain, or nothing for a message:
// a seal moved to another place does not open there. The key id names the
// key without revealing it, so a seal made under another key says so instead
// of failing to open as if it were damaged.
//
// Open and OpenFor also open the two forms without the prefix, which stored
// rows still hold: a message's (nonce | sealed text, nothing bound) and a
// vault login's (the nonce kept apart, bound to "jarvis-vault/v1/" + place).
// What is sealed now is always the form above.
package seal

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	// prefix starts every seal made now, and names its form.
	prefix = "jsl2"
	idSize = 4
	// HeadSize is how much of a seal comes before the sealed text: the
	// prefix, the key id and the nonce.
	HeadSize = len(prefix) + idSize + 12
	// keyIDLabel is what a key id is hashed with. vault-svc keeps each row's
	// key id in a column of its own, so it stays the label those ids were
	// made with.
	keyIDLabel = "jarvis-vault-key-id/"
	// vaultLabel is what a vault login without the prefix is bound to,
	// before its place.
	vaultLabel = "jarvis-vault/v1/"
)

// ErrOtherKey is a seal made under another key: whole, but not this key's.
var ErrOtherKey = errors.New("다른 키로 봉해졌습니다")

// Sealer seals with one key.
type Sealer struct {
	aead cipher.AEAD
	id   [idSize]byte
}

// New is a Sealer on a 32-byte key.
func New(key []byte) (*Sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &Sealer{aead: aead}
	sum := sha256.Sum256(append([]byte(keyIDLabel), key...))
	copy(s.id[:], sum[:idSize])
	return s, nil
}

// FromBase64 is a Sealer on a key as it is kept in the environment.
func FromBase64(v string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
	if err != nil || len(key) != 32 {
		return nil, errors.New("base64 로 된 32바이트여야 합니다 (openssl rand -base64 32)")
	}
	return New(key)
}

// ID names the key without revealing it: eight hex digits, the same in
// every seal made with it.
func (s *Sealer) ID() string { return hex.EncodeToString(s.id[:]) }

// Seal is plain sealed for no place in particular; Open opens it.
func (s *Sealer) Seal(plain []byte) ([]byte, error) { return s.SealFor("", plain) }

// Open is what Seal sealed, in either form.
func (s *Sealer) Open(body []byte) ([]byte, error) { return s.OpenFor("", body) }

// SealFor is plain sealed for place: it opens for that place only.
func (s *Sealer) SealFor(place string, plain []byte) ([]byte, error) {
	out := make([]byte, HeadSize, HeadSize+len(plain)+s.aead.Overhead())
	copy(out, prefix)
	copy(out[len(prefix):], s.id[:])
	nonce := out[len(prefix)+idSize:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(out, nonce, plain, bound(out, place)), nil
}

// OpenFor is what SealFor sealed for place, in either form.
func (s *Sealer) OpenFor(place string, body []byte) ([]byte, error) {
	if len(body) < HeadSize || !bytes.HasPrefix(body, []byte(prefix)) {
		return s.openBare(place, body)
	}
	plain, err := s.open(place, body)
	if err != nil {
		// A bare seal's random nonce starts with the prefix once in 2^32.
		if bare, bareErr := s.openBare(place, body); bareErr == nil {
			return bare, nil
		}
	}
	return plain, err
}

// SealApart is SealFor with the head (HeadSize bytes) apart from the rest,
// for a store that keeps the two in columns of their own.
func (s *Sealer) SealApart(place string, plain []byte) (head, rest []byte, err error) {
	whole, err := s.SealFor(place, plain)
	if err != nil {
		return nil, nil, err
	}
	return whole[:HeadSize:HeadSize], whole[HeadSize:], nil
}

// OpenApart is what SealApart sealed for place. A vault login without the
// prefix has its nonce for a head, and opens the same way.
func (s *Sealer) OpenApart(place string, head, rest []byte) ([]byte, error) {
	whole := make([]byte, 0, len(head)+len(rest))
	return s.OpenFor(place, append(append(whole, head...), rest...))
}

func (s *Sealer) open(place string, body []byte) ([]byte, error) {
	if id := body[len(prefix) : len(prefix)+idSize]; !bytes.Equal(id, s.id[:]) {
		return nil, fmt.Errorf("%w (%x, 이 키는 %s)", ErrOtherKey, id, s.ID())
	}
	plain, err := s.aead.Open(nil, body[len(prefix)+idSize:HeadSize], body[HeadSize:], bound(body, place))
	if err != nil {
		return nil, fmt.Errorf("봉해진 내용을 열지 못했습니다: %w", err)
	}
	return plain, nil
}

// openBare opens a seal without the prefix: a message's when place is empty,
// a vault login's otherwise.
func (s *Sealer) openBare(place string, body []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(body) < n {
		return nil, errors.New("봉해진 내용이 잘렸습니다")
	}
	var data []byte
	if place != "" {
		data = []byte(vaultLabel + place)
	}
	plain, err := s.aead.Open(nil, body[:n], body[n:], data)
	if err != nil {
		return nil, fmt.Errorf("봉해진 내용을 열지 못했습니다: %w", err)
	}
	return plain, nil
}

// bound is what a seal is bound to: its prefix and key id, then its place.
// It is a copy, never the seal's own bytes.
func bound(head []byte, place string) []byte {
	data := make([]byte, 0, len(prefix)+idSize+len(place))
	return append(append(data, head[:len(prefix)+idSize]...), place...)
}
