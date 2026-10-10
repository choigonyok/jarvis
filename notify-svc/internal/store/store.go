// Package store keeps notifications, push subscriptions and the one row of
// settings in Postgres (migration 031).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Notification struct {
	ID          int64      `json:"id"`
	Source      string     `json:"source"`
	Kind        string     `json:"kind"`
	Tier        string     `json:"tier"`
	Level       string     `json:"level"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	URL         string     `json:"url"`
	DedupeKey   *string    `json:"-"`
	CreatedAt   time.Time  `json:"createdAt"`
	DeliveredAt *time.Time `json:"deliveredAt"`
	ReadAt      *time.Time `json:"readAt"`
}

type Subscription struct {
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, source, kind, tier, level, title, body, url, dedupe_key, created_at, delivered_at, read_at`

func scan(row pgx.Row) (Notification, error) {
	var n Notification
	err := row.Scan(&n.ID, &n.Source, &n.Kind, &n.Tier, &n.Level, &n.Title, &n.Body, &n.URL,
		&n.DedupeKey, &n.CreatedAt, &n.DeliveredAt, &n.ReadAt)
	return n, err
}

func collect(rows pgx.Rows) ([]Notification, error) {
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		n, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Insert stores a notification. A dedupe key already seen leaves the table as
// it was and reports duplicate, so a sender can retry or repeat freely.
func (s *Store) Insert(ctx context.Context, n Notification) (Notification, bool, error) {
	row := s.pool.QueryRow(ctx, `
		insert into notifications (source, kind, tier, level, title, body, url, dedupe_key)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		on conflict (dedupe_key) do nothing
		returning `+cols,
		n.Source, n.Kind, n.Tier, n.Level, n.Title, n.Body, n.URL, n.DedupeKey)
	saved, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Notification{}, true, nil
	}
	return saved, false, err
}

// MarkDelivered stamps notifications as sent (or deliberately not sent).
func (s *Store) MarkDelivered(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `update notifications set delivered_at = now() where id = any($1) and delivered_at is null`, ids)
	return err
}

// Pending is what of one tier is still waiting to go out, oldest first.
func (s *Store) Pending(ctx context.Context, tier string) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `select `+cols+` from notifications where tier = $1 and delivered_at is null order by created_at`, tier)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// List is the inbox: newest first, and how many are unread in all.
func (s *Store) List(ctx context.Context, limit int) ([]Notification, int, error) {
	rows, err := s.pool.Query(ctx, `select `+cols+` from notifications order by created_at desc limit $1`, limit)
	if err != nil {
		return nil, 0, err
	}
	items, err := collect(rows)
	if err != nil {
		return nil, 0, err
	}
	var unread int
	err = s.pool.QueryRow(ctx, `select count(*) from notifications where read_at is null`).Scan(&unread)
	return items, unread, err
}

// MarkRead marks the given ids read, or every unread one when ids is empty.
func (s *Store) MarkRead(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		_, err := s.pool.Exec(ctx, `update notifications set read_at = now() where read_at is null`)
		return err
	}
	_, err := s.pool.Exec(ctx, `update notifications set read_at = now() where id = any($1) and read_at is null`, ids)
	return err
}

func (s *Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := s.pool.Query(ctx, `select endpoint, p256dh, auth, user_agent from push_subscriptions order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) Subscribe(ctx context.Context, sub Subscription) error {
	_, err := s.pool.Exec(ctx, `
		insert into push_subscriptions (endpoint, p256dh, auth, user_agent) values ($1, $2, $3, $4)
		on conflict (endpoint) do update set p256dh = excluded.p256dh, auth = excluded.auth,
		  user_agent = excluded.user_agent, failures = 0`,
		sub.Endpoint, sub.P256dh, sub.Auth, sub.UserAgent)
	return err
}

func (s *Store) Unsubscribe(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx, `delete from push_subscriptions where endpoint = $1`, endpoint)
	return err
}

// PushResult records how one delivery to one device went. A device that keeps
// failing is dropped after enough tries; one the push service says is gone
// (404/410) is dropped at once by the caller through Unsubscribe.
func (s *Store) PushResult(ctx context.Context, endpoint string, ok bool) error {
	if ok {
		_, err := s.pool.Exec(ctx, `update push_subscriptions set last_ok_at = now(), failures = 0 where endpoint = $1`, endpoint)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		with u as (update push_subscriptions set failures = failures + 1 where endpoint = $1 returning failures)
		delete from push_subscriptions where endpoint = $1 and (select failures from u) >= 20`, endpoint)
	return err
}

// Settings reads the one settings row into v (a pointer); WriteSettings replaces it.
func (s *Store) Settings(ctx context.Context, v any) error {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `select settings from notify_settings where id`).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (s *Store) WriteSettings(ctx context.Context, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		insert into notify_settings (id, settings, updated_at) values (true, $1, now())
		on conflict (id) do update set settings = excluded.settings, updated_at = now()`, raw)
	return err
}
