package mind

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/link"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// The night (03:00) tidies what the day left: the repetition counts gain the
// plans that ended since last night - only those - and the links to plans
// that did happen are corroborated. Later layers (reflection, notes) hang
// here too: the night is when the surprises of the day are looked at.

const nightAt = 3 * 60 // minutes after midnight

// Night runs the night's work now.
func (m *Mind) Night(ctx context.Context) error {
	now := m.Opts.Now()
	last, err := m.Opts.Store.Cursor(ctx, "night:patterns")
	if err != nil {
		return err
	}
	since := now.Add(-7 * 24 * time.Hour) // a first night looks back a week
	if last > 0 {
		since = time.UnixMilli(last)
	}
	ended, err := m.ended(ctx, since, now)
	if err != nil {
		return err
	}
	if err := m.closePast(ctx, now); err != nil {
		return err
	}
	var kinds []string
	for _, p := range ended {
		if err := m.countPlan(ctx, p); err != nil {
			return err
		}
		if err := m.corroborate(ctx, p); err != nil {
			return err
		}
		if !slices.Contains(kinds, p.State.Type) {
			kinds = append(kinds, p.State.Type)
		}
	}
	if err := m.Opts.Store.SetCursor(ctx, "night:patterns", now.UnixMilli()); err != nil {
		return err
	}
	if err := m.notesFromPatterns(ctx, kinds); err != nil {
		return err
	}
	// What was predicted for yesterday is known now; what missed is looked
	// at, and only that.
	if _, err := m.Predict.Score(ctx); err != nil {
		return err
	}
	if _, err := m.Reflect.Night(ctx); err != nil {
		m.Opts.Logger.Warn("성찰하지 못했습니다", "err", err)
	}
	for _, step := range m.nightly {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ended is the plans with a kind that ended in (since, until]: the days
// they touch are read, not every plan.
func (m *Mind) ended(ctx context.Context, since, until time.Time) ([]model.Entity, error) {
	loc := m.Opts.Loc
	plans, err := m.Opts.Store.PlansOn(ctx, since.In(loc).AddDate(0, 0, -30).Format("2006-01-02"), until.In(loc).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	var out []model.Entity
	for _, p := range plans {
		st := p.State
		if (st.Status != model.Confirmed && st.Status != model.Done) || st.Type == "" || st.Type == "기타" {
			continue
		}
		sp, ok := m.World.Span(ctx, p, until, loc)
		if ok && sp.End.After(since) && !sp.End.After(until) {
			out = append(out, p)
		}
	}
	return out, nil
}

// closePast marks done the confirmed plans that are over: a trip last month
// is not still "confirmed", and a conversation about the next one is not
// read as moving it.
func (m *Mind) closePast(ctx context.Context, now time.Time) error {
	loc := m.Opts.Loc
	plans, err := m.Opts.Store.PlansOn(ctx, now.In(loc).AddDate(0, 0, -30).Format("2006-01-02"), now.In(loc).Format("2006-01-02"))
	if err != nil {
		return err
	}
	for _, p := range plans {
		if p.State.Status != model.Confirmed {
			continue
		}
		sp, ok := m.World.Span(ctx, p, now, loc)
		if !ok || now.Sub(sp.End) < 3*time.Hour {
			continue
		}
		if _, _, err := m.World.Update(ctx, p.ID, func(s *model.State) bool { s.Status = model.Done; return true },
			world.Meta{Actor: "observer", Why: "날짜가 지남"}); err != nil {
			return err
		}
	}
	return nil
}

// countPlan adds one plan to the repetition counts: one more plan of its
// kind, and one more for each class of thing bought in the two weeks before
// it, in the bucket of how long before.
func (m *Mind) countPlan(ctx context.Context, p model.Entity) error {
	st := m.Opts.Store
	sp, ok := m.World.Span(ctx, p, m.Opts.Now(), m.Opts.Loc)
	if !ok {
		return nil
	}
	kind := p.State.Type
	if err := st.CountPattern(ctx, model.Pattern{KindA: kind, Rel: model.PrepFor, Total: 1, LastSeen: sp.Start}); err != nil {
		return err
	}
	items, err := st.Occurrences(ctx, sp.Start.Add(-14*24*time.Hour), sp.Start, "purchase.item")
	if err != nil {
		return err
	}
	counted := map[string]bool{}
	for i := len(items) - 1; i >= 0; i-- { // nearest first: a class counts once, at its nearest
		it := items[i]
		class, err := st.ThingClass(ctx, it.Title)
		if errors.Is(err, model.ErrNotFound) && m.Link.Judge != nil {
			j, jerr := m.Link.Judge.Of(ctx, it.Title, kind)
			if jerr != nil {
				continue
			}
			class, err = j.Class, nil
		}
		if err != nil || class == "" || counted[class] {
			continue
		}
		counted[class] = true
		if err := st.CountPattern(ctx, model.Pattern{KindA: kind, Rel: model.PrepFor, KindB: class,
			Bucket: link.Bucket(sp.Start.Sub(it.At)), Support: 1, LastSeen: it.At}); err != nil {
			return err
		}
	}
	return nil
}

// corroborate raises a plan's prep links once it is known to have
// happened - something during it was linked for sure.
func (m *Mind) corroborate(ctx context.Context, p model.Entity) error {
	links, err := m.Opts.Store.LinksOf(ctx, p.ID)
	if err != nil {
		return err
	}
	happened := false
	for _, l := range links {
		if l.To == p.ID && l.Rel == model.During && l.State == model.Confirmed {
			happened = true
		}
	}
	if !happened {
		return nil
	}
	now := m.Opts.Now()
	for _, l := range links {
		if l.To != p.ID || (l.Rel != model.PrepFor && l.Rel != model.UsedDuring) || l.State != model.Estimated || l.By == "user" ||
			strings.Contains(l.Why, link.Happened) {
			continue
		}
		up := math.Min(0.9, math.Round((l.Confidence+0.1)*100)/100)
		if up <= l.Confidence {
			continue
		}
		l.Confidence, l.UpdatedAt = up, now
		l.Why += link.Happened
		if err := m.Opts.Store.PutLink(ctx, l); err != nil {
			return err
		}
	}
	return nil
}

// RebuildPatterns counts the repetitions again from the plans and
// purchases they are derived from - the whole history, on purpose.
func (m *Mind) RebuildPatterns(ctx context.Context) (int, error) {
	if err := m.Opts.Store.ResetPatterns(ctx); err != nil {
		return 0, err
	}
	ended, err := m.ended(ctx, time.Time{}, m.Opts.Now())
	if err != nil {
		return 0, err
	}
	for _, p := range ended {
		if err := m.countPlan(ctx, p); err != nil {
			return 0, err
		}
	}
	return len(ended), m.Opts.Store.SetCursor(ctx, "night:patterns", m.Opts.Now().UnixMilli())
}

// Tick runs a time of day: the night's work, the morning's predictions,
// or a tick the skills hear.
func (m *Mind) Tick(ctx context.Context, name string) error {
	switch name {
	case "night":
		return m.Night(ctx)
	case "predict":
		return m.Morning(ctx)
	case "review":
		return m.reviews(ctx)
	}
	m.Attn.Tick(ctx, name)
	return nil
}

// nights runs the night once a day at 03:00.
func (m *Mind) nights(ctx context.Context, every time.Duration) {
	m.daily(ctx, every, "tick:night", nightAt, m.Night)
}

// daily runs step once a day at minute (after midnight), or the first
// moment after, remembering across restarts that it did.
func (m *Mind) daily(ctx context.Context, every time.Duration, cursor string, minute int, step func(context.Context) error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := m.Opts.Now().In(m.Opts.Loc)
		if now.Hour()*60+now.Minute() < minute {
			continue
		}
		today, _ := strconv.ParseInt(now.Format("20060102"), 10, 64)
		last, err := m.Opts.Store.Cursor(ctx, cursor)
		if err != nil || last >= today {
			continue
		}
		if err := m.Opts.Store.SetCursor(ctx, cursor, today); err != nil {
			continue
		}
		if err := step(ctx); err != nil {
			m.Opts.Logger.Warn("하루 한 번 하는 일을 하지 못했습니다", "what", cursor, "err", fmt.Sprint(err))
		}
	}
}
