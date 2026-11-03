package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
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

// Busy is what keeps the operator busy now, "" when nothing does.
func (m *Mind) Busy() string {
	m.busy.mu.Lock()
	defer m.busy.mu.Unlock()
	if m.busy.why == "" || m.Opts.Now().After(m.busy.until) {
		return ""
	}
	return m.busy.why
}
