package imsg

import (
	"strings"
	"testing"
	"time"
)

// archive builds the part of a typedstream blob the decoder reads.
func archive(text string) []byte {
	b := []byte("\x04\x0bstreamtyped\x81\xe8\x03\x84\x01@\x84\x84\x84\x12NSAttributedString\x00\x84\x84\x08NSObject\x00\x85\x92\x84\x84\x84\x08NSString\x01\x94\x84\x01+")
	n := len(text)
	switch {
	case n < 0x80:
		b = append(b, byte(n))
	default:
		b = append(b, 0x81, byte(n), byte(n>>8))
	}
	b = append(b, text...)
	return append(b, "\x86\x84\x02iI\x01"...)
}

func TestTextFromAttributedBody(t *testing.T) {
	for _, s := range []string{"안녕", "택배 도착했어요 👍", strings.Repeat("긴 문장 ", 60)} {
		if got := TextFromAttributedBody(archive(s)); got != s {
			t.Errorf("got %q want %q", got, s)
		}
	}
	if TextFromAttributedBody([]byte("garbage")) != "" {
		t.Fatal("garbage must decode to empty")
	}
	if TextFromAttributedBody(archive("x")[:60]) != "" {
		t.Fatal("truncated blob must decode to empty")
	}
}

func TestAppleTime(t *testing.T) {
	// 2026-10-08 00:00:00 UTC as nanoseconds since 2001-01-01.
	want := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	ns := want.Sub(time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)).Nanoseconds()
	if got := appleTime(ns); !got.Equal(want) {
		t.Fatalf("ns: %v", got)
	}
	// Older rows store seconds.
	if got := appleTime(ns / 1e9); !got.Equal(want) {
		t.Fatalf("s: %v", got)
	}
}

func TestDigits(t *testing.T) {
	if digits("+82 10-1234-5678") != "01012345678" || digits("010.1234.5678") != "01012345678" {
		t.Fatal(digits("+82 10-1234-5678"))
	}
}
