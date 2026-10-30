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
	a, closeA := h.Subscribe("owner")
	b, closeB := h.Subscribe("owner")
	closeB()
	h.Broadcast("owner", []byte("x"))
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

// The guest's console hears the guest's notifications, never the operator's.
func TestBroadcastStaysWithItsPerson(t *testing.T) {
	h := New()
	owner, closeOwner := h.Subscribe("owner")
	guest, closeGuest := h.Subscribe("guest")
	defer closeOwner()
	defer closeGuest()
	h.Broadcast("owner", []byte("home"))
	select {
	case <-guest:
		t.Fatal("the guest heard the operator's notification")
	default:
	}
	if got := <-owner; string(got) != "home" {
		t.Fatalf("operator got %q", got)
	}
	h.Broadcast("guest", []byte("card"))
	if got := <-guest; string(got) != "card" {
		t.Fatalf("guest got %q", got)
	}
}
