// Package live is the console as it is open right now: which devices are
// looking at it, and a stream to each open window.
//
// A device that has the console on screen should not get a phone push for
// what it can be shown in the page - the phone's banner would cover the
// page's own. The console says it is on screen every few seconds while it
// is (and that it is not, as it leaves); a device that has said so recently
// is skipped by the pusher and gets the notification over its stream instead.
// One that went quiet - suspended without a word - is pushed to again once
// its last word is older than Fresh.
package live

import (
	"sync"
	"time"
)

// Fresh is how long "on screen" holds without being said again. The console
// says it every 10 seconds.
const Fresh = 15 * time.Second

type Hub struct {
	mu      sync.Mutex
	seen    map[string]time.Time // push endpoint -> last "on screen"
	streams map[chan []byte]struct{}
	now     func() time.Time
}

func New() *Hub {
	return &Hub{seen: map[string]time.Time{}, streams: map[chan []byte]struct{}{}, now: time.Now}
}

// Seen records what a device said: on screen, or leaving.
func (h *Hub) Seen(endpoint string, visible bool) {
	if endpoint == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if visible {
		h.seen[endpoint] = h.now()
	} else {
		delete(h.seen, endpoint)
	}
}

// Present says whether a device has the console on screen.
func (h *Hub) Present(endpoint string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, ok := h.seen[endpoint]
	return ok && h.now().Sub(at) < Fresh
}

// Subscribe opens a stream; the returned func closes it.
func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.streams[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.streams, ch)
		h.mu.Unlock()
	}
}

// Broadcast sends a message to every open stream. A stream too far behind
// misses it rather than holding the sender up.
func (h *Hub) Broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.streams {
		select {
		case ch <- msg:
		default:
		}
	}
}
