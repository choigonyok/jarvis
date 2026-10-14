// Package predict says what is likely - "오늘 운동 65%", "11/14까지 장작
// 구매 60%" - from the notes and from comparable past days, registers each
// prediction as an expectation an occurrence can meet, and scores it once
// it is due. A prediction that misses is a surprise; only surprises are
// reflected on at night. While predictions hold, nothing here asks a model.
package predict

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/link"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

type Predictor struct {
	St    store.Store
	World *world.World
	Judge *link.Judge
	Loc   *time.Location
	Now   func() time.Time
}

// Surprise is how far off a prediction must be to be reflected on.
const Surprise = 0.5

// ID is a prediction's id from its target: predicting the same thing again
// updates it.
func ID(target string) string {
	sum := sha256.Sum256([]byte(target))
	return "pred_" + hex.EncodeToString(sum[:5])
}

// Put records a prediction and the expectations it waits on (keys).
func (p *Predictor) Put(ctx context.Context, pr model.Prediction, keys ...string) (model.Prediction, error) {
	pr.ID = ID(pr.Target)
	if old, err := p.St.Prediction(ctx, pr.ID); err == nil {
		if old.ScoredAt != nil {
			return old, nil // known already
		}
		pr.CreatedAt = old.CreatedAt
	} else if !errors.Is(err, model.ErrNotFound) {
		return pr, err
	}
	if pr.CreatedAt.IsZero() {
		pr.CreatedAt = p.Now()
	}
	pr.Prob = math.Round(pr.Prob*100) / 100
	if err := p.St.PutPrediction(ctx, pr); err != nil {
		return pr, err
	}
	for _, k := range keys {
		existing, _ := p.St.Expectations(ctx, pr.ID)
		met := false
		for _, e := range existing {
			met = met || e.Key == k && e.State == "met"
		}
		if met {
			continue
		}
		if err := p.St.PutExpectation(ctx, model.Expectation{PredictionID: pr.ID, Key: k, Due: pr.Due, State: "open"}); err != nil {
			return pr, err
		}
	}
	return pr, nil
}

// --- workouts ----------------------------------------------------------------

// history is the last four weeks of workout days, for the base rate.
const history = 28 * 24 * time.Hour

// Day is what one day looks like, for predicting and for reflecting.
type Day struct {
	Date    string
	Weekday time.Weekday
	Workout bool
	Routine string
	Occ     []model.Occurrence
	Plans   []model.Entity
}

// Days is the days in [from, to) with what happened in each: bounded by the
// window, never the whole history.
func (p *Predictor) Days(ctx context.Context, from, to time.Time) ([]Day, error) {
	occ, err := p.St.Occurrences(ctx, from, to)
	if err != nil {
		return nil, err
	}
	plans, err := p.St.PlansOn(ctx, from.In(p.Loc).Format("2006-01-02"), to.In(p.Loc).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	byDate := map[string]*Day{}
	var order []string
	for d := dayStart(from, p.Loc); d.Before(to); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		byDate[k] = &Day{Date: k, Weekday: d.Weekday()}
		order = append(order, k)
	}
	for _, o := range occ {
		d := byDate[o.At.In(p.Loc).Format("2006-01-02")]
		if d == nil {
			continue
		}
		d.Occ = append(d.Occ, o)
		if o.Kind == "workout" {
			d.Workout, d.Routine = true, o.Title
		}
	}
	for _, pl := range plans {
		if pl.State.Status == model.Cancelled || pl.State.Status == model.Proposed {
			continue
		}
		if d := byDate[pl.State.Date]; d != nil {
			d.Plans = append(d.Plans, pl)
		}
	}
	out := make([]Day, len(order))
	for i, k := range order {
		out[i] = *byDate[k]
	}
	return out, nil
}

func dayStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func weekday(w time.Weekday) bool { return w != time.Saturday && w != time.Sunday }

// Workout predicts whether the operator works out on day: the share of
// comparable days (weekdays or weekends) of the last four weeks that had a
// workout, moved by the rate notes that hold that day.
func (p *Predictor) Workout(ctx context.Context, day time.Time) (model.Prediction, bool, error) {
	day = dayStart(day, p.Loc)
	past, err := p.Days(ctx, day.Add(-history), day)
	if err != nil {
		return model.Prediction{}, false, err
	}
	n, k, sessions := 0, 0, 0
	for _, d := range past {
		if d.Workout {
			sessions++
		}
		if weekday(d.Weekday) == weekday(day.Weekday()) {
			n++
			if d.Workout {
				k++
			}
		}
	}
	if sessions < 4 || n == 0 {
		return model.Prediction{}, false, nil // too little to say anything
	}
	base := self.Shrunk(k, n, 0.5, 1)
	prob, why := base, fmt.Sprintf("최근 4주 %s %d일 중 %d일", map[bool]string{true: "평일", false: "주말"}[weekday(day.Weekday())], n, k)
	var basis []string
	today, err := p.Days(ctx, day, day.AddDate(0, 0, 1))
	if err != nil {
		return model.Prediction{}, false, err
	}
	notes, err := p.St.Notes(ctx)
	if err != nil {
		return model.Prediction{}, false, err
	}
	for _, note := range notes {
		r := note.Rule
		if note.Status == model.Rejected || r == nil || r.Kind != "rate" || r.Target != "workout" || len(today) == 0 {
			continue
		}
		if !Holds(r.Feature, r.Arg, today[0], p.Loc) {
			continue
		}
		w := note.Confidence
		if note.Status == model.Confirmed {
			w = 1
		}
		prob = prob + w*(r.Rate-prob)
		basis = append(basis, note.ID)
		why += " · " + note.Text
	}
	return model.Prediction{
		Target: "workout:" + day.Format("2006-01-02"), Text: fmt.Sprintf("%s 운동 %.0f%% (%s)", label(day, p.Now(), p.Loc), prob*100, why),
		Prob: prob, Due: day.AddDate(0, 0, 1).Add(-time.Minute), Basis: basis,
	}, true, nil
}

func label(day, now time.Time, loc *time.Location) string {
	switch dayStart(now, loc).Sub(day) / (24 * time.Hour) {
	case 0:
		return "오늘"
	case -1:
		return "내일"
	}
	return fmt.Sprintf("%d/%d", int(day.Month()), day.Day())
}

// --- prep --------------------------------------------------------------------

// Prep predicts, for an upcoming plan, the things the notes say are bought
// before plans of its kind: "11/14까지 장작 구매 60%", due the day before
// the plan, waiting for a purchase of that class.
func (p *Predictor) Prep(ctx context.Context, plan model.Entity) ([]model.Prediction, error) {
	st := plan.State
	if st.Status != model.Confirmed || st.Type == "" || st.Date == "" {
		return nil, nil
	}
	sp, ok := p.World.Span(ctx, plan, p.Now(), p.Loc)
	if !ok || !sp.Start.After(p.Now()) {
		return nil, nil
	}
	notes, err := p.St.Notes(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Prediction
	for _, n := range notes {
		r := n.Rule
		if n.Status == model.Rejected || r == nil || r.Kind != "prep" || r.PlanKind != st.Type {
			continue
		}
		due := dayStart(sp.Start, p.Loc).Add(-time.Minute)
		prob := self.Shrunk(r.Support, r.Total, 0.3, 1)
		if len(r.Except) > 0 {
			prob *= 0.8 // it does not always hold
		}
		pr, err := p.Put(ctx, model.Prediction{
			Target: "buy:" + r.Class + ":" + plan.ID, PlanID: plan.ID, Basis: []string{n.ID},
			Text: fmt.Sprintf("%d/%d까지 %s 구매 %.0f%%", int(due.Month()), due.Day(), r.Class, prob*100), Prob: prob, Due: due,
		}, "purchase.item:"+r.Class)
		if err != nil {
			return out, err
		}
		out = append(out, pr)
	}
	return out, nil
}

// --- matching and scoring ---------------------------------------------------

// Match checks one occurrence against the open expectations of its key
// only. It reports the predictions it met.
func (p *Predictor) Match(ctx context.Context, o model.Occurrence) ([]string, error) {
	var keys []string
	switch o.Kind {
	case "workout":
		keys = []string{"workout"}
	case "purchase.item":
		class, err := p.St.ThingClass(ctx, o.Title)
		if err == nil {
			keys = []string{"purchase.item:" + class}
			break
		}
		// Not classified yet: only if something is waiting on an item, ask
		// the judge (cached) for the classes the notes wait on.
		notes, _ := p.St.Notes(ctx)
		for _, n := range notes {
			if n.Rule == nil || n.Rule.Kind != "prep" || p.Judge == nil {
				continue
			}
			open, _ := p.St.OpenExpectations(ctx, "purchase.item:"+n.Rule.Class)
			if len(open) == 0 {
				continue
			}
			if j, err := p.Judge.Of(ctx, o.Title, n.Rule.PlanKind); err == nil && j.Class == n.Rule.Class {
				keys = append(keys, "purchase.item:"+j.Class)
			}
		}
	}
	var met []string
	for _, k := range keys {
		open, err := p.St.OpenExpectations(ctx, k)
		if err != nil {
			return met, err
		}
		for _, e := range open {
			if o.At.After(e.Due) {
				continue
			}
			if strings.HasPrefix(e.Key, "workout") {
				pr, err := p.St.Prediction(ctx, e.PredictionID)
				if err != nil || pr.Target != "workout:"+o.At.In(p.Loc).Format("2006-01-02") {
					continue
				}
			}
			e.State, e.MetBy = "met", o.ID
			if err := p.St.PutExpectation(ctx, e); err != nil {
				return met, err
			}
			met = append(met, e.PredictionID)
		}
	}
	return met, nil
}

// Score marks every prediction due by now: what happened (an expectation
// met), its Brier score, and whether it was a surprise.
func (p *Predictor) Score(ctx context.Context) ([]model.Prediction, error) {
	now := p.Now()
	due, err := p.St.DuePredictions(ctx, now)
	if err != nil {
		return nil, err
	}
	var out []model.Prediction
	for _, pr := range due {
		exps, err := p.St.Expectations(ctx, pr.ID)
		if err != nil {
			return out, err
		}
		happened := false
		for _, e := range exps {
			if e.State == "met" {
				happened = true
			} else if e.State == "open" {
				e.State = "missed"
				if err := p.St.PutExpectation(ctx, e); err != nil {
					return out, err
				}
			}
		}
		o := 0.0
		if happened {
			o = 1
		}
		score := (pr.Prob - o) * (pr.Prob - o)
		pr.Outcome, pr.Score, pr.ScoredAt = &happened, &score, &now
		pr.Surprise = math.Abs(pr.Prob-o) >= Surprise
		if err := p.St.PutPrediction(ctx, pr); err != nil {
			return out, err
		}
		out = append(out, pr)
	}
	return out, nil
}

// Brier is the mean Brier score of the predictions scored in [from, to),
// and how many: lower is better; always saying 50% scores 0.25.
func (p *Predictor) Brier(ctx context.Context, from, to time.Time) (float64, int, error) {
	prs, err := p.St.Predictions(ctx, from, to)
	if err != nil {
		return 0, 0, err
	}
	sum, n := 0.0, 0
	for _, pr := range prs {
		if pr.Score != nil {
			sum += *pr.Score
			n++
		}
	}
	if n == 0 {
		return 0, 0, nil
	}
	return sum / float64(n), n, nil
}

// --- features ---------------------------------------------------------------

// Features is what a reflection may test a hypothesis with: each says of a
// day, from its occurrences and plans, whether something held. They are
// what both a past day (what happened) and a coming one (what is planned)
// can be asked.
var Features = map[string]string{
	"late_leave": "arg 시각(HH:MM) 이후 퇴근: 그 시각 이후 교통 결제가 있었거나, 그 시각 2시간 전 이후에 업무 일정이 있음",
	"merchant":   "arg 낱말이 들어간 가맹점 결제가 있음",
	"plan_type":  "arg 종류의 일정이 있음",
	"weekday":    "arg 요일(월~일)",
	"evening":    "arg 시각(HH:MM) 이후에 시작하는 일정이 있음",
	"spent_over": "그날 결제 합이 arg 원 이상",
}

// Holds is whether feature(arg) holds on day d.
func Holds(feature, arg string, d Day, loc *time.Location) bool {
	switch feature {
	case "late_leave", "evening":
		at, err := time.Parse("15:04", arg)
		if err != nil {
			return false
		}
		cut := at.Hour()*60 + at.Minute()
		for _, o := range d.Occ {
			t := o.At.In(loc)
			if feature == "late_leave" && o.Kind == "purchase" && o.Class == "교통" && t.Hour()*60+t.Minute() >= cut {
				return true
			}
		}
		for _, pl := range d.Plans {
			st, err := time.Parse("15:04", pl.State.Start)
			if err != nil {
				continue
			}
			start := st.Hour()*60 + st.Minute()
			if feature == "evening" && start >= cut || feature == "late_leave" && pl.State.Type == "업무" && start >= cut-120 {
				return true
			}
		}
	case "merchant":
		for _, o := range d.Occ {
			if o.Kind == "purchase" && arg != "" && strings.Contains(o.Title, arg) {
				return true
			}
		}
	case "plan_type":
		for _, pl := range d.Plans {
			if pl.State.Type == arg {
				return true
			}
		}
	case "weekday":
		return []string{"일", "월", "화", "수", "목", "금", "토"}[d.Weekday] == arg
	case "spent_over":
		var limit, sum int64
		_, _ = fmt.Sscan(arg, &limit)
		for _, o := range d.Occ {
			if o.Kind == "purchase" {
				sum += o.Amount
			}
		}
		return limit > 0 && sum >= limit
	}
	return false
}

// Routine is the routine the operator usually does on a weekday, from the
// days given.
func Routine(days []Day, w time.Weekday) string {
	count := map[string]int{}
	for _, d := range days {
		if d.Weekday == w && d.Routine != "" {
			count[d.Routine]++
		}
	}
	var names []string
	for k := range count {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		return count[names[i]] > count[names[j]] || count[names[i]] == count[names[j]] && names[i] < names[j]
	})
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
