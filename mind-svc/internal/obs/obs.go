// Package obs is the observation log: everything that happened, in order,
// kept so it can be read again. Sensors append; the loop is fed from it -
// never from a sensor directly - so a day can be replayed through a changed
// interpreter and the two results compared.
//
// In the stack the log is the record stream: an append goes through
// input.outbox, and the loop reads it with gocommon/queue. On a laptop it is
// MemoryLog, which keeps the same contract inside the process.
package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
)

// Observation is one thing that happened, as the event log delivered it.
type Observation struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject,omitempty"`
	Data       json.RawMessage `json:"data"`
	Key        string          `json:"key,omitempty"`
	OccurredAt time.Time       `json:"occurredAt"`
	// Notify is wording for a person (notify-svc reads it).
	Notify json.RawMessage `json:"notify,omitempty"`
}

// Types the loop reads.
const (
	MessageObserved = "message.observed" // a chat message; text in the content store
	ActionExecuted  = "action.executed"  // the gate did something
	ActionUndone    = "action.undone"    // and took it back
	// OccurrenceObserved is something that happened, read from a structured
	// source (a payment, a photo, a workout); the occurrence is in the store.
	OccurrenceObserved = "occurrence.observed"
)

// OccurrenceRef is an occurrence.observed's data.
type OccurrenceRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

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
// order, as the stream does.
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

// Stream is the record stream. Appends go to input.outbox when there is a
// database - late rather than lost if input-svc is restarting - and are posted
// to input-svc otherwise. What the loop reads comes through gocommon/queue;
// Read here is the record tables, for a replay.
type Stream struct {
	url   string
	token string
	db    *pgxpool.Pool
	http  *http.Client
	// kinds is what Read reads: job progress and the guard's questions are
	// in the same tables, and are not the loop's to replay.
	kinds []string
}

// NewStream is the stream at input-svc's url and the database, read for kinds.
func NewStream(url, token string, db *pgxpool.Pool, kinds []string) *Stream {
	return &Stream{url: strings.TrimRight(url, "/"), token: token, db: db, http: &http.Client{Timeout: 10 * time.Second}, kinds: kinds}
}

func (l *Stream) Append(ctx context.Context, o Observation) error {
	body, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if l.db != nil {
		if _, err := l.db.Exec(ctx, `insert into input.outbox (payload) values ($1)`, body); err == nil {
			return nil
		}
	}
	return l.post(ctx, body)
}

// Read is the records the loop reads past an id, oldest first.
func (l *Stream) Read(ctx context.Context, after int64, limit int) ([]Observation, error) {
	if l.db == nil {
		return nil, errors.New("기록을 읽을 데이터베이스가 없습니다")
	}
	rows, err := l.db.Query(ctx, `
		select id, received_at, kind, source, source_key, subject, occurred_at, payload from (
		  select * from input.records where id > $1 and kind like any($3)
		  union all
		  select * from input.guest_records where id > $1 and kind like any($3)
		) r order by id limit $2`, after, limit, likes(l.kinds))
	if err != nil {
		return nil, err
	}
	msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (queue.Message, error) {
		var m queue.Message
		err := r.Scan(&m.ID, &m.At, &m.Kind, &m.Source, &m.SourceKey, &m.Subject, &m.OccurredAt, &m.Payload)
		return m, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Observation, 0, len(msgs))
	for _, m := range msgs {
		o, err := FromMessage(m)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// likes turns stream patterns into LIKE patterns.
func likes(patterns []string) []string {
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = strings.ReplaceAll(p, "*", "%")
	}
	return out
}

// FromMessage is a message of the stream as an observation.
func FromMessage(m queue.Message) (Observation, error) {
	raw, err := m.Event()
	if err != nil {
		return Observation{}, err
	}
	var o Observation
	err = json.Unmarshal(raw, &o)
	return o, err
}

func (l *Stream) post(ctx context.Context, body []byte) error {
	if l.url == "" {
		return errors.New("input-svc 주소가 없습니다")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url+"/records", bytes.NewReader(body))
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
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("input-svc %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
