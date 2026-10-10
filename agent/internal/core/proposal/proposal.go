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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
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
	// OriginIntercept is a request the browser was about to send, stopped at
	// the network rather than at a tool call. Nobody asked for it in words -
	// it is where a page the agent is driving tried to spend money.
	OriginIntercept = "intercept"
	// OriginNotice is a background job asking the operator to do something
	// by hand (log in again) before it can go on. Approving says "done".
	// Nothing is held open on it, so it survives a restart like a chat card.
	OriginNotice = "notice"
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

// Store keeps every proposal raised here, and releases whoever is waiting on
// a decision. One operator, one process, so a mutex and a slice are the whole
// concurrency story; Persist adds a file behind them so the record of what was
// decided outlives the container it was decided in.
type Store struct {
	mu      sync.Mutex
	order   []*Proposal
	byID    map[string]*Proposal
	waiters map[string]chan Decision
	seq     int
	bus     *bus.Bus
	// path is empty until Persist is called.
	path string
	log  *slog.Logger
	// onDecide hears every decision after it is recorded. Nobody blocks on a
	// card raised in conversation any more; this is how its decision gets
	// back to whoever has to act on it.
	onDecide []func(Proposal, Decision)
	// onOpen hears every new card - how it reaches the phone (notify-svc).
	onOpen []func(Proposal)
}

type file struct {
	Proposals []*Proposal `json:"proposals"`
	Seq       int         `json:"seq"`
}

// Persist points the store at a file and reads what is already there.
//
// A card raised in conversation stays pending across a restart: nothing is
// blocked on it, and its decision reaches the assistant through OnDecide
// whenever it comes. Anything else still pending is settled as rejected on the
// way in - an intercepted payment is a held network request, and that request
// died with the process that held it.
func (s *Store) Persist(path string, log *slog.Logger) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var saved file
	if err := jsonfile.Load(path, &saved); err != nil {
		return err
	}
	s.order = saved.Proposals
	s.seq = saved.Seq
	for _, p := range s.order {
		if p.State == Pending && p.Origin != OriginChat && p.Origin != OriginNotice {
			p.State = Rejected
			p.Note = "에이전트가 재시작되어 만료되었습니다."
			if p.DecidedAt == "" {
				p.DecidedAt = time.Now().Format("15:04")
			}
		}
		s.byID[p.ID] = p
	}
	s.path = path
	s.log = log
	return nil
}

// save must be called with the lock held. Logged rather than returned for the
// same reason as the transcript: the decision was already made.
func (s *Store) save() {
	if s.path == "" {
		return
	}
	if err := jsonfile.Save(s.path, file{Proposals: s.order, Seq: s.seq}); err != nil && s.log != nil {
		s.log.Error("제안을 저장하지 못했습니다", "err", err)
	}
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
	s.save()
	snapshot := *stored
	hooks := append([]func(Proposal){}, s.onOpen...)
	s.mu.Unlock()

	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
	for _, fn := range hooks {
		fn(snapshot)
	}
	return stored, ch
}

// OnOpen registers fn to hear every proposal as it is raised. Register before
// serving; fn must not block - it runs on the producer's goroutine.
func (s *Store) OnOpen(fn func(Proposal)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onOpen = append(s.onOpen, fn)
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
	s.save()
	snapshot := *p
	listeners := s.onDecide
	s.mu.Unlock()

	if ch != nil {
		ch <- d
		close(ch)
	}
	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
	// Off the request: acting on a decision can take a while (running the
	// action, starting a turn), and the person who tapped the button should
	// see the card settle now.
	for _, fn := range listeners {
		go fn(snapshot, d)
	}
	return nil
}

// Revise swaps a pending proposal's action and card for an edited one, just
// before it is decided. Only the operator's own edits come through here.
func (s *Store) Revise(id string, a action.Action, c action.Card) error {
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
	p.Action = a
	p.Card = c
	s.save()
	s.mu.Unlock()
	return nil
}

// OnDecide registers fn to hear every decision. Register before serving.
func (s *Store) OnDecide(fn func(Proposal, Decision)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDecide = append(s.onDecide, fn)
}

// FindPending returns the card already waiting on the same action, if any.
// The model asking twice for the same thing is one question, not two cards.
func (s *Store) FindPending(a action.Action) (Proposal, bool) {
	want := Canonical(a.Input)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.order {
		if p.State == Pending && p.Action.Kind == a.Kind && bytes.Equal(Canonical(p.Action.Input), want) {
			return *p, true
		}
	}
	return Proposal{}, false
}

// Canonical re-encodes JSON with sorted keys and no insignificant whitespace,
// so two encodings of the same arguments compare equal.
func Canonical(raw json.RawMessage) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
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
	s.save()
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
	s.save()
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
