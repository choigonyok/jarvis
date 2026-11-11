// Package proposal is the heart of this assistant: something wants to change
// the world, and a person decides whether it may.
//
// It deliberately knows nothing about Claude Code. A card inside the CLI's
// permission callback could only exist while a tool call sat blocked waiting
// on it, and the assistant proposes things nobody asked for ("캠핑 일정
// 추가할까요?"), where there is no blocked call to hang a card on. So a
// proposal is a stored object with its own lifecycle, and the permission
// callback is just one of the things that can open one.
package proposal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
	// OriginSuggest is the assistant offering something nobody asked for,
	// because an event made it worth asking (internal/suggest). Approving runs
	// the action it carries, if any; rejecting is remembered so it is not
	// offered again soon.
	OriginSuggest = "suggest"
)

type Proposal struct {
	ID     string        `json:"id"`
	Origin string        `json:"origin"`
	Action action.Action `json:"action"`
	Card   action.Card   `json:"card"`
	// Confidence is 0 when the proposal came from a direct instruction:
	// there is nothing to be confident about when a person just asked.
	Confidence float64 `json:"confidence,omitempty"`
	// Quiet: the card is up, but it is not worth a buzz now (mind's gate
	// said so) - its notification goes with the evening digest.
	Quiet     bool   `json:"quiet,omitempty"`
	State     State  `json:"state"`
	Note      string `json:"note,omitempty"`
	At        string `json:"at"`
	DecidedAt string `json:"decidedAt,omitempty"`
	// ChangedAt is when the card last changed (RFC 3339): raised, edited,
	// redrawn, decided, settled or abandoned. A decided card stays live for
	// two weeks after it (archive.go), however long it waited before.
	ChangedAt string `json:"changedAt,omitempty"`
	// Reason is what the operator said when deciding - why a card was
	// turned down. It is what the next judgement about the same thing reads.
	Reason string `json:"reason,omitempty"`
	// Edits is every change the operator made to a form card before
	// deciding, oldest first: what was proposed and what they wanted instead.
	Edits []Edit `json:"edits,omitempty"`
	// Abandoned: nobody decided it - it was taken down (Abandon) because what
	// it asked stopped mattering: the job's turn came, a corrected card took
	// its place, the run behind it ended. Its state reads rejected for every
	// caller that waits on it; the console shows it as taken down, not as
	// the operator's no.
	Abandoned bool `json:"abandoned,omitempty"`
}

// Opened is when the card was raised, read off its id (Open stamps it there;
// At keeps only the time of day). Zero for an id of another shape.
func (p Proposal) Opened() time.Time {
	rest, ok := strings.CutPrefix(p.ID, "p-")
	if !ok {
		return time.Time{}
	}
	ms, _, _ := strings.Cut(rest, "-")
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(n)
}

// Changed is when the card last changed: ChangedAt, or when it was raised
// for a card that does not carry it.
func (p Proposal) Changed() time.Time {
	if t, err := time.Parse(time.RFC3339, p.ChangedAt); err == nil {
		return t
	}
	return p.Opened()
}

// changed stamps the card as changed at now.
func (p *Proposal) changed(now time.Time) { p.ChangedAt = now.Format(time.RFC3339) }

// Edit is one revision of a form card by the operator.
type Edit struct {
	At     string            `json:"at"`
	Fields map[string]string `json:"fields"`
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
// decided outlives the container it was decided in, and an archive beside it
// for the decided cards that have not changed in a while (archive.go).
type Store struct {
	mu sync.Mutex
	// order and byID are the live cards: every one still waiting, and those
	// that changed lately.
	order   []*Proposal
	byID    map[string]*Proposal
	waiters map[string]chan Decision
	seq     int
	bus     *bus.Bus
	// path is empty until Persist is called.
	path string
	log  *slog.Logger
	// archivePath is where decided cards go once old; archived and
	// archivedOrder hold them once read (nil until a lookup needs them), and
	// archiveBroken says the file could not be read. archivedAt is the last
	// pass that looked for cards to move there.
	archivePath   string
	archived      map[string]*Proposal
	archivedOrder []*Proposal
	archiveBroken bool
	archivedAt    time.Time
	// now is the clock: time.Now, but for a test.
	now func() time.Time
	// onDecide hears every decision after it is recorded. Nobody blocks on a
	// card raised in conversation; this is how its decision gets back to
	// whoever has to act on it.
	onDecide []func(Proposal, Decision)
	// onOpen hears every new card - how it reaches the phone (output-svc).
	onOpen  []func(Proposal)
	onClose []func(Proposal)
}

type file struct {
	Proposals []*Proposal `json:"proposals"`
	Seq       int         `json:"seq"`
}

// Persist points the store at a file and reads what is already there.
//
// A card raised in conversation stays pending across a restart: nothing is
// blocked on it, and its decision reaches the assistant through OnDecide
// whenever it comes. So does a payment card - the request it stopped was
// refused at once, and approving it only opens the host for a retry. Anything
// else still pending is settled as rejected on the way in.
//
// Decided cards that have not changed for long enough move to the archive
// beside the file on the way in, and once a day after (archive.go); the
// archive itself is not read until a card in it is asked for.
func (s *Store) Persist(path string, log *slog.Logger) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var saved file
	if err := jsonfile.Load(path, &saved); err != nil {
		return err
	}
	s.order = saved.Proposals
	s.seq = saved.Seq
	now := s.now()
	for _, p := range s.order {
		// A suggestion waits too: approving it runs its action directly, with
		// no turn that the restart could have cut short.
		if p.State == Pending && p.Origin != OriginChat && p.Origin != OriginNotice && p.Origin != OriginSuggest && p.Origin != OriginIntercept {
			p.State = Rejected
			p.Abandoned = true
			p.Note = "에이전트가 재시작되어 만료되었습니다."
			if p.DecidedAt == "" {
				p.DecidedAt = now.Format("15:04")
			}
			p.changed(now)
		}
		s.byID[p.ID] = p
	}
	s.path = path
	s.archivePath = archivePathFor(path)
	s.log = log
	if s.archiveLocked(now) {
		s.writeLocked()
	}
	return nil
}

// save must be called with the lock held. Logged rather than returned for the
// same reason as the transcript: the decision was already made. It says
// whether the live file was written.
func (s *Store) save() bool {
	if s.path == "" {
		return false
	}
	if now := s.now(); now.Sub(s.archivedAt) >= archiveEvery {
		s.archiveLocked(now)
	}
	return s.writeLocked()
}

// saveChange saves a change just made to p. A card brought back from the
// archive for it (unarchived) leaves the archive once the live file holding
// it is written - in that order, a write cut short leaves it in both files,
// and the live copy is the one read.
func (s *Store) saveChange(p *Proposal, unarchived bool) {
	if s.save() && unarchived {
		s.dropArchivedLocked(p.ID)
	}
}

// writeLocked writes the live file, and says whether it did.
func (s *Store) writeLocked() bool {
	if err := jsonfile.Save(s.path, file{Proposals: s.order, Seq: s.seq}); err != nil {
		if s.log != nil {
			s.log.Error("제안을 저장하지 못했습니다", "err", err)
		}
		return false
	}
	return true
}

func NewStore(b *bus.Bus) *Store {
	return &Store{
		byID:    map[string]*Proposal{},
		waiters: map[string]chan Decision{},
		bus:     b,
		now:     time.Now,
	}
}

// Open posts a pending proposal and returns it with the channel its decision
// will arrive on. A producer that must block (the CLI permission callback)
// reads the channel; one that must not (a background detector) ignores it.
func (s *Store) Open(p Proposal) (*Proposal, <-chan Decision) {
	s.mu.Lock()
	s.seq++
	now := s.now()
	p.ID = fmt.Sprintf("p-%d-%d", now.UnixMilli(), s.seq)
	p.State = Pending
	p.At = now.Format("15:04")
	p.changed(now)
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

// OnClose registers fn to hear a pending card closed without a decision -
// abandoned by the run behind it, or replaced by a corrected card. What
// hangs on the card (its notification) can then be put away.
func (s *Store) OnClose(fn func(Proposal)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onClose = append(s.onClose, fn)
}

// Decide records the operator's call and releases the waiter. Approved is a
// terminal state for a proposal the CLI then runs itself - the calendar's own
// live update is better evidence that it happened than a duplicated note.
// Settle exists for the producer that executes an action on its own.
func (s *Store) Decide(id string, d Decision) error { return s.DecideWhy(id, d, "") }

// DecideWhy decides with the operator's reason, kept on the proposal.
func (s *Store) DecideWhy(id string, d Decision, reason string) error {
	if d != Approve && d != Reject {
		return fmt.Errorf("invalid decision %q", d)
	}
	s.mu.Lock()
	p, unarchived, err := s.takeLocked(id, waiting)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	now := s.now()
	p.DecidedAt = now.Format("15:04")
	p.changed(now)
	if r := []rune(strings.TrimSpace(reason)); len(r) > 0 {
		if len(r) > 500 {
			r = r[:500]
		}
		p.Reason = string(r)
	}
	if d == Reject {
		p.State = Rejected
	} else {
		p.State = Approved
	}
	ch := s.waiters[id]
	delete(s.waiters, id)
	s.saveChange(p, unarchived)
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
	return s.ReviseWith(id, a, c, nil)
}

// ReviseWith revises a form card and records the operator's edits.
func (s *Store) ReviseWith(id string, a action.Action, c action.Card, edits map[string]string) error {
	s.mu.Lock()
	p, unarchived, err := s.takeLocked(id, waiting)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	now := s.now()
	p.Action = a
	p.Card = c
	if len(edits) > 0 {
		p.Edits = append(p.Edits, Edit{At: now.Format(time.RFC3339), Fields: edits})
	}
	p.changed(now)
	s.saveChange(p, unarchived)
	s.mu.Unlock()
	return nil
}

// Refresh puts on a waiting card what it asks now, for the screens to redraw
// where it is: unlike ReviseWith nobody edited it, and it is said at once.
// Asking what it asked already is no change to the card (ChangedAt).
func (s *Store) Refresh(id string, a action.Action, c action.Card, confidence float64) error {
	s.mu.Lock()
	p, unarchived, err := s.takeLocked(id, waiting)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if !p.asks(a, c, confidence) {
		p.changed(s.now())
	}
	p.Action, p.Card, p.Confidence = a, c, confidence
	s.saveChange(p, unarchived)
	snapshot := *p
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
	return nil
}

// OnDecide registers fn to hear every decision. Register before serving.
func (s *Store) OnDecide(fn func(Proposal, Decision)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDecide = append(s.onDecide, fn)
}

// FindPendingWhere returns the first pending card match accepts.
func (s *Store) FindPendingWhere(match func(Proposal) bool) (Proposal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.order {
		if p.State == Pending && match(*p) {
			return *p, true
		}
	}
	return Proposal{}, false
}

// PendingWhere is every pending card match accepts, oldest first.
func (s *Store) PendingWhere(match func(Proposal) bool) []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Proposal
	for _, p := range s.order {
		if p.State == Pending && match(*p) {
			out = append(out, *p)
		}
	}
	return out
}

// PendingChat is every card still waiting that the conversation raised.
func (s *Store) PendingChat() []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Proposal
	for _, p := range s.order {
		if p.State == Pending && p.Origin == OriginChat {
			out = append(out, *p)
		}
	}
	return out
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

// asks is whether the card already asks this: the same action, card and
// confidence.
func (p *Proposal) asks(a action.Action, c action.Card, confidence float64) bool {
	if a.Kind != p.Action.Kind || confidence != p.Confidence || !bytes.Equal(Canonical(a.Input), Canonical(p.Action.Input)) {
		return false
	}
	was, err := json.Marshal(p.Card)
	if err != nil {
		return false
	}
	now, err := json.Marshal(c)
	return err == nil && bytes.Equal(was, now)
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

// Settle records how the action ended. Called by whoever ran it, however
// long after the decision: a card archived meanwhile comes back among the
// live ones, so what acts on a decision always finds its card.
func (s *Store) Settle(id string, state State, note string) {
	s.mu.Lock()
	p, unarchived, err := s.takeLocked(id, nil)
	if err != nil {
		s.mu.Unlock()
		return
	}
	now := s.now()
	p.State = state
	p.Note = note
	// How it ended is said now: no longer a card nobody decided.
	p.Abandoned = false
	if p.DecidedAt == "" {
		p.DecidedAt = now.Format("15:04")
	}
	p.changed(now)
	s.saveChange(p, unarchived)
	snapshot := *p
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
}

// Abandon marks a pending proposal rejected when the run behind it died, or
// when a corrected card took its place, so the UI never shows a card nobody
// is listening to. Nobody decided it, so OnDecide is not told; OnClose is.
func (s *Store) Abandon(id, note string) {
	s.mu.Lock()
	p, unarchived, err := s.takeLocked(id, waiting)
	if err != nil {
		s.mu.Unlock()
		return
	}
	now := s.now()
	p.State = Rejected
	p.Abandoned = true
	p.Note = note
	p.DecidedAt = now.Format("15:04")
	p.changed(now)
	delete(s.waiters, id)
	s.saveChange(p, unarchived)
	snapshot := *p
	hooks := append([]func(Proposal){}, s.onClose...)
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "proposal", Proposal: snapshot})
	for _, fn := range hooks {
		fn(snapshot)
	}
}

// Get is the card of this id, live or archived: a turn of long ago still
// shows its card, and a job still hears what became of the one it waited on.
func (s *Store) Get(id string) (Proposal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _, ok := s.lookupLocked(id)
	if !ok {
		return Proposal{}, false
	}
	return *p, true
}

// List is the live cards, oldest first: every one still waiting, and those
// that changed lately. An archived card is reached by its id (Get, ListWith)
// or in the archive's own list (Archived).
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

// Waiting is every card still waiting for an answer, oldest first, a line
// each as the conversation is told of them: what the card says, who raised
// it, and when.
func (s *Store) Waiting(now time.Time) []string {
	var out []string
	for _, p := range s.PendingWhere(func(Proposal) bool { return true }) {
		out = append(out, p.line(now))
	}
	return out
}

// raisedBy is who raised a card, in the words the conversation is told.
var raisedBy = map[string]string{
	OriginChat:      "대화에서 올린 카드",
	OriginIntercept: "브라우저가 보내려던 요청을 결제 감시가 멈춘 것",
	OriginNotice:    "작업이 손으로 해 달라고 부탁한 것",
	OriginSuggest:   "비서가 먼저 묻는 것",
}

// waitingRunes bounds what one waiting card shows of its body.
const waitingRunes = 240

func (p Proposal) line(now time.Time) string {
	var parts []string
	for _, l := range strings.Split(p.Card.Body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	body := strings.Join(parts, " / ")
	if r := []rune(body); len(r) > waitingRunes {
		body = string(r[:waitingRunes]) + "…"
	}
	at := p.At
	if t := p.Opened(); !t.IsZero() {
		at = t.In(now.Location()).Format("15:04")
		if y, m, d := t.In(now.Location()).Date(); y != now.Year() || m != now.Month() || d != now.Day() {
			at = t.In(now.Location()).Format("1/2 15:04")
		}
	}
	by := raisedBy[p.Origin]
	if by == "" {
		by = p.Origin
	}
	line := "- " + p.Card.Title
	if body != "" {
		line += ": " + body
	}
	return line + " (" + by + ", " + at + ")"
}
