package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/context-svc/internal/sleep"
)

// The nights (migration 106, context_sleep): one row a day, kept for good.
// A night's timeline is raw, and goes when the raw reports do
// (ForgetTimelines).

const nightColumns = `night::text, bed_at, wake_at, asleep_min, in_bed_min, awake_min, deep_min, core_min, rem_min,
	bed_only, source, naps, timeline, updated_at`

// Nights is the nights of the days from and to (YYYY-MM-DD, both kept),
// newest first.
func (s *Store) Nights(ctx context.Context, from, to string) ([]sleep.Night, error) {
	rows, err := s.pool.Query(ctx, `select `+nightColumns+` from context_sleep
		where night between $1::date and $2::date order by night desc`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sleep.Night{}
	for rows.Next() {
		n, err := scanNight(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Night is one day's night, nil when there is none.
func (s *Store) Night(ctx context.Context, day string) (*sleep.Night, error) {
	n, err := scanNight(s.pool.QueryRow(ctx, `select `+nightColumns+` from context_sleep where night = $1::date`, day))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func scanNight(row pgx.Row) (sleep.Night, error) {
	var n sleep.Night
	var naps, timeline []byte
	if err := row.Scan(&n.Night, &n.BedAt, &n.WakeAt, &n.AsleepMinutes, &n.InBedMinutes, &n.AwakeMinutes,
		&n.DeepMinutes, &n.CoreMinutes, &n.REMMinutes, &n.BedOnly, &n.Source, &naps, &timeline, &n.UpdatedAt); err != nil {
		return n, err
	}
	n.Naps = []sleep.Nap{}
	if err := json.Unmarshal(naps, &n.Naps); err != nil {
		return n, err
	}
	if timeline != nil {
		if err := json.Unmarshal(timeline, &n.Timeline); err != nil {
			return n, err
		}
	}
	return n, nil
}

// PutNight keeps a day's night as it now stands, in place of what was kept.
func (s *Store) PutNight(ctx context.Context, n sleep.Night) error {
	naps := n.Naps
	if naps == nil {
		naps = []sleep.Nap{}
	}
	napsJSON, err := json.Marshal(naps)
	if err != nil {
		return err
	}
	var timeline []byte
	if n.Timeline != nil {
		if timeline, err = json.Marshal(n.Timeline); err != nil {
			return err
		}
	}
	_, err = s.pool.Exec(ctx, `insert into context_sleep (night, bed_at, wake_at, asleep_min, in_bed_min, awake_min,
			deep_min, core_min, rem_min, bed_only, source, naps, timeline, updated_at)
		values ($1::date, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
		on conflict (night) do update set bed_at = excluded.bed_at, wake_at = excluded.wake_at, asleep_min = excluded.asleep_min,
			in_bed_min = excluded.in_bed_min, awake_min = excluded.awake_min, deep_min = excluded.deep_min,
			core_min = excluded.core_min, rem_min = excluded.rem_min, bed_only = excluded.bed_only, source = excluded.source,
			naps = excluded.naps, timeline = excluded.timeline, updated_at = excluded.updated_at`,
		n.Night, n.BedAt, n.WakeAt, n.AsleepMinutes, n.InBedMinutes, n.AwakeMinutes,
		n.DeepMinutes, n.CoreMinutes, n.REMMinutes, n.BedOnly, n.Source, napsJSON, timeline)
	return err
}

// ForgetTimelines lets go of the timelines of the nights that ended before
// a time - the raw part of them, which lasts as long as the raw reports do -
// and keeps the rest of the night.
func (s *Store) ForgetTimelines(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `update context_sleep set timeline = null where timeline is not null and wake_at < $1`, before)
	return tag.RowsAffected(), err
}

// DropReadings removes the readings of a kind that one source said for a
// night: a night sent again leaves one reading of it, not two.
func (s *Store) DropReadings(ctx context.Context, kind, source, night string) error {
	_, err := s.pool.Exec(ctx, `delete from context_raw where kind = $1 and source = $2 and body->>'night' = $3`, kind, source, night)
	return err
}
