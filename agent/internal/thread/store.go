// Package thread holds the single 1:1 conversation. It used to hold the
// approval cards too; those live in core/proposal now, because a proposal can
// outlive - and start outside - a conversation. What is left here is the
// transcript, plus the pointer from a turn to the proposal it raised.
package thread

import (
	"fmt"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

type Role string

const (
	RoleAgent Role = "agent"
	RoleUser  Role = "user"
)

type Turn struct {
	ID         string   `json:"id"`
	Role       Role     `json:"role"`
	At         string   `json:"at"`
	Paragraphs []string `json:"paragraphs,omitempty"`
	Text       string   `json:"text,omitempty"`
	// ProposalID points at core/proposal. The transcript records that a card
	// was raised here; what the card says, and what became of it, is the
	// proposal's business.
	ProposalID string `json:"proposalId,omitempty"`
}

type Store struct {
	mu       sync.Mutex
	turns    []*Turn
	byID     map[string]*Turn
	seq      int
	thinking bool
	bus      *bus.Bus
}

func NewStore(b *bus.Bus) *Store {
	return &Store{byID: map[string]*Turn{}, bus: b}
}

func (s *Store) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixMilli(), s.seq)
}

func clock() string { return time.Now().Format("15:04") }

// Snapshot returns the whole thread plus whether a run is in flight, for a
// client that has just connected and needs to catch up.
func (s *Store) Snapshot() ([]*Turn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Turn, len(s.turns))
	for i, t := range s.turns {
		c := *t
		out[i] = &c
	}
	return out, s.thinking
}

func (s *Store) AppendUser(text string) *Turn {
	return s.append(&Turn{Role: RoleUser, Text: text}, "u")
}

func (s *Store) AppendAgent(paragraphs []string) *Turn {
	if len(paragraphs) == 0 {
		return nil
	}
	return s.append(&Turn{Role: RoleAgent, Paragraphs: paragraphs}, "a")
}

// AttachProposal records in the transcript that a card was raised at this
// point in the conversation.
func (s *Store) AttachProposal(proposalID string) {
	s.append(&Turn{Role: RoleAgent, ProposalID: proposalID}, "a")
}

func (s *Store) append(t *Turn, prefix string) *Turn {
	s.mu.Lock()
	t.ID = s.nextID(prefix)
	t.At = clock()
	s.turns = append(s.turns, t)
	s.byID[t.ID] = t
	snapshot := *t
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "turn", Turn: snapshot})
	return t
}

func (s *Store) SetThinking(v bool) {
	s.mu.Lock()
	if s.thinking == v {
		s.mu.Unlock()
		return
	}
	s.thinking = v
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "status", Thinking: v})
}

func (s *Store) Fail(msg string) {
	s.bus.Publish(bus.Event{Type: "error", Message: msg})
}
