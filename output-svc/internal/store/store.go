// Package store keeps notifications, push subscriptions and each person's
// settings in Postgres (migrations 031, 054). Everything is someone's: the
// operator ("owner") or the guest account ("guest"), and every read names whose.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The two people notifications are for.
const (
	Owner = "owner"
	Guest = "guest"
)

// Audiences is everyone a notification can be for.
var Audiences = []string{Owner, Guest}

type Notification struct {
	ID          int64      `json:"id"`
	Audience    string     `json:"audience"`
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
	Audience  string
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, audience, source, kind, tier, level, title, body, url, dedupe_key, created_at, delivered_at, read_at`

func scan(row pgx.Row) (Notification, error) {
	var n Notification
	err := row.Scan(&n.ID, &n.Audience, &n.Source, &n.Kind, &n.Tier, &n.Level, &n.Title, &n.Body, &n.URL,
		&n.DedupeKey, &n.CreatedAt, &n.DeliveredAt, &n.ReadAt)
	return n, err
}

// who is an audience as stored: anything not the guest is the operator's.
func who(audience string) string {
	if audience == Guest {
		return Guest
	}
	return Owner
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
		insert into output.notifications (audience, source, kind, tier, level, title, body, url, dedupe_key)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		on conflict (dedupe_key) do nothing
		returning `+cols,
		who(n.Audience), n.Source, n.Kind, n.Tier, n.Level, n.Title, n.Body, n.URL, n.DedupeKey)
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
	_, err := s.pool.Exec(ctx, `update output.notifications set delivered_at = now() where id = any($1) and delivered_at is null`, ids)
	return err
}

// Supersede marks a still-pending notification as done without a push:
// something better (a suggestion card about the same thing) took its place.
func (s *Store) Supersede(ctx context.Context, key string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `update output.notifications set delivered_at = now() where dedupe_key = $1 and delivered_at is null`, key)
	return tag.RowsAffected() > 0, err
}

// Resolve marks the notification behind a key as read - and delivered, if
// it was still waiting: the card it pointed at has been decided, so there
// is nothing left to look at. False when no notification carries the key.
func (s *Store) Resolve(ctx context.Context, key string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `update output.notifications set read_at = coalesce(read_at, now()), delivered_at = coalesce(delivered_at, now()) where dedupe_key = $1`, key)
	return tag.RowsAffected() > 0, err
}

// Pending is what of one tier is still waiting to go out to one person, oldest first.
func (s *Store) Pending(ctx context.Context, audience, tier string) ([]Notification, error) {
	rows, err := s.pool.Query(ctx, `select `+cols+` from output.notifications where audience = $1 and tier = $2 and delivered_at is null order by created_at`, who(audience), tier)
	if err != nil {
		return nil, err
	}
	return collect(rows)
}

// Inbox is a page of one person's inbox and what it is a page of.
type Inbox struct {
	Items []Notification
	// Unread is how many are unread in all, not on this page.
	Unread int
	// Total is how many there are in all - the console's pages are counted from it.
	Total int
}

// List is a page of one person's inbox, newest first: `limit` of them after
// skipping `offset`. The id breaks a tie in time, so two pages never share a
// row nor skip one.
func (s *Store) List(ctx context.Context, audience string, limit, offset int) (Inbox, error) {
	rows, err := s.pool.Query(ctx, `select `+cols+` from output.notifications where audience = $1 order by created_at desc, id desc limit $2 offset $3`, who(audience), limit, offset)
	if err != nil {
		return Inbox{}, err
	}
	items, err := collect(rows)
	if err != nil {
		return Inbox{}, err
	}
	out := Inbox{Items: items}
	err = s.pool.QueryRow(ctx, `select count(*) filter (where read_at is null), count(*) from output.notifications where audience = $1`, who(audience)).Scan(&out.Unread, &out.Total)
	return out, err
}

// MarkRead marks the given ids of one person's read, or every unread one of
// theirs when ids is empty.
func (s *Store) MarkRead(ctx context.Context, audience string, ids []int64) error {
	if len(ids) == 0 {
		_, err := s.pool.Exec(ctx, `update output.notifications set read_at = now() where audience = $1 and read_at is null`, who(audience))
		return err
	}
	_, err := s.pool.Exec(ctx, `update output.notifications set read_at = now() where audience = $1 and id = any($2) and read_at is null`, who(audience), ids)
	return err
}

// Subscriptions are one person's devices.
func (s *Store) Subscriptions(ctx context.Context, audience string) ([]Subscription, error) {
	rows, err := s.pool.Query(ctx, `select audience, endpoint, p256dh, auth, user_agent from output.push_subscriptions where audience = $1 order by created_at`, who(audience))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.Audience, &sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Subscribe keeps a device for a person. The same browser signed in as the
// other person moves to them.
func (s *Store) Subscribe(ctx context.Context, sub Subscription) error {
	_, err := s.pool.Exec(ctx, `
		insert into output.push_subscriptions (audience, endpoint, p256dh, auth, user_agent) values ($1, $2, $3, $4, $5)
		on conflict (endpoint) do update set audience = excluded.audience, p256dh = excluded.p256dh,
		  auth = excluded.auth, user_agent = excluded.user_agent, failures = 0`,
		who(sub.Audience), sub.Endpoint, sub.P256dh, sub.Auth, sub.UserAgent)
	return err
}

func (s *Store) Unsubscribe(ctx context.Context, endpoint string) error {
	_, err := s.pool.Exec(ctx, `delete from output.push_subscriptions where endpoint = $1`, endpoint)
	return err
}

// PushResult records how one delivery to one device went. A device that keeps
// failing is dropped after enough tries; one the push service says is gone
// (404/410) is dropped at once by the caller through Unsubscribe.
// Devices is how the operator's phones are taking pushes: how many are
// registered, the fewest failures in a row among them, and the last push
// any of them took.
func (s *Store) Devices(ctx context.Context) (n, fewest int, lastOK *time.Time, err error) {
	err = s.pool.QueryRow(ctx, `select count(*), coalesce(min(failures), 0), max(last_ok_at)
		from output.push_subscriptions where audience = 'owner'`).Scan(&n, &fewest, &lastOK)
	return
}

func (s *Store) PushResult(ctx context.Context, endpoint string, ok bool) error {
	if ok {
		_, err := s.pool.Exec(ctx, `update output.push_subscriptions set last_ok_at = now(), failures = 0 where endpoint = $1`, endpoint)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		with u as (update output.push_subscriptions set failures = failures + 1 where endpoint = $1 returning failures)
		delete from output.push_subscriptions where endpoint = $1 and (select failures from u) >= 20`, endpoint)
	return err
}

// Settings reads one person's settings into v (a pointer), leaving v as it
// was when they have none yet; WriteSettings replaces them.
func (s *Store) Settings(ctx context.Context, audience string, v any) error {
	var raw []byte
	err := s.pool.QueryRow(ctx, `select settings from output.settings where audience = $1`, who(audience)).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (s *Store) WriteSettings(ctx context.Context, audience string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		insert into output.settings (audience, settings, updated_at) values ($1, $2, now())
		on conflict (audience) do update set settings = excluded.settings, updated_at = now()`, who(audience), raw)
	return err
}
