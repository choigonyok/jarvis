// Package store keeps sealed logins and the record of every release.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Login is one saved login, sealed. Name and Hint are what is shown; the
// username and password are inside Sealed.
type Login struct {
	Domain string
	Name   string
	Hint   string
	Nonce  []byte
	Sealed []byte
	KeyID  string
}

// Meta is a saved login as it may be shown: no secret in it.
type Meta struct {
	Domain     string     `json:"domain"`
	Name       string     `json:"name"`
	Hint       string     `json:"hint"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Uses       int        `json:"uses"`
}

// Use is one release asked for: who, for which page, and what came of it.
type Use struct {
	At      time.Time `json:"at"`
	Domain  string    `json:"domain"`
	Host    string    `json:"host"`
	Purpose string    `json:"purpose"`
	Outcome string    `json:"outcome"`
	Note    string    `json:"note"`
}

var ErrNotFound = errors.New("저장된 로그인 정보가 없습니다")

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Put(ctx context.Context, l Login) error {
	_, err := s.pool.Exec(ctx, `insert into vault_logins (domain, name, hint, nonce, sealed, key_id)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (domain) do update set name = excluded.name, hint = excluded.hint, nonce = excluded.nonce,
		  sealed = excluded.sealed, key_id = excluded.key_id, updated_at = now()`,
		l.Domain, l.Name, l.Hint, l.Nonce, l.Sealed, l.KeyID)
	return err
}

func (s *Store) Get(ctx context.Context, domain string) (Login, error) {
	var l Login
	err := s.pool.QueryRow(ctx, `select domain, name, hint, nonce, sealed, key_id from vault_logins where domain = $1`, domain).
		Scan(&l.Domain, &l.Name, &l.Hint, &l.Nonce, &l.Sealed, &l.KeyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	return l, err
}

func (s *Store) List(ctx context.Context) ([]Meta, error) {
	rows, err := s.pool.Query(ctx, `select domain, name, hint, created_at, updated_at, last_used_at, uses
		from vault_logins order by name, domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Meta{}
	for rows.Next() {
		var m Meta
		if err := rows.Scan(&m.Domain, &m.Name, &m.Hint, &m.CreatedAt, &m.UpdatedAt, &m.LastUsedAt, &m.Uses); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, domain string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `delete from vault_logins where domain = $1`, domain)
	return tag.RowsAffected() > 0, err
}

// Used counts a release against the login.
func (s *Store) Used(ctx context.Context, domain string) error {
	_, err := s.pool.Exec(ctx, `update vault_logins set last_used_at = now(), uses = uses + 1 where domain = $1`, domain)
	return err
}

func (s *Store) Record(ctx context.Context, u Use) error {
	_, err := s.pool.Exec(ctx, `insert into vault_uses (domain, host, purpose, outcome, note) values ($1, $2, $3, $4, $5)`,
		u.Domain, u.Host, u.Purpose, u.Outcome, u.Note)
	return err
}

func (s *Store) Uses(ctx context.Context, limit int) ([]Use, error) {
	rows, err := s.pool.Query(ctx, `select at, domain, host, purpose, outcome, note from vault_uses order by at desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Use{}
	for rows.Next() {
		var u Use
		if err := rows.Scan(&u.At, &u.Domain, &u.Host, &u.Purpose, &u.Outcome, &u.Note); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
