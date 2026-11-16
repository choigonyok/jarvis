package uniple

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Moving the couple's space off uniple: every row of the space, table by
// table with its id, and every photo the rows name, into the local tables
// (migration 107). What mind and the agent hold of an entry keeps pointing at
// it, and the news read in the app stays read.
//
// It holds uniple's session as calendar-svc does - the token rotates on use -
// so it runs while calendar-svc is stopped.

// Imported is what one import copied.
type Imported struct {
	Rows    map[string]int `json:"rows"`
	Photos  int            `json:"photos"`
	Missing []string       `json:"missing,omitempty"` // photos uniple did not give
	Skipped []string       `json:"skipped,omitempty"` // tables uniple did not answer
}

// importTables are the space's tables in the order their rows can go in.
var importTables = []string{
	"calendar_events", "dday_items", "bucket_items", "free_items", "workout_checks",
	"map_records", "map_visits", "worldmap_records", "worldmap_visits", "album_photos",
	"calendar_day_photos", "log_entries", "food_entries", "diary_entries", "budget_dates",
	"notifications",
}

// Import copies the space remote (uniple) holds into the local one. A local
// space that already has anything is left alone unless replace says to clear
// it first.
func Import(ctx context.Context, remote, dst *Client, replace bool, log *slog.Logger) (Imported, error) {
	l := dst.local
	if l == nil || remote.local != nil {
		return Imported{}, errors.New("uniple 에서 로컬로만 옮깁니다")
	}
	out := Imported{Rows: map[string]int{}}
	space, me, err := remote.where(ctx)
	if err != nil {
		return out, fmt.Errorf("uniple 공간 찾기: %w", err)
	}

	var have int
	if err := l.pool.QueryRow(ctx, `select (select count(*) from couple.calendar_events) + (select count(*) from couple.dday_items) + (select count(*) from couple.notifications)`).Scan(&have); err != nil {
		return out, err
	}
	if have > 0 && !replace {
		return out, errors.New("로컬 커플 공간에 이미 기록이 있습니다. 지우고 다시 옮기려면 -replace")
	}
	if _, err := l.pool.Exec(ctx, `truncate couple.media, couple.news_seen, couple.notifications, couple.budget_expenses, couple.budget_dates,
		couple.diary_comments, couple.diary_entries, couple.food_entries, couple.log_entries, couple.calendar_day_photos, couple.album_photos,
		couple.worldmap_visits, couple.worldmap_records, couple.map_visits, couple.map_records, couple.workout_checks, couple.free_items,
		couple.bucket_items, couple.dday_items, couple.calendar_events, couple.blocks, couple.sides, couple.profiles, couple.spaces`); err != nil {
		return out, fmt.Errorf("로컬 커플 공간 비우기: %w", err)
	}

	pull := func(table string, q url.Values) ([]map[string]any, error) {
		var all []map[string]any
		for page := 0; ; page++ {
			q.Set("select", "*")
			q.Set("order", "id")
			q.Set("limit", "1000")
			q.Set("offset", fmt.Sprint(page*1000))
			var rows []map[string]any
			if err := remote.rest(ctx, http.MethodGet, table+"?"+q.Encode(), nil, &rows); err != nil {
				return nil, err
			}
			all = append(all, rows...)
			time.Sleep(300 * time.Millisecond) // uniple answers 521 to a burst
			if len(rows) < 1000 {
				return all, nil
			}
		}
	}
	put := func(table string, rows []map[string]any) error {
		for _, r := range rows {
			if err := l.importRow(ctx, table, r); err != nil {
				return fmt.Errorf("%s %v: %w", table, r["id"], err)
			}
		}
		out.Rows[table] += len(rows)
		return nil
	}

	spaces, err := pull("spaces", url.Values{"id": {"eq." + space}})
	if err != nil || len(spaces) == 0 {
		return out, fmt.Errorf("uniple 공간 읽기: %v", err)
	}
	if err := put("spaces", spaces); err != nil {
		return out, err
	}
	people, err := pull("profiles", url.Values{"space_id": {"eq." + space}})
	if err != nil {
		return out, fmt.Errorf("두 사람 읽기: %w", err)
	}
	if err := put("profiles", people); err != nil {
		return out, err
	}
	partner := ""
	for _, p := range people {
		if id, _ := p["id"].(string); id != me {
			partner = id
		}
	}
	if partner == "" {
		return out, errors.New("uniple 공간에서 상대를 찾지 못했습니다")
	}
	if _, err := l.pool.Exec(ctx, `insert into couple.sides (side, user_id) values ('owner', $1::uuid), ('guest', $2::uuid)`, me, partner); err != nil {
		return out, err
	}

	blocks, err := pull("blocks", url.Values{"space_id": {"eq." + space}})
	if err != nil {
		return out, fmt.Errorf("블록 읽기: %w", err)
	}
	if err := put("blocks", blocks); err != nil {
		return out, err
	}
	var blockIDs []string
	for _, b := range blocks {
		if id, _ := b["id"].(string); id != "" {
			blockIDs = append(blockIDs, id)
		}
	}

	parents := map[string][]string{}
	for _, table := range importTables {
		rows, err := pull(table, url.Values{"space_id": {"eq." + space}})
		var refused *Refused
		if errors.As(err, &refused) && refused.Status == http.StatusBadRequest && len(blockIDs) > 0 && table != "notifications" {
			// A table without space_id is read by its blocks.
			rows, err = pull(table, url.Values{"block_id": {"in.(" + strings.Join(blockIDs, ",") + ")"}})
		}
		if errors.As(err, &refused) && (refused.Status == http.StatusNotFound || refused.Status == http.StatusBadRequest) {
			out.Skipped = append(out.Skipped, table)
			log.Warn("uniple 이 이 표를 주지 않아 건너뜁니다", "table", table, "err", err)
			continue
		}
		if err != nil {
			return out, fmt.Errorf("%s 읽기: %w", table, err)
		}
		if err := put(table, rows); err != nil {
			return out, err
		}
		for _, r := range rows {
			if id, _ := r["id"].(string); id != "" {
				parents[table] = append(parents[table], id)
			}
		}
	}
	for _, child := range []struct{ table, fk, parent string }{
		{"diary_comments", "entry_id", "diary_entries"},
		{"budget_expenses", "date_id", "budget_dates"},
	} {
		ids := parents[child.parent]
		for len(ids) > 0 {
			n := min(len(ids), 100)
			rows, err := pull(child.table, url.Values{child.fk: {"in.(" + strings.Join(ids[:n], ",") + ")"}})
			if err != nil {
				out.Skipped = append(out.Skipped, child.table)
				break
			}
			if err := put(child.table, rows); err != nil {
				return out, err
			}
			ids = ids[n:]
		}
	}

	// The D-day's entries on the calendar keep their ids from here on: each
	// is tied to the anniversary or the count of days it stands for.
	if _, err := l.pool.Exec(ctx, `
		update couple.calendar_events e set origin_key = 'anniversary:' || d.id
		  from couple.dday_items d
		 where e.origin = 'anniversary' and e.origin_key is null and d.is_start_date = false
		   and d.title = e.title and d.date = e.start_date`); err != nil {
		return out, err
	}
	if _, err := l.pool.Exec(ctx, `
		update couple.calendar_events set origin_key = case
		    when title ~ '^[0-9]+일$' then 'milestone:' || substring(title from '^([0-9]+)') || 'd'
		    when title ~ '^[0-9]+주년$' then 'milestone:' || substring(title from '^([0-9]+)') || 'y' end
		 where origin = 'milestone' and origin_key is null`); err != nil {
		return out, err
	}

	// The photos the rows name, each kept here with its thumb.
	assets, err := l.namedAssets(ctx)
	if err != nil {
		return out, err
	}
	for _, id := range assets {
		body, kind, err := remotePhoto(ctx, remote, id)
		if err == nil {
			err = l.keep(ctx, id, kind, body, "")
		}
		if err != nil {
			out.Missing = append(out.Missing, id)
			log.Warn("사진을 옮기지 못했습니다", "asset", id, "err", err)
			continue
		}
		out.Photos++
		time.Sleep(150 * time.Millisecond)
	}
	return out, nil
}

// importRow puts one row as uniple gave it: the columns the local table has,
// with their values; a column uniple has and this does not is left behind.
func (l *local) importRow(ctx context.Context, table string, row map[string]any) error {
	cols := l.cols[table]
	keep := map[string]any{}
	var names []string
	for k, v := range row {
		// A value uniple left empty takes the column's default here.
		if _, ok := cols[k]; ok && v != nil && ident.MatchString(k) {
			keep[k] = v
			names = append(names, k)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	enc, _ := json.Marshal(keep)
	_, err := l.pool.Exec(ctx, fmt.Sprintf(`insert into couple.%[1]s (%[2]s) select %[2]s from jsonb_populate_record(null::couple.%[1]s, $1::jsonb) on conflict do nothing`,
		table, strings.Join(names, ", ")), string(enc))
	return err
}

// namedAssets are the photos any row names: on maps, in the album, on
// calendar days, in the shelves, and an album's latest or pinned.
func (l *local) namedAssets(ctx context.Context) ([]string, error) {
	rows, err := l.pool.Query(ctx, `
		select distinct a from (
		  select asset_id::text a from couple.map_visits union all select clip_asset_id::text from couple.map_visits
		  union all select asset_id::text from couple.worldmap_visits union all select clip_asset_id::text from couple.worldmap_visits
		  union all select asset_id::text from couple.album_photos
		  union all select asset_id::text from couple.calendar_day_photos
		  union all select card_asset from couple.log_entries union all select unnest(photo_asset_ids) from couple.log_entries
		  union all select card_asset from couple.food_entries union all select unnest(photo_asset_ids) from couple.food_entries
		  union all select unnest(photo_asset_ids) from couple.diary_entries
		  union all select v->>'asset' from couple.blocks, jsonb_each(coalesce(config->'latest', '{}'::jsonb)) as l(k, v)
		  union all select v->>'asset' from couple.blocks, jsonb_each(coalesce(config->'pinned', '{}'::jsonb)) as p(k, v)
		) x where a is not null`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		if uuidLike(a) {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

// remotePhoto is a photo's largest size uniple gives.
func remotePhoto(ctx context.Context, remote *Client, id string) ([]byte, string, error) {
	var last error
	for _, size := range []string{"display", "thumb"} {
		p, err := remote.Photo(ctx, id, size)
		if err != nil {
			last = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(p.Body, 32<<20))
		p.Body.Close()
		if err != nil {
			last = err
			continue
		}
		kind, _, _ := strings.Cut(p.Type, ";")
		return body, strings.TrimSpace(kind), nil
	}
	return nil, "", last
}
