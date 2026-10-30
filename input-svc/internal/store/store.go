// Package store is input-svc's side of the tables (migration 078): it empties
// the outbox into the records, one transaction for the record and its
// deliveries, and keeps the tables from growing without end.
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
)

// Store is the database.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New is a store on pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool, now: time.Now} }

// Pool is the database, for what reads the queue itself (/health, the held box).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// topicOf is where a record belongs.
func topicOf(e envelope.Envelope) queue.Topic {
	if e.IsGuest() {
		return queue.Guest
	}
	return queue.Records
}

// batch is what a round learns once: who reads each topic, and which it
// touched.
type batch struct {
	tx      pgx.Tx
	subs    map[string][]queue.Sub
	touched map[string]queue.Topic
}

func newBatch(tx pgx.Tx) *batch {
	return &batch{tx: tx, subs: map[string][]queue.Sub{}, touched: map[string]queue.Topic{}}
}

// put stores a record and leaves it for its consumers. A record already
// stored is reported and changes nothing, so a sender may repeat itself.
func (b *batch) put(ctx context.Context, e envelope.Envelope) (int64, bool, error) {
	t := topicOf(e)
	subs, ok := b.subs[t.Name]
	if !ok {
		var err error
		if subs, err = queue.Subs(ctx, b.tx, t); err != nil {
			return 0, false, err
		}
		b.subs[t.Name] = subs
	}
	var id *int64
	var at *time.Time
	err := b.tx.QueryRow(ctx, `select r_id, r_at from input.append($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb)`,
		e.IsGuest(), e.Kind, e.Source, e.SourceKey, e.Actor, e.Channel, e.Subject, e.OccurredAt, e.Tier, e.SchemaVersion,
		e.TraceID, e.CausationID, string(e.Payload)).Scan(&id, &at)
	if err != nil {
		return 0, false, err
	}
	if id == nil {
		return 0, true, nil
	}
	if err := queue.Deliver(ctx, b.tx, t, subs, *id, *at, e.Kind, e.Subject); err != nil {
		return 0, false, err
	}
	b.touched[t.Name] = t
	return *id, false, nil
}

func (b *batch) wake(ctx context.Context) error {
	for _, t := range b.touched {
		if err := queue.Wake(ctx, b.tx, t); err != nil {
			return err
		}
	}
	return nil
}

// Drain moves up to limit rows from the outbox into the records, oldest
// first, each in the transaction that removes it - so a row is stored exactly
// once, however a crash falls. A row that will never be a record goes to
// input.rejected, with why, rather than being retried forever or lost. It
// returns how many rows it took.
func (s *Store) Drain(ctx context.Context, limit int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `select id, payload from input.outbox order by id limit $1 for update skip locked`, limit)
	if err != nil {
		return 0, err
	}
	type row struct {
		id      int64
		payload []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.id, &x.payload)
		return x, err
	})
	if err != nil {
		return 0, err
	}
	b := newBatch(tx)
	for _, r := range list {
		e, why := envelope.Parse(r.payload)
		if why != nil {
			if _, err := tx.Exec(ctx, `insert into input.rejected (id, payload, why) values ($1, $2, $3) on conflict do nothing`,
				r.id, string(r.payload), why.Error()); err != nil {
				return 0, err
			}
		} else if _, _, err := b.put(ctx, e); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `delete from input.outbox where id = $1`, r.id); err != nil {
			return 0, err
		}
	}
	if err := b.wake(ctx); err != nil {
		return 0, err
	}
	return len(list), tx.Commit(ctx)
}

// Put stores one record that came over HTTP: the way in for a process with no
// database (the guest's agent) or one that lost it.
func (s *Store) Put(ctx context.Context, e envelope.Envelope) (int64, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	b := newBatch(tx)
	id, dup, err := b.put(ctx, e)
	if err != nil {
		return 0, false, err
	}
	if err := b.wake(ctx); err != nil {
		return 0, false, err
	}
	return id, dup, tx.Commit(ctx)
}

// Prune deletes the raw tier past keep and says how much went. What a
// consumer has not taken stays until it has.
func (s *Store) Prune(ctx context.Context, keep time.Duration) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `select input.prune_raw($1)`, s.now().Add(-keep)).Scan(&n)
	return n, err
}

// Partitions makes sure the months ahead have theirs, and says how many it
// made.
func (s *Store) Partitions(ctx context.Context, months int) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `select input.ensure_partitions($1)`, months).Scan(&n)
	return n, err
}

// Rejected is a row that could not be a record.
type Rejected struct {
	ID  int64     `json:"id"`
	Why string    `json:"why"`
	At  time.Time `json:"at"`
}

// Rejections is what was turned away, newest first, and how many there are.
func (s *Store) Rejections(ctx context.Context, limit int) ([]Rejected, int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `select count(*) from input.rejected`).Scan(&n); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `select id, why, rejected_at from input.rejected order by id desc limit $1`, limit)
	if err != nil {
		return nil, 0, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Rejected, error) {
		var x Rejected
		err := r.Scan(&x.ID, &x.Why, &x.At)
		return x, err
	})
	return list, n, err
}

// Oldest is how long the outbox's oldest row has waited; zero when it is
// empty. A row that sits there is what a stopped drain looks like from outside.
func (s *Store) Oldest(ctx context.Context) (time.Duration, error) {
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `select min(created_at) from input.outbox`).Scan(&at); err != nil {
		return 0, err
	}
	if at == nil {
		return 0, nil
	}
	return s.now().Sub(*at), nil
}
