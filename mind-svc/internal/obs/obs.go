// Package obs is the observation log: everything that happened, in order,
// kept so it can be read again. Sensors append; the loop is fed from it -
// never from a sensor directly - so a day can be replayed through a changed
// interpreter and the two results compared.
//
// In the stack the log is events-svc (an append goes through the outbox, and
// events-svc delivers to POST /consume). On a laptop it is MemoryLog, which
// keeps the same contract inside the process.
package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Observation is one thing that happened. Its shape is events-svc's event.
type Observation struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject,omitempty"`
	Data       json.RawMessage `json:"data"`
	Key        string          `json:"key,omitempty"`
	OccurredAt time.Time       `json:"occurredAt"`
	// Notify is wording for a person (events-svc hands it to notify-svc).
	Notify json.RawMessage `json:"notify,omitempty"`
}

// Types the loop reads.
const (
	MessageObserved = "message.observed" // a chat message; text in the content store
	ActionExecuted  = "action.executed"  // the gate did something
	ActionUndone    = "action.undone"    // and took it back
)

// MessageRef is a message.observed's data.
type MessageRef struct {
	Ref  string `json:"ref"`
	Room string `json:"room"`
}

// ActionData is an action.executed / action.undone's data.
type ActionData struct {
	IntentID string          `json:"intentId"`
	Kind     string          `json:"kind"`
	EntityID string          `json:"entityId"`
	Result   json.RawMessage `json:"result,omitempty"`
}

type Log interface {
	Append(ctx context.Context, o Observation) error
	// Read is the log past an id, oldest first - for replay.
	Read(ctx context.Context, after int64, limit int) ([]Observation, error)
}

// Handler is the loop's entry point.
type Handler func(ctx context.Context, o Observation) error

// MemoryLog delivers each observation to the handler on one goroutine, in
// order, as events-svc would.
type MemoryLog struct {
	mu      sync.Mutex
	all     []Observation
	keys    map[string]bool
	queue   chan Observation
	handler Handler
	log     *slog.Logger
	idle    sync.WaitGroup
}

func NewMemoryLog(log *slog.Logger) *MemoryLog {
	return &MemoryLog{keys: map[string]bool{}, queue: make(chan Observation, 1024), log: log}
}

// Run delivers until ctx ends.
func (l *MemoryLog) Run(ctx context.Context, h Handler) {
	l.handler = h
	for {
		select {
		case <-ctx.Done():
			return
		case o := <-l.queue:
			if err := h(ctx, o); err != nil {
				l.log.Warn("관측을 처리하지 못했습니다", "type", o.Type, "id", o.ID, "err", err)
			}
			l.idle.Done()
		}
	}
}

func (l *MemoryLog) Append(_ context.Context, o Observation) error {
	l.mu.Lock()
	if o.Key != "" && l.keys[o.Key] {
		l.mu.Unlock()
		return nil
	}
	if o.Key != "" {
		l.keys[o.Key] = true
	}
	o.ID = int64(len(l.all) + 1)
	if o.OccurredAt.IsZero() {
		o.OccurredAt = time.Now()
	}
	l.all = append(l.all, o)
	l.idle.Add(1)
	l.mu.Unlock()
	l.queue <- o
	return nil
}

// Drain waits until everything appended so far has been handled.
func (l *MemoryLog) Drain() { l.idle.Wait() }

func (l *MemoryLog) Read(_ context.Context, after int64, limit int) ([]Observation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Observation{}
	for _, o := range l.all {
		if o.ID > after && len(out) < limit {
			out = append(out, o)
		}
	}
	return out, nil
}

// EventsLog is events-svc. Appends go to the outbox table when there is a
// database - late rather than lost if events-svc is restarting - and are
// posted directly otherwise.
type EventsLog struct {
	url   string
	token string
	db    *pgxpool.Pool
	http  *http.Client
}

func NewEventsLog(url, token string, db *pgxpool.Pool) *EventsLog {
	return &EventsLog{url: strings.TrimRight(url, "/"), token: token, db: db, http: &http.Client{Timeout: 10 * time.Second}}
}

func (l *EventsLog) Append(ctx context.Context, o Observation) error {
	body, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if l.db != nil {
		if _, err := l.db.Exec(ctx, `insert into event_outbox (payload) values ($1)`, body); err == nil {
			return nil
		}
	}
	return l.do(ctx, http.MethodPost, "/events", body, nil)
}

func (l *EventsLog) Read(ctx context.Context, after int64, limit int) ([]Observation, error) {
	var out struct {
		Events []Observation `json:"events"`
	}
	err := l.do(ctx, http.MethodGet, fmt.Sprintf("/events?after=%d&limit=%d", after, limit), nil, &out)
	return out.Events, err
}

// Subscribe registers this service with events-svc, retrying until it
// answers: deliveries of types come to self.
func (l *EventsLog) Subscribe(ctx context.Context, consumer, self string, types []string, log *slog.Logger) {
	body, _ := json.Marshal(map[string]any{"url": self, "types": types})
	for wait := time.Second; ; wait = min(wait*2, time.Minute) {
		if err := l.do(ctx, http.MethodPut, "/subscriptions/"+consumer, body, nil); err == nil {
			log.Info("events-svc 에 구독했습니다", "types", types)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (l *EventsLog) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, l.url+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.token)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("events-svc %s %s: %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
