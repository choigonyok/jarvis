// Package uploads keeps the photos the operator attaches in conversation.
//
// One flat directory on its own volume. The model reads them with the CLI's
// Read tool (which can look at images), and the browser container mounts the
// same volume read-only so a photo can go into a web form. Nothing else may
// name a file here: every name is minted by Save, and every lookup checks the
// name against that shape, so a request cannot walk out of the directory.
package uploads

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// MaxBytes bounds one photo. The console shrinks photos before sending, so a
// file this large is not a phone photo.
const MaxBytes = 15 << 20

var namePattern = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|webp)$`)

var ErrNotFound = errors.New("그런 사진이 없습니다.")

var extOf = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
}

type Store struct {
	Dir string
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// Valid reports whether name is one Save could have produced.
func Valid(name string) bool { return namePattern.MatchString(name) }

// Save writes one image and returns its name. The type is sniffed from the
// bytes, not taken from the request: what is stored is what is served back.
func (s *Store) Save(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxBytes {
		return "", fmt.Errorf("사진이 너무 큽니다 (%dMB 까지).", MaxBytes>>20)
	}
	ext, ok := extOf[http.DetectContentType(data)]
	if !ok {
		return "", errors.New("JPEG, PNG, WebP 사진만 올릴 수 있습니다.")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	name := hex.EncodeToString(id[:]) + "." + ext
	tmp := filepath.Join(s.Dir, "."+name)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(s.Dir, name)); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// Path is where a photo lives, for the model and the browser. ErrNotFound if
// the name is malformed or the file is gone.
func (s *Store) Path(name string) (string, error) {
	if !Valid(name) {
		return "", ErrNotFound
	}
	p := filepath.Join(s.Dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// Remove deletes a photo. A photo already gone is not an error.
func (s *Store) Remove(name string) error {
	if !Valid(name) {
		return ErrNotFound
	}
	err := os.Remove(filepath.Join(s.Dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
