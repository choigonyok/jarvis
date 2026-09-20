// Package bus fans one event out to every live subscriber. It is the seam the
// roadmap puts Redis behind later: producers only ever see Publish, and the
// transport only ever sees Subscribe.
//
// In-memory is the right first adapter. A single process with one operator
// gains nothing from a broker except another container to keep alive; what
// Redis would really buy - surviving a restart - is a property of the stores
// that persist, not of the fan-out.
package bus

import "sync"

// Event is one SSE frame. Slots are typed as any so the bus stays free of
// imports on the packages that fill them; exactly one slot is set per event,
// and the JSON shape is what the browser reads, so the names are contractual.
type Event struct {
	Type string `json:"type"` // turn | status | error | proposal | calendar

	Turn     any    `json:"turn,omitempty"`
	Proposal any    `json:"proposal,omitempty"`
	Calendar any    `json:"calendar,omitempty"`
	Thinking bool   `json:"thinking,omitempty"`
	Message  string `json:"message,omitempty"`
}

type Bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish never blocks on a slow subscriber: a client that cannot keep up
// drops frames and re-hydrates over HTTP when it reconnects. Every store
// behind this bus exposes a snapshot endpoint for exactly that reason.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
