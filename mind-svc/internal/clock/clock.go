// Package clock is the loop's time. Real time in the stack; a clock that can
// be moved on a laptop (to see tomorrow's reminders today) and pinned in
// evaluations (so "내일" means the same day every run).
package clock

import (
	"sync"
	"time"
)

type Clock struct {
	mu     sync.Mutex
	offset time.Duration
	pinned *time.Time
}

func New() *Clock { return &Clock{} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pinned != nil {
		return *c.pinned
	}
	return time.Now().Add(c.offset)
}

// Advance moves the clock forward and says what time it is now.
func (c *Clock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pinned != nil {
		t := c.pinned.Add(d)
		c.pinned = &t
		return t
	}
	c.offset += d
	return time.Now().Add(c.offset)
}

// Pin stops the clock at t.
func (c *Clock) Pin(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinned = &t
}
