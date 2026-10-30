// Package queue is the stream the services read: tables in Postgres and this
// library, no process of its own.
//
// Everything that happens reaches input.outbox; input-svc alone takes it out,
// checks the envelope, and in one transaction stores the record and leaves one
// delivery row for each consumer that asked for its kind. What mind decides
// goes out the same way on a topic of its own (Effects). A consumer takes its
// rows with SKIP LOCKED, deletes each once handled, and keeps a cursor: the
// highest id below which nothing is waiting. One offset cannot say that the
// row after the one still running is done, which parallel workers need; a
// row per consumer can. LISTEN/NOTIFY only wakes - a poll is the spare.
//
// Delivery is at least once, so a handler is idempotent, as it was behind the
// HTTP deliveries this replaces. Order is kept per subject: a row waits while
// an earlier one with the same subject is unfinished, whatever other workers
// do. What a handler's error means is gocommon/health's rule, the same as
// everywhere else:
//
//   - ErrOutage: the other side is down. It costs the message no try, and the
//     worker waits and comes back to it.
//   - ErrRefused: it will never be taken. It is skipped and logged.
//   - anything else: it failed on its own. It goes again later, waiting
//     longer each time, and after health.MaxTries it is held: set aside with
//     the reason, the stream goes on, input-svc's /health counts it. It goes
//     again once when the consumer takes something after failing, when it
//     starts again, and when a person asks.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Topic is one stream: its messages and who is reading them.
type Topic struct {
	Name       string
	Messages   string
	Deliveries string
	Consumers  string
}

var (
	// Records is everything that happened that is not the guest's.
	Records = Topic{Name: "records", Messages: "input.records", Deliveries: "input.deliveries", Consumers: "input.consumers"}
	// Guest is the guest's own: separate tables, and a role that sees no others.
	Guest = Topic{Name: "guest", Messages: "input.guest_records", Deliveries: "input.guest_deliveries", Consumers: "input.guest_consumers"}
	// Effects is what mind decided should go out. It does not pass through
	// input: a record is what the world said, an effect what mind answered.
	Effects = Topic{Name: "effects", Messages: "mind.effects", Deliveries: "mind.effect_deliveries", Consumers: "mind.effect_consumers"}
	// GuestEffects is the same for the guest.
	GuestEffects = Topic{Name: "guest_effects", Messages: "mind.guest_effects", Deliveries: "mind.guest_effect_deliveries", Consumers: "mind.guest_effect_consumers"}
	// All is every stream, for what looks over them (input-svc's /health).
	All = []Topic{Records, Guest, Effects, GuestEffects}
)

// channel is what is notified when the topic has something new.
func (t Topic) channel() string { return "queue_" + t.Name }

// OutboxChannel is what an insert into input.outbox notifies (migration 078).
const OutboxChannel = "input_outbox"

// Message is one record as a consumer gets it.
type Message struct {
	ID            int64
	At            time.Time // when it was received
	Kind          string
	Source        string
	SourceKey     string
	Actor         string
	Channel       string
	Subject       string
	OccurredAt    time.Time
	Tier          string
	SchemaVersion int
	TraceID       string
	CausationID   string
	Payload       json.RawMessage
}

// Event is the message as the event log used to deliver it: data, and apart
// from it the wording a sender attached. The producers still write that shape
// and the consumers still read it, until each moves to its own records.
func (m Message) Event() ([]byte, error) {
	var fields map[string]json.RawMessage
	if len(m.Payload) > 0 {
		if err := json.Unmarshal(m.Payload, &fields); err != nil {
			return nil, fmt.Errorf("기록 %d 의 payload 를 읽지 못했습니다: %w", m.ID, err)
		}
	}
	notify := fields["notify"]
	delete(fields, "notify")
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if fields == nil {
		data = []byte("{}")
	}
	return json.Marshal(struct {
		ID         int64           `json:"id"`
		Source     string          `json:"source"`
		Type       string          `json:"type"`
		Subject    string          `json:"subject,omitempty"`
		Data       json.RawMessage `json:"data"`
		Notify     json.RawMessage `json:"notify,omitempty"`
		Key        string          `json:"key,omitempty"`
		OccurredAt time.Time       `json:"occurredAt"`
	}{m.ID, m.Source, m.Kind, m.Subject, data, notify, m.SourceKey, m.OccurredAt})
}

var (
	// ErrOutage says the other side is down, not the message at fault.
	ErrOutage = errors.New("상대가 받을 수 없습니다")
	// ErrRefused says the message will never be taken.
	ErrRefused = errors.New("받지 않는 메시지입니다")
)

// Matches reads a consumer's patterns: "*", an exact kind, or "x.*" for every
// kind in category x.
func Matches(patterns []string, kind string) bool {
	for _, p := range patterns {
		switch {
		case p == "*", p == kind:
			return true
		case strings.HasSuffix(p, ".*") && strings.HasPrefix(kind, strings.TrimSuffix(p, "*")):
			return true
		}
	}
	return false
}

// Sub is who wants what.
type Sub struct {
	Name  string
	Kinds []string
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Subs is who reads the topic.
func Subs(ctx context.Context, q querier, t Topic) ([]Sub, error) {
	rows, err := q.Query(ctx, `select name, kinds from `+t.Consumers+` order by name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Sub, error) {
		var s Sub
		err := r.Scan(&s.Name, &s.Kinds)
		return s, err
	})
}

// Deliver leaves a stored message for each consumer that wants its kind, in
// the transaction that stored it.
func Deliver(ctx context.Context, tx pgx.Tx, t Topic, subs []Sub, id int64, at time.Time, kind, subject string) error {
	for _, s := range subs {
		if !Matches(s.Kinds, kind) {
			continue
		}
		if _, err := tx.Exec(ctx, `insert into `+t.Deliveries+` (consumer, id, at, subject) values ($1, $2, $3, $4)
			on conflict do nothing`, s.Name, id, at, subject); err != nil {
			return err
		}
	}
	return nil
}

// Publish stores a message on a topic its writer owns, with its deliveries and
// its wake-up, in one transaction: how mind sends an effect. A key already
// stored is reported and changes nothing, so a retry is free.
func Publish(ctx context.Context, pool *pgxpool.Pool, t Topic, m Message) (int64, bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	if m.OccurredAt.IsZero() {
		m.OccurredAt = time.Now()
	}
	if len(m.Payload) == 0 {
		m.Payload = json.RawMessage("{}")
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = 1
	}
	var id int64
	var at time.Time
	err = tx.QueryRow(ctx, `insert into `+t.Messages+`
		(kind, source, source_key, actor, channel, subject, occurred_at, schema_version, trace_id, causation_id, payload)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
		on conflict (source, source_key) do nothing returning id, received_at`,
		m.Kind, m.Source, m.SourceKey, m.Actor, m.Channel, m.Subject, m.OccurredAt, m.SchemaVersion,
		m.TraceID, m.CausationID, string(m.Payload)).Scan(&id, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	subs, err := Subs(ctx, tx, t)
	if err != nil {
		return 0, false, err
	}
	if err := Deliver(ctx, tx, t, subs, id, at, m.Kind, m.Subject); err != nil {
		return 0, false, err
	}
	if err := Wake(ctx, tx, t); err != nil {
		return 0, false, err
	}
	return id, false, tx.Commit(ctx)
}

// Wake tells the topic's consumers there is something new, when the
// transaction commits.
func Wake(ctx context.Context, tx pgx.Tx, t Topic) error {
	_, err := tx.Exec(ctx, `select pg_notify($1, '')`, t.channel())
	return err
}

// State is how a consumer is getting on, for /health.
type State struct {
	Name         string
	Kinds        []string
	Cursor       int64
	Failures     int
	LastError    string
	LastOK       *time.Time
	FailingSince *time.Time
	// Oldest is when the oldest message it has not got past came in.
	Oldest *time.Time
	Held   int
}

// States is how every consumer of the topic is getting on.
func States(ctx context.Context, pool *pgxpool.Pool, t Topic) ([]State, error) {
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		select c.name, c.kinds, c.cursor, c.failures, c.last_error, c.last_ok_at, c.failing_since,
		       (select min(d.at) from %[1]s d where d.consumer = c.name and d.held_at is null),
		       (select count(*) from %[1]s d where d.consumer = c.name and d.held_at is not null)
		  from %[2]s c order by c.name`, t.Deliveries, t.Consumers))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (State, error) {
		var s State
		err := r.Scan(&s.Name, &s.Kinds, &s.Cursor, &s.Failures, &s.LastError, &s.LastOK, &s.FailingSince, &s.Oldest, &s.Held)
		return s, err
	})
}

// HeldItem is a message in the held box, with why it is there.
type HeldItem struct {
	Consumer  string     `json:"consumer"`
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Tries     int        `json:"tries"`
	LastError string     `json:"lastError"`
	HeldAt    time.Time  `json:"heldAt"`
	NextTry   *time.Time `json:"nextTry,omitempty"`
}

// Held is the topic's held box, newest first.
func Held(ctx context.Context, pool *pgxpool.Pool, t Topic, limit int) ([]HeldItem, error) {
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		select d.consumer, d.id, coalesce(m.kind, ''), d.tries, d.last_error, d.held_at, d.not_before
		  from %s d left join %s m on m.received_at = d.at and m.id = d.id
		 where d.held_at is not null order by d.held_at desc, d.id desc limit $1`, t.Deliveries, t.Messages), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (HeldItem, error) {
		var h HeldItem
		err := r.Scan(&h.Consumer, &h.ID, &h.Kind, &h.Tries, &h.LastError, &h.HeldAt, &h.NextTry)
		return h, err
	})
}

// Retry has one held message go again now. It reports whether there was one.
func Retry(ctx context.Context, pool *pgxpool.Pool, t Topic, consumer string, id int64) (bool, error) {
	tag, err := pool.Exec(ctx, `update `+t.Deliveries+` set not_before = now(), lease_until = null
		where consumer = $1 and id = $2 and held_at is not null`, consumer, id)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	_, err = pool.Exec(ctx, `select pg_notify($1, '')`, t.channel())
	return true, err
}

// Drop is a person giving up on a held message. It reports whether there was
// one.
func Drop(ctx context.Context, pool *pgxpool.Pool, t Topic, consumer string, id int64) (bool, error) {
	tag, err := pool.Exec(ctx, `delete from `+t.Deliveries+` where consumer = $1 and id = $2 and held_at is not null`, consumer, id)
	return tag.RowsAffected() > 0, err
}
