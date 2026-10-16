// Package store keeps context-svc's state in Postgres (migration 046): the
// raw reports for a while, the regions, the visits derived from them, and
// the current state, one value per key.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/context-svc/internal/place"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Pool is the connection, for the event outbox.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

var ErrNotFound = errors.New("not found")

// Raw records a report as it came.
func (s *Store) Raw(ctx context.Context, source, kind string, at time.Time, body []byte) error {
	_, err := s.pool.Exec(ctx, `insert into context_raw (source, kind, at, body) values ($1, $2, $3, $4)`, source, kind, at, body)
	return err
}

// Readings is the numeric reports of a kind since a time, oldest first:
// what the raw log still holds (CONTEXT_RAW_DAYS), which is the baseline's
// window.
func (s *Store) Readings(ctx context.Context, kind string, since time.Time) ([]place.Reading, error) {
	rows, err := s.pool.Query(ctx, `select at, (body->>'state')::double precision from context_raw
		where kind = $1 and at >= $2 and body->>'state' ~ '^-?[0-9]+(\.[0-9]+)?$' order by at`, kind, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []place.Reading
	for rows.Next() {
		var r place.Reading
		if err := rows.Scan(&r.At, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Purge drops raw reports older than before.
func (s *Store) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `delete from context_raw where at < $1`, before)
	return tag.RowsAffected(), err
}

func (s *Store) Regions(ctx context.Context) ([]place.Region, error) {
	rows, err := s.pool.Query(ctx, `select name, kind, lat, lon, radius_m, area from context_regions order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []place.Region{}
	for rows.Next() {
		var r place.Region
		if err := rows.Scan(&r.Name, &r.Kind, &r.Lat, &r.Lon, &r.RadiusM, &r.Area); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) PutRegion(ctx context.Context, r place.Region) error {
	_, err := s.pool.Exec(ctx, `insert into context_regions (name, kind, lat, lon, radius_m, area) values ($1, $2, $3, $4, $5, $6)
		on conflict (name) do update set kind = excluded.kind, lat = excluded.lat, lon = excluded.lon, radius_m = excluded.radius_m, area = excluded.area`,
		r.Name, r.Kind, r.Lat, r.Lon, r.RadiusM, r.Area)
	return err
}

func (s *Store) DeleteRegion(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, `delete from context_regions where name = $1`, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Visit is a stay in a region.
type Visit struct {
	ID        int64      `json:"id"`
	Region    string     `json:"region"`
	Kind      string     `json:"kind,omitempty"`
	Area      string     `json:"area,omitempty"`
	EnteredAt time.Time  `json:"enteredAt"`
	LeftAt    *time.Time `json:"leftAt,omitempty"`
	Source    string     `json:"source"`
}

// Open is the visits not yet left.
func (s *Store) Open(ctx context.Context) ([]Visit, error) {
	return s.visits(ctx, `where v.left_at is null order by v.entered_at`)
}

// Visits is the visits that overlap [from, to).
func (s *Store) Visits(ctx context.Context, from, to time.Time) ([]Visit, error) {
	return s.visits(ctx, `where v.entered_at < $2 and (v.left_at is null or v.left_at >= $1) order by v.entered_at`, from, to)
}

func (s *Store) visits(ctx context.Context, q string, args ...any) ([]Visit, error) {
	rows, err := s.pool.Query(ctx, `select v.id, v.region, coalesce(r.kind, ''), coalesce(r.area, ''), v.entered_at, v.left_at, v.source
		from context_visits v left join context_regions r on r.name = v.region `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Visit{}
	for rows.Next() {
		var v Visit
		if err := rows.Scan(&v.ID, &v.Region, &v.Kind, &v.Area, &v.EnteredAt, &v.LeftAt, &v.Source); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Enter opens a visit to region at at, unless one is open. It reports
// whether it opened one.
func (s *Store) Enter(ctx context.Context, region string, at time.Time, source string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `insert into context_visits (region, entered_at, source)
		select $1, $2, $3 where not exists (select 1 from context_visits where region = $1 and left_at is null)`, region, at, source)
	return tag.RowsAffected() > 0, err
}

// Leave closes the open visit to region at at. It reports whether there
// was one. A leave older than the visit's start belongs to an earlier visit
// (a report that arrived late) and closes nothing.
func (s *Store) Leave(ctx context.Context, region string, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `update context_visits set left_at = $2 where region = $1 and left_at is null and entered_at <= $2`, region, at)
	return tag.RowsAffected() > 0, err
}

// State is one key of the current state.
type State struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
	At    time.Time       `json:"at"`
}

// Set writes a key of the current state, unless what is there is newer:
// a late report does not overwrite a fresher one.
func (s *Store) Set(ctx context.Context, key string, value any, at time.Time) error {
	_, err := s.SetIfNewer(ctx, key, value, at)
	return err
}

// SetIfNewer is Set, reporting whether the value was newer than the one kept
// (a report that arrives late is kept out).
func (s *Store) SetIfNewer(ctx context.Context, key string, value any, at time.Time) (bool, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `insert into context_state (key, value, at) values ($1, $2, $3)
		on conflict (key) do update set value = excluded.value, at = excluded.at where context_state.at <= excluded.at`, key, body, at)
	return tag.RowsAffected() > 0, err
}

// Drop removes a key of the current state.
func (s *Store) Drop(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `delete from context_state where key = $1`, key)
	return err
}

func (s *Store) Get(ctx context.Context, key string, into any) (time.Time, error) {
	var body []byte
	var at time.Time
	err := s.pool.QueryRow(ctx, `select value, at from context_state where key = $1`, key).Scan(&body, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	return at, json.Unmarshal(body, into)
}

// States is every key of the current state.
func (s *Store) States(ctx context.Context) ([]State, error) {
	rows, err := s.pool.Query(ctx, `select key, value, at from context_state order by key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []State{}
	for rows.Next() {
		var st State
		if err := rows.Scan(&st.Key, &st.Value, &st.At); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// String is for logs.
func (v Visit) String() string {
	return fmt.Sprintf("%s@%s", v.Region, v.EnteredAt.Format(time.RFC3339))
}
