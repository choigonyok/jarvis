// Package link draws the relations between what happened and what was
// planned that need no judgement: a payment or a photo inside a plan's time
// and place happened during it, and a plan's people were with the operator.
// (A purchase made for a plan - firewood for camping - needs judgement, and
// is the judge's.)
//
// Each new occurrence is matched against the plans of its own days only,
// and each changed plan against the occurrences inside its span - never the
// whole history.
package link

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

type Linker struct {
	St    store.Store
	World *world.World
	Loc   *time.Location
	Now   func() time.Time
	Log   *slog.Logger
	// OnLink hears every link drawn or changed (the judge and the skills
	// look again).
	OnLink func(ctx context.Context, l model.Link)
	// Judge is common sense for purchases made for a plan; nil draws only
	// the links that need none.
	Judge *Judge
	// OnPlan hears each changed plan after it was linked (predictions are
	// made for it).
	OnPlan func(ctx context.Context, e model.Entity)

	mu      sync.Mutex
	pending []string // plans changed, to be linked
}

// Kinds of occurrence that can happen during a plan.
var duringKinds = []string{"purchase", "photo", "workout"}

// online merchants: a purchase there is not made where the plan is.
var online = []string{"쿠팡", "COUPANG", "네이버페이", "NAVERPAY", "11번가", "G마켓", "지마켓", "옥션", "무신사", "29CM", "SSG",
	"컬리", "알리익스프레스", "ALIEXPRESS", "AMAZON", "아마존", "테무", "TEMU", "에이블리", "지그재그", "APPLE.COM", "GOOGLE", "NETFLIX"}

// delivery apps: bought from wherever the operator is.
var delivery = []string{"배달의민족", "배민", "우아한형제", "쿠팡이츠", "요기요", "땡겨요"}

// Watch queues every changed plan for linking; Settle or Run links them.
func (l *Linker) Watch(e model.Entity, _ model.Change) {
	if e.Kind != model.KindPlan {
		return
	}
	l.mu.Lock()
	if !slices.Contains(l.pending, e.ID) {
		l.pending = append(l.pending, e.ID)
	}
	l.mu.Unlock()
}

// Run links queued plans every so often until ctx ends.
func (l *Linker) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.Settle(ctx)
		}
	}
}

// Settle links every queued plan now.
func (l *Linker) Settle(ctx context.Context) {
	l.mu.Lock()
	ids := l.pending
	l.pending = nil
	l.mu.Unlock()
	for _, id := range ids {
		e, err := l.World.Entity(ctx, id)
		if err != nil {
			continue
		}
		if err := l.Plan(ctx, e); err != nil {
			l.Log.Warn("일정을 잇지 못했습니다", "plan", id, "err", err)
		}
		if l.OnPlan != nil {
			l.OnPlan(ctx, e)
		}
	}
}

// Occurrence links one occurrence to the plans of its days.
func (l *Linker) Occurrence(ctx context.Context, id string) error {
	o, err := l.St.Occurrence(ctx, id)
	if err != nil {
		return nil
	}
	if o.Kind == "transfer" {
		return l.paid(ctx, o)
	}
	if o.Kind == "purchase.item" {
		day := o.At.In(l.Loc)
		plans, err := l.St.PlansOn(ctx, day.Format("2006-01-02"), day.Add(window).Format("2006-01-02"))
		if err != nil {
			return err
		}
		return l.prepAll(ctx, o, plans)
	}
	if !slices.Contains(duringKinds, o.Kind) {
		return nil
	}
	day := o.At.In(l.Loc)
	plans, err := l.St.PlansOn(ctx, day.AddDate(0, 0, -1).Format("2006-01-02"), day.Format("2006-01-02"))
	if err != nil {
		return err
	}
	for _, p := range plans {
		if err := l.during(ctx, o, p); err != nil {
			return err
		}
	}
	return nil
}

// Plan links a plan to its people and to what happened inside its span.
func (l *Linker) Plan(ctx context.Context, p model.Entity) error {
	if !planned(p) {
		return nil
	}
	for _, name := range p.State.People {
		if err := l.put(ctx, model.Link{From: p.ID, To: "person:" + name, Rel: model.With, Confidence: 1, State: model.Confirmed,
			Why: "일정에 함께하는 사람", By: "rule"}); err != nil {
			return err
		}
	}
	sp, ok := l.World.Span(ctx, p, l.Now(), l.Loc)
	if !ok {
		return nil
	}
	if l.Judge != nil && p.State.Type != "" && p.State.Type != "기타" {
		items, err := l.St.Occurrences(ctx, sp.Start.Add(-window), sp.Start, "purchase.item")
		if err != nil {
			return err
		}
		for _, it := range items {
			plans, err := l.St.PlansOn(ctx, it.At.In(l.Loc).Format("2006-01-02"), it.At.Add(window).In(l.Loc).Format("2006-01-02"))
			if err != nil {
				return err
			}
			if err := l.prepAll(ctx, it, plans); err != nil {
				return err
			}
		}
	}
	occ, err := l.St.Occurrences(ctx, sp.Start.Add(-early), sp.End, duringKinds...)
	if err != nil {
		return err
	}
	for _, o := range occ {
		if err := l.during(ctx, o, p); err != nil {
			return err
		}
	}
	return nil
}

const early = 30 * time.Minute

// Happened marks a link raised because its plan was seen to happen.
const Happened = " · 일정이 실제로 있었음"

func planned(p model.Entity) bool {
	s := p.State.Status
	return p.Kind == model.KindPlan && (s == model.Confirmed || s == model.Done) && p.State.Date != ""
}

// during decides whether o happened during plan p, and how sure that is.
//   - Inside the plan's time and in its place (neighbourhood or name): yes.
//   - Inside its time, somewhere else: no.
//   - Inside its time, with no place to compare: perhaps (estimated) - unless
//     what it was fits the plan (a restaurant bill during a dinner, a meal
//     delivered during a date at home), which makes it yes.
//
// An untimed one-day plan has no time to be inside: only its place counts.
func (l *Linker) during(ctx context.Context, o model.Occurrence, p model.Entity) error {
	if !planned(p) {
		return nil
	}
	if o.Kind == "purchase" && containsAny(o.Title, online) {
		return nil
	}
	sp, ok := l.World.Span(ctx, p, l.Now(), l.Loc)
	if !ok || o.At.Before(sp.Start.Add(-early)) || !o.At.Before(sp.End) {
		return nil
	}
	st := p.State
	timed := st.Start != "" || st.EndDate != ""
	place := world.Known(o.Area, o.Place, st.Area, st.Place)
	same := place && world.SamePlace(o.Area, o.Place, st.Area, st.Place)
	link := model.Link{From: o.ID, To: p.ID, Rel: model.During, By: "rule", Evidence: []string{o.Ref}}
	if o.Kind == "photo" {
		// A photo's place is a city ("Seoul", "Gapyeong"): it can tell a trip
		// from home, not one street from the next.
		switch {
		case same:
			link.Confidence, link.State, link.Why = 0.9, model.Confirmed, "일정 시간·장소 안의 사진"
		case place && world.City(o.Area) && world.City(st.Area):
			return nil
		case !timed:
			return nil
		case together(o.People, st.People):
			link.Confidence, link.State, link.Why = 0.85, model.Confirmed, "일정 시간 안, 함께한 사람이 찍힌 사진"
		default:
			link.Confidence, link.State, link.Why = 0.6, model.Estimated, "일정 시간 안의 사진"
		}
		return l.put(ctx, link)
	}
	switch {
	case same:
		link.Confidence, link.State, link.Why = 0.9, model.Confirmed, "일정 시간·장소 안"
	case place && !containsAny(o.Title, delivery):
		return nil // somewhere else
	case !timed:
		return nil
	case fits(o, st):
		link.Confidence, link.State, link.Why = 0.8, model.Confirmed, "일정 시간 안, 일정과 맞는 결제"
	default:
		link.Confidence, link.State, link.Why = 0.6, model.Estimated, "일정 시간 안(장소는 모름)"
	}
	return l.put(ctx, link)
}

// fits is whether what was bought is what the plan is: a meal during a
// dinner or a get-together, food delivered during a date at home.
func fits(o model.Occurrence, st model.State) bool {
	if o.Kind != "purchase" {
		return false
	}
	food := o.Class == "식비" || o.Class == "카페·간식" || containsAny(o.Title, delivery)
	switch st.Type {
	case "식사", "회식", "모임":
		return food
	case "데이트":
		return food && (st.PlaceKind == "집" || containsAny(o.Title, delivery))
	case "공연":
		return o.Class == "문화·여가"
	case "운동":
		return o.Class == "운동"
	}
	return false
}

// put writes a link unless the operator already said what it is.
func (l *Linker) put(ctx context.Context, link model.Link) error {
	existing, err := l.St.LinksOf(ctx, link.From)
	if err != nil {
		return err
	}
	now := l.Now()
	link.CreatedAt, link.UpdatedAt = now, now
	for _, e := range existing {
		if e.To != link.To || e.Rel != link.Rel {
			continue
		}
		if link.By == "judge" && strings.Contains(e.Why, Happened) && !strings.Contains(link.Why, Happened) {
			// Judged again (the plan changed): the plan having happened still
			// stands for it.
			link.Confidence = min(0.9, math.Round((link.Confidence+0.1)*100)/100)
			link.Why += Happened
		}
		if e.By == "user" || e.State == model.Rejected ||
			e.State == link.State && e.Confidence == link.Confidence {
			return nil
		}
		if e.State == model.Confirmed && link.State == model.Estimated {
			return nil
		}
		link.CreatedAt = e.CreatedAt
	}
	if err := l.St.PutLink(ctx, link); err != nil {
		return err
	}
	if l.OnLink != nil {
		l.OnLink(ctx, link)
	}
	return nil
}

func together(inPhoto, inPlan []string) bool {
	for _, a := range inPhoto {
		for _, b := range inPlan {
			if samePerson(a, b) {
				return true
			}
		}
	}
	return false
}

func containsAny(s string, words []string) bool {
	u := strings.ToUpper(s)
	for _, w := range words {
		if strings.Contains(u, strings.ToUpper(w)) {
			return true
		}
	}
	return false
}

// prepAll judges an item against every plan it may have been bought for,
// sharing it among the plans common sense allows (exclusivity).
func (l *Linker) prepAll(ctx context.Context, item model.Occurrence, plans []model.Entity) error {
	if l.Judge == nil {
		return nil
	}
	var fit []model.Entity
	for _, p := range plans {
		if !planned(p) || p.State.Type == "" || p.State.Type == "기타" {
			continue
		}
		sp, ok := l.World.Span(ctx, p, l.Now(), l.Loc)
		if !ok || !sp.Start.After(item.At) || sp.Start.Sub(item.At) > window {
			continue
		}
		j, err := l.Judge.Of(ctx, item.Title, p.State.Type)
		if errors.Is(err, ErrNoModel) {
			return nil
		}
		if err != nil {
			return err
		}
		if j.Rel != "unrelated" && j.Plausibility >= weak {
			fit = append(fit, p)
		}
	}
	for _, p := range fit {
		if err := l.prep(ctx, item, p, len(fit)); err != nil {
			return err
		}
	}
	return nil
}

// paid closes what someone owed the operator once their transfer arrives:
// the open settlement loop for that person and (about) that amount.
func (l *Linker) paid(ctx context.Context, o model.Occurrence) error {
	if len(o.People) == 0 || o.Amount <= 0 {
		return nil
	}
	loops, err := l.World.Entities(ctx, model.KindLoop, "", true)
	if err != nil {
		return err
	}
	for _, lp := range loops {
		st := lp.State
		if st.Type != "정산" || st.Owner != "them" || len(st.People) != 1 || !samePerson(st.People[0], o.People[0]) {
			continue
		}
		var owed int64
		_, _ = fmt.Sscan(st.Note, &owed)
		if owed > 0 && (o.Amount < owed*9/10 || o.Amount > owed*3) {
			continue
		}
		meta := world.Meta{Actor: "observer", Why: "송금이 들어옴", Evidence: []model.Evidence{{Ref: o.Ref, Quote: o.Quote}}}
		if _, _, err := l.World.Update(ctx, lp.ID, func(s *model.State) bool { s.Status = model.Closed; return true }, meta); err != nil {
			return err
		}
		return nil
	}
	return nil
}

// samePerson: a transfer names the sender in full ("김민수"), a plan by how
// the operator calls them ("민수").
func samePerson(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && b != "" && (a == b || strings.HasSuffix(a, b) || strings.HasSuffix(b, a))
}
