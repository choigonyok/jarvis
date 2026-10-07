// Package store keeps the transcript in Postgres.
package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/chat-svc/internal/turn"
)

type Store struct {
	pool *pgxpool.Pool
	// mu guards seq only. The id has to be unique, and two turns appended in
	// the same millisecond would otherwise collide.
	mu  sync.Mutex
	seq int64
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

// Resume seeds the id sequence from what is already stored, so ids minted
// after a restart do not collide with ones written in the same millisecond
// before it.
func (s *Store) Resume(ctx context.Context) error {
	var count int64
	if err := s.pool.QueryRow(ctx, `select count(*) from turns`).Scan(&count); err != nil {
		return fmt.Errorf("턴 수 조회: %w", err)
	}
	s.mu.Lock()
	s.seq = count
	s.mu.Unlock()
	return nil
}

// 시각은 Postgres 가 문자열로 만든다. 벽시계를 Go 의 time.Time 으로 왕복시키면
// 시간대를 한 번 더 거치게 되고, 자정 근처에서 날짜가 밀린다. tz 는 연결이 아니라
// 질의에 붙여서, 컨테이너의 TZ 설정에 의존하지 않게 한다.
const selectList = `
	select id, role,
	       to_char(at at time zone $1, 'HH24:MI'),
	       to_char(at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
	       coalesce(paragraphs, '{}'),
	       coalesce(body, ''),
	       coalesce(proposal_id, ''),
	       coalesce(session_id, '')
	  from turns`

// All returns the whole transcript, oldest first.
//
// There is no pagination and no limit. The console renders the entire thread
// on load - that is the product, one long 1:1 conversation - and a transcript
// that quietly stops at 200 turns would read as history having been lost. When
// this gets slow the fix is a window the UI asks for, not a cap the service
// imposes without saying so.
func (s *Store) All(ctx context.Context, tz string) ([]turn.Turn, error) {
	rows, err := s.pool.Query(ctx, selectList+` order by at, id`, tz)
	if err != nil {
		return nil, fmt.Errorf("턴 조회: %w", err)
	}
	defer rows.Close()

	out := []turn.Turn{}
	for rows.Next() {
		var t turn.Turn
		if err := rows.Scan(&t.ID, &t.Role, &t.At, &t.AtISO, &t.Paragraphs,
			&t.Text, &t.ProposalID, &t.SessionID); err != nil {
			return nil, fmt.Errorf("턴 읽기: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Append stores one turn and returns it as stored, with the id and the clock
// the service minted.
//
// The id prefix says who spoke ('u' or 'a'), which is what the old file-backed
// store did; the record surface reads the epoch back out of this format.
func (s *Store) Append(ctx context.Context, t turn.Turn, tz string) (turn.Turn, error) {
	if t.Role != turn.RoleUser && t.Role != turn.RoleAgent {
		return turn.Turn{}, fmt.Errorf("모르는 역할입니다: %q", t.Role)
	}

	prefix := "a"
	if t.Role == turn.RoleUser {
		prefix = "u"
	}

	s.mu.Lock()
	s.seq++
	id := fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixMilli(), s.seq)
	s.mu.Unlock()

	// paragraphs 는 비어 있으면 NULL 로 넣는다. 빈 배열과 NULL 을 섞어 두면
	// 읽는 쪽에서 둘을 다르게 처리하게 되는데, 실제로 다른 것이 없다.
	var paragraphs any
	if len(t.Paragraphs) > 0 {
		paragraphs = t.Paragraphs
	}

	rows, err := s.pool.Query(ctx, `
		with inserted as (
		  insert into turns (id, role, at, paragraphs, body, proposal_id, session_id)
		  values ($2, $3, now(), $4, nullif($5,''), nullif($6,''), nullif($7,''))
		  returning *
		)
		select id, role,
		       to_char(at at time zone $1, 'HH24:MI'),
		       to_char(at, 'YYYY-MM-DD"T"HH24:MI:SSOF'),
		       coalesce(paragraphs, '{}'),
		       coalesce(body, ''),
		       coalesce(proposal_id, ''),
		       coalesce(session_id, '')
		  from inserted`,
		tz, id, string(t.Role), paragraphs, t.Text, t.ProposalID, t.SessionID)
	if err != nil {
		return turn.Turn{}, fmt.Errorf("턴 저장: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return turn.Turn{}, fmt.Errorf("턴 저장 확인: %w", err)
		}
		return turn.Turn{}, fmt.Errorf("턴이 저장되지 않았습니다")
	}
	var saved turn.Turn
	if err := rows.Scan(&saved.ID, &saved.Role, &saved.At, &saved.AtISO,
		&saved.Paragraphs, &saved.Text, &saved.ProposalID, &saved.SessionID); err != nil {
		return turn.Turn{}, fmt.Errorf("저장된 턴 읽기: %w", err)
	}
	return saved, nil
}
