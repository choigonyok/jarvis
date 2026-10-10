// Package store keeps the event log and the subscriptions (migration 033).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Notify is the wording a sender may attach so a notification can be made
// without knowing the event type. Optional.
type Notify struct {
	Tier  string `json:"tier"`
	Level string `json:"level,omitempty"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Event is one thing that happened, as delivered to consumers.
type Event struct {
	ID         int64           `json:"id"`
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject,omitempty"`
	Data       json.RawMessage `json:"data"`
	Notify     *Notify         `json:"notify,omitempty"`
	Key        string          `json:"key,omitempty"`
	OccurredAt time.Time       `json:"occurredAt"`
}

type Subscription struct {
	Consumer  string     `json:"consumer"`
	URL       string     `json:"url"`
	Types     []string   `json:"types"`
	Cursor    int64      `json:"cursor"`
	Failures  int        `json:"failures"`
	LastError string     `json:"lastError"`
	LastOKAt  *time.Time `json:"lastOkAt"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append stores an event. A key already stored is reported as a duplicate
// and changes nothing, so senders may repeat themselves freely.
func (s *Store) Append(ctx context.Context, e Event) (Event, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Event{}, false, err
	}
	defer tx.Rollback(ctx)
	saved, dup, err := appendTx(ctx, tx, e)
	if err != nil {
		return Event{}, false, err
	}
	return saved, dup, tx.Commit(ctx)
}

// appendLock serialises appends. Consumers read past a cursor by id, and
// ids are handed out before commit: two appends at once could commit 8
// after 9 was delivered, and 8 would be skipped for good. Under the lock
// they commit in id order. At this volume the wait is nothing.
const appendLock = 0x6576656e7473 // "events"

func appendTx(ctx context.Context, tx pgx.Tx, e Event) (Event, bool, error) {
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, appendLock); err != nil {
		return Event{}, false, err
	}
	var key *string
	if e.Key != "" {
		key = &e.Key
	}
	var notify []byte
	if e.Notify != nil {
		notify, _ = json.Marshal(e.Notify)
	}
	if len(e.Data) == 0 {
		e.Data = json.RawMessage("{}")
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now()
	}
	err := tx.QueryRow(ctx, `
		insert into events (source, type, subject, data, notify, dedupe_key, occurred_at)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (dedupe_key) do nothing
		returning id`,
		e.Source, e.Type, e.Subject, e.Data, notify, key, e.OccurredAt).Scan(&e.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, true, nil
	}
	return e, false, err
}

// Drain moves what senders left in the outbox (migration 034) into the log,
// oldest first, each row in the same transaction as its event - so a row is
// in the log exactly once, however a crash falls. parse turns a payload into
// an event, or says why it never will be one: such a row is dropped (and
// reported) rather than retried forever. It returns how many it moved.
func (s *Store) Drain(ctx context.Context, limit int, parse func([]byte) (Event, error), rejected func(id int64, err error)) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `select id, payload from event_outbox order by id limit $1 for update skip locked`, limit)
	if err != nil {
		return 0, err
	}
	type row struct {
		id      int64
		payload []byte
	}
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.payload); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	moved := 0
	for _, r := range batch {
		if e, err := parse(r.payload); err != nil {
			rejected(r.id, err)
		} else if _, dup, err := appendTx(ctx, tx, e); err != nil {
			return 0, err
		} else if !dup {
			moved++
		}
		if _, err := tx.Exec(ctx, `delete from event_outbox where id = $1`, r.id); err != nil {
			return 0, err
		}
	}
	return moved, tx.Commit(ctx)
}

// Prune deletes events older than keep that every consumer is already past.
// One a consumer has yet to take stays, however old.
func (s *Store) Prune(ctx context.Context, keep time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from events
		 where received_at < now() - make_interval(secs => $1)
		   and id <= (select coalesce(min(cursor), 0) from event_subscriptions)`, keep.Seconds())
	return tag.RowsAffected(), err
}

// After is the next events past cursor, oldest first.
func (s *Store) After(ctx context.Context, cursor int64, limit int) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `
		select id, source, type, subject, data, notify, coalesce(dedupe_key, ''), occurred_at
		  from events where id > $1 order by id limit $2`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var notify []byte
		if err := rows.Scan(&e.ID, &e.Source, &e.Type, &e.Subject, &e.Data, &notify, &e.Key, &e.OccurredAt); err != nil {
			return nil, err
		}
		if len(notify) > 0 {
			e.Notify = &Notify{}
			_ = json.Unmarshal(notify, e.Notify)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Latest(ctx context.Context) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `select coalesce(max(id), 0) from events`).Scan(&id)
	return id, err
}

// Subscribe registers (or updates) a consumer. A new consumer starts at the
// newest event unless fromStart: it was not around for what came before.
// An existing one keeps its place.
func (s *Store) Subscribe(ctx context.Context, consumer, url string, types []string, fromStart bool) (Subscription, error) {
	start := int64(0)
	if !fromStart {
		var err error
		if start, err = s.Latest(ctx); err != nil {
			return Subscription{}, err
		}
	}
	_, err := s.pool.Exec(ctx, `
		insert into event_subscriptions (consumer, url, types, cursor) values ($1, $2, $3, $4)
		on conflict (consumer) do update set url = excluded.url, types = excluded.types, updated_at = now()`,
		consumer, url, types, start)
	if err != nil {
		return Subscription{}, err
	}
	subs, err := s.Subscriptions(ctx)
	for _, sub := range subs {
		if sub.Consumer == consumer {
			return sub, err
		}
	}
	return Subscription{}, err
}

func (s *Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := s.pool.Query(ctx, `
		select consumer, url, types, cursor, failures, last_error, last_ok_at
		  from event_subscriptions order by consumer`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.Consumer, &sub.URL, &sub.Types, &sub.Cursor, &sub.Failures, &sub.LastError, &sub.LastOKAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Advance moves a consumer past an event: delivered, or not for it.
func (s *Store) Advance(ctx context.Context, consumer string, cursor int64, delivered bool) error {
	q := `update event_subscriptions set cursor = greatest(cursor, $2), failures = 0, last_error = '' where consumer = $1`
	if delivered {
		q = `update event_subscriptions set cursor = greatest(cursor, $2), failures = 0, last_error = '', last_ok_at = now() where consumer = $1`
	}
	_, err := s.pool.Exec(ctx, q, consumer, cursor)
	return err
}

func (s *Store) Failed(ctx context.Context, consumer, msg string) error {
	if len(msg) > 300 {
		msg = msg[:300]
	}
	_, err := s.pool.Exec(ctx, `update event_subscriptions set failures = failures + 1, last_error = $2 where consumer = $1`, consumer, msg)
	return err
}
