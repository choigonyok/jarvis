package uniple

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/gocommon/notify"
)

// What uniple's server did on its own, done for the local space: the calls
// the client made by name (rpc/…), and what follows a write (after).

func (l *local) rpc(ctx context.Context, me, name string, body any, out any) error {
	raw, _ := json.Marshal(body)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	s := func(k string) string { v, _ := p[k].(string); return v }

	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var answer any
	switch name {
	case "sync_dday_calendar_events":
		err = l.syncDDays(ctx, tx, s("p_space_id"), me)
	case "patch_block_config":
		unset, _ := json.Marshal(p["p_unset"])
		set, _ := json.Marshal(p["p_set"])
		_, err = tx.Exec(ctx, `update couple.blocks set config = (coalesce(config, '{}'::jsonb) || $2::jsonb) - $3::text[], updated_at = now() where id = $1::uuid`,
			s("p_block_id"), string(set), stringsOf(unset))
	case "set_calendar_day_photo_cover":
		_, err = tx.Exec(ctx, `
			update couple.calendar_day_photos p set is_cover = (p.id = $1::uuid), updated_at = now()
			  from couple.calendar_day_photos o
			 where o.id = $1::uuid and p.block_id = o.block_id and p.date = o.date`, s("p_photo_id"))
	case "add_map_face_asset":
		err = l.mapFace(ctx, tx, me, p)
	case "set_worldmap_fill_asset":
		answer, err = l.worldFace(ctx, tx, me, p)
	case "album_bulk_delete":
		err = l.albumDelete(ctx, tx, s("p_block_id"), p["p_photo_ids"])
	case "set_album_pinned_asset":
		err = l.albumPin(ctx, tx, me, s("p_block_id"), s("p_asset_id"))
	default:
		return &Refused{Status: 404, Code: "PGRST202", Message: "그런 함수가 없습니다: " + name}
	}
	if err != nil {
		return refusedOf(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return refusedOf(err)
	}
	if out == nil || answer == nil {
		return nil
	}
	enc, _ := json.Marshal(answer)
	return json.Unmarshal(enc, out)
}

func stringsOf(raw []byte) []string {
	var list []string
	_ = json.Unmarshal(raw, &list)
	if list == nil {
		list = []string{}
	}
	return list
}

/* ───────────────────────────── after a write ───────────────────────────── */

// newsOf is what a write to a table tells the other person, by the kinds the
// app's news uses: "" for something added, update, done, photo, record,
// delete. Moving things in a list, a memo on a check and a photo turned are
// not news.
func newsOf(method, table string, body map[string]any) (blockType, kind string, ok bool) {
	only := func(keys ...string) bool {
		for k := range body {
			found := false
			for _, want := range keys {
				found = found || k == want
			}
			if !found {
				return false
			}
		}
		return true
	}
	switch table {
	case "calendar_events":
		switch method {
		case "POST":
			return "calendar", "", true
		case "PATCH":
			if only("recurrence_exdates") {
				return "calendar", "delete", true
			}
			return "calendar", "update", true
		case "DELETE":
			return "calendar", "delete", true
		}
	case "calendar_day_photos":
		return "calendar", "photo", method == "POST"
	case "dday_items":
		switch method {
		case "POST":
			return "dday", "", true
		case "PATCH":
			return "dday", "update", true
		}
	case "bucket_items":
		switch method {
		case "POST":
			return "bucket", "", true
		case "PATCH":
			if done, _ := body["is_completed"].(bool); done {
				return "bucket", "done", true
			}
			if _, renamed := body["title"]; renamed {
				return "bucket", "update", true
			}
		}
	case "free_items":
		switch method {
		case "POST":
			return "free", "", true
		case "PATCH":
			if _, ok := body["content"]; ok {
				return "free", "update", true
			}
		}
	case "workout_checks":
		switch method {
		case "POST":
			return "workout", "", true
		case "DELETE":
			return "workout", "uncheck", true
		}
	case "map_visits":
		return "map", "", method == "POST"
	case "map_records":
		return "map", "record", method == "POST"
	case "worldmap_visits":
		return "worldmap", "", method == "POST"
	case "worldmap_records":
		return "worldmap", "record", method == "POST"
	case "album_photos":
		if method == "POST" {
			if snap, _ := body["is_snap"].(bool); snap {
				return "album", "snap", true
			}
			return "album", "", true
		}
	}
	return "", "", false
}

// after does what uniple's server did after a write: the news of it (one per
// write, about its first row; a series' own days going with it are not news
// of their own), and the album's latest photo of whoever put one in.
func (l *local) after(ctx context.Context, tx pgx.Tx, me, method, table string, rows []map[string]any, body any) error {
	if len(rows) == 0 {
		return nil
	}
	fields := map[string]any{}
	if m, ok := body.(map[string]any); ok {
		fields = m
	} else if list, ok := body.([]map[string]any); ok && len(list) > 0 {
		fields = list[0]
	}
	if table == "album_photos" && method == "POST" {
		last := rows[len(rows)-1]
		if _, err := tx.Exec(ctx, `
			update couple.blocks
			   set config = jsonb_set(coalesce(config, '{}'::jsonb), array['latest'], coalesce(config->'latest', '{}'::jsonb) || jsonb_build_object($2::text, jsonb_build_object('asset', $3::text, 'at', now())))
			 where id = $1::uuid`, last["block_id"], me, last["asset_id"]); err != nil {
			return err
		}
	}
	blockType, kind, ok := newsOf(method, table, fields)
	if !ok {
		return nil
	}
	first := rows[0]
	if table == "calendar_events" {
		// The days of a series changed on their own go with it: the series is
		// the news, and a write of those days alone is none.
		var series map[string]any
		for _, r := range rows {
			if r["recurrence_parent_id"] == nil {
				series = r
				break
			}
		}
		if series == nil || series["origin"] != nil {
			return nil
		}
		first = series
	}
	item, _ := first["id"].(string)
	var eventTime any
	if table == "calendar_events" {
		eventTime = first["start_time"]
	}
	_, err := tx.Exec(ctx, `
		insert into couple.notifications (space_id, actor_id, block_id, block_type, kind, item_id, event_time)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, left($7::text, 5))`,
		first["space_id"], me, first["block_id"], blockType, kind, item, eventTime)
	return err
}

/* ───────────────────────────── the D-day on the calendar ───────────────────────────── */

// milestonesAhead is how many days to come from the day they met are kept on
// the calendar: every hundred days and each year, the next ten.
const milestonesAhead = 10

type wantEntry struct {
	key, origin, title, date, recurrence string
	createdBy                            string
}

// syncDDays writes the D-day block's dates onto the calendar as uniple did:
// each anniversary as an all-day shared entry (yearly when it repeats), and
// from the day they met (day 1) every hundred days and each year - those
// passed and the next ten. Each keeps its id from one sync to the next
// (origin_key), and one no longer wanted goes.
func (l *local) syncDDays(ctx context.Context, tx pgx.Tx, space, me string) error {
	var block string
	if err := tx.QueryRow(ctx, `select id::text from couple.blocks where space_id = $1::uuid and type = 'calendar' and archived_at is null order by order_index limit 1`, space).Scan(&block); err != nil {
		return fmt.Errorf("캘린더 블록: %w", err)
	}
	rows, err := tx.Query(ctx, `
		select d.id::text, d.title, to_char(d.date, 'YYYY-MM-DD'), d.is_start_date, coalesce(d.recurrence, ''), coalesce(d.created_by::text, '')
		  from couple.dday_items d join couple.blocks b on b.id = d.block_id
		 where b.space_id = $1::uuid and b.archived_at is null`, space)
	if err != nil {
		return err
	}
	var want []wantEntry
	start := ""
	for rows.Next() {
		var id, title, date, rec, by string
		var isStart bool
		if err := rows.Scan(&id, &title, &date, &isStart, &rec, &by); err != nil {
			rows.Close()
			return err
		}
		if isStart {
			start = date
			continue
		}
		want = append(want, wantEntry{key: "anniversary:" + id, origin: "anniversary", title: title, date: date, recurrence: rec, createdBy: by})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var met string
	_ = tx.QueryRow(ctx, `select coalesce(to_char(anniversary_date, 'YYYY-MM-DD'), '') from couple.spaces where id = $1::uuid`, space).Scan(&met)
	if met == "" {
		met = start
	}
	if met != "" {
		want = append(want, milestones(met, today())...)
	}

	keys := make([]string, 0, len(want))
	for _, w := range want {
		keys = append(keys, w.key)
		by := w.createdBy
		if by == "" {
			by = me
		}
		if _, err := tx.Exec(ctx, `
			insert into couple.calendar_events (block_id, space_id, title, start_date, recurrence, created_by, origin, origin_key)
			values ($1::uuid, $2::uuid, $3, $4::date, nullif($5, ''), $6::uuid, $7, $8)
			on conflict (space_id, origin_key) where origin_key is not null do update set
			  title = excluded.title, start_date = excluded.start_date, recurrence = excluded.recurrence,
			  updated_at = case when (couple.calendar_events.title, couple.calendar_events.start_date, coalesce(couple.calendar_events.recurrence, ''))
			                        = (excluded.title, excluded.start_date, coalesce(excluded.recurrence, ''))
			                    then couple.calendar_events.updated_at else now() end`,
			block, space, w.title, w.date, w.recurrence, by, w.origin, w.key); err != nil {
			return fmt.Errorf("기념일 일정 쓰기: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `delete from couple.calendar_events where space_id = $1::uuid and origin in ('anniversary', 'milestone') and not (coalesce(origin_key, '') = any($2::text[]))`, space, keys)
	return err
}

// milestones are the days counted from the day they met (D+1): every hundred
// days and each year, all those passed and the next ten from today.
func milestones(met, today string) []wantEntry {
	from, err := time.Parse(time.DateOnly, met)
	if err != nil {
		return nil
	}
	type m struct {
		date  string
		key   string
		title string
	}
	var all []m
	horizon := from.AddDate(200, 0, 0)
	for n := 100; ; n += 100 {
		d := from.AddDate(0, 0, n-1)
		if d.After(horizon) {
			break
		}
		all = append(all, m{d.Format(time.DateOnly), fmt.Sprintf("milestone:%dd", n), fmt.Sprintf("%d일", n)})
		if len(all) > 4000 {
			break
		}
	}
	for y := 1; y <= 100; y++ {
		d := from.AddDate(y, 0, 0)
		all = append(all, m{d.Format(time.DateOnly), fmt.Sprintf("milestone:%dy", y), fmt.Sprintf("%d주년", y)})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].date < all[j].date })
	var out []wantEntry
	ahead := 0
	for _, x := range all {
		if x.date >= today {
			if ahead == milestonesAhead {
				break
			}
			ahead++
		}
		out = append(out, wantEntry{key: x.key, origin: "milestone", title: x.title, date: x.date})
	}
	return out
}

/* ───────────────────────────── maps and the album ───────────────────────────── */

// mapFace puts a photo on a region of the date map as its face: the region's
// other photos are no longer.
func (l *local) mapFace(ctx context.Context, tx pgx.Tx, me string, p map[string]any) error {
	if _, err := tx.Exec(ctx, `update couple.map_visits set is_rep = false, updated_at = now() where block_id = $1::uuid and region_code = $2 and is_rep`,
		p["p_block_id"], p["p_region_code"]); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into couple.map_visits (id, block_id, space_id, region_code, asset_id, clip_asset_id, is_rep, created_by)
		values ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::uuid, true, $7::uuid)`,
		p["p_id"], p["p_block_id"], p["p_space_id"], p["p_region_code"], p["p_asset_id"], p["p_clip_asset_id"], me); err != nil {
		return err
	}
	return l.newsRow(ctx, tx, me, p["p_space_id"], p["p_block_id"], "map", "", p["p_id"])
}

// worldFace is a country's face on the world map: the one photo not of a
// trip, put in place or replaced.
func (l *local) worldFace(ctx context.Context, tx pgx.Tx, me string, p map[string]any) (any, error) {
	var id string
	err := tx.QueryRow(ctx, `select id::text from couple.worldmap_visits where block_id = $1::uuid and country_code = $2 and record_id is null limit 1`,
		p["p_block_id"], p["p_country_code"]).Scan(&id)
	fresh := errors.Is(err, pgx.ErrNoRows)
	switch {
	case fresh:
		id = newUUID()
		_, err = tx.Exec(ctx, `
			insert into couple.worldmap_visits (id, block_id, space_id, country_code, asset_id, clip_asset_id, rotation, created_by)
			values ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::uuid, coalesce($7::float8, 0), $8::uuid)`,
			id, p["p_block_id"], p["p_space_id"], p["p_country_code"], p["p_asset_id"], p["p_clip_asset_id"], p["p_rotation"], me)
	case err == nil:
		_, err = tx.Exec(ctx, `update couple.worldmap_visits set asset_id = $2::uuid, clip_asset_id = $3::uuid, rotation = coalesce($4::float8, 0), updated_at = now() where id = $1::uuid`,
			id, p["p_asset_id"], p["p_clip_asset_id"], p["p_rotation"])
	}
	if err != nil {
		return nil, err
	}
	if fresh {
		if err := l.newsRow(ctx, tx, me, p["p_space_id"], p["p_block_id"], "worldmap", "", id); err != nil {
			return nil, err
		}
	}
	return rowsOf(ctx, tx, `select coalesce(json_agg(v), '[]'::json) from couple.worldmap_visits v where v.id = $1::uuid`, id)
}

func (l *local) newsRow(ctx context.Context, tx pgx.Tx, me string, space, block any, blockType, kind string, item any) error {
	_, err := tx.Exec(ctx, `insert into couple.notifications (space_id, actor_id, block_id, block_type, kind, item_id) values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::text)`,
		space, me, block, blockType, kind, item)
	return err
}

// albumDelete takes photos out of an album; whoever's latest or pinned photo
// went is moved to their newest one left, or let go.
func (l *local) albumDelete(ctx context.Context, tx pgx.Tx, block string, ids any) error {
	raw, _ := json.Marshal(ids)
	if _, err := tx.Exec(ctx, `delete from couple.album_photos where block_id = $1::uuid and id = any($2::text[]::uuid[])`, block, stringsOf(raw)); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		with cfg as (select coalesce(config, '{}'::jsonb) c from couple.blocks where id = $1::uuid),
		latest as (
		  select coalesce(jsonb_object_agg(k, jsonb_build_object('asset', p.asset_id, 'at', p.sort_at)) filter (where p.asset_id is not null), '{}'::jsonb) v
		    from cfg, jsonb_object_keys(coalesce(cfg.c->'latest', '{}'::jsonb)) k
		    left join lateral (select asset_id, sort_at from couple.album_photos where block_id = $1::uuid and uploaded_by::text = k order by sort_at desc limit 1) p on true),
		pinned as (
		  select coalesce(jsonb_object_agg(k, v), '{}'::jsonb) v
		    from cfg, jsonb_each(coalesce(cfg.c->'pinned', '{}'::jsonb)) as e(k, v)
		   where exists (select 1 from couple.album_photos where block_id = $1::uuid and asset_id::text = v->>'asset'))
		update couple.blocks b set config = (select c from cfg) || jsonb_build_object('latest', (select v from latest), 'pinned', (select v from pinned)), updated_at = now()
		 where b.id = $1::uuid`, block)
	return err
}

// albumPin puts one photo of the album on the home for whoever asks, or
// takes theirs off.
func (l *local) albumPin(ctx context.Context, tx pgx.Tx, me, block, asset string) error {
	if asset == "" {
		_, err := tx.Exec(ctx, `update couple.blocks set config = jsonb_set(coalesce(config, '{}'::jsonb), array['pinned'], coalesce(config->'pinned', '{}'::jsonb) - $2::text), updated_at = now() where id = $1::uuid`, block, me)
		return err
	}
	_, err := tx.Exec(ctx, `update couple.blocks set config = jsonb_set(coalesce(config, '{}'::jsonb), array['pinned'], coalesce(config->'pinned', '{}'::jsonb) || jsonb_build_object($2::text, jsonb_build_object('asset', $3::text, 'at', now()))), updated_at = now() where id = $1::uuid`,
		block, me, asset)
	return err
}

/* ───────────────────────────── telling the other ───────────────────────────── */

// tell is the push the app sent the partner after a habit check, the first
// photo on a map's place, or a snap - here through jarvis's own notifications,
// to the other of the two.
func (l *local) tell(ctx context.Context, block string, extra map[string]any) {
	if l.notify == nil {
		return
	}
	_, other, err := l.who(ctx)
	if err != nil {
		return
	}
	var kind, title string
	_ = l.pool.QueryRow(ctx, `select type, title from couple.blocks where id = $1::uuid`, block).Scan(&kind, &title)
	l.mu.Lock()
	toGuest := other == l.sides["guest"]
	l.mu.Unlock()
	ev := notify.Event{Source: "calendar", Kind: "calendar.partner", Tier: "now", URL: "/couple/" + block}
	switch {
	case kind == "workout":
		ev.Title = "상대가 습관을 체크했어요"
		if d, ok := extra["date"].(string); ok && len(d) == 10 {
			ev.Body = strings.TrimSpace(title + " · " + d[5:])
			ev.URL += "?date=" + d
		}
	case extra["kind"] == "snap":
		ev.Title, ev.Body = "상대가 사진을 찍어 올렸어요", title
	case kind == "worldmap":
		ev.Title, ev.Body = "상대가 세계 지도에 사진을 올렸어요", title
	default:
		ev.Title, ev.Body = "상대가 지도에 사진을 올렸어요", title
	}
	ev.Key = fmt.Sprintf("couple:%s:%v", block, firstNonNil(extra["itemId"], extra["date"], extra["kind"]))
	if toGuest {
		ev.Audience, ev.Key = "guest", "guest-"+ev.Key
	}
	l.notify.Send(ev)
	slog.Debug("상대에게 알렸습니다", "block", block, "guest", toGuest)
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return ""
}
