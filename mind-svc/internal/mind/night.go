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
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// The night (03:00) tidies what the day left: the repetition counts gain the
// plans that ended since last night - only those - and the links to plans
// that did happen are corroborated. Later layers (reflection, notes) hang
// here too: the night is when the surprises of the day are looked at.

const nightAt = 3 * 60 // minutes after midnight

// Night runs the night's work now. Each stage runs even if an earlier one
// failed; the failures are returned together.
func (m *Mind) Night(ctx context.Context) error {
	steps := []func(context.Context) error{
		m.nightPatterns,
		func(ctx context.Context) error {
			// What was predicted for yesterday is known now; what missed is
			// looked at, and only that.
			prs, err := m.Predict.Score(ctx)
			m.scored(ctx, prs...)
			return err
		},
		// The rules found again and graded by how they did, before the
		// reflection reads which of them are worth reflecting on.
		m.engineNight,
		m.lapseLoops,
		func(ctx context.Context) error {
			if _, err := m.Reflect.Night(ctx); err != nil {
				m.Opts.Logger.Warn("성찰하지 못했습니다", "err", err)
			}
			return nil
		},
	}
	return m.stages(ctx, "night", append(steps, m.nightly...))
}

// stages runs every step, logging and collecting failures instead of
// stopping at the first: one broken step must not cost the rest of the day.
func (m *Mind) stages(ctx context.Context, what string, steps []func(context.Context) error) error {
	var errs []error
	for i, step := range steps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := step(ctx); err != nil {
			m.Opts.Logger.Warn("단계를 하지 못했습니다", "what", what, "step", i, "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Mind) nightPatterns(ctx context.Context) error {
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
	return m.notesFromPatterns(ctx, kinds)
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

// lapsedAfter is how long past its deadline an open loop is folded: a to-do
// a fortnight overdue is not coming, and read out every morning until it
// does it only buries the day's (8 of 34 open loops were, 2026-10-27).
const lapsedAfter = 14 * 24 * time.Hour

// lapseLoops folds the loops long past their deadline and says so once, for
// the evening: the operator can still take one up from the record.
func (m *Mind) lapseLoops(ctx context.Context) error {
	now := m.Opts.Now()
	loops, err := m.Opts.Store.Entities(ctx, model.KindLoop, "", true)
	if err != nil {
		return err
	}
	var folded []string
	for _, l := range loops {
		at, ok := skill.DueAt(l.State.Due, m.Opts.Loc)
		if !ok || now.Sub(at) < lapsedAfter {
			continue
		}
		_, changed, err := m.World.Update(ctx, l.ID, func(s *model.State) bool { s.Status = model.Closed; return true },
			world.Meta{Actor: "observer", Why: "기한(" + l.State.Due + ")이 2주 넘게 지나 접음"})
		if err != nil {
			return err
		}
		if changed {
			folded = append(folded, l.State.Title)
		}
	}
	if len(folded) == 0 {
		return nil
	}
	_, err = m.Gate.Submit(ctx, model.Intent{Skill: "loops", Kind: "info", Topic: "lapsed:" + now.In(m.Opts.Loc).Format("2006-01-02"),
		Title:  fmt.Sprintf("기한이 2주 넘게 지난 할 일 %d개를 접었어요", len(folded)),
		Body:   "· " + strings.Join(folded, "\n· ") + "\n아직 할 일이면 말씀해 주세요.",
		Reason: "밤 정리", Confidence: 1})
	return err
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
	// The same plan read twice - a copy the calendar left, the same entry
	// told in two rooms - is one plan.
	if twin, err := m.twinBefore(ctx, p); err != nil || twin {
		return err
	}
	if err := st.CountPattern(ctx, model.Pattern{KindA: kind, Rel: model.PrepFor, Total: 1, LastSeen: sp.Start}); err != nil {
		return err
	}
	items, err := st.Occurrences(ctx, sp.Start.Add(-14*24*time.Hour), sp.Start, "purchase.item")
	if err != nil {
		return err
	}
	// The other plans of the kind in those two weeks: a thing bought before
	// one of them counts for that one, the nearest after it, never for
	// every plan that follows (one purchase was "bought before" four plans
	// in a row, and a rule rested on it, 2026-10-20).
	var starts []time.Time
	if others, err := st.PlansOn(ctx, sp.Start.Add(-14*24*time.Hour).In(m.Opts.Loc).Format("2006-01-02"), sp.Start.In(m.Opts.Loc).Format("2006-01-02")); err == nil {
		for _, o := range others {
			if o.ID == p.ID || o.State.Type != kind || (o.State.Status != model.Confirmed && o.State.Status != model.Done) {
				continue
			}
			if osp, ok := m.World.Span(ctx, o, m.Opts.Now(), m.Opts.Loc); ok && osp.Start.Before(sp.Start) {
				starts = append(starts, osp.Start)
			}
		}
	}
	counted := map[string]bool{}
	for i := len(items) - 1; i >= 0; i-- { // nearest first: a class counts once, at its nearest
		it := items[i]
		if slices.ContainsFunc(starts, func(s time.Time) bool { return s.After(it.At) }) {
			continue // bought for a plan nearer to it
		}
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

// twinBefore is whether another plan is this one told again and was there
// first: the same kind, day and title, not called off.
func (m *Mind) twinBefore(ctx context.Context, p model.Entity) (bool, error) {
	plans, err := m.Opts.Store.PlansOn(ctx, p.State.Date, p.State.Date)
	if err != nil {
		return false, err
	}
	title := strings.Join(strings.Fields(p.State.Title), " ")
	for _, o := range plans {
		if o.ID == p.ID || o.State.Status == model.Cancelled || o.State.Status == model.Proposed || o.State.Type != p.State.Type ||
			o.State.Date != p.State.Date || strings.Join(strings.Fields(o.State.Title), " ") != title {
			continue
		}
		if o.CreatedAt.Before(p.CreatedAt) || o.CreatedAt.Equal(p.CreatedAt) && o.ID < p.ID {
			return true, nil
		}
	}
	return false, nil
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

// pruneMessages drops message text past its retention.
func (m *Mind) pruneMessages(ctx context.Context) error {
	if m.Opts.MessageRetain <= 0 {
		return nil
	}
	n, err := m.Opts.Store.PruneMessages(ctx, m.Opts.Now().Add(-m.Opts.MessageRetain))
	if n > 0 {
		m.Opts.Logger.Info("보존 기한이 지난 메시지를 지웠습니다", "count", n)
	}
	return err
}
