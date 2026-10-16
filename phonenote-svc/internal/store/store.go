// Package store keeps phone notifications and what came of them in Postgres
// (migration 056).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, device, app, title, subtitle, body, posted_at, received_at, status, coalesce(reading::text, ''), applied, error, tries`

func scan(row pgx.Row) (note.Note, error) {
	var n note.Note
	var reading string
	err := row.Scan(&n.ID, &n.Device, &n.App, &n.Title, &n.Subtitle, &n.Body, &n.At, &n.ReceivedAt, &n.Status, &reading, &n.Applied, &n.Error, &n.Tries)
	if err == nil && reading != "" {
		var r note.Reading
		if json.Unmarshal([]byte(reading), &r) == nil {
			n.Reading = &r
		}
	}
	return n, err
}

func collect(rows pgx.Rows) ([]note.Note, error) {
	defer rows.Close()
	out := []note.Note{}
	for rows.Next() {
		n, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Add stores notifications from one device; one already stored is left as
// it was. It says how many were new.
func (s *Store) Add(ctx context.Context, device string, in []note.In) (int, error) {
	added := 0
	for _, n := range in {
		tag, err := s.pool.Exec(ctx, `
			insert into phone_notes (device, source_key, app, title, subtitle, body, posted_at)
			values ($1, $2, $3, $4, $5, $6, $7)
			on conflict (device, source_key) do nothing`,
			device, n.Key, n.App, n.Title, n.Subtitle, n.Body, n.At)
		if err != nil {
			return added, err
		}
		added += int(tag.RowsAffected())
	}
	return added, nil
}

// Seen records that a collector got in touch, and what it could not do.
func (s *Store) Seen(ctx context.Context, device, problem, lastKey string) error {
	_, err := s.pool.Exec(ctx, `
		insert into phone_collectors (device, seen_at, problem, last_key) values ($1, now(), $2, $3)
		on conflict (device) do update set seen_at = now(), problem = excluded.problem,
		  last_key = case when excluded.last_key = '' then phone_collectors.last_key else excluded.last_key end`,
		device, problem, lastKey)
	return err
}

// Open is what is still to be read or applied, oldest first.
func (s *Store) Open(ctx context.Context, limit int) ([]note.Note, error) {
	rows, err := s.pool.Query(ctx, `select `+cols+` from phone_notes where status in ('new', 'read') order by posted_at, id limit $1`, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// Save writes back what was made of a notification.
func (s *Store) Save(ctx context.Context, n note.Note) error {
	var reading any
	if n.Reading != nil {
		b, _ := json.Marshal(n.Reading)
		reading = string(b)
	}
	_, err := s.pool.Exec(ctx, `
		update phone_notes set status = $2, reading = $3::jsonb, applied = $4, error = $5, tries = $6 where id = $1`,
		n.ID, n.Status, reading, n.Applied, n.Error, n.Tries)
	return err
}

// Recent is the newest notifications, read or not.
func (s *Store) Recent(ctx context.Context, limit int) ([]note.Note, error) {
	rows, err := s.pool.Query(ctx, `select `+cols+` from phone_notes order by posted_at desc, id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// Meaningful is what was worth keeping since a time, oldest first - what
// becomes occurrences for mind.
func (s *Store) Meaningful(ctx context.Context, since time.Time, limit int) ([]note.Note, error) {
	rows, err := s.pool.Query(ctx, `
		select `+cols+` from phone_notes
		 where posted_at > $1 and status in ('read', 'done') and (reading->>'meaningful')::boolean
		 order by posted_at, id limit $2`, since, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// Usual is how the app's last notifications read: their kinds, newest first.
// An app whose every recent one was noise need not be read by the model.
func (s *Store) Usual(ctx context.Context, app string, n int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		select case when (reading->>'meaningful')::boolean then reading->>'kind' else 'noise' end
		  from phone_notes where app = $1 and reading is not null and status <> 'failed'
		 order by posted_at desc limit $2`, app, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k *string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k != nil {
			out = append(out, *k)
		}
	}
	return out, rows.Err()
}

// Collector is one device's collector as last heard.
type Collector struct {
	Device  string    `json:"device"`
	SeenAt  time.Time `json:"seenAt"`
	Problem string    `json:"problem,omitempty"`
}

func (s *Store) Collectors(ctx context.Context) ([]Collector, error) {
	rows, err := s.pool.Query(ctx, `select device, seen_at, problem from phone_collectors order by seen_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collector{}
	for rows.Next() {
		var c Collector
		if err := rows.Scan(&c.Device, &c.SeenAt, &c.Problem); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var ErrNotFound = errors.New("그런 알림이 없습니다")
