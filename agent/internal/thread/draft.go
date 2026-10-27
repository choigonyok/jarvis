package thread

import (
	"fmt"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// draftEvery is how often a message being written is sent on: often enough
// to read as typing, seldom enough that a long answer is not a frame per
// token through the tunnel.
const draftEvery = 50 * time.Millisecond

// Draft is one frame of a message being written. Text goes at At (UTF-16
// units, the browser's string length) of what the message says so far: At 0
// is the whole of it, so a client that joins - or missed a frame - is set
// right by one. Gone says the message will not become a turn.
type Draft struct {
	ID   string `json:"id"`
	At   int    `json:"at"`
	Text string `json:"text,omitempty"`
	Gone bool   `json:"gone,omitempty"`
}

// draft is the message being written, and how much of it has been sent.
type draft struct {
	id    string
	text  string
	sent  int // bytes of text sent
	units int // the same, in UTF-16 units
	at    time.Time
	timer *time.Timer
}

// drafts holds the one message being written. It lives in memory only: a
// restart is not in the middle of a message.
type drafts struct {
	mu  sync.Mutex
	cur *draft
}

var draftSeq struct {
	sync.Mutex
	n int64
}

// StartDraft begins a message the model is writing and names it. Nothing is
// sent until its first words.
func (s *Store) StartDraft() string {
	draftSeq.Lock()
	draftSeq.n++
	id := fmt.Sprintf("draft-%d-%d", time.Now().UnixMilli(), draftSeq.n)
	draftSeq.Unlock()

	s.drafts.mu.Lock()
	s.stopLocked()
	s.drafts.cur = &draft{id: id}
	s.drafts.mu.Unlock()
	return id
}

// WriteDraft adds words to the message being written. They go out at most
// every draftEvery; what waits goes out when that time is up.
func (s *Store) WriteDraft(id, text string) {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	d := s.drafts.cur
	if d == nil || d.id != id || text == "" {
		return
	}
	d.text += text
	wait := draftEvery - time.Since(d.at)
	if wait <= 0 {
		s.flushLocked(d)
		return
	}
	if d.timer == nil {
		d.timer = time.AfterFunc(wait, func() {
			s.drafts.mu.Lock()
			defer s.drafts.mu.Unlock()
			if s.drafts.cur == d {
				s.flushLocked(d)
			}
		})
	}
}

// flushLocked sends what of d has not been sent.
func (s *Store) flushLocked(d *draft) {
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	if d.sent == len(d.text) {
		return
	}
	text := d.text[d.sent:]
	s.bus.Publish(bus.Event{Type: "draft", Draft: Draft{ID: d.id, At: d.units, Text: text}})
	d.sent = len(d.text)
	d.units += utf16Len(text)
	d.at = time.Now()
}

// stopLocked forgets the message being written, its waiting words with it.
func (s *Store) stopLocked() *draft {
	d := s.drafts.cur
	if d != nil && d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	s.drafts.cur = nil
	return d
}

// SettleDraft stores the message that draft id was the writing of, and
// tells the clients the turn replaces it. A message with nothing to say
// leaves no turn: its draft is taken away.
func (s *Store) SettleDraft(id string, paragraphs []string) *Turn {
	s.drafts.mu.Lock()
	if d := s.drafts.cur; d != nil && d.id == id {
		// The last words, while the turn is being stored.
		s.flushLocked(d)
		s.stopLocked()
	}
	s.drafts.mu.Unlock()
	if len(paragraphs) == 0 {
		s.bus.Publish(bus.Event{Type: "draft", Draft: Draft{ID: id, Gone: true}})
		return nil
	}
	return s.appendAs(&Turn{Role: RoleAgent, Paragraphs: paragraphs}, id)
}

// DropDraft takes away a message that will not be finished: the turn
// failed or ended under it.
func (s *Store) DropDraft() {
	s.drafts.mu.Lock()
	d := s.stopLocked()
	s.drafts.mu.Unlock()
	if d != nil && d.text != "" {
		s.bus.Publish(bus.Event{Type: "draft", Draft: Draft{ID: d.id, Gone: true}})
	}
}

// CurrentDraft is the message being written as far as it has come, whole -
// what a client that has just connected is shown first. Nil when none is.
func (s *Store) CurrentDraft() *Draft {
	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()
	d := s.drafts.cur
	if d == nil || d.text == "" {
		return nil
	}
	return &Draft{ID: d.id, Text: d.text}
}

// utf16Len is how long s is as a browser counts: a character beyond the
// basic plane is two.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += max(utf16.RuneLen(r), 1)
	}
	return n
}
