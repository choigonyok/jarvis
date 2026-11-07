// Package seal keeps what is written down from lying about in the clear: what
// is sealed with a key opens only with that key, so a copy of the database or
// of its backups alone does not hand over the conversations and logins in it.
// A message's text is sealed where it comes in (the collectors) and stays
// sealed in the records and in mind's store; vault-svc seals each login for
// its domain.
//
// A seal is AES-256-GCM with a fresh nonce, in one of two forms:
//
//	Bare    nonce (12) | sealed text and tag
//	Headed  "jsl2" | key id (4 bytes) | nonce (12) | sealed text and tag
//
// A bare seal binds nothing when it has no place (a message), and
// "jarvis-vault/v1/" + its place when it has one (a vault row). A headed seal
// binds its first eight bytes and its place, and its key id names the key
// without revealing it, so a seal made under another key says so instead of
// failing to open as if it were damaged. The place is where a seal is kept -
// a login's domain, a payment site, a shipping profile: a seal moved to
// another place does not open there.
//
// Every Open reads both forms. Written alone decides which one is written.
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

// Form is how a seal is laid out.
type Form int

const (
	// Bare is the nonce, then the sealed text.
	Bare Form = iota
	// Headed is the prefix, the key id and the nonce, then the sealed text.
	Headed
)

// Written is the form every seal is written in.
//
// A seal its reader cannot open is lost to that reader: mind-svc skips a
// message it cannot open for good, and a service rolled back to code that
// knows only the bare form no longer reads what was sealed headed. So seals
// are written Bare, the form every reader opens, and Headed is read but not
// written. Written turns to Headed in a release of its own, once mind-svc,
// input-svc, vault-svc and both collectors run code that opens it.
const Written = Bare

const (
	// prefix starts every headed seal, and names its form.
	prefix    = "jsl2"
	idSize    = 4
	nonceSize = 12
	// HeadSize is how much of a headed seal comes before the sealed text:
	// the prefix, the key id and the nonce.
	HeadSize = len(prefix) + idSize + nonceSize
	// keyIDLabel is what a key id is hashed with. vault-svc keeps each row's
	// key id in a column of its own, so it stays the label those ids were
	// made with.
	keyIDLabel = "jarvis-vault-key-id/"
	// vaultLabel is what a bare seal with a place is bound to, before the
	// place: the binding vault-svc's rows are made with.
	vaultLabel = "jarvis-vault/v1/"
)

// ErrOtherKey is a headed seal made under another key: whole, but not this
// key's.
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
// every headed seal made with it and in vault-svc's key_id column.
func (s *Sealer) ID() string { return hex.EncodeToString(s.id[:]) }

// Seal is plain sealed for no place in particular; Open opens it.
func (s *Sealer) Seal(plain []byte) ([]byte, error) { return s.SealFor("", plain) }

// Open is what Seal sealed, in either form.
func (s *Sealer) Open(body []byte) ([]byte, error) { return s.OpenFor("", body) }

// SealFor is plain sealed for place, in the Written form: it opens for that
// place only.
func (s *Sealer) SealFor(place string, plain []byte) ([]byte, error) {
	return s.seal(Written, place, plain)
}

// OpenFor is what SealFor sealed for place, in either form.
func (s *Sealer) OpenFor(place string, body []byte) ([]byte, error) {
	if len(body) < HeadSize || !bytes.HasPrefix(body, []byte(prefix)) {
		return s.openBare(place, body)
	}
	plain, err := s.openHeaded(place, body)
	if err != nil {
		// A bare seal's random nonce starts with the prefix once in 2^32.
		if bare, bareErr := s.openBare(place, body); bareErr == nil {
			return bare, nil
		}
	}
	return plain, err
}

// SealApart is SealFor with the head apart from the rest, for a store that
// keeps the two in columns of their own: the nonce of a bare seal, or the
// HeadSize bytes of a headed one.
func (s *Sealer) SealApart(place string, plain []byte) (head, rest []byte, err error) {
	return s.sealApart(Written, place, plain)
}

// OpenApart is what SealApart sealed for place, in either form, told apart by
// the length of its head.
func (s *Sealer) OpenApart(place string, head, rest []byte) ([]byte, error) {
	whole := make([]byte, 0, len(head)+len(rest))
	whole = append(append(whole, head...), rest...)
	switch {
	case len(head) == nonceSize:
		return s.openBare(place, whole)
	case len(head) == HeadSize && bytes.HasPrefix(head, []byte(prefix)):
		return s.openHeaded(place, whole)
	}
	return nil, fmt.Errorf("봉인의 머리가 맞지 않습니다 (%d바이트)", len(head))
}

// seal is plain sealed for place in form f.
func (s *Sealer) seal(f Form, place string, plain []byte) ([]byte, error) {
	n := headSize(f)
	out := make([]byte, n, n+len(plain)+s.aead.Overhead())
	nonce := out[n-nonceSize:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	if f == Headed {
		copy(out, prefix)
		copy(out[len(prefix):], s.id[:])
		return s.aead.Seal(out, nonce, plain, bound(out, place)), nil
	}
	return s.aead.Seal(out, nonce, plain, bareBound(place)), nil
}

// sealApart is seal with its head apart from the rest.
func (s *Sealer) sealApart(f Form, place string, plain []byte) (head, rest []byte, err error) {
	whole, err := s.seal(f, place, plain)
	if err != nil {
		return nil, nil, err
	}
	n := headSize(f)
	return whole[:n:n], whole[n:], nil
}

// headSize is how much of a seal in form f comes before the sealed text.
func headSize(f Form) int {
	if f == Headed {
		return HeadSize
	}
	return nonceSize
}

// openHeaded opens a headed seal; body is at least HeadSize long.
func (s *Sealer) openHeaded(place string, body []byte) ([]byte, error) {
	if id := body[len(prefix) : len(prefix)+idSize]; !bytes.Equal(id, s.id[:]) {
		return nil, fmt.Errorf("%w (%x, 이 키는 %s)", ErrOtherKey, id, s.ID())
	}
	plain, err := s.aead.Open(nil, body[len(prefix)+idSize:HeadSize], body[HeadSize:], bound(body, place))
	if err != nil {
		return nil, fmt.Errorf("봉해진 내용을 열지 못했습니다: %w", err)
	}
	return plain, nil
}

// openBare opens a bare seal: a message's when place is empty, a vault row's
// otherwise.
func (s *Sealer) openBare(place string, body []byte) ([]byte, error) {
	if len(body) < nonceSize {
		return nil, errors.New("봉해진 내용이 잘렸습니다")
	}
	plain, err := s.aead.Open(nil, body[:nonceSize], body[nonceSize:], bareBound(place))
	if err != nil {
		return nil, fmt.Errorf("봉해진 내용을 열지 못했습니다: %w", err)
	}
	return plain, nil
}

// bound is what a headed seal is bound to: its prefix and key id, then its
// place. It is a copy, never the seal's own bytes.
func bound(head []byte, place string) []byte {
	data := make([]byte, 0, len(prefix)+idSize+len(place))
	return append(append(data, head[:len(prefix)+idSize]...), place...)
}

// bareBound is what a bare seal is bound to: nothing without a place, the
// vault's label and the place with one.
func bareBound(place string) []byte {
	if place == "" {
		return nil
	}
	return []byte(vaultLabel + place)
}
