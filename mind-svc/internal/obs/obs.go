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
	"crypto/rand"
	"encoding/hex"
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
	"github.com/choigonyok/jarvis/gocommon/record"
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
	// Notify is wording for a person (output-svc reads it).
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

// IsEffect is whether an observation is something mind decided, which goes out
// on the effects topic, rather than something that happened, which goes in
// through the outbox. What the sensors read is the second, and so is what the
// gate did (action.*), which the loop reads back.
func IsEffect(o Observation) bool {
	return o.Source == mindSource && !strings.HasPrefix(o.Type, "action.") && o.Type != TimerFired
}

// TimerFired is mind's clock on the log: a timer whose moment came.
const TimerFired = "timer.fired"

// Command puts a request for mind on the stream, from whoever came to mind's
// door with it (the console, the screen's note): nothing changes but through
// the stream. It is a record like what the agent writes itself.
func (l *Stream) Command(ctx context.Context, key, kind, actor, method, path string, body json.RawMessage) error {
	if l.db == nil {
		return errors.New("기록을 남길 데이터베이스가 없습니다")
	}
	if len(body) == 0 {
		body = json.RawMessage("null")
	}
	raw, err := json.Marshal(map[string]any{
		"kind": kind, "source": mindSource, "source_key": key, "actor": actor, "channel": "console", "subject": path,
		"occurred_at": time.Now(), "tier": "raw", "schema_version": 1,
		"payload": map[string]any{"method": method, "path": path, "body": body},
	})
	if err != nil {
		return err
	}
	_, err = l.db.Exec(ctx, `insert into input.outbox (payload) values ($1)`, raw)
	return err
}

const mindSource = "mind"

// topicFor is who an effect is for: the guest's go to the guest's tables.
func topicFor(notify json.RawMessage) queue.Topic {
	var n struct {
		Audience string `json:"audience"`
	}
	if len(notify) > 0 && json.Unmarshal(notify, &n) == nil && n.Audience == "guest" {
		return queue.GuestEffects
	}
	return queue.Effects
}

// publish sends an effect. Its data and wording lie side by side in the
// payload, as they do in a record.
func (l *Stream) publish(ctx context.Context, o Observation) error {
	if l.db == nil {
		return errors.New("효과를 낼 데이터베이스가 없습니다")
	}
	fields := map[string]json.RawMessage{}
	if len(o.Data) > 0 {
		if err := json.Unmarshal(o.Data, &fields); err != nil {
			return fmt.Errorf("효과의 data 를 읽지 못했습니다: %w", err)
		}
	}
	if len(o.Notify) > 0 {
		fields["notify"] = o.Notify
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	key := o.Key
	if key == "" {
		key = fmt.Sprintf("%s:%d", o.Type, time.Now().UnixNano())
	}
	_, _, err = queue.Publish(ctx, l.db, topicFor(o.Notify), queue.Message{
		Kind: o.Type, Source: o.Source, SourceKey: key, Actor: mindSource, Subject: o.Subject, OccurredAt: o.OccurredAt, Payload: payload,
	})
	return err
}

// Wake tells an agent's scheduler a job may be due now: the operator's, or the
// guest's on the guest's topic.
func (l *Stream) Wake(ctx context.Context, guest bool) error {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	topic := queue.Effects
	if guest {
		topic = queue.GuestEffects
	}
	_, _, err := queue.Publish(ctx, l.db, topic, queue.Message{Kind: KindJobsWake, Source: mindSource,
		SourceKey: "wake-" + hex.EncodeToString(raw), Actor: mindSource, Subject: "jobs"})
	return err
}

// KindJobsWake says a job may be due: the scheduler looks now rather than on
// its own long clock.
const KindJobsWake = "jobs.wake"

// KindListChanged says how a list stands has changed: the screens showing it
// redraw it, on every device.
const KindListChanged = "checklist.changed"

// ListChanged tells a person's agent a list changed: the operator's, or the
// guest's on the guest's topic.
func (l *Stream) ListChanged(ctx context.Context, guest bool, id string) error {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"checklist": id})
	if err != nil {
		return err
	}
	topic := queue.Effects
	if guest {
		topic = queue.GuestEffects
	}
	_, _, err = queue.Publish(ctx, l.db, topic, queue.Message{Kind: KindListChanged, Source: mindSource,
		SourceKey: "list-" + hex.EncodeToString(raw), Actor: mindSource, Subject: id, Payload: payload})
	return err
}

// KindCalendarChanged says what an entry became: the screens showing it redraw.
const KindCalendarChanged = "calendar.changed"

// CalendarChanged tells a person's agent what a screen did to an entry.
func (l *Stream) CalendarChanged(ctx context.Context, guest bool, op string, event json.RawMessage) error {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"op": op, "event": event})
	if err != nil {
		return err
	}
	topic := queue.Effects
	if guest {
		topic = queue.GuestEffects
	}
	_, _, err = queue.Publish(ctx, l.db, topic, queue.Message{Kind: KindCalendarChanged, Source: mindSource,
		SourceKey: "cal-" + hex.EncodeToString(raw), Actor: mindSource, Subject: "calendar", Payload: payload})
	return err
}

// Effect sends something decided that is not an observation: a write for the
// hand to carry out.
func (l *Stream) Effect(ctx context.Context, m queue.Message) error {
	_, _, err := queue.Publish(ctx, l.db, queue.Effects, m)
	return err
}

// relayed are the kinds without wording that output still has to hear of:
// what took a notification's place, what answered it, and being busy.
var relayed = map[string]bool{"suggestion.raised": true, "proposal.decided": true, "context.state": true}

// direct is what output reads from the records itself, so it does not need
// mind to be up: that mind is down cannot be said through mind, and a payment
// waiting on a card does not wait for a redeploy.
func direct(kind string) bool {
	return strings.HasPrefix(kind, "status.") || strings.HasPrefix(kind, "approval.")
}

// Relay passes on what another service wants said. The wording is still that
// service's and goes out as it is; deciding what is worth saying moves here
// with the rules (the registry). It reads the stream on its own consumer, so
// a loop busy with a message holds up no push.
func (l *Stream) Relay(topic queue.Topic) func(context.Context, queue.Message) error {
	return func(ctx context.Context, m queue.Message) error {
		if topic == queue.Effects && direct(m.Kind) {
			return nil
		}
		var p struct {
			Notify json.RawMessage `json:"notify"`
		}
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			return fmt.Errorf("%w: %v", queue.ErrRefused, err)
		}
		if len(p.Notify) == 0 && !relayed[m.Kind] {
			return nil
		}
		m.CausationID = m.SourceKey
		_, _, err := queue.Publish(ctx, l.db, topic, m)
		return err
	}
}

// PruneEffects forgets what went out long ago and everyone has taken. An
// effect is on its way somewhere: what happened is in the records.
func (l *Stream) PruneEffects(ctx context.Context, keep time.Duration) (int64, error) {
	var n int64
	for _, t := range []queue.Topic{queue.Effects, queue.GuestEffects} {
		tag, err := l.db.Exec(ctx, `delete from `+t.Messages+` m where m.received_at < $1
			and not exists (select 1 from `+t.Deliveries+` d where d.id = m.id)`, time.Now().Add(-keep))
		if err != nil {
			return n, err
		}
		n += tag.RowsAffected()
	}
	return n, nil
}

func (l *Stream) Append(ctx context.Context, o Observation) error {
	if IsEffect(o) {
		return l.publish(ctx, o)
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	body, err := record.FromEvent(raw, time.Now())
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
