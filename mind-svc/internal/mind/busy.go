package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/rhythm"
)

// The operator's state as context-svc says it (context.state): driving, in a
// meeting, in a focus. Output holds pushes through it; mind only passed it on,
// and its own judgement of what is worth a buzz now did not hear it (gate.Busy).

// KindState is context-svc's word on the operator's state.
const KindState = "context.state"

// busyLapse is how long a state is believed without a word that it ended (as
// output-svc believes it).
const busyLapse = 4 * time.Hour

type busy struct {
	mu    sync.Mutex
	why   string
	until time.Time
}

// StateSaid hears context.state.
func (m *Mind) StateSaid(_ context.Context, msg queue.Message) error {
	var p struct {
		State struct {
			Driving bool `json:"driving"`
			Meeting bool `json:"meeting"`
			Focus   bool `json:"focus"`
		} `json:"state"`
	}
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrRefused, err)
	}
	why := ""
	switch {
	case p.State.Driving:
		why = "운전 중"
	case p.State.Meeting:
		why = "회의 중"
	case p.State.Focus:
		why = "집중 모드"
	}
	m.busy.mu.Lock()
	m.busy.why, m.busy.until = why, m.Opts.Now().Add(busyLapse)
	m.busy.mu.Unlock()
	return nil
}

// BusyOrSlow is what the gate reads before a buzz: what keeps the operator
// busy now by context-svc's word, or else an hour they usually answer late at
// (rhythm) - a buzz then costs as one while driving does.
func (m *Mind) BusyOrSlow() string {
	if why := m.Busy(); why != "" {
		return why
	}
	if slow, why := m.Rhythm(context.Background()).Slow(m.Opts.Now()); slow {
		return why
	}
	return ""
}

// rhythmCache keeps the profile for a while: it moves by the day, and the gate
// reads it before every card.
type rhythmCache struct {
	mu sync.Mutex
	at time.Time
	p  rhythm.Profile
}

const rhythmFresh = time.Hour

// Rhythm is when the operator answers, from the cards of the last two months.
// One that could not be read knows nothing, and says nothing slow.
func (m *Mind) Rhythm(ctx context.Context) rhythm.Profile {
	c := &m.rhythm
	c.mu.Lock()
	defer c.mu.Unlock()
	now := m.Opts.Now()
	if !c.at.IsZero() && now.Sub(c.at) < rhythmFresh && now.After(c.at) {
		return c.p
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ins, err := m.Opts.Store.IntentsSince(ctx, now.Add(-rhythm.Window))
	if err != nil {
		m.Opts.Logger.Warn("카드에 답한 때를 읽지 못했습니다", "err", err)
		return rhythm.Profile{}
	}
	c.p, c.at = rhythm.Build(ins, m.Opts.Loc, now), now
	return c.p
}

// Busy is what keeps the operator busy now, "" when nothing does.
func (m *Mind) Busy() string {
	m.busy.mu.Lock()
	defer m.busy.mu.Unlock()
	if m.busy.why == "" || m.Opts.Now().After(m.busy.until) {
		return ""
	}
	return m.busy.why
}
