// Package seal encrypts a saved login with the vault's key.
//
// AES-256-GCM, a fresh nonce per write, and the login's domain as associated
// data: a sealed row moved under another domain does not open. The key lives
// only in this service's environment (the Mac mini's .env), never in the
// database or its backups - a dump holds ciphertext alone.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

type Sealer struct {
	aead cipher.AEAD
	id   string
}

// FromBase64 reads a 32-byte key written as base64 (openssl rand -base64 32).
func FromBase64(s string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(key) != 32 {
		return nil, errors.New("VAULT_KEY 는 base64 로 쓴 32바이트여야 합니다 (openssl rand -base64 32)")
	}
	return New(key)
}

func New(key []byte) (*Sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte("jarvis-vault-key-id/"), key...))
	return &Sealer{aead: aead, id: hex.EncodeToString(sum[:4])}, nil
}

// ID names the key without revealing it, so a row sealed under another key
// says so instead of failing to open as if it were damaged.
func (s *Sealer) ID() string { return s.id }

func aad(domain string) []byte { return []byte("jarvis-vault/v1/" + domain) }

func (s *Sealer) Seal(domain string, plain []byte) (nonce, sealed []byte, err error) {
	nonce = make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, s.aead.Seal(nil, nonce, plain, aad(domain)), nil
}

func (s *Sealer) Open(domain string, nonce, sealed []byte) ([]byte, error) {
	if len(nonce) != s.aead.NonceSize() {
		return nil, errors.New("봉인이 손상되었습니다")
	}
	plain, err := s.aead.Open(nil, nonce, sealed, aad(domain))
	if err != nil {
		return nil, errors.New("봉인을 열지 못했습니다(키가 다르거나 손상)")
	}
	return plain, nil
}
