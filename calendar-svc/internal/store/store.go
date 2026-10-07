// Package store keeps the calendar in Postgres.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

type Store struct {
	pool *pgxpool.Pool
	seq  int64
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 해석: %w", err)
	}
	cfg.MaxConns = 4
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("풀 생성: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("연결 확인: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// selectList is the one projection every read uses, so a column added in one
// place cannot be missing from another.
const selectList = `
	select id,
	       to_char(on_date, 'YYYY-MM-DD'),
	       coalesce(to_char(start_time, 'HH24:MI'), ''),
	       coalesce(to_char(end_time,   'HH24:MI'), ''),
	       title,
	       coalesce(place, ''),
	       coalesce(memo,  ''),
	       origin,
	       to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
	  from commitments`

// 날짜는 Postgres 가 문자열로 만들어 준다. date 를 Go 의 time.Time 으로 받아
// 다시 찍으면 시간대를 한 번 왕복하게 되고, 그 왕복이 자정 근처에서 하루를
// 밀어낸다. 벽시계를 벽시계로 주고받으면 밀릴 곳이 없다.

func scan(rows pgx.Rows) ([]event.Event, error) {
	out := []event.Event{}
	for rows.Next() {
		var e event.Event
		if err := rows.Scan(&e.ID, &e.Date, &e.Start, &e.End, &e.Title,
			&e.Place, &e.Memo, &e.Source, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("일정 읽기: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Range returns events with date in [from, to], both inclusive, both
// YYYY-MM-DD. An empty bound means unbounded on that side.
//
// Ordering puts all-day entries at the top of their day, which is what the
// file-backed store did and what the month grid expects.
func (s *Store) Range(ctx context.Context, from, to string) ([]event.Event, error) {
	rows, err := s.pool.Query(ctx, selectList+`
		 where ($1 = '' or on_date >= $1::date)
		   and ($2 = '' or on_date <= $2::date)
		 order by on_date, start_time asc nulls first, id`, from, to)
	if err != nil {
		return nil, fmt.Errorf("기간 조회: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

func (s *Store) Get(ctx context.Context, id string) (event.Event, error) {
	rows, err := s.pool.Query(ctx, selectList+` where id = $1`, id)
	if err != nil {
		return event.Event{}, fmt.Errorf("일정 조회: %w", err)
	}
	defer rows.Close()

	found, err := scan(rows)
	if err != nil {
		return event.Event{}, err
	}
	if len(found) == 0 {
		return event.Event{}, event.ErrNotFound
	}
	return found[0], nil
}

// Put inserts or replaces. An empty ID means insert.
//
// The id format ('e-<epoch ms>-<seq>') is kept from the file-backed store
// because the SSE frames and the UI's React keys are built on it, and because
// the record surface reads the timestamp back out of it.
func (s *Store) Put(ctx context.Context, e event.Event) (event.Event, error) {
	if err := e.Validate(); err != nil {
		return event.Event{}, err
	}
	if e.ID == "" {
		s.seq++
		e.ID = fmt.Sprintf("e-%d-%d", time.Now().UnixMilli(), s.seq)
	}

	// span 과 updated_at 은 트리거가 채운다. 여기서 쓰지 않는 것이 중요하다 -
	// 두 곳에서 계산하면 언젠가 갈라진다.
	_, err := s.pool.Exec(ctx, `
		insert into commitments
		  (id, on_date, start_time, end_time, title, place, memo, origin)
		values ($1, $2::date, nullif($3,'')::time, nullif($4,'')::time,
		        $5, nullif($6,''), nullif($7,''), $8)
		on conflict (id) do update set
		  on_date    = excluded.on_date,
		  start_time = excluded.start_time,
		  end_time   = excluded.end_time,
		  title      = excluded.title,
		  place      = excluded.place,
		  memo       = excluded.memo,
		  origin     = excluded.origin`,
		e.ID, e.Date, e.Start, e.End, e.Title, e.Place, e.Memo, e.Source)
	if err != nil {
		return event.Event{}, fmt.Errorf("일정 저장: %w", err)
	}

	// 트리거가 채운 값을 그대로 돌려주기 위해 다시 읽는다. 여기서 Go 가
	// 계산한 updated_at 을 돌려주면 데이터베이스에 있는 값과 다를 수 있다.
	return s.Get(ctx, e.ID)
}

func (s *Store) Delete(ctx context.Context, id string) (event.Event, error) {
	gone, err := s.Get(ctx, id)
	if err != nil {
		return event.Event{}, err
	}
	if _, err := s.pool.Exec(ctx, `delete from commitments where id = $1`, id); err != nil {
		return event.Event{}, fmt.Errorf("일정 삭제: %w", err)
	}
	return gone, nil
}

// Conflicts returns confirmed events overlapping this one, excluding itself.
//
// buffer is travel time. A 19:00 in Gangnam and a 20:00 in Pangyo do not
// overlap but cannot both be attended, and adding slack is the only way to
// notice that. Zero is the default because assuming 30 minutes without knowing
// the journey calls every back-to-back day a conflict.
func (s *Store) Conflicts(ctx context.Context, id string, buffer time.Duration) ([]event.Event, error) {
	rows, err := s.pool.Query(ctx, `
		select c.id,
		       to_char(c.on_date, 'YYYY-MM-DD'),
		       coalesce(to_char(c.start_time, 'HH24:MI'), ''),
		       coalesce(to_char(c.end_time,   'HH24:MI'), ''),
		       c.title,
		       coalesce(c.place, ''),
		       coalesce(c.memo,  ''),
		       c.origin,
		       to_char(c.updated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		  from conflicts_for($1, make_interval(secs => $2)) c`,
		id, buffer.Seconds())
	if err != nil {
		return nil, fmt.Errorf("충돌 조회: %w", err)
	}
	defer rows.Close()
	return scan(rows)
}

// NextID seeds the sequence so ids minted after a restart do not collide with
// ones already stored in the same millisecond. Called once at boot.
func (s *Store) NextID(ctx context.Context) error {
	var count int64
	if err := s.pool.QueryRow(ctx, `select count(*) from commitments`).Scan(&count); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("일정 수 조회: %w", err)
	}
	s.seq = count
	return nil
}
