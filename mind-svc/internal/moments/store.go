package moments

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Memory keeps the reminders in the process: a laptop run, the tests.
type Memory struct {
	mu sync.Mutex
	rs map[string]Reminder
}

func NewMemory() *Memory { return &Memory{rs: map[string]Reminder{}} }

func (m *Memory) PutReminder(_ context.Context, r Reminder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rs[r.ID] = r
	return nil
}

func (m *Memory) Reminder(_ context.Context, id string) (Reminder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rs[id]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}

func (m *Memory) Reminders(_ context.Context, since time.Time) ([]Reminder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Reminder{}
	for _, r := range m.rs {
		if r.Status == Waiting || !since.IsZero() && !r.UpdatedAt.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Postgres keeps them in mind_reminders (migration 061): the reminder as
// it is, with what it is looked up by beside it.
type Postgres struct{ db *pgxpool.Pool }

func NewPostgres(db *pgxpool.Pool) *Postgres { return &Postgres{db: db} }

func (p *Postgres) PutReminder(ctx context.Context, r Reminder) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_reminders (id, status, topic, body, created_at, updated_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set status = excluded.status, topic = excluded.topic, body = excluded.body, updated_at = excluded.updated_at`,
		r.ID, r.Status, r.Topic, body, r.CreatedAt, r.UpdatedAt)
	return err
}

func (p *Postgres) Reminder(ctx context.Context, id string) (Reminder, error) {
	var r Reminder
	var body []byte
	err := p.db.QueryRow(ctx, `select body from mind_reminders where id = $1`, id).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(body, &r)
}

func (p *Postgres) Reminders(ctx context.Context, since time.Time) ([]Reminder, error) {
	q, args := `select body from mind_reminders where status = 'waiting'`, []any{}
	if !since.IsZero() {
		q, args = q+` or updated_at >= $1`, append(args, since)
	}
	rows, err := p.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reminder{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var r Reminder
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
