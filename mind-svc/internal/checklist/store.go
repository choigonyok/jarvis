package checklist

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Memory keeps the lists in the process: a laptop run, the tests.
type Memory struct {
	mu sync.Mutex
	ls map[string]List
}

func NewMemory() *Memory { return &Memory{ls: map[string]List{}} }

func (m *Memory) PutList(_ context.Context, l List) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l.Items = append([]Item(nil), l.Items...)
	m.ls[l.ID] = l
	return nil
}

func (m *Memory) List(_ context.Context, id string) (List, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.ls[id]
	if !ok {
		return l, ErrNotFound
	}
	l.Items = append([]Item(nil), l.Items...)
	return l, nil
}

func (m *Memory) Lists(_ context.Context, since time.Time) ([]List, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []List{}
	for _, l := range m.ls {
		if l.Status == Active || !since.IsZero() && !l.UpdatedAt.Before(since) {
			l.Items = append([]Item(nil), l.Items...)
			out = append(out, l)
		}
	}
	return out, nil
}

// Postgres keeps them in mind_checklists (migration 062): the list as it
// is, with what it is looked up by beside it.
type Postgres struct{ db *pgxpool.Pool }

func NewPostgres(db *pgxpool.Pool) *Postgres { return &Postgres{db: db} }

func (p *Postgres) PutList(ctx context.Context, l List) error {
	body, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_checklists (id, status, topic, body, created_at, updated_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set status = excluded.status, topic = excluded.topic, body = excluded.body, updated_at = excluded.updated_at`,
		l.ID, l.Status, l.Topic, body, l.CreatedAt, l.UpdatedAt)
	return err
}

func (p *Postgres) List(ctx context.Context, id string) (List, error) {
	var l List
	var body []byte
	err := p.db.QueryRow(ctx, `select body from mind_checklists where id = $1`, id).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	return l, json.Unmarshal(body, &l)
}

func (p *Postgres) Lists(ctx context.Context, since time.Time) ([]List, error) {
	q, args := `select body from mind_checklists where status = 'active'`, []any{}
	if !since.IsZero() {
		q, args = q+` or updated_at >= $1`, append(args, since)
	}
	rows, err := p.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []List{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var l List
		if err := json.Unmarshal(body, &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
