// Package jsonfile is the one way this agent puts state on disk.
//
// Every writer goes through Save, which writes a temporary file beside the
// target and renames it. A crash halfway through leaves the previous version
// intact rather than a truncated one - the stores here are the record of what
// the operator decided, and half of that is worse than none of it.
package jsonfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Load reads path into v. A missing file is not an error: first run must not
// need a setup step, and v keeps whatever zero value it arrived with.
func Load(path string, v any) error {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s 읽기: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("%s 해석: %w", filepath.Base(path), err)
	}
	return nil
}

// Save writes v to path atomically. 0600 because this is one person's
// conversation with their assistant, not something the rest of the host reads.
func Save(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
