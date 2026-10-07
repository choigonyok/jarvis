package uploads

import (
	"bytes"
	"strings"
	"testing"
)

// A 1x1 PNG.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func TestSaveAndPath(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name, err := s.Save(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	if !Valid(name) || !strings.HasSuffix(name, ".png") {
		t.Fatalf("name %q", name)
	}
	if _, err := s.Path(name); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Path(name); err != ErrNotFound {
		t.Fatalf("after remove: %v", err)
	}
}

func TestRejects(t *testing.T) {
	s, _ := New(t.TempDir())
	if _, err := s.Save(strings.NewReader("<html>not a photo</html>")); err == nil {
		t.Fatal("html accepted")
	}
	for _, bad := range []string{"../etc/passwd", "a.jpg", "0123456789abcdef0123456789abcdef.svg", ""} {
		if _, err := s.Path(bad); err != ErrNotFound {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
