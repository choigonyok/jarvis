package uniple

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/notify"
)

// The couple's space kept in this Postgres (schema couple, migration 107)
// instead of in uniple. The tables have uniple's shape, so the client above
// does not change: what it asked of uniple's PostgREST - a table, filters, an
// order, the rows with their items - this answers from the local tables with
// the same JSON, and what uniple's server did on its own (ids and times, the
// news of what each did, the D-day's entries on the calendar, the photos'
// bytes) is done here (local_rpc.go, local_media.go).
//
// Each person reads and writes as themselves: whoever the request is from
// (As) is "me", so the guest's check is the guest's and the operator's
// entries are the partner's to them, with nothing turned around afterwards.

// LocalConfig is what the local space needs besides the database.
type LocalConfig struct {
	// Notify carries a push to the other person when one does what the app
	// told the partner about (a habit checked, a photo on the map, a snap).
	Notify *notify.Client
}

type local struct {
	pool   *pgxpool.Pool
	notify *notify.Client

	// cols is every column of the couple tables with its type, read once:
	// nothing names a table or a column in SQL that is not here.
	cols map[string]map[string]string

	mu    sync.Mutex
	block string
	space string
	sides map[string]string // owner | guest -> user id
}

// OpenLocal opens the couple's space kept in this database. An empty one
// (nothing imported) is started with a space, two people and a calendar.
func OpenLocal(ctx context.Context, dsn string, cfg Config, lc LocalConfig) (*Client, error) {
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 해석: %w", err)
	}
	pcfg.MaxConns = 4
	// Times come out as uniple gave them, in UTC.
	pcfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("풀 생성: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("연결 확인: %w", err)
	}
	l := &local{pool: pool, notify: lc.Notify}
	if err := l.readColumns(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := l.seed(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Client{cfg: cfg, pool: pool, local: l}, nil
}

func (l *local) readColumns(ctx context.Context) error {
	rows, err := l.pool.Query(ctx, `
		select table_name, column_name, udt_name
		  from information_schema.columns
		 where table_schema = 'couple'`)
	if err != nil {
		return fmt.Errorf("couple 표 읽기: %w", err)
	}
	defer rows.Close()
	l.cols = map[string]map[string]string{}
	for rows.Next() {
		var t, c, u string
		if err := rows.Scan(&t, &c, &u); err != nil {
			return err
		}
		if l.cols[t] == nil {
			l.cols[t] = map[string]string{}
		}
		l.cols[t][c] = u
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(l.cols["calendar_events"]) == 0 {
		return errors.New("couple 표가 없습니다. 마이그레이션 107 을 적용하세요")
	}
	return nil
}

// seed starts an empty space: one space, the operator and the guest under
// placeholder names, and the calendar block every space has.
func (l *local) seed(ctx context.Context) error {
	var n int
	if err := l.pool.QueryRow(ctx, `select count(*) from couple.spaces`).Scan(&n); err != nil {
		return fmt.Errorf("커플 공간 읽기: %w", err)
	}
	if n > 0 {
		return nil
	}
	space, owner, guest := newUUID(), newUUID(), newUUID()
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`insert into couple.spaces (id) values ($1)`, []any{space}},
		{`insert into couple.profiles (id, space_id, nickname) values ($1, $3, '나'), ($2, $3, '상대')`, []any{owner, guest, space}},
		{`insert into couple.sides (side, user_id) values ('owner', $1), ('guest', $2)`, []any{owner, guest}},
		{`insert into couple.blocks (space_id, type, title, order_index, is_default) values ($1, 'calendar', $2, 0, true)`, []any{space, newBlockTitle["calendar"]}},
	} {
		if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
			return fmt.Errorf("새 커플 공간 만들기: %w", err)
		}
	}
	return tx.Commit(ctx)
}

/* ───────────────────────────── who ───────────────────────────── */

type sideKey struct{}

// As is a request made by one of the two: "partner" is the guest, anything
// else the operator. Only the local space reads it - on uniple every request
// is the operator's session.
func As(ctx context.Context, side string) context.Context {
	return context.WithValue(ctx, sideKey{}, side)
}

// Native is whether the client reads and writes as whoever asks (As). When it
// does, nothing it answers needs turning around for the guest.
func (c *Client) Native() bool { return c.local != nil }

func (l *local) who(ctx context.Context) (me, other string, err error) {
	if err := l.loadSides(ctx); err != nil {
		return "", "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if s, _ := ctx.Value(sideKey{}).(string); s == "partner" {
		return l.sides["guest"], l.sides["owner"], nil
	}
	return l.sides["owner"], l.sides["guest"], nil
}

func (l *local) loadSides(ctx context.Context) error {
	l.mu.Lock()
	known := len(l.sides) == 2
	l.mu.Unlock()
	if known {
		return nil
	}
	rows, err := l.pool.Query(ctx, `select side, user_id::text from couple.sides`)
	if err != nil {
		return fmt.Errorf("두 사람 읽기: %w", err)
	}
	defer rows.Close()
	sides := map[string]string{}
	for rows.Next() {
		var s, id string
		if err := rows.Scan(&s, &id); err != nil {
			return err
		}
		sides[s] = id
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(sides) != 2 {
		return errors.New("커플 공간에 두 사람이 없습니다(couple.sides)")
	}
	l.mu.Lock()
	l.sides = sides
	l.mu.Unlock()
	return nil
}

// calendar is the space's calendar block, the space, and whoever asks.
func (l *local) calendar(ctx context.Context, pinned string) (block, space, me string, err error) {
	me, _, err = l.who(ctx)
	if err != nil {
		return "", "", "", err
	}
	l.mu.Lock()
	block, space = l.block, l.space
	l.mu.Unlock()
	if block != "" {
		return block, space, me, nil
	}
	q := `select id::text, space_id::text from couple.blocks
	       where type = 'calendar' and archived_at is null and ($1 = '' or id::text = $1)
	       order by order_index limit 1`
	if err := l.pool.QueryRow(ctx, q, pinned).Scan(&block, &space); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", "", errors.New("커플 공간에서 캘린더를 찾지 못했습니다")
		}
		return "", "", "", err
	}
	l.mu.Lock()
	l.block, l.space = block, space
	l.mu.Unlock()
	return block, space, me, nil
}

/* ───────────────────────────── the PostgREST the client speaks ───────────────────────────── */

// rest answers one call the client would have made to uniple's PostgREST.
func (l *local) rest(ctx context.Context, method, path string, body any, out any) error {
	me, _, err := l.who(ctx)
	if err != nil {
		return err
	}
	table, raw, _ := strings.Cut(path, "?")
	if name, ok := strings.CutPrefix(table, "rpc/"); ok {
		return l.rpc(ctx, me, name, body, out)
	}
	cols, ok := l.cols[table]
	if !ok {
		return &Refused{Status: 404, Code: "PGRST205", Message: "그런 표가 없습니다: " + table}
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return &Refused{Status: 400, Message: err.Error()}
	}
	var got []byte
	switch method {
	case "GET":
		got, err = l.read(ctx, table, cols, q)
	case "POST", "PATCH", "DELETE":
		got, err = l.write(ctx, me, method, table, cols, q, body)
	default:
		return &Refused{Status: 405, Message: method}
	}
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(got, out)
}

// relations are the items a row comes with, by the column that points back.
var relations = map[string]map[string]string{
	"blocks": {
		"dday_items": "block_id", "bucket_items": "block_id", "free_items": "block_id", "map_visits": "block_id",
		"map_records": "block_id", "worldmap_visits": "block_id", "worldmap_records": "block_id", "album_photos": "block_id",
		"log_entries": "block_id", "food_entries": "block_id", "diary_entries": "block_id", "budget_dates": "block_id",
		"workout_checks": "block_id", "calendar_events": "block_id",
	},
	"diary_entries": {"diary_comments": "entry_id"},
	"budget_dates":  {"budget_expenses": "date_id"},
}

// read is a select: every column of the rows that pass, with the items the
// select names, in the order asked.
func (l *local) read(ctx context.Context, table string, cols map[string]string, q url.Values) ([]byte, error) {
	args := &sqlArgs{}
	where, err := l.filters(table, cols, q, "t", args)
	if err != nil {
		return nil, err
	}
	embeds, err := l.embeds(table, q, args)
	if err != nil {
		return nil, err
	}
	order, err := orderBy(cols, q.Get("order"), "t")
	if err != nil {
		return nil, err
	}
	sql := "select t.*" + embeds + " from couple." + table + " t" + where + order + limitOffset(q, "")
	return l.json(ctx, sql, args.list)
}

func (l *local) json(ctx context.Context, sql string, args []any) ([]byte, error) {
	var out []byte
	err := l.pool.QueryRow(ctx, "select coalesce(json_agg(r), '[]'::json) from ("+sql+") r", args...).Scan(&out)
	return out, refusedOf(err)
}

// embeds are the related rows the select asks for, each as a JSON column:
// rel(cols) in the select, with rel.order, rel.limit and rel.<column>
// filters of their own.
func (l *local) embeds(table string, q url.Values, args *sqlArgs) (string, error) {
	var b strings.Builder
	for _, part := range splitTop(q.Get("select")) {
		name, _, ok := strings.Cut(part, "(")
		if !ok {
			continue
		}
		fk, known := relations[table][name]
		cols, exists := l.cols[name]
		if !known || !exists {
			return "", &Refused{Status: 400, Code: "PGRST200", Message: "이을 수 없는 묶음입니다: " + name}
		}
		sub := url.Values{}
		for k, v := range q {
			if rest, ok := strings.CutPrefix(k, name+"."); ok {
				sub[rest] = v
			}
		}
		where, err := l.filters(name, cols, sub, "e", args)
		if err != nil {
			return "", err
		}
		link := "e." + fk + " = t.id"
		if where == "" {
			where = " where " + link
		} else {
			where += " and " + link
		}
		order, err := orderBy(cols, sub.Get("order"), "e")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, ", (select coalesce(json_agg(x), '[]'::json) from (select e.* from couple.%s e%s%s%s) x) as %s",
			name, where, order, limitOffset(sub, ""), name)
	}
	return b.String(), nil
}

type sqlArgs struct{ list []any }

func (a *sqlArgs) add(v any) string {
	a.list = append(a.list, v)
	return "$" + strconv.Itoa(len(a.list))
}

var ident = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// reserved are the query keys that are not column filters.
var reserved = map[string]bool{"select": true, "order": true, "limit": true, "offset": true, "or": true, "and": true, "on_conflict": true, "columns": true}

// filters is the where clause of a query: col=op.value for each column (two
// values for one column both hold), and or=(...) / and=(...) trees.
func (l *local) filters(table string, cols map[string]string, q url.Values, alias string, args *sqlArgs) (string, error) {
	var conds []string
	for k, vs := range q {
		if reserved[k] || strings.Contains(k, ".") {
			continue
		}
		for _, v := range vs {
			c, err := condition(cols, alias, k, v, args)
			if err != nil {
				return "", err
			}
			conds = append(conds, c)
		}
	}
	for _, op := range []string{"or", "and"} {
		for _, v := range q[op] {
			c, err := logic(cols, alias, op, v, args)
			if err != nil {
				return "", err
			}
			conds = append(conds, c)
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " where " + strings.Join(conds, " and "), nil
}

// condition is one column's test: eq, neq, gt, gte, lt, lte, in.(…), is.null
// and its not. form.
func condition(cols map[string]string, alias, col, v string, args *sqlArgs) (string, error) {
	typ, ok := cols[col]
	if !ok || !ident.MatchString(col) {
		return "", &Refused{Status: 400, Code: "42703", Message: "그런 열이 없습니다: " + col}
	}
	ref := alias + "." + col
	if rest, ok := strings.CutPrefix(v, "not."); ok {
		c, err := condition(cols, alias, col, rest, args)
		if err != nil {
			return "", err
		}
		return "not (" + c + ")", nil
	}
	op, val, ok := strings.Cut(v, ".")
	if !ok {
		return "", &Refused{Status: 400, Message: "알 수 없는 조건입니다: " + col + "=" + v}
	}
	switch op {
	case "is":
		switch val {
		case "null":
			return ref + " is null", nil
		case "true", "false":
			return ref + " is " + val, nil
		}
		return "", &Refused{Status: 400, Message: "is 는 null·true·false 만 받습니다"}
	case "in":
		inner := strings.TrimSuffix(strings.TrimPrefix(val, "("), ")")
		var list []string
		if inner != "" {
			for _, s := range strings.Split(inner, ",") {
				list = append(list, strings.Trim(s, `"`))
			}
		}
		return ref + " = any(" + args.add(list) + "::text[]::" + arrayOf(typ) + ")", nil
	}
	sym := map[string]string{"eq": "=", "neq": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
	if sym == "" {
		return "", &Refused{Status: 400, Message: "알 수 없는 연산입니다: " + op}
	}
	return ref + " " + sym + " " + args.add(val) + "::text::" + scalarOf(typ), nil
}

// logic is an or=(…)/and=(…) tree: items are col.op.value, or and(…)/or(…).
func logic(cols map[string]string, alias, op, v string, args *sqlArgs) (string, error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(v, "("), ")")
	var parts []string
	for _, item := range splitTop(inner) {
		var c string
		var err error
		switch {
		case strings.HasPrefix(item, "and("):
			c, err = logic(cols, alias, "and", strings.TrimPrefix(item, "and"), args)
		case strings.HasPrefix(item, "or("):
			c, err = logic(cols, alias, "or", strings.TrimPrefix(item, "or"), args)
		default:
			col, test, ok := strings.Cut(item, ".")
			if !ok {
				return "", &Refused{Status: 400, Message: "알 수 없는 조건입니다: " + item}
			}
			c, err = condition(cols, alias, col, test, args)
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return "true", nil
	}
	return "(" + strings.Join(parts, " "+op+" ") + ")", nil
}

// splitTop splits on the commas outside parentheses.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				if p := strings.TrimSpace(s[start:i]); p != "" {
					out = append(out, p)
				}
				start = i + 1
			}
		}
	}
	if p := strings.TrimSpace(s[start:]); p != "" {
		out = append(out, p)
	}
	return out
}

// orderBy reads "col.desc.nullslast,col2".
func orderBy(cols map[string]string, spec, alias string) (string, error) {
	if spec == "" {
		return "", nil
	}
	var parts []string
	for _, item := range strings.Split(spec, ",") {
		bits := strings.Split(strings.TrimSpace(item), ".")
		col := bits[0]
		if _, ok := cols[col]; !ok || !ident.MatchString(col) {
			return "", &Refused{Status: 400, Code: "42703", Message: "그런 열이 없습니다: " + col}
		}
		p := alias + "." + col
		for _, b := range bits[1:] {
			switch b {
			case "asc":
				p += " asc"
			case "desc":
				p += " desc"
			case "nullslast":
				p += " nulls last"
			case "nullsfirst":
				p += " nulls first"
			default:
				return "", &Refused{Status: 400, Message: "알 수 없는 순서입니다: " + item}
			}
		}
		parts = append(parts, p)
	}
	return " order by " + strings.Join(parts, ", "), nil
}

func limitOffset(q url.Values, _ string) string {
	var out string
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n >= 0 {
		out += " limit " + strconv.Itoa(n)
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n > 0 {
		out += " offset " + strconv.Itoa(n)
	}
	return out
}

// scalarOf is a column type as a cast; arrayOf the array of it.
func scalarOf(udt string) string {
	if strings.HasPrefix(udt, "_") {
		return udt[1:] + "[]"
	}
	return udt
}

func arrayOf(udt string) string {
	if strings.HasPrefix(udt, "_") {
		return udt[1:] + "[]"
	}
	return udt + "[]"
}

/* ───────────────────────────── writes ───────────────────────────── */

// write is an insert (one row or many), an update or a delete of the rows
// that pass, each answered with the rows as they are after it - and what
// uniple's server did besides: the news of it, the album's latest photo.
func (l *local) write(ctx context.Context, me, method, table string, cols map[string]string, q url.Values, body any) ([]byte, error) {
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var rows []map[string]any
	switch method {
	case "POST":
		rows, err = l.insert(ctx, tx, table, cols, body)
	case "PATCH":
		rows, err = l.update(ctx, tx, table, cols, q, body)
	case "DELETE":
		rows, err = l.remove(ctx, tx, table, cols, q)
	}
	if err != nil {
		return nil, err
	}
	if err := l.after(ctx, tx, me, method, table, rows, body); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, refusedOf(err)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return json.Marshal(rows)
}

func (l *local) insert(ctx context.Context, tx pgx.Tx, table string, cols map[string]string, body any) ([]map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var list []map[string]any
	if len(raw) > 0 && raw[0] == '[' {
		err = json.Unmarshal(raw, &list)
	} else {
		var one map[string]any
		err = json.Unmarshal(raw, &one)
		list = []map[string]any{one}
	}
	if err != nil {
		return nil, &Refused{Status: 400, Message: "본문이 행이 아닙니다"}
	}
	var out []map[string]any
	for _, row := range list {
		// An empty value in a new row is the column's default (an id, a time,
		// no days left out of a series).
		for k, v := range row {
			if v == nil {
				delete(row, k)
			}
		}
		names, err := columnsOf(cols, row)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, &Refused{Status: 400, Message: "넣을 칸이 없습니다"}
		}
		enc, _ := json.Marshal(row)
		sql := fmt.Sprintf(`with r as (insert into couple.%[1]s (%[2]s) select %[2]s from jsonb_populate_record(null::couple.%[1]s, $1::jsonb) returning *)
			select coalesce(json_agg(r), '[]'::json) from r`, table, strings.Join(names, ", "))
		got, err := rowsOf(ctx, tx, sql, string(enc))
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func (l *local) update(ctx context.Context, tx pgx.Tx, table string, cols map[string]string, q url.Values, body any) ([]map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil || row == nil {
		return nil, &Refused{Status: 400, Message: "본문이 행이 아닙니다"}
	}
	names, err := columnsOf(cols, row)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, &Refused{Status: 400, Message: "바꿀 칸이 없습니다"}
	}
	args := &sqlArgs{}
	enc := args.add(string(raw))
	where, err := l.filters(table, cols, q, "t", args)
	if err != nil {
		return nil, err
	}
	if where == "" {
		return nil, &Refused{Status: 400, Code: "21000", Message: "조건 없이 모든 행을 바꾸지 않습니다"}
	}
	set := fmt.Sprintf("(%[1]s) = (select %[1]s from jsonb_populate_record(null::couple.%[2]s, %[3]s::jsonb))", strings.Join(names, ", "), table, enc)
	if _, ok := cols["updated_at"]; ok && row["updated_at"] == nil {
		set += ", updated_at = now()"
	}
	sql := fmt.Sprintf(`with r as (update couple.%s t set %s%s returning t.*) select coalesce(json_agg(r), '[]'::json) from r`, table, set, where)
	return rowsOf(ctx, tx, sql, args.list...)
}

func (l *local) remove(ctx context.Context, tx pgx.Tx, table string, cols map[string]string, q url.Values) ([]map[string]any, error) {
	args := &sqlArgs{}
	where, err := l.filters(table, cols, q, "t", args)
	if err != nil {
		return nil, err
	}
	if where == "" {
		return nil, &Refused{Status: 400, Code: "21000", Message: "조건 없이 모든 행을 지우지 않습니다"}
	}
	sql := fmt.Sprintf(`with r as (delete from couple.%s t%s returning t.*) select coalesce(json_agg(r), '[]'::json) from r`, table, where)
	return rowsOf(ctx, tx, sql, args.list...)
}

// columnsOf is a row's keys, each a column of the table.
func columnsOf(cols map[string]string, row map[string]any) ([]string, error) {
	names := make([]string, 0, len(row))
	for k := range row {
		if _, ok := cols[k]; !ok || !ident.MatchString(k) {
			return nil, &Refused{Status: 400, Code: "PGRST204", Message: "그런 칸이 없습니다: " + k}
		}
		names = append(names, k)
	}
	return names, nil
}

func rowsOf(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]map[string]any, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, sql, args...).Scan(&raw); err != nil {
		return nil, refusedOf(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// refusedOf says a database refusal the way uniple's PostgREST said it, so
// the API answers as it did (409 for a duplicate, 400 for a value refused).
func refusedOf(err error) error {
	if err == nil {
		return nil
	}
	var pg interface {
		SQLState() string
		Error() string
	}
	if errors.As(err, &pg) {
		switch code := pg.SQLState(); {
		case code == "23505":
			return &Refused{Status: 409, Code: code, Message: pg.Error()}
		case strings.HasPrefix(code, "22"), strings.HasPrefix(code, "23"):
			return &Refused{Status: 400, Code: code, Message: pg.Error()}
		}
	}
	return err
}

/* ───────────────────────────── news read ───────────────────────────── */

// NewsSeen is up to when whoever asks has read the news, "" when never: the
// same on every device they read it on.
func (c *Client) NewsSeen(ctx context.Context) (string, error) {
	_, me, err := c.where(ctx)
	if err != nil {
		return "", err
	}
	var at string
	err = c.pool.QueryRow(ctx, `select to_char(seen_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"+00:00"') from couple.news_seen where user_id = $1::uuid`, me).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return at, err
}

// SeeNews marks the news read up to at. It never goes back: a device that
// read less does not make the rest new again.
func (c *Client) SeeNews(ctx context.Context, at string) error {
	_, me, err := c.where(ctx)
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `
		insert into couple.news_seen (user_id, seen_at) values ($1::uuid, $2::timestamptz)
		on conflict (user_id) do update set seen_at = greatest(couple.news_seen.seen_at, excluded.seen_at)`, me, at)
	return refusedOf(err)
}
