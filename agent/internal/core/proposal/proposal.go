// Package proposal is the heart of this assistant: something wants to change
// the world, and a person decides whether it may.
//
// It deliberately knows nothing about Claude Code. The approval card used to
// live inside the CLI's permission callback, which meant a card could only
// exist while a tool call sat blocked waiting on it - and the assistant this
// is growing into must be able to propose things nobody asked for ("캠핑
// 일정 추가할까요?"), where there is no blocked call to hang a card on. So a
// proposal is a stored object with its own lifecycle, and the permission
// callback became just one of the things that can open one.
package proposal

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

type State string

const (
	Pending  State = "pending"
	Approved State = "approved"
	Rejected State = "rejected"
	Executed State = "executed"
	Failed   State = "failed"
)

// Origin says who raised this. The operator sees it, because "you asked for
// this" and "I noticed something" deserve different trust.
const (
	OriginChat = "chat" // 운영자가 대화에서 직접 시킨 일
)

type Proposal struct {
	ID     string        `json:"id"`
	Origin string        `json:"origin"`
	Action action.Action `json:"action"`
	Card   action.Card   `json:"card"`
	// Confidence is 0 when the proposal came from a direct instruction:
	// there is nothing to be confident about when a person just asked.
	Confidence float64 `json:"confidence,omitempty"`
	State      State   `json:"state"`
	Note       string  `json:"note,omitempty"`
	At         string  `json:"at"`
	DecidedAt  string  `json:"decidedAt,omitempty"`
}

type Decision string

const (
	Approve Decision = "approved"
	Reject  Decision = "rejected"
)

var (
	ErrNotFound       = errors.New("no such proposal")
	ErrAlreadyDecided = errors.New("proposal already decided")
)

// Store keeps every proposal this process has raised, and releases whoever is
// waiting on a decision. One operator, one process: in memory is enough, and
// the browser re-hydrates from List on reconnect.
type Store struct {
	mu      sync.Mutex
	order   []*Proposal
	byID    map[string]*Proposal
	waiters map[string]chan Decision
	seq     int
	bus     *bus.Bus
}

func NewStore(b *bus.Bus) *Store {
	return &Store{
		byID:    map[string]*Proposal{},
		waiters: map[string]chan Decision{},
		bus:     b,
	}
}

// Open posts a pending proposal and returns it with the channel its decision
// will arrive on. A producer that must block (the CLI permission callback)
// reads the channel; one that must not (a background detector) ignores it.
func (s *Store) Open(p Proposal) (*Proposal, <-chan Decision) {
	s.mu.Lock()
	s.seq++
	p.ID = fmt.Sprintf("p-%d-%d", time.Now().UnixMilli(), s.seq)
	p.State = Pending
	p.At = time.Now().Format("15:04")
	if p.Origin == "" {
		p.Origin = OriginChat
	}
	stored := &p
	s.order = append(s.order, stored)
	s.byID[stored.ID] = stored

	// Buffered so a producer that walked away never blocks the deciding side.
	ch := make(chan Decision, 1)
	s.waiters[stored.ID] = ch
	snapshot := *stored
	s.mu.Unlock()

	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
	return stored, ch
}

// Decide records the operator's call and releases the waiter. Approved is a
// terminal state for a proposal the CLI then runs itself - the calendar's own
// live update is better evidence that it happened than a duplicated note.
// Settle exists for the producer that executes an action on its own.
func (s *Store) Decide(id string, d Decision) error {
	if d != Approve && d != Reject {
		return fmt.Errorf("invalid decision %q", d)
	}
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	if p.State != Pending {
		s.mu.Unlock()
		return ErrAlreadyDecided
	}
	p.DecidedAt = time.Now().Format("15:04")
	if d == Reject {
		p.State = Rejected
	} else {
		p.State = Approved
	}
	ch := s.waiters[id]
	delete(s.waiters, id)
	snapshot := *p
	s.mu.Unlock()

	if ch != nil {
		ch <- d
		close(ch)
	}
	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
	return nil
}

// Settle records how the action ended. Called by whoever ran it.
func (s *Store) Settle(id string, state State, note string) {
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	p.State = state
	p.Note = note
	if p.DecidedAt == "" {
		p.DecidedAt = time.Now().Format("15:04")
	}
	snapshot := *p
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
}

// Abandon marks a pending proposal rejected when the run behind it died, so
// the UI never shows a card nobody is listening to.
func (s *Store) Abandon(id, note string) {
	s.mu.Lock()
	p, ok := s.byID[id]
	if !ok || p.State != Pending {
		s.mu.Unlock()
		return
	}
	p.State = Rejected
	p.Note = note
	p.DecidedAt = time.Now().Format("15:04")
	delete(s.waiters, id)
	snapshot := *p
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
}

func (s *Store) Get(id string) (Proposal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	if !ok {
		return Proposal{}, false
	}
	return *p, true
}

func (s *Store) List() []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Proposal, 0, len(s.order))
	for _, p := range s.order {
		out = append(out, *p)
	}
	return out
}

func (s *Store) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.order {
		if p.State == Pending {
			n++
		}
	}
	return n
}
