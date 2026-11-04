package goals

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Memory keeps the goals in the process: a laptop run, the tests.
type Memory struct {
	mu    sync.Mutex
	goals map[string]Goal
	marks map[string]map[string]Mark
}

func NewMemory() *Memory {
	return &Memory{goals: map[string]Goal{}, marks: map[string]map[string]Mark{}}
}

func (m *Memory) PutGoal(_ context.Context, g Goal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g.Weekdays, g.Rests = slices.Clone(g.Weekdays), slices.Clone(g.Rests)
	m.goals[g.ID] = g
	return nil
}

func (m *Memory) Goal(_ context.Context, id string) (Goal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.goals[id]
	if !ok {
		return g, ErrNotFound
	}
	g.Weekdays, g.Rests = slices.Clone(g.Weekdays), slices.Clone(g.Rests)
	return g, nil
}

func (m *Memory) Goals(_ context.Context, ended bool) ([]Goal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Goal{}
	for _, g := range m.goals {
		if ended || g.Status != Ended {
			g.Weekdays, g.Rests = slices.Clone(g.Weekdays), slices.Clone(g.Rests)
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *Memory) DeleteGoal(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.goals[id]; !ok {
		return ErrNotFound
	}
	delete(m.goals, id)
	delete(m.marks, id)
	return nil
}

func (m *Memory) PutMark(_ context.Context, mk Mark) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.marks[mk.Goal] == nil {
		m.marks[mk.Goal] = map[string]Mark{}
	}
	m.marks[mk.Goal][mk.Day] = mk
	return nil
}

func (m *Memory) DeleteMark(_ context.Context, goal, day string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.marks[goal], day)
	return nil
}

func (m *Memory) Marks(_ context.Context, goal string) (map[string]Mark, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]Mark{}
	maps.Copy(out, m.marks[goal])
	return out, nil
}

// Postgres keeps them in mind_goals and mind_goal_marks (migrations 100, 102): a
// goal as it is, with what it is looked up by beside it, and a row for each day
// marked or under way.
type Postgres struct{ db *pgxpool.Pool }

func NewPostgres(db *pgxpool.Pool) *Postgres { return &Postgres{db: db} }

func (p *Postgres) PutGoal(ctx context.Context, g Goal) error {
	body, err := json.Marshal(g)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_goals (id, status, body, created_at, updated_at) values ($1, $2, $3, $4, $5)
		on conflict (id) do update set status = excluded.status, body = excluded.body, updated_at = excluded.updated_at`,
		g.ID, g.Status, body, g.CreatedAt, g.UpdatedAt)
	return err
}

func (p *Postgres) Goal(ctx context.Context, id string) (Goal, error) {
	var g Goal
	var body []byte
	err := p.db.QueryRow(ctx, `select body from mind_goals where id = $1`, id).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	return g, json.Unmarshal(body, &g)
}

func (p *Postgres) Goals(ctx context.Context, ended bool) ([]Goal, error) {
	rows, err := p.db.Query(ctx, `select body from mind_goals where $1 or status <> 'ended'`, ended)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Goal{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var g Goal
		if err := json.Unmarshal(body, &g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteGoal(ctx context.Context, id string) error {
	tag, err := p.db.Exec(ctx, `delete from mind_goals where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) PutMark(ctx context.Context, m Mark) error {
	_, err := p.db.Exec(ctx, `
		insert into mind_goal_marks (goal_id, day, mark, count, note, by_whom, at) values ($1, $2::date, $3, $4, $5, $6, $7)
		on conflict (goal_id, day) do update set mark = excluded.mark, count = excluded.count, note = excluded.note, by_whom = excluded.by_whom, at = excluded.at`,
		m.Goal, m.Day, m.Mark, m.Count, m.Note, m.By, m.At)
	return err
}

func (p *Postgres) DeleteMark(ctx context.Context, goal, day string) error {
	_, err := p.db.Exec(ctx, `delete from mind_goal_marks where goal_id = $1 and day = $2::date`, goal, day)
	return err
}

func (p *Postgres) Marks(ctx context.Context, goal string) (map[string]Mark, error) {
	rows, err := p.db.Query(ctx, `select to_char(day, 'YYYY-MM-DD'), mark, count, note, by_whom, at from mind_goal_marks where goal_id = $1`, goal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Mark{}
	for rows.Next() {
		m := Mark{Goal: goal}
		if err := rows.Scan(&m.Day, &m.Mark, &m.Count, &m.Note, &m.By, &m.At); err != nil {
			return nil, err
		}
		out[m.Day] = m
	}
	return out, rows.Err()
}
