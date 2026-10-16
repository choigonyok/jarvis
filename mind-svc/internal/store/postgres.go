package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Postgres is the store in the stack (migrations 035, 038). Message text is sealed
// with AES-GCM when a key is given: a database dump alone does not hand
// over anyone's conversations.
type Postgres struct {
	db   *pgxpool.Pool
	aead cipher.AEAD
}

func NewPostgres(db *pgxpool.Pool, key []byte) *Postgres {
	p := &Postgres{db: db}
	if len(key) == 32 {
		if block, err := aes.NewCipher(key); err == nil {
			p.aead, _ = cipher.NewGCM(block)
		}
	}
	return p
}

var _ Store = (*Postgres)(nil)

func (p *Postgres) Entity(ctx context.Context, id string) (model.Entity, error) {
	row := p.db.QueryRow(ctx, `select id, kind, room, state, version, created_at, updated_at from mind_entities where id = $1`, id)
	e, err := scanEntity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, model.ErrNotFound
	}
	return e, err
}

func (p *Postgres) Entities(ctx context.Context, kind, room string, liveOnly bool) ([]model.Entity, error) {
	rows, err := p.db.Query(ctx, `
		select id, kind, room, state, version, created_at, updated_at from mind_entities
		 where ($1 = '' or kind = $1) and ($2 = '' or room = $2)
		 order by created_at`, kind, room)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Entity{}
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		if !liveOnly || e.Live() {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

func (p *Postgres) PlansOn(ctx context.Context, from, to string) ([]model.Entity, error) {
	rows, err := p.db.Query(ctx, `
		select id, kind, room, state, version, created_at, updated_at from mind_entities
		 where kind = 'plan' and coalesce(state->>'date', '') <> '' and state->>'date' <= $2
		   and coalesce(nullif(state->>'endDate', ''), state->>'date') >= $1
		 order by id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Entity{}
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEntity(row pgx.Row) (model.Entity, error) {
	var e model.Entity
	var state []byte
	if err := row.Scan(&e.ID, &e.Kind, &e.Room, &state, &e.Version, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return e, err
	}
	return e, json.Unmarshal(state, &e.State)
}

func (p *Postgres) Put(ctx context.Context, e model.Entity, c model.Change) (model.Entity, model.Change, error) {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return e, c, err
	}
	defer tx.Rollback(ctx)

	var before []byte
	err = tx.QueryRow(ctx, `select state from mind_entities where id = $1 and version = $2 for update`, e.ID, e.Version).Scan(&before)
	switch {
	case errors.Is(err, pgx.ErrNoRows) && e.Version == 0:
		e.CreatedAt = c.At
	case errors.Is(err, pgx.ErrNoRows):
		return e, c, model.ErrConflict
	case err != nil:
		return e, c, err
	default:
		var b model.State
		if err := json.Unmarshal(before, &b); err != nil {
			return e, c, err
		}
		c.Before = &b
	}
	e.Version++
	e.UpdatedAt = c.At
	state, _ := json.Marshal(e.State)
	if e.Version == 1 {
		if _, err := tx.Exec(ctx, `insert into mind_entities (id, kind, room, state, version, created_at, updated_at) values ($1, $2, $3, $4, 1, $5, $5)`,
			e.ID, e.Kind, e.Room, state, c.At); err != nil {
			return e, c, conflictOr(err)
		}
	} else if _, err := tx.Exec(ctx, `update mind_entities set state = $2, version = $3, updated_at = $4 where id = $1`, e.ID, state, e.Version, c.At); err != nil {
		return e, c, err
	}
	if e.CreatedAt.IsZero() {
		_ = tx.QueryRow(ctx, `select created_at from mind_entities where id = $1`, e.ID).Scan(&e.CreatedAt)
	}
	c.EntityID, c.Version, c.After = e.ID, e.Version, e.State
	var beforeJSON []byte
	if c.Before != nil {
		beforeJSON, _ = json.Marshal(c.Before)
	}
	ev, _ := json.Marshal(c.Evidence)
	var obsID *int64
	if c.ObservationID != 0 {
		obsID = &c.ObservationID
	}
	if err := tx.QueryRow(ctx, `
		insert into mind_changes (entity_id, version, actor, before, after, evidence, confidence, observation_id, why, at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) returning id`,
		c.EntityID, c.Version, c.Actor, beforeJSON, state, ev, c.Confidence, obsID, c.Why, c.At).Scan(&c.ID); err != nil {
		return e, c, err
	}
	return e, c, tx.Commit(ctx)
}

// conflictOr is a duplicate key - two writers creating the same entity - as
// a conflict to retry, and anything else as itself.
func conflictOr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return model.ErrConflict
	}
	return err
}

func (p *Postgres) Changes(ctx context.Context, entityID string, limit int) ([]model.Change, error) {
	rows, err := p.db.Query(ctx, `
		select id, entity_id, version, actor, before, after, evidence, confidence, coalesce(observation_id, 0), why, at
		  from mind_changes where ($1 = '' or entity_id = $1) order by id desc limit $2`, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Change{}
	for rows.Next() {
		var c model.Change
		var before, after, ev []byte
		if err := rows.Scan(&c.ID, &c.EntityID, &c.Version, &c.Actor, &before, &after, &ev, &c.Confidence, &c.ObservationID, &c.Why, &c.At); err != nil {
			return nil, err
		}
		if len(before) > 0 {
			c.Before = &model.State{}
			_ = json.Unmarshal(before, c.Before)
		}
		_ = json.Unmarshal(after, &c.After)
		_ = json.Unmarshal(ev, &c.Evidence)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) PutMessage(ctx context.Context, m model.Message) error {
	body, sealed, err := p.seal([]byte(m.Text))
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_messages (ref, source, room, room_name, sender, is_mine, sent_at, body, sealed)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9) on conflict (ref) do nothing`,
		m.Ref, m.Source, m.Room, m.RoomName, m.Sender, m.IsMine, m.SentAt, body, sealed)
	return err
}

func (p *Postgres) PruneMessages(ctx context.Context, before time.Time) (int64, error) {
	tag, err := p.db.Exec(ctx, `delete from mind_messages where sent_at < $1`, before)
	return tag.RowsAffected(), err
}

const messageCols = `ref, source, room, room_name, sender, is_mine, sent_at, body, sealed`

func (p *Postgres) scanMessage(row pgx.Row) (model.Message, error) {
	var m model.Message
	var body []byte
	var sealed bool
	if err := row.Scan(&m.Ref, &m.Source, &m.Room, &m.RoomName, &m.Sender, &m.IsMine, &m.SentAt, &body, &sealed); err != nil {
		return m, err
	}
	text, err := p.open(body, sealed)
	m.Text = string(text)
	return m, err
}

func (p *Postgres) Message(ctx context.Context, ref string) (model.Message, error) {
	m, err := p.scanMessage(p.db.QueryRow(ctx, `select `+messageCols+` from mind_messages where ref = $1`, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, model.ErrNotFound
	}
	return m, err
}

func (p *Postgres) RoomMessages(ctx context.Context, room string, after, before time.Time, limit int) ([]model.Message, error) {
	var upper *time.Time
	if !before.IsZero() {
		upper = &before
	}
	rows, err := p.db.Query(ctx, `
		select `+messageCols+` from (
		  select `+messageCols+` from mind_messages
		   where room = $1 and sent_at > $2 and ($3::timestamptz is null or sent_at < $3)
		   order by sent_at desc, ref desc limit $4
		) m order by sent_at, ref`, room, after, upper, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Message{}
	for rows.Next() {
		m, err := p.scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *Postgres) RoomMessagesFrom(ctx context.Context, room string, after, before time.Time, limit int) ([]model.Message, error) {
	var upper *time.Time
	if !before.IsZero() {
		upper = &before
	}
	rows, err := p.db.Query(ctx, `
		select `+messageCols+` from mind_messages
		 where room = $1 and sent_at > $2 and ($3::timestamptz is null or sent_at < $3)
		 order by sent_at, ref limit $4`, room, after, upper, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Message{}
	for rows.Next() {
		m, err := p.scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *Postgres) Rooms(ctx context.Context) ([]Room, error) {
	rows, err := p.db.Query(ctx, `
		select m.room, max(m.room_name), max(m.source), max(m.sent_at), coalesce(r.interpreted, 'epoch'),
		       count(*), bool_and(m.is_mine),
		       count(*) filter (where m.sent_at > coalesce(r.interpreted, 'epoch'))
		  from mind_messages m left join mind_rooms r on r.room = m.room
		 group by m.room, r.interpreted
		 order by max(m.sent_at) desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Room{}
	for rows.Next() {
		var r Room
		if err := rows.Scan(&r.Room, &r.Name, &r.Source, &r.LastAt, &r.Interpreted, &r.Messages, &r.OnlyMine, &r.Uninterpreted); err != nil {
			return nil, err
		}
		if r.Interpreted.Equal(time.Unix(0, 0)) {
			r.Interpreted = time.Time{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) MarkRoom(ctx context.Context, room string, through time.Time) error {
	_, err := p.db.Exec(ctx, `
		insert into mind_rooms (room, interpreted) values ($1, $2)
		on conflict (room) do update set interpreted = greatest(mind_rooms.interpreted, excluded.interpreted)`, room, through)
	return err
}

func (p *Postgres) SetTimer(ctx context.Context, t model.Timer) error {
	_, err := p.db.Exec(ctx, `
		insert into mind_timers (skill, entity_id, name, fire_at) values ($1, $2, $3, $4)
		on conflict (skill, entity_id, name) do update set fire_at = excluded.fire_at, fired_at = null`,
		t.Skill, t.EntityID, t.Name, t.FireAt)
	return err
}

func (p *Postgres) CancelTimers(ctx context.Context, skill, entityID string) error {
	_, err := p.db.Exec(ctx, `delete from mind_timers where skill = $1 and entity_id = $2`, skill, entityID)
	return err
}

func (p *Postgres) timers(ctx context.Context, q string, args ...any) ([]model.Timer, error) {
	rows, err := p.db.Query(ctx, `select id, skill, entity_id, name, fire_at, fired_at from mind_timers `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Timer{}
	for rows.Next() {
		var t model.Timer
		if err := rows.Scan(&t.ID, &t.Skill, &t.EntityID, &t.Name, &t.FireAt, &t.FiredAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) DueTimers(ctx context.Context, now time.Time) ([]model.Timer, error) {
	return p.timers(ctx, `where fired_at is null and fire_at <= $1 order by fire_at`, now)
}

func (p *Postgres) Timers(ctx context.Context) ([]model.Timer, error) {
	return p.timers(ctx, `order by fire_at`)
}

func (p *Postgres) FireTimer(ctx context.Context, id int64, at time.Time) error {
	_, err := p.db.Exec(ctx, `update mind_timers set fired_at = $2 where id = $1`, id, at)
	return err
}

func (p *Postgres) PutIntent(ctx context.Context, in model.Intent) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_intents (id, entity_id, kind, status, body, created_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set status = excluded.status, body = excluded.body`,
		in.ID, in.EntityID, in.Kind, in.Status, body, in.CreatedAt)
	return err
}

func (p *Postgres) intents(ctx context.Context, q string, args ...any) ([]model.Intent, error) {
	rows, err := p.db.Query(ctx, `select body from mind_intents `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Intent{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var in model.Intent
		if err := json.Unmarshal(body, &in); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (p *Postgres) Intent(ctx context.Context, id string) (model.Intent, error) {
	out, err := p.intents(ctx, `where id = $1`, id)
	if err != nil {
		return model.Intent{}, err
	}
	if len(out) == 0 {
		return model.Intent{}, model.ErrNotFound
	}
	return out[0], nil
}

func (p *Postgres) Intents(ctx context.Context, limit int) ([]model.Intent, error) {
	return p.intents(ctx, `order by created_at desc, id desc limit $1`, limit)
}

func (p *Postgres) IntentsFor(ctx context.Context, entityID string) ([]model.Intent, error) {
	return p.intents(ctx, `where entity_id = $1 order by created_at desc, id desc`, entityID)
}

func (p *Postgres) IntentsSince(ctx context.Context, t time.Time) ([]model.Intent, error) {
	return p.intents(ctx, `where created_at >= $1 order by created_at desc, id desc`, t)
}

func (p *Postgres) Cursor(ctx context.Context, name string) (int64, error) {
	var v int64
	err := p.db.QueryRow(ctx, `select value from mind_cursors where name = $1`, name).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

func (p *Postgres) SetCursor(ctx context.Context, name string, v int64) error {
	_, err := p.db.Exec(ctx, `insert into mind_cursors (name, value) values ($1, $2) on conflict (name) do update set value = excluded.value`, name, v)
	return err
}

func (p *Postgres) PutFact(ctx context.Context, f model.Fact) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_facts (id, room, body, status, next_try, created_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set body = excluded.body, status = excluded.status, next_try = excluded.next_try`,
		f.ID, f.Room, body, f.Status, f.NextTry, f.CreatedAt)
	return err
}

func (p *Postgres) facts(ctx context.Context, q string, args ...any) ([]model.Fact, error) {
	rows, err := p.db.Query(ctx, `select body from mind_facts `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Fact{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var f model.Fact
		if err := json.Unmarshal(body, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (p *Postgres) Fact(ctx context.Context, id string) (model.Fact, error) {
	fs, err := p.facts(ctx, `where id = $1`, id)
	if err != nil {
		return model.Fact{}, err
	}
	if len(fs) == 0 {
		return model.Fact{}, model.ErrNotFound
	}
	return fs[0], nil
}

func (p *Postgres) Facts(ctx context.Context, limit int) ([]model.Fact, error) {
	return p.facts(ctx, `order by created_at desc, id desc limit $1`, limit)
}

func (p *Postgres) DueFacts(ctx context.Context, now time.Time, limit int) ([]model.Fact, error) {
	return p.facts(ctx, `where status = 'pending' and next_try <= $1 order by created_at, id limit $2`, now, limit)
}

func (p *Postgres) FactsWith(ctx context.Context, relations ...string) ([]model.Fact, error) {
	up := make([]string, len(relations))
	for i, r := range relations {
		up[i] = strings.ToUpper(r)
	}
	return p.facts(ctx, `where upper(body->>'relation') = any($1) order by created_at desc, id desc`, up)
}

func (p *Postgres) FactsOn(ctx context.Context, relation string, from, to time.Time) ([]model.Fact, error) {
	return p.facts(ctx, `where body->>'relation' = $1 and created_at >= $2 and created_at < $3 order by created_at, id`, relation, from, to)
}

func (p *Postgres) RecordHash(ctx context.Context, key string) (string, error) {
	var h string
	err := p.db.QueryRow(ctx, `select hash from mind_records where key = $1`, key).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return h, err
}

func (p *Postgres) PutRecordHash(ctx context.Context, key, hash string, at time.Time) error {
	_, err := p.db.Exec(ctx, `insert into mind_records (key, hash, sent_at) values ($1, $2, $3)
		on conflict (key) do update set hash = excluded.hash, sent_at = excluded.sent_at`, key, hash, at)
	return err
}

func (p *Postgres) PutAlias(ctx context.Context, a model.Alias) error {
	_, err := p.db.Exec(ctx, `
		insert into mind_aliases (name, kind, node, seen, at) values ($1, $2, $3, 1, $4)
		on conflict (name) do update set node = excluded.node, seen = mind_aliases.seen + 1, at = excluded.at,
		  kind = case when excluded.kind = '' then mind_aliases.kind else excluded.kind end`,
		a.Name, a.Kind, a.Node, a.At)
	return err
}

func (p *Postgres) Aliases(ctx context.Context) ([]model.Alias, error) {
	rows, err := p.db.Query(ctx, `select name, kind, node, seen, at from mind_aliases order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Alias{}
	for rows.Next() {
		var a model.Alias
		if err := rows.Scan(&a.Name, &a.Kind, &a.Node, &a.Seen, &a.At); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) seal(plain []byte) ([]byte, bool, error) {
	if p.aead == nil {
		return plain, false, nil
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, false, err
	}
	return p.aead.Seal(nonce, nonce, plain, nil), true, nil
}

func (p *Postgres) open(body []byte, sealed bool) ([]byte, error) {
	if !sealed {
		return body, nil
	}
	if p.aead == nil {
		return nil, errors.New("봉해진 메시지인데 MIND_CONTENT_KEY 가 없습니다")
	}
	n := p.aead.NonceSize()
	if len(body) < n {
		return nil, errors.New("봉해진 메시지가 잘렸습니다")
	}
	return p.aead.Open(nil, body[:n], body[n:], nil)
}

func (p *Postgres) policies(ctx context.Context, q string, args ...any) ([]model.PolicyStat, error) {
	rows, err := p.db.Query(ctx, `select skill, context_key, shown, approved, rejected, undone, last, updated_at from mind_policy_stats `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PolicyStat{}
	for rows.Next() {
		var s model.PolicyStat
		if err := rows.Scan(&s.Skill, &s.Context, &s.Shown, &s.Approved, &s.Rejected, &s.Undone, &s.Last, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) Policy(ctx context.Context, skill, context string) (model.PolicyStat, error) {
	out, err := p.policies(ctx, `where skill = $1 and context_key = $2`, skill, context)
	if err != nil || len(out) == 0 {
		return model.PolicyStat{Skill: skill, Context: context}, err
	}
	return out[0], nil
}

func (p *Postgres) Policies(ctx context.Context) ([]model.PolicyStat, error) {
	return p.policies(ctx, `order by skill, context_key`)
}

func (p *Postgres) CountPolicy(ctx context.Context, skill, context, outcome string, at time.Time) error {
	col := map[string]string{model.OutcomeShown: "shown", model.OutcomeApproved: "approved", model.OutcomeRejected: "rejected", model.OutcomeUndone: "undone"}[outcome]
	if col == "" {
		return errors.New("모르는 결과 " + outcome)
	}
	last := "last"
	if outcome != model.OutcomeShown {
		last = "'" + outcome + "'"
	}
	_, err := p.db.Exec(ctx, `
		insert into mind_policy_stats (skill, context_key, `+col+`, last, updated_at)
		values ($1, $2, 1, $3, $4)
		on conflict (skill, context_key) do update set `+col+` = mind_policy_stats.`+col+` + 1,
			last = `+strings.ReplaceAll(last, "last", "mind_policy_stats.last")+`, updated_at = excluded.updated_at`,
		skill, context, map[bool]string{true: "", false: outcome}[outcome == model.OutcomeShown], at)
	return err
}

func (p *Postgres) ResetPolicy(ctx context.Context) error {
	_, err := p.db.Exec(ctx, `delete from mind_policy_stats`)
	return err
}

func (p *Postgres) PutOccurrence(ctx context.Context, o model.Occurrence) error {
	body, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_occurrences (id, kind, at, body) values ($1, $2, $3, $4)
		on conflict (id) do update set kind = excluded.kind, at = excluded.at, body = excluded.body`,
		o.ID, o.Kind, o.At, body)
	return err
}

func (p *Postgres) occurrences(ctx context.Context, q string, args ...any) ([]model.Occurrence, error) {
	rows, err := p.db.Query(ctx, `select body from mind_occurrences `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Occurrence{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var o model.Occurrence
		if err := json.Unmarshal(body, &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *Postgres) Occurrence(ctx context.Context, id string) (model.Occurrence, error) {
	out, err := p.occurrences(ctx, `where id = $1`, id)
	if err != nil {
		return model.Occurrence{}, err
	}
	if len(out) == 0 {
		return model.Occurrence{}, model.ErrNotFound
	}
	return out[0], nil
}

func (p *Postgres) Occurrences(ctx context.Context, from, to time.Time, kinds ...string) ([]model.Occurrence, error) {
	if len(kinds) == 0 {
		return p.occurrences(ctx, `where at >= $1 and at < $2 order by at, id`, from, to)
	}
	return p.occurrences(ctx, `where at >= $1 and at < $2 and kind = any($3) order by at, id`, from, to, kinds)
}

func (p *Postgres) PutLink(ctx context.Context, l model.Link) error {
	body, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `
		insert into mind_links (from_id, to_id, rel, state, body, updated_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (from_id, to_id, rel) do update set state = excluded.state, body = excluded.body, updated_at = excluded.updated_at`,
		l.From, l.To, l.Rel, l.State, body, l.UpdatedAt)
	return err
}

func (p *Postgres) links(ctx context.Context, q string, args ...any) ([]model.Link, error) {
	rows, err := p.db.Query(ctx, `select body from mind_links `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Link{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var l model.Link
		if err := json.Unmarshal(body, &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (p *Postgres) LinksOf(ctx context.Context, id string) ([]model.Link, error) {
	return p.links(ctx, `where from_id = $1 or to_id = $1 order by updated_at, from_id, to_id, rel`, id)
}

func (p *Postgres) LinksSince(ctx context.Context, t time.Time) ([]model.Link, error) {
	return p.links(ctx, `where updated_at >= $1 order by updated_at, from_id, to_id, rel`, t)
}

func (p *Postgres) ThingClass(ctx context.Context, name string) (string, error) {
	var c string
	err := p.db.QueryRow(ctx, `select class from mind_thing_classes where name = $1`, name).Scan(&c)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", model.ErrNotFound
	}
	return c, err
}

func (p *Postgres) PutThingClass(ctx context.Context, name, class string) error {
	_, err := p.db.Exec(ctx, `insert into mind_thing_classes (name, class) values ($1, $2)
		on conflict (name) do update set class = excluded.class, at = now()`, name, class)
	return err
}

func (p *Postgres) LinkCache(ctx context.Context, class, planKind string) (model.Judgement, error) {
	j := model.Judgement{Class: class}
	err := p.db.QueryRow(ctx, `select rel, plausibility, why from mind_link_cache where thing_class = $1 and plan_kind = $2`,
		class, planKind).Scan(&j.Rel, &j.Plausibility, &j.Why)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Judgement{}, model.ErrNotFound
	}
	return j, err
}

func (p *Postgres) PutLinkCache(ctx context.Context, planKind string, j model.Judgement) error {
	_, err := p.db.Exec(ctx, `insert into mind_link_cache (thing_class, plan_kind, rel, plausibility, why) values ($1, $2, $3, $4, $5)
		on conflict (thing_class, plan_kind) do update set rel = excluded.rel, plausibility = excluded.plausibility, why = excluded.why, at = now()`,
		j.Class, planKind, j.Rel, j.Plausibility, j.Why)
	return err
}

func (p *Postgres) CountPattern(ctx context.Context, pt model.Pattern) error {
	var seen *time.Time
	if !pt.LastSeen.IsZero() {
		seen = &pt.LastSeen
	}
	_, err := p.db.Exec(ctx, `
		insert into mind_pattern_counts (kind_a, rel, kind_b, gap_bucket, support, total, last_seen) values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (kind_a, rel, kind_b, gap_bucket) do update set support = mind_pattern_counts.support + excluded.support,
			total = mind_pattern_counts.total + excluded.total,
			last_seen = greatest(mind_pattern_counts.last_seen, excluded.last_seen)`,
		pt.KindA, pt.Rel, pt.KindB, pt.Bucket, pt.Support, pt.Total, seen)
	return err
}

func (p *Postgres) Patterns(ctx context.Context, kindA string) ([]model.Pattern, error) {
	rows, err := p.db.Query(ctx, `select kind_a, rel, kind_b, gap_bucket, support, total, last_seen from mind_pattern_counts
		where $1 = '' or kind_a = $1 order by kind_a, rel, kind_b, gap_bucket`, kindA)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Pattern{}
	for rows.Next() {
		var pt model.Pattern
		var seen *time.Time
		if err := rows.Scan(&pt.KindA, &pt.Rel, &pt.KindB, &pt.Bucket, &pt.Support, &pt.Total, &seen); err != nil {
			return nil, err
		}
		if seen != nil {
			pt.LastSeen = *seen
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}

func (p *Postgres) ResetPatterns(ctx context.Context) error {
	_, err := p.db.Exec(ctx, `delete from mind_pattern_counts`)
	return err
}

func (p *Postgres) PutNote(ctx context.Context, n model.Note) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `insert into mind_notes (id, status, scope, body, version, updated_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set status = excluded.status, scope = excluded.scope, body = excluded.body, version = excluded.version, updated_at = excluded.updated_at`,
		n.ID, n.Status, n.Scope, body, n.Version, n.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into mind_note_versions (note_id, version, body, at) values ($1, $2, $3, $4)
		on conflict (note_id, version) do update set body = excluded.body, at = excluded.at`, n.ID, n.Version, body, n.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanBodies[T any](ctx context.Context, p *Postgres, q string, args ...any) ([]T, error) {
	rows, err := p.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) Note(ctx context.Context, id string) (model.Note, error) {
	out, err := scanBodies[model.Note](ctx, p, `select body from mind_notes where id = $1`, id)
	if err != nil {
		return model.Note{}, err
	}
	if len(out) == 0 {
		return model.Note{}, model.ErrNotFound
	}
	return out[0], nil
}

func (p *Postgres) Notes(ctx context.Context) ([]model.Note, error) {
	return scanBodies[model.Note](ctx, p, `select body from mind_notes order by body->>'createdAt', id`)
}

func (p *Postgres) NoteVersions(ctx context.Context, id string) ([]model.Note, error) {
	return scanBodies[model.Note](ctx, p, `select body from mind_note_versions where note_id = $1 order by version`, id)
}

func (p *Postgres) PutPrediction(ctx context.Context, pr model.Prediction) error {
	body, err := json.Marshal(pr)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `insert into mind_predictions (id, target, due, scored, body, created_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (id) do update set target = excluded.target, due = excluded.due, scored = excluded.scored, body = excluded.body`,
		pr.ID, pr.Target, pr.Due, pr.ScoredAt != nil, body, pr.CreatedAt)
	return err
}

func (p *Postgres) Prediction(ctx context.Context, id string) (model.Prediction, error) {
	out, err := scanBodies[model.Prediction](ctx, p, `select body from mind_predictions where id = $1`, id)
	if err != nil {
		return model.Prediction{}, err
	}
	if len(out) == 0 {
		return model.Prediction{}, model.ErrNotFound
	}
	return out[0], nil
}

func (p *Postgres) Predictions(ctx context.Context, from, to time.Time) ([]model.Prediction, error) {
	return scanBodies[model.Prediction](ctx, p, `select body from mind_predictions where due >= $1 and due < $2 order by due, id`, from, to)
}

func (p *Postgres) DuePredictions(ctx context.Context, now time.Time) ([]model.Prediction, error) {
	return scanBodies[model.Prediction](ctx, p, `select body from mind_predictions where not scored and due <= $1 order by due, id`, now)
}

func (p *Postgres) PutExpectation(ctx context.Context, e model.Expectation) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `insert into mind_expectations (prediction_id, match_key, due, state, body) values ($1, $2, $3, $4, $5)
		on conflict (prediction_id, match_key) do update set due = excluded.due, state = excluded.state, body = excluded.body`,
		e.PredictionID, e.Key, e.Due, e.State, body)
	return err
}

func (p *Postgres) OpenExpectations(ctx context.Context, key string) ([]model.Expectation, error) {
	return scanBodies[model.Expectation](ctx, p, `select body from mind_expectations where match_key = $1 and state = 'open' order by prediction_id`, key)
}

func (p *Postgres) Expectations(ctx context.Context, predictionID string) ([]model.Expectation, error) {
	return scanBodies[model.Expectation](ctx, p, `select body from mind_expectations where prediction_id = $1 order by match_key`, predictionID)
}

func (p *Postgres) AddReplySituation(ctx context.Context, r model.ReplySituation) error {
	_, err := p.db.Exec(ctx, `insert into mind_reply_examples (kind, room, in_ref, out_ref, gap_days, at) values ($1, $2, $3, $4, $5, $6)
		on conflict (kind, in_ref) do nothing`, r.Kind, r.Room, r.InRef, r.OutRef, r.GapDays, r.At)
	return err
}

func (p *Postgres) AnswerReplySituations(ctx context.Context, room, outRef string) error {
	_, err := p.db.Exec(ctx, `update mind_reply_examples set out_ref = $2 where room = $1 and out_ref = ''`, room, outRef)
	return err
}

func (p *Postgres) ReplyExamples(ctx context.Context, kind string, limit int) ([]model.ReplySituation, error) {
	rows, err := p.db.Query(ctx, `select kind, room, in_ref, out_ref, gap_days, at from mind_reply_examples
		where kind = $1 and out_ref <> '' order by at desc limit $2`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ReplySituation{}
	for rows.Next() {
		var r model.ReplySituation
		if err := rows.Scan(&r.Kind, &r.Room, &r.InRef, &r.OutRef, &r.GapDays, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) ResetReplySituations(ctx context.Context) error {
	_, err := p.db.Exec(ctx, `delete from mind_reply_examples`)
	return err
}

func (p *Postgres) AddUsage(ctx context.Context, u model.Usage) error {
	_, err := p.db.Exec(ctx, `insert into mind_llm_usage (day, purpose, calls, usd, in_tokens, out_tokens) values ($1, $2, $3, $4, $5, $6)
		on conflict (day, purpose) do update set calls = mind_llm_usage.calls + excluded.calls, usd = mind_llm_usage.usd + excluded.usd,
			in_tokens = mind_llm_usage.in_tokens + excluded.in_tokens, out_tokens = mind_llm_usage.out_tokens + excluded.out_tokens`,
		u.Day, u.Purpose, u.Calls, u.USD, u.In, u.Out)
	return err
}

func (p *Postgres) Usages(ctx context.Context, from string) ([]model.Usage, error) {
	rows, err := p.db.Query(ctx, `select to_char(day, 'YYYY-MM-DD'), purpose, calls, usd, in_tokens, out_tokens from mind_llm_usage
		where day >= $1::date order by day, purpose`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Usage{}
	for rows.Next() {
		var u model.Usage
		if err := rows.Scan(&u.Day, &u.Purpose, &u.Calls, &u.USD, &u.In, &u.Out); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (p *Postgres) AddObservations(ctx context.Context, obs []model.Observation) error {
	for _, o := range obs {
		c, _ := json.Marshal(o.Context)
		if _, err := p.db.Exec(ctx, `insert into mind_observations (subject_type, subject_id, dimension, polarity, score, detail, context, source_id, at)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, o.SubjectType, o.Subject, o.Dimension, o.Polarity, o.Score, o.Detail, c, o.Source, o.At); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) ForgetObservations(ctx context.Context, source string) error {
	_, err := p.db.Exec(ctx, `delete from mind_observations where source_id = $1`, source)
	return err
}

func (p *Postgres) ObservationsOn(ctx context.Context, dimension string) ([]model.Observation, error) {
	rows, err := p.db.Query(ctx, `select id, subject_type, subject_id, dimension, polarity, score, detail, context, source_id, at
		from mind_observations where dimension = $1 order by at, id`, dimension)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Observation{}
	for rows.Next() {
		var o model.Observation
		var c []byte
		if err := rows.Scan(&o.ID, &o.SubjectType, &o.Subject, &o.Dimension, &o.Polarity, &o.Score, &o.Detail, &c, &o.Source, &o.At); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(c, &o.Context)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *Postgres) Attributes(ctx context.Context, subjectType, subject string) ([]model.Attribute, error) {
	rows, err := p.db.Query(ctx, `select subject_type, subject_id, dimension, value, evidence_count from mind_attributes
		where subject_type = $1 and subject_id = $2 order by dimension`, subjectType, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Attribute{}
	for rows.Next() {
		var a model.Attribute
		var v float32
		if err := rows.Scan(&a.SubjectType, &a.Subject, &a.Dimension, &v, &a.Count); err != nil {
			return nil, err
		}
		a.Value = float64(v)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) PutPreference(ctx context.Context, pr model.Preference) error {
	body, err := json.Marshal(pr)
	if err != nil {
		return err
	}
	_, err = p.db.Exec(ctx, `insert into mind_preferences (dimension, polarity, scope, status, body, updated_at) values ($1, $2, $3, $4, $5, $6)
		on conflict (dimension, polarity, scope) do update set status = excluded.status, body = excluded.body, updated_at = excluded.updated_at`,
		pr.Dimension, pr.Polarity, pr.Scope, pr.Status, body, pr.UpdatedAt)
	return err
}

func (p *Postgres) Preferences(ctx context.Context) ([]model.Preference, error) {
	return scanBodies[model.Preference](ctx, p, `select body from mind_preferences order by dimension, scope`)
}
