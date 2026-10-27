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
	// Audience is who the notification is for (notify-svc): "" is the
	// operator, "guest" the guest account.
	Audience string `json:"audience,omitempty"`
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
	Consumer string   `json:"consumer"`
	URL      string   `json:"url"`
	Types    []string `json:"types"`
	// Guest is the guest's side (migration 071): registered with the guest's
	// token, it is sent only what is for the guest, with the guest's token.
	Guest     bool       `json:"guest,omitempty"`
	Cursor    int64      `json:"cursor"`
	Failures  int        `json:"failures"`
	LastError string     `json:"lastError"`
	LastOKAt  *time.Time `json:"lastOkAt"`
	// FailingSince is when the failures in a row began; nil while it takes
	// what it is sent.
	FailingSince *time.Time `json:"failingSince"`
	// HeadTries is how often the event it is stuck on failed on its own.
	HeadTries int `json:"headTries,omitempty"`
	// Held is how many of its events are in the held box (event_held).
	Held int `json:"held"`
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

// Prune deletes events older than keep that every live consumer is already
// past. One a consumer has yet to take stays, however old - unless that
// consumer has taken nothing for longer than keep: a stopped consumer must
// not hold the log forever. It is unsubscribed, not waited for. A held event
// stays until it is delivered or dropped.
func (s *Store) Prune(ctx context.Context, keep time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from events
		 where received_at < now() - make_interval(secs => $1)
		   and id <= (select coalesce(min(cursor), (select coalesce(max(id), 0) from events))
		                from event_subscriptions
		               where coalesce(last_ok_at, updated_at) >= now() - make_interval(secs => $1))
		   and not exists (select 1 from event_held h where h.event_id = events.id)`, keep.Seconds())
	return tag.RowsAffected(), err
}

// Unsubscribe removes a consumer. It reports whether there was one.
func (s *Store) Unsubscribe(ctx context.Context, consumer string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `delete from event_subscriptions where consumer = $1`, consumer)
	return tag.RowsAffected() > 0, err
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

// ErrOtherSide is registering a consumer that is the other side's: the
// guest's token at an operator's subscription, or the other way round.
var ErrOtherSide = errors.New("다른 쪽의 구독입니다")

// Subscribe registers (or updates) a consumer. A new consumer starts at the
// newest event unless fromStart: it was not around for what came before.
// An existing one keeps its place - and, back from a restart, gets its held
// events once more: a consumer is restarted when it was fixed. guest is the
// side registering it, and an existing consumer stays on its own side
// (ErrOtherSide).
func (s *Store) Subscribe(ctx context.Context, consumer, url string, types []string, fromStart, guest bool) (Subscription, error) {
	start := int64(0)
	if !fromStart {
		var err error
		if start, err = s.Latest(ctx); err != nil {
			return Subscription{}, err
		}
	}
	tag, err := s.pool.Exec(ctx, `
		insert into event_subscriptions (consumer, url, types, cursor, guest) values ($1, $2, $3, $4, $5)
		on conflict (consumer) do update set url = excluded.url, types = excluded.types, updated_at = now()
		 where event_subscriptions.guest = excluded.guest`,
		consumer, url, types, start, guest)
	if err != nil {
		return Subscription{}, err
	}
	if tag.RowsAffected() == 0 {
		return Subscription{}, ErrOtherSide
	}
	if err := s.ReleaseHeld(ctx, consumer); err != nil {
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
		select consumer, url, types, guest, cursor, failures, last_error, last_ok_at, failing_since, head_tries,
		       (select count(*) from event_held h where h.consumer = s.consumer)
		  from event_subscriptions s order by consumer`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.Consumer, &sub.URL, &sub.Types, &sub.Guest, &sub.Cursor, &sub.Failures, &sub.LastError, &sub.LastOKAt,
			&sub.FailingSince, &sub.HeadTries, &sub.Held); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// cleared is what a subscription's failure columns are once it got past an
// event.
const cleared = `failures = 0, last_error = '', failing_since = null, head_event = null, head_tries = 0`

// Advance moves a consumer past an event: delivered, or not for it.
func (s *Store) Advance(ctx context.Context, consumer string, cursor int64, delivered bool) error {
	q := `update event_subscriptions set cursor = greatest(cursor, $2), ` + cleared + ` where consumer = $1`
	if delivered {
		q = `update event_subscriptions set cursor = greatest(cursor, $2), ` + cleared + `, last_ok_at = now() where consumer = $1`
	}
	_, err := s.pool.Exec(ctx, q, consumer, cursor)
	return err
}

// Failed records an outage: the consumer could not be reached, or refused
// the token. It costs the event it was stuck on no try.
func (s *Store) Failed(ctx context.Context, consumer, msg string) error {
	_, err := s.pool.Exec(ctx, `update event_subscriptions set failures = failures + 1, last_error = $2,
		failing_since = coalesce(failing_since, now()) where consumer = $1`, consumer, clip(msg))
	return err
}

// ItemFailed records that the consumer, reachable, could not take this event,
// and returns how often it has not in a row.
func (s *Store) ItemFailed(ctx context.Context, consumer string, event int64, msg string) (int, error) {
	var tries int
	err := s.pool.QueryRow(ctx, `update event_subscriptions set failures = failures + 1, last_error = $3,
		failing_since = coalesce(failing_since, now()),
		head_tries = case when head_event = $2 then head_tries + 1 else 1 end, head_event = $2
		where consumer = $1 returning head_tries`, consumer, event, clip(msg)).Scan(&tries)
	return tries, err
}

// Hold sets an event the consumer keeps failing on aside, and moves the
// consumer past it - the rest of its events are not held up by one.
func (s *Store) Hold(ctx context.Context, consumer string, event int64, tries int, msg string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `insert into event_held (consumer, event_id, tries, last_error) values ($1, $2, $3, $4)
		on conflict (consumer, event_id) do update set tries = excluded.tries, last_error = excluded.last_error,
		  held_at = now(), next_try = null`, consumer, event, tries, clip(msg)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update event_subscriptions set cursor = greatest(cursor, $2), `+cleared+` where consumer = $1`,
		consumer, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// HeldEvent is an event in the held box, with why it is there.
type HeldEvent struct {
	Consumer  string     `json:"consumer"`
	Event     Event      `json:"event"`
	Tries     int        `json:"tries"`
	LastError string     `json:"lastError"`
	HeldAt    time.Time  `json:"heldAt"`
	NextTry   *time.Time `json:"nextTry,omitempty"`
}

func (s *Store) held(ctx context.Context, where string, args ...any) ([]HeldEvent, error) {
	rows, err := s.pool.Query(ctx, `
		select h.consumer, h.tries, h.last_error, h.held_at, h.next_try,
		       e.id, e.source, e.type, e.subject, e.data, e.notify, coalesce(e.dedupe_key, ''), e.occurred_at
		  from event_held h join events e on e.id = h.event_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeldEvent{}
	for rows.Next() {
		var h HeldEvent
		var notify []byte
		if err := rows.Scan(&h.Consumer, &h.Tries, &h.LastError, &h.HeldAt, &h.NextTry,
			&h.Event.ID, &h.Event.Source, &h.Event.Type, &h.Event.Subject, &h.Event.Data, &notify, &h.Event.Key, &h.Event.OccurredAt); err != nil {
			return nil, err
		}
		if len(notify) > 0 {
			h.Event.Notify = &Notify{}
			_ = json.Unmarshal(notify, h.Event.Notify)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Held is the held box, newest first.
func (s *Store) Held(ctx context.Context, limit int) ([]HeldEvent, error) {
	return s.held(ctx, `order by h.held_at desc, h.event_id desc limit $1`, limit)
}

// DueHeld is a consumer's held events whose time to go again has come,
// oldest first.
func (s *Store) DueHeld(ctx context.Context, consumer string, limit int) ([]HeldEvent, error) {
	return s.held(ctx, `where h.consumer = $1 and h.next_try <= now() order by h.event_id limit $2`, consumer, limit)
}

// ReleaseHeld has a consumer's held events go again on its next pass.
func (s *Store) ReleaseHeld(ctx context.Context, consumer string) error {
	_, err := s.pool.Exec(ctx, `update event_held set next_try = now() where consumer = $1 and next_try is null`, consumer)
	return err
}

// RetryHeld has one held event go again now. It reports whether there was one.
func (s *Store) RetryHeld(ctx context.Context, consumer string, event int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `update event_held set next_try = now() where consumer = $1 and event_id = $2`, consumer, event)
	return tag.RowsAffected() > 0, err
}

// HeldDelivered takes a held event out of the box: it went through.
func (s *Store) HeldDelivered(ctx context.Context, consumer string, event int64) error {
	_, err := s.pool.Exec(ctx, `delete from event_held where consumer = $1 and event_id = $2`, consumer, event)
	return err
}

// HeldFailed keeps a held event in the box, waiting for the next occasion.
func (s *Store) HeldFailed(ctx context.Context, consumer string, event int64, msg string) error {
	_, err := s.pool.Exec(ctx, `update event_held set tries = tries + 1, last_error = $3, next_try = null
		where consumer = $1 and event_id = $2`, consumer, event, clip(msg))
	return err
}

// DropHeld is a person giving up on a held event. It reports whether there
// was one.
func (s *Store) DropHeld(ctx context.Context, consumer string, event int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `delete from event_held where consumer = $1 and event_id = $2`, consumer, event)
	return tag.RowsAffected() > 0, err
}

// Waiting is when the oldest event past each consumer's cursor came in: the
// one its worker has not got past.
func (s *Store) Waiting(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.pool.Query(ctx, `
		select s.consumer, e.received_at from event_subscriptions s
		  join lateral (select received_at from events where id > s.cursor order by id limit 1) e on true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var c string
		var at time.Time
		if err := rows.Scan(&c, &at); err != nil {
			return nil, err
		}
		out[c] = at
	}
	return out, rows.Err()
}

func clip(msg string) string {
	if r := []rune(msg); len(r) > 300 {
		return string(r[:300])
	}
	return msg
}
