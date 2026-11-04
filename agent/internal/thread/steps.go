package thread

import (
	"slices"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// Step is one thing the turn under way has done, said in a line
// (steps.Line). The console shows the newest as the one being done now and
// the ones before it as done.
type Step struct {
	Text string `json:"text"`
	At   string `json:"at"`
}

// maxSteps is how much of a stretch is kept: a long search is a long list,
// and only its end is read.
const maxSteps = 12

// Step adds a line to what the turn under way is doing and tells the
// clients the stretch as it now stands. A stretch is what was done since
// the last thing said in the conversation: words, or a card.
func (s *Store) Step(text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	s.steps = append(s.steps, Step{Text: text, At: time.Now().Format(time.RFC3339)})
	if len(s.steps) > maxSteps {
		s.steps = slices.Clone(s.steps[len(s.steps)-maxSteps:])
	}
	list := slices.Clone(s.steps)
	s.mu.Unlock()
	s.bus.Publish(bus.Event{Type: "steps", Steps: list})
}

// Steps is the stretch as it stands - what a client that has just connected
// is shown. Never nil: an empty list is news.
func (s *Store) Steps() []Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		return []Step{}
	}
	return slices.Clone(s.steps)
}

// endSteps closes the stretch: something was said, or the run started or
// ended. The clients are told only when there was one.
func (s *Store) endSteps() {
	s.mu.Lock()
	had := len(s.steps) > 0
	s.steps = nil
	s.mu.Unlock()
	if had {
		s.bus.Publish(bus.Event{Type: "steps", Steps: []Step{}})
	}
}
