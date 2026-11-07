package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// The two readers that know only the bare form, copied as they are:
// gocommon/seal's Sealer without the headed form (a message: nonce | sealed
// text, nothing bound) and vault-svc's own seal package (a row: the nonce in
// a column of its own, bound to "jarvis-vault/v1/" + its domain, with the key
// id it keeps in key_id). Until Written turns, a service that opens seals may
// still run one of them, so what is written must open with them.

type bareMessages struct{ aead cipher.AEAD }

func newBareMessages(key []byte) (*bareMessages, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &bareMessages{aead: aead}, nil
}

func (s *bareMessages) Open(body []byte) ([]byte, error) {
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

type bareVault struct {
	aead cipher.AEAD
	id   string
}

func newBareVault(key []byte) (*bareVault, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte("jarvis-vault-key-id/"), key...))
	return &bareVault{aead: aead, id: hex.EncodeToString(sum[:4])}, nil
}

func (s *bareVault) ID() string { return s.id }

func aad(domain string) []byte { return []byte("jarvis-vault/v1/" + domain) }

func (s *bareVault) Open(domain string, nonce, sealed []byte) ([]byte, error) {
	if len(nonce) != s.aead.NonceSize() {
		return nil, errors.New("봉인이 손상되었습니다")
	}
	plain, err := s.aead.Open(nil, nonce, sealed, aad(domain))
	if err != nil {
		return nil, errors.New("봉인을 열지 못했습니다(키가 다르거나 손상)")
	}
	return plain, nil
}
