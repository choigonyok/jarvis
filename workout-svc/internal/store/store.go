// Package store keeps the training log in Postgres.
//
// The log is normalised down to individual sets rather than parked in one
// jsonb column, because the rule gate has to be able to ask "how much leg
// volume in the last three weeks" and "when was the best set for this
// movement" in SQL. Those are the questions a proactive suggestion is built
// on, and answering them in application code means loading the whole log to
// answer any of them.
//
// Nothing derived is stored - no volume, no 1RM, no streak. The sets you
// actually logged are the only numbers here, so there is no second number
// that can disagree with them.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/workout-svc/internal/book"
	"github.com/choigonyok/jarvis/workout-svc/internal/muscle"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 해석: %w", err)
	}
	// 개인용 서비스에 커넥션이 많을 이유가 없다. 기본값(코어 수)은 이 규모에서
	// 그냥 유휴 커넥션을 늘릴 뿐이다.
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

// ── 읽기 ────────────────────────────────────────────────────────────────

// Load reads the whole book. One query per level rather than a join, because
// a join would repeat every session row once per set and the assembling code
// would have to undo that; three ordered passes are less code and less to get
// wrong.
func (s *Store) Load(ctx context.Context) (book.Book, error) {
	out := book.Book{Sessions: []book.Session{}}

	rows, err := s.pool.Query(ctx, `
		select id, on_date, coalesce(routine_id,''), coalesce(routine_name,''),
		       started_at, ended_at, coalesce(note,''), body_weight
		  from workout_sessions
		 order by started_at`)
	if err != nil {
		return out, fmt.Errorf("세션 조회: %w", err)
	}
	defer rows.Close()

	index := map[string]int{}
	for rows.Next() {
		var (
			s0      book.Session
			onDate  time.Time
			started time.Time
			ended   *time.Time
			weight  *float64
		)
		if err := rows.Scan(&s0.ID, &onDate, &s0.RoutineID, &s0.RoutineName,
			&started, &ended, &s0.Note, &weight); err != nil {
			return out, fmt.Errorf("세션 읽기: %w", err)
		}
		s0.Date = onDate.Format("2006-01-02")
		s0.StartedAt = started.UnixMilli()
		if ended != nil {
			ms := ended.UnixMilli()
			s0.EndedAt = &ms
		}
		s0.BodyWeight = weight
		s0.Exercises = []book.Exercise{}
		index[s0.ID] = len(out.Sessions)
		out.Sessions = append(out.Sessions, s0)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("세션 순회: %w", err)
	}

	exRows, err := s.pool.Query(ctx, `
		select e.id, e.session_id, coalesce(e.client_id,''), e.name, coalesce(e.note,'')
		  from workout_exercises e
		 order by e.session_id, e.ord`)
	if err != nil {
		return out, fmt.Errorf("종목 조회: %w", err)
	}
	defer exRows.Close()

	// 세트를 붙일 때 종목을 찾기 위한 것. 종목 id 는 DB 가 발급한 bigint 다.
	where := map[int64][2]int{} // exercise id → (session index, exercise index)
	for exRows.Next() {
		var (
			id        int64
			sessionID string
			e         book.Exercise
		)
		if err := exRows.Scan(&id, &sessionID, &e.ID, &e.Name, &e.Note); err != nil {
			return out, fmt.Errorf("종목 읽기: %w", err)
		}
		si, ok := index[sessionID]
		if !ok {
			continue // 있을 수 없다(FK). 있으면 그 행은 버린다
		}
		e.Sets = []book.Set{}
		if e.ID == "" {
			// client_id 가 없는 행은 이 서비스 이전에 들어온 것이다. 화면이
			// 종목을 고칠 때 쓸 id 가 필요하므로 안정적인 값을 만들어 준다.
			e.ID = fmt.Sprintf("e-%d", id)
		}
		out.Sessions[si].Exercises = append(out.Sessions[si].Exercises, e)
		where[id] = [2]int{si, len(out.Sessions[si].Exercises) - 1}
	}
	if err := exRows.Err(); err != nil {
		return out, fmt.Errorf("종목 순회: %w", err)
	}

	setRows, err := s.pool.Query(ctx, `
		select exercise_id, weight, reps, done, warmup
		  from workout_sets
		 order by exercise_id, ord`)
	if err != nil {
		return out, fmt.Errorf("세트 조회: %w", err)
	}
	defer setRows.Close()

	for setRows.Next() {
		var (
			exerciseID int64
			set        book.Set
		)
		if err := setRows.Scan(&exerciseID, &set.Weight, &set.Reps, &set.Done, &set.Warmup); err != nil {
			return out, fmt.Errorf("세트 읽기: %w", err)
		}
		at, ok := where[exerciseID]
		if !ok {
			continue
		}
		ex := &out.Sessions[at[0]].Exercises[at[1]]
		ex.Sets = append(ex.Sets, set)
	}
	if err := setRows.Err(); err != nil {
		return out, fmt.Errorf("세트 순회: %w", err)
	}

	if err := s.loadRoutines(ctx, &out); err != nil {
		return out, err
	}
	if err := s.loadRestTarget(ctx, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) loadRoutines(ctx context.Context, out *book.Book) error {
	rows, err := s.pool.Query(ctx,
		`select id, name, exercises from routines order by sort_order, name`)
	if err != nil {
		return fmt.Errorf("루틴 조회: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r book.Routine
		if err := rows.Scan(&r.ID, &r.Name, &r.Exercises); err != nil {
			return fmt.Errorf("루틴 읽기: %w", err)
		}
		out.Routines = append(out.Routines, r)
	}
	return rows.Err()
}

func (s *Store) loadRestTarget(ctx context.Context, out *book.Book) error {
	var seconds int
	err := s.pool.QueryRow(ctx,
		`select (value->>'seconds')::int from settings
		  where scope = 'workout' and key = 'restTarget'`).Scan(&seconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // 설정한 적 없음. 화면의 기본값이 쓰인다
	}
	if err != nil {
		return fmt.Errorf("휴식 목표 조회: %w", err)
	}
	out.RestTarget = &seconds
	return nil
}

// ── 쓰기 ────────────────────────────────────────────────────────────────

// Save replaces the book. The client sends the whole thing on every change
// (debounced), so this has to be cheap for the common case: one session -
// the one being trained right now - differs and the other few hundred do not.
//
// The content hash on each session row is what makes that cheap. Sessions
// whose exercises hash the same are left completely alone; only the session
// row's own columns are upserted, which is a handful of writes instead of
// thousands.
func (s *Store) Save(ctx context.Context, b book.Book) error {
	book.Normalize(&b)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("트랜잭션 시작: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 커밋 후에는 무해하다

	// 현재 저장된 해시. 무엇을 다시 쓸지 정하는 근거다.
	stored := map[string]string{}
	rows, err := tx.Query(ctx, `select id, coalesce(content_hash,'') from workout_sessions`)
	if err != nil {
		return fmt.Errorf("해시 조회: %w", err)
	}
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			return fmt.Errorf("해시 읽기: %w", err)
		}
		stored[id] = hash
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("해시 순회: %w", err)
	}

	// 루틴이 세션보다 먼저다. 세션의 routine_id 는 존재하는 루틴만 참조하도록
	// 서브쿼리로 걸러지므로, 순서가 뒤집히면 같은 요청에 함께 온 새 루틴을
	// 가리키는 세션이 조용히 routine_id = NULL 로 저장된다.
	if err := saveRoutines(ctx, tx, b.Routines); err != nil {
		return err
	}

	sent := make(map[string]struct{}, len(b.Sessions))
	for _, session := range b.Sessions {
		sent[session.ID] = struct{}{}
		if err := saveSession(ctx, tx, session, stored[session.ID]); err != nil {
			return err
		}
	}

	// 화면이 보내지 않은 세션은 지워진 것이다. endSession 이 아무것도 체크하지
	// 않은 세션을 걸러내는 경로가 실제로 이걸 쓴다.
	for id := range stored {
		if _, ok := sent[id]; ok {
			continue
		}
		if _, err := tx.Exec(ctx, `delete from workout_sessions where id = $1`, id); err != nil {
			return fmt.Errorf("세션 삭제(%s): %w", id, err)
		}
	}

	if b.RestTarget != nil {
		if _, err := tx.Exec(ctx,
			`select set_setting('workout', 'restTarget', jsonb_build_object('seconds', $1::int))`,
			*b.RestTarget); err != nil {
			return fmt.Errorf("휴식 목표 저장: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("커밋: %w", err)
	}
	return nil
}

func saveSession(ctx context.Context, tx pgx.Tx, s book.Session, storedHash string) error {
	hash := s.Fingerprint()

	var ended *time.Time
	if s.EndedAt != nil {
		t := time.UnixMilli(*s.EndedAt)
		ended = &t
	}
	// 루틴 id 는 FK 다. 루틴이 지워졌는데 세션이 이름만 들고 있는 경우가
	// 정상이므로, 존재하지 않는 id 는 NULL 로 떨어뜨린다 - 이름은 남는다.
	if _, err := tx.Exec(ctx, `
		insert into workout_sessions
		  (id, on_date, routine_id, routine_name, started_at, ended_at, note,
		   body_weight, content_hash, updated_at)
		values ($1, $2::date,
		        (select id from routines where id = nullif($3,'')),
		        nullif($4,''), $5, $6, nullif($7,''), $8, $9, now())
		on conflict (id) do update set
		  on_date      = excluded.on_date,
		  routine_id   = excluded.routine_id,
		  routine_name = excluded.routine_name,
		  started_at   = excluded.started_at,
		  ended_at     = excluded.ended_at,
		  note         = excluded.note,
		  body_weight  = excluded.body_weight,
		  content_hash = excluded.content_hash,
		  updated_at   = now()`,
		s.ID, s.Date, s.RoutineID, s.RoutineName,
		time.UnixMilli(s.StartedAt), ended, s.Note, s.BodyWeight, hash); err != nil {
		return fmt.Errorf("세션 저장(%s): %w", s.ID, err)
	}

	// 내용이 그대로면 자식 행은 건드리지 않는다. 운동 중 저장의 거의 전부가
	// 이 분기로 빠진다.
	if hash != "" && hash == storedHash {
		return nil
	}

	// 바뀐 세션은 자식을 통째로 갈아끼운다. 종목 추가·삭제·순서 변경까지
	// 전부 diff 하는 것보다, 세션 하나 분량(보통 6종목 × 4세트)을 다시 쓰는
	// 편이 짧고 틀릴 데가 없다.
	if _, err := tx.Exec(ctx,
		`delete from workout_exercises where session_id = $1`, s.ID); err != nil {
		return fmt.Errorf("종목 정리(%s): %w", s.ID, err)
	}

	for i, e := range s.Exercises {
		var exerciseID int64
		if err := tx.QueryRow(ctx, `
			insert into workout_exercises
			  (session_id, ord, name, muscle_group, note, client_id)
			values ($1, $2, $3, $4, nullif($5,''), nullif($6,''))
			returning id`,
			s.ID, i, e.Name, muscle.Of(e.Name), e.Note, e.ID,
		).Scan(&exerciseID); err != nil {
			return fmt.Errorf("종목 저장(%s/%s): %w", s.ID, e.Name, err)
		}

		for j, set := range e.Sets {
			if _, err := tx.Exec(ctx, `
				insert into workout_sets (exercise_id, ord, weight, reps, done, warmup)
				values ($1, $2, $3, $4, $5, $6)`,
				exerciseID, j, set.Weight, set.Reps, set.Done, set.Warmup); err != nil {
				return fmt.Errorf("세트 저장(%s/%s/%d): %w", s.ID, e.Name, j, err)
			}
		}
	}
	return nil
}

// saveRoutines upserts what was sent and deletes the rest.
//
// An absent list is not an empty list: the client omits Routines entirely
// when the operator has never edited them, and its own defaults apply. Wiping
// the table on that would turn "never decided" into "decided to have none".
func saveRoutines(ctx context.Context, tx pgx.Tx, routines []book.Routine) error {
	if routines == nil {
		return nil
	}

	keep := make([]string, 0, len(routines))
	for i, r := range routines {
		if _, err := tx.Exec(ctx, `
			insert into routines (id, name, exercises, sort_order, updated_at)
			values ($1, $2, $3, $4, now())
			on conflict (id) do update set
			  name       = excluded.name,
			  exercises  = excluded.exercises,
			  sort_order = excluded.sort_order,
			  updated_at = now()`,
			r.ID, r.Name, r.Exercises, i); err != nil {
			return fmt.Errorf("루틴 저장(%s): %w", r.ID, err)
		}
		keep = append(keep, r.ID)
	}

	// 지워진 루틴. 세션의 routine_id 는 on delete set null 이라 기록은 남고
	// routine_name 으로 무엇이었는지 알 수 있다.
	if _, err := tx.Exec(ctx,
		`delete from routines where id <> all($1::text[])`, keep); err != nil {
		return fmt.Errorf("루틴 정리: %w", err)
	}
	return nil
}

// ── 룰 게이트용 ─────────────────────────────────────────────────────────

// Stats is what a proactive suggestion needs to know before it decides
// whether there is anything to say. Everything here is computed in SQL by
// the functions in db/migrations/013_queries.sql, so the answer the agent
// gets is the same one the surface would show.
type Stats struct {
	// GapDays is whole days since the last finished session. nil = 기록 없음.
	GapDays  *int            `json:"gapDays"`
	Routines []RoutineStatus `json:"routines"`
	// WeekVolume is kg per muscle group over the last 7 days.
	WeekVolume map[string]float64 `json:"weekVolume"`
	Running    *string            `json:"running"` // 진행 중인 세션 id
}

type RoutineStatus struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	LastDone  *string `json:"lastDone"` // YYYY-MM-DD, nil = 한 번도 안 함
	DoneCount int     `json:"doneCount"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	out := Stats{WeekVolume: map[string]float64{}}

	if err := s.pool.QueryRow(ctx, `select workout_gap_days()`).Scan(&out.GapDays); err != nil {
		return out, fmt.Errorf("공백 조회: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`select id, name, last_done, done_count from workout_routine_status`)
	if err != nil {
		return out, fmt.Errorf("루틴 상태 조회: %w", err)
	}
	for rows.Next() {
		var (
			r    RoutineStatus
			last *time.Time
		)
		if err := rows.Scan(&r.ID, &r.Name, &last, &r.DoneCount); err != nil {
			rows.Close()
			return out, fmt.Errorf("루틴 상태 읽기: %w", err)
		}
		if last != nil {
			d := last.Format("2006-01-02")
			r.LastDone = &d
		}
		out.Routines = append(out.Routines, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("루틴 상태 순회: %w", err)
	}

	volRows, err := s.pool.Query(ctx, `select muscle_group, volume_kg from volume_by_group(7)`)
	if err != nil {
		return out, fmt.Errorf("주간 볼륨 조회: %w", err)
	}
	for volRows.Next() {
		var (
			group  string
			volume float64
		)
		if err := volRows.Scan(&group, &volume); err != nil {
			volRows.Close()
			return out, fmt.Errorf("주간 볼륨 읽기: %w", err)
		}
		out.WeekVolume[group] = volume
	}
	volRows.Close()
	if err := volRows.Err(); err != nil {
		return out, fmt.Errorf("주간 볼륨 순회: %w", err)
	}

	err = s.pool.QueryRow(ctx, `
		select id from workout_sessions
		 where ended_at is null order by started_at desc limit 1`).Scan(&out.Running)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("진행 중 세션 조회: %w", err)
	}
	return out, nil
}
