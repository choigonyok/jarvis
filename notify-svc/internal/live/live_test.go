package live

import (
	"testing"
	"time"
)

func TestPresenceExpiresAndLeaving(t *testing.T) {
	h := New()
	now := time.Now()
	h.now = func() time.Time { return now }
	h.Seen("a", true)
	if !h.Present("a") || h.Present("b") {
		t.Fatal("a should be present, b not")
	}
	now = now.Add(Fresh + time.Second)
	if h.Present("a") {
		t.Fatal("a went quiet and is still present - it would miss pushes")
	}
	h.Seen("a", true)
	h.Seen("a", false)
	if h.Present("a") {
		t.Fatal("a left and is still present")
	}
}

func TestBroadcastReachesOpenStreamsOnly(t *testing.T) {
	h := New()
	a, closeA := h.Subscribe()
	b, closeB := h.Subscribe()
	closeB()
	h.Broadcast([]byte("x"))
	select {
	case got := <-a:
		if string(got) != "x" {
			t.Fatalf("got %q", got)
		}
	default:
		t.Fatal("open stream got nothing")
	}
	select {
	case <-b:
		t.Fatal("closed stream got a message")
	default:
	}
	closeA()
}
