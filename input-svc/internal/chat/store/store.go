// Package store keeps the transcript in Postgres.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/input-svc/internal/chat/turn"
)

type Store struct {
	pool *pgxpool.Pool
	// mu guards seq only. The id has to be unique, and two turns appended in
	// the same millisecond would otherwise collide.
	mu  sync.Mutex
	seq int64
}

// New is a store on pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// After runs in the transaction of a turn stored or changed, with the turn as
// it now stands: the record of it and the turn land together.
type After func(ctx context.Context, tx pgx.Tx, t turn.Turn) error

// Resume seeds the id sequence from what is already stored, so ids minted
// after a restart do not collide with ones written in the same millisecond
// before it.
func (s *Store) Resume(ctx context.Context) error {
	var count int64
	if err := s.pool.QueryRow(ctx, `select count(*) from input.turns`).Scan(&count); err != nil {
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
const columns = `
	select id, role,
	       to_char(at at time zone $1, 'HH24:MI'),
	       to_char(at, 'YYYY-MM-DD"T"HH24:MI:SS.USTZH:TZM'),
	       coalesce(paragraphs, '{}'),
	       coalesce(body, ''),
	       coalesce(proposal_id, ''),
	       coalesce(session_id, ''),
	       coalesce(images, '{}'),
	       coalesce(ref::text, ''),
	       coalesce(origin::text, '')`

const selectList = columns + `
	  from input.turns`

// All returns the whole transcript, oldest first.
//
// There is no pagination and no limit. The console renders the entire thread
// on load - that is the product, one long 1:1 conversation - and a transcript
// that quietly stops at 200 turns would read as history having been lost. When
// this gets slow the fix is a window the UI asks for, not a cap the service
// imposes without saying so.
// All is a thread's turns after since (zero = the whole thread).
func (s *Store) All(ctx context.Context, tz, thread string, since time.Time) ([]turn.Turn, error) {
	rows, err := s.pool.Query(ctx, selectList+` where thread = $2 and at > $3 order by at, id`, tz, thread, since)
	if err != nil {
		return nil, fmt.Errorf("턴 조회: %w", err)
	}
	defer rows.Close()

	out := []turn.Turn{}
	for rows.Next() {
		var t turn.Turn
		var ref, origin string
		if err := rows.Scan(&t.ID, &t.Role, &t.At, &t.AtISO, &t.Paragraphs,
			&t.Text, &t.ProposalID, &t.SessionID, &t.Images, &ref, &origin); err != nil {
			return nil, fmt.Errorf("턴 읽기: %w", err)
		}
		t.Ref = readRef(ref)
		t.Origin = readOrigin(origin)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Window is the newest `limit` turns of a thread before `before` (zero: the
// newest of all), oldest first, and whether older ones remain. The console
// opens on the newest page and asks for the one above as the reader scrolls
// up - the thread is months long, and all of it at once was the page's
// slowest part.
//
// The cursor is the id of the oldest turn on the page below (`beforeID`):
// the page is what comes before that turn in the thread's order. A time
// (`before`) is taken too, for callers that have one - but an id survives a
// trip through URLs that a timestamp's "+09:00" did not (2026-10-19: the
// console's cursor arrived with a space for the plus).
func (s *Store) Window(ctx context.Context, tz, thread string, before time.Time, beforeID string, limit int) ([]turn.Turn, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	var beforeArg any
	if !before.IsZero() {
		beforeArg = before
	}
	var idArg any
	if beforeID != "" {
		idArg = beforeID
	}
	rows, err := s.pool.Query(ctx, selectList+`
		 where thread = $2
		   and ($3::timestamptz is null or at < $3)
		   and ($5::text is null or (at, id) < (select o.at, o.id from input.turns o where o.id = $5))
		 order by at desc, id desc limit $4`, tz, thread, beforeArg, limit+1, idArg)
	if err != nil {
		return nil, false, fmt.Errorf("턴 조회: %w", err)
	}
	defer rows.Close()
	var out []turn.Turn
	for rows.Next() {
		t, err := scanRow(rows)
		if err != nil {
			return nil, false, fmt.Errorf("턴 읽기: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out == nil {
		out = []turn.Turn{}
	}
	return out, more, nil
}

// Append stores one turn and returns it as stored, with the id and the clock
// the service minted.
//
// The id prefix says who spoke ('u' or 'a'), which is what the old file-backed
// store did; the record surface reads the epoch back out of this format.
func (s *Store) Append(ctx context.Context, t turn.Turn, tz string, after After) (turn.Turn, error) {
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

	var images any
	if len(t.Images) > 0 {
		images = t.Images
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return turn.Turn{}, fmt.Errorf("턴 저장: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 커밋 후에는 무해하다
	saved, err := scanRow(tx.QueryRow(ctx, `
		with inserted as (
		  insert into input.turns (id, role, at, paragraphs, body, proposal_id, session_id, thread, images, ref, origin)
		  values ($2, $3, now(), $4, nullif($5,''), nullif($6,''), nullif($7,''), $8, $9, $10::jsonb, $11::jsonb)
		  returning *
		)
		`+columns+`
		  from inserted`,
		tz, id, string(t.Role), paragraphs, t.Text, t.ProposalID, t.SessionID, t.Thread, images, refJSON(t.Ref), originJSON(t.Origin)))
	if err != nil {
		return turn.Turn{}, fmt.Errorf("턴 저장: %w", err)
	}
	saved.Thread = t.Thread
	if after != nil {
		if err := after(ctx, tx, saved); err != nil {
			return turn.Turn{}, err
		}
	}
	return saved, tx.Commit(ctx)
}

// Update replaces what a turn says and brings it to the present: it is a
// job's card given its newest word, and the newest word belongs at the
// bottom of the thread, not wherever the run first spoke. Only turns with an
// origin may be rewritten - the conversation's own words stand as said.
// ErrNoTurn when no such turn is in this thread.
func (s *Store) Update(ctx context.Context, thread, id string, paragraphs []string, tz string, after After) (turn.Turn, error) {
	var p any
	if len(paragraphs) > 0 {
		p = paragraphs
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return turn.Turn{}, fmt.Errorf("턴 갱신: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 커밋 후에는 무해하다
	saved, err := scanRow(tx.QueryRow(ctx, `
		with updated as (
		  update input.turns set paragraphs = $4, at = now()
		   where id = $2 and thread = $3 and origin is not null
		  returning *
		)
		`+columns+`
		  from updated`, tz, id, thread, p))
	if errors.Is(err, pgx.ErrNoRows) {
		return turn.Turn{}, ErrNoTurn
	}
	if err != nil {
		return turn.Turn{}, fmt.Errorf("턴 갱신: %w", err)
	}
	saved.Thread = thread
	if after != nil {
		if err := after(ctx, tx, saved); err != nil {
			return turn.Turn{}, err
		}
	}
	return saved, tx.Commit(ctx)
}

// ErrNoTurn: no turn with that id in that thread that may be rewritten.
var ErrNoTurn = errors.New("그런 턴이 없습니다")

type scanner interface {
	Scan(dest ...any) error
}

func scanRow(r scanner) (turn.Turn, error) {
	var t turn.Turn
	var ref, origin string
	if err := r.Scan(&t.ID, &t.Role, &t.At, &t.AtISO,
		&t.Paragraphs, &t.Text, &t.ProposalID, &t.SessionID, &t.Images, &ref, &origin); err != nil {
		return turn.Turn{}, err
	}
	t.Ref = readRef(ref)
	t.Origin = readOrigin(origin)
	return t, nil
}

func originJSON(o *turn.Origin) any {
	if o == nil || o.Kind == "" {
		return nil
	}
	b, _ := json.Marshal(o)
	return string(b)
}

func readOrigin(raw string) *turn.Origin {
	if raw == "" {
		return nil
	}
	var o turn.Origin
	if json.Unmarshal([]byte(raw), &o) != nil || o.Kind == "" {
		return nil
	}
	return &o
}

// refJSON is the ref column's value: NULL when the turn quotes nothing.
func refJSON(r *turn.Ref) any {
	if r == nil || r.ID == "" {
		return nil
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func readRef(raw string) *turn.Ref {
	if raw == "" {
		return nil
	}
	var r turn.Ref
	if json.Unmarshal([]byte(raw), &r) != nil || r.ID == "" {
		return nil
	}
	return &r
}
