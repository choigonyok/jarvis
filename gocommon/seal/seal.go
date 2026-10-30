// Package seal keeps a message's text from lying about in the clear: what is
// sealed with the content key can be read only with it. The machine has no
// disk encryption, so this is what stands between a copy of the database and
// the conversations in it. Text is sealed where it comes in (the collectors)
// and stays sealed in the records and in mind's store.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// Sealer seals with one key.
type Sealer struct{ aead cipher.AEAD }

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
	return &Sealer{aead: aead}, nil
}

// FromBase64 is a Sealer on a key as it is kept in the environment.
func FromBase64(v string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(key) != 32 {
		return nil, errors.New("base64 로 된 32바이트여야 합니다 (openssl rand -base64 32)")
	}
	return New(key)
}

// Seal is plain with a fresh nonce in front.
func (s *Sealer) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

// Open is what Seal sealed.
func (s *Sealer) Open(body []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(body) < n {
		return nil, errors.New("봉해진 내용이 잘렸습니다")
	}
	plain, err := s.aead.Open(nil, body[:n], body[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("봉해진 내용을 열지 못했습니다: %w", err)
	}
	return plain, nil
}
