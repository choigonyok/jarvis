package mind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
)

// The morning (07:00) is when the day is predicted: yesterday's predictions
// are scored, today's (and tomorrow's) are made and registered as
// expectations, and what the notes say should already have happened by now
// and has not - firewood two days before camping - is brought up.

const morningAt = 7 * 60

// Morning runs the morning's predictions now. Each stage runs even if an
// earlier one failed.
func (m *Mind) Morning(ctx context.Context) error {
	now := m.Opts.Now().In(m.Opts.Loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, m.Opts.Loc)
	steps := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := m.Predict.Score(ctx)
			return err
		},
		func(ctx context.Context) error { return m.morningWorkout(ctx, today) },
		func(ctx context.Context) error {
			plans, err := m.Opts.Store.PlansOn(ctx, today.Format("2006-01-02"), today.AddDate(0, 0, 14).Format("2006-01-02"))
			if err != nil {
				return err
			}
			var errs []error
			for _, p := range plans {
				if _, err := m.Predict.Prep(ctx, p); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		},
	}
	steps = append(steps, m.mornings...)
	steps = append(steps, func(ctx context.Context) error { return m.prepCards(ctx, now) })
	return m.stages(ctx, "morning", steps)
}

func (m *Mind) morningWorkout(ctx context.Context, today time.Time) error {
	todayP, okToday, err := m.Predict.Workout(ctx, today)
	if err != nil {
		return err
	}
	if okToday {
		if todayP, err = m.Predict.Put(ctx, todayP, "workout"); err != nil {
			return err
		}
	}
	tomorrow, okTomorrow, err := m.Predict.Workout(ctx, today.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	if okTomorrow {
		if tomorrow, err = m.Predict.Put(ctx, tomorrow, "workout"); err != nil {
			return err
		}
		if okToday {
			m.moveWorkout(ctx, today, todayP, tomorrow)
		}
	}
	return nil
}

// moveWorkout: tomorrow looks unlikely for a workout that is usually done -
// a late meeting - while today looks fine, so the evening's summary says so
// once. It does not ask: moving a workout is the operator's to decide.
func (m *Mind) moveWorkout(ctx context.Context, today time.Time, todayP, tomorrow model.Prediction) {
	if tomorrow.Prob >= 0.3 || todayP.Prob < 0.5 || len(tomorrow.Basis) == 0 {
		return
	}
	days, err := m.Predict.Days(ctx, today.Add(-28*24*time.Hour), today)
	if err != nil {
		return
	}
	routine := predict.Routine(days, today.AddDate(0, 0, 1).Weekday())
	if routine == "" {
		routine = "운동"
	}
	w := []string{"일", "월", "화", "수", "목", "금", "토"}
	why := ""
	if n, err := m.Opts.Store.Note(ctx, tomorrow.Basis[0]); err == nil {
		why = " · " + n.Text
	}
	_, _ = m.Gate.Submit(ctx, model.Intent{
		Skill: "predict", Kind: "info", Topic: "move:" + tomorrow.Target,
		Title:  fmt.Sprintf("내일(%s) 운동 확률 %.0f%%. %s를 오늘(%s)로?", w[today.AddDate(0, 0, 1).Weekday()], tomorrow.Prob*100, routine, w[today.Weekday()]),
		Body:   strings.TrimPrefix(why, " · "),
		Reason: "내일 예측이 낮음", Confidence: 1,
	})
}

// prepCards brings up a prep prediction still unmet two or three days
// before it is due: "11/15 캠핑. 지난번엔 3일 전에 장작 10kg(2만 원)을
// 샀어요. 담아둘까요?", with the last time's purchase and the plan's own
// words as grounds.
func (m *Mind) prepCards(ctx context.Context, now time.Time) error {
	prs, err := m.Opts.Store.Predictions(ctx, now, now.Add(4*24*time.Hour))
	if err != nil {
		return err
	}
	for _, pr := range prs {
		if !strings.HasPrefix(pr.Target, "buy:") || pr.ScoredAt != nil || pr.PlanID == "" {
			continue
		}
		left := pr.Due.Sub(now)
		if left < 24*time.Hour || left > 3*24*time.Hour {
			continue
		}
		exps, _ := m.Opts.Store.Expectations(ctx, pr.ID)
		met := false
		for _, e := range exps {
			met = met || e.State == "met"
		}
		if met || len(pr.Basis) == 0 {
			continue
		}
		in, ok := m.prepIntent(ctx, pr)
		if !ok {
			continue
		}
		if _, err := m.Gate.Submit(ctx, in); err != nil {
			return err
		}
	}
	return nil
}

// onExpect is an expectation's timer: due, the prediction is scored now -
// a miss is a surprise for tonight's reflection; ahead, a prep purchase
// still unmet is brought up, as the morning would.
func (m *Mind) onExpect(ctx context.Context, t model.Timer) {
	switch t.Name {
	case predict.DueTimer:
		pr, err := m.Predict.ScoreOne(ctx, t.EntityID)
		if err != nil {
			m.Opts.Logger.Warn("예측을 채점하지 못했습니다", "err", err)
			return
		}
		if pr.Surprise {
			m.Opts.Logger.Info("예측이 빗나갔습니다(오늘 밤 성찰)", "prediction", pr.Text)
		}
	case predict.LeadTimer:
		pr, err := m.Opts.Store.Prediction(ctx, t.EntityID)
		if err != nil || pr.ScoredAt != nil || len(pr.Basis) == 0 {
			return
		}
		exps, _ := m.Opts.Store.Expectations(ctx, pr.ID)
		for _, e := range exps {
			if e.State == "met" {
				return
			}
		}
		if in, ok := m.prepIntent(ctx, pr); ok {
			if _, err := m.Gate.Submit(ctx, in); err != nil {
				m.Opts.Logger.Warn("준비 카드를 넘기지 못했습니다", "err", err)
			}
		}
	}
}

func (m *Mind) prepIntent(ctx context.Context, pr model.Prediction) (model.Intent, bool) {
	st := m.Opts.Store
	note, err := st.Note(ctx, pr.Basis[0])
	if err != nil || note.Rule == nil || note.Status == model.Rejected {
		return model.Intent{}, false
	}
	plan, err := st.Entity(ctx, pr.PlanID)
	if err != nil || plan.State.Status != model.Confirmed {
		return model.Intent{}, false
	}
	class := note.Rule.Class
	// The last time: the purchase linked to an earlier plan of the kind.
	last, lastPlan := m.lastPrep(ctx, plan, class)
	day := func(d string) string {
		t, err := time.ParseInLocation("2006-01-02", d, m.Opts.Loc)
		if err != nil {
			return d
		}
		return fmt.Sprintf("%d/%d", int(t.Month()), t.Day())
	}
	title := fmt.Sprintf("%s %s. ", day(plan.State.Date), plan.State.Type)
	item := class
	var ev []model.Evidence
	if last != nil {
		before := ""
		if d, err := time.ParseInLocation("2006-01-02", lastPlan.State.Date, m.Opts.Loc); err == nil {
			bought := last.At.In(m.Opts.Loc)
			bought = time.Date(bought.Year(), bought.Month(), bought.Day(), 0, 0, 0, 0, m.Opts.Loc)
			before = fmt.Sprintf("%d일 전에 ", int(math.Round(d.Sub(bought).Hours()/24)))
		}
		item = last.Title
		title += fmt.Sprintf("지난번엔 %s%s(%s)을 샀어요. 담아둘까요?", before, last.Title, money(last.Amount))
		ev = append(ev, model.Evidence{Ref: last.Ref, Quote: last.Quote})
		if cs, err := st.Changes(ctx, lastPlan.ID, 50); err == nil && len(cs) > 0 {
			first := cs[len(cs)-1]
			ev = append(ev, first.Evidence...)
		}
	} else {
		title += fmt.Sprintf("보통 %s 전에 %s 사셨어요. 담아둘까요?", plan.State.Type, self.Object(class))
	}
	body := note.Text + fmt.Sprintf(" (가능성 %.0f%%)", pr.Prob*100)
	if note.Rule != nil && len(note.Rule.Except) > 0 {
		body += "\n" + strings.Join(note.Rule.Except, ", ")
	}
	payload, _ := json.Marshal(map[string]string{"item": item, "class": class, "planId": plan.ID, "noteId": note.ID, "site": "coupang.com"})
	return model.Intent{
		Skill: "predict", Kind: "cart.add", EntityID: plan.ID, Topic: "prep:" + class + ":" + plan.ID, Context: "prep:" + plan.State.Type,
		Payload: payload, Title: title, Body: body, Reason: "준비물을 아직 안 사셨어요", Confidence: math.Max(0.75, pr.Prob),
		Value: 0.9, Evidence: ev, Reversible: true,
	}, true
}

// lastPrep is the most recent purchase of the class linked to an earlier
// plan of the same kind - what "지난번" refers to.
func (m *Mind) lastPrep(ctx context.Context, plan model.Entity, class string) (*model.Occurrence, model.Entity) {
	st := m.Opts.Store
	sp, ok := m.World.Span(ctx, plan, m.Opts.Now(), m.Opts.Loc)
	if !ok {
		return nil, model.Entity{}
	}
	items, err := st.Occurrences(ctx, sp.Start.Add(-120*24*time.Hour), sp.Start, "purchase.item")
	if err != nil {
		return nil, model.Entity{}
	}
	for i := len(items) - 1; i >= 0; i-- {
		it := items[i]
		if c, err := st.ThingClass(ctx, it.Title); err != nil || c != class {
			continue
		}
		links, _ := st.LinksOf(ctx, it.ID)
		for _, l := range links {
			if l.From != it.ID || (l.Rel != model.PrepFor && l.Rel != model.UsedDuring) || l.State == model.Rejected || l.To == plan.ID {
				continue
			}
			if p, err := st.Entity(ctx, l.To); err == nil && p.State.Type == plan.State.Type {
				return &it, p
			}
		}
	}
	return nil, model.Entity{}
}

// money is 20000 as "2만 원", 23500 as "2만 3,500원", 8400 as "8,400원".
func money(n int64) string {
	if n < 10000 {
		return act.Won(n) + "원"
	}
	if r := n % 10000; r > 0 {
		return fmt.Sprintf("%d만 %s원", n/10000, act.Won(r))
	}
	return fmt.Sprintf("%d만 원", n/10000)
}

// learn hears what came of an intent made from a note: approved, the note
// is surer; turned down, its reason becomes a condition.
func (m *Mind) learn(ctx context.Context, in model.Intent) {
	if in.Skill != "predict" || len(in.Payload) == 0 {
		return
	}
	var p struct {
		NoteID string `json:"noteId"`
	}
	if json.Unmarshal(in.Payload, &p) != nil || p.NoteID == "" {
		return
	}
	switch in.Status {
	case model.Executed:
		_ = m.Self.Learn(ctx, p.NoteID, true, "")
	case model.Rejected:
		_ = m.Self.Learn(ctx, p.NoteID, false, in.Error)
	}
}

// notesFromPatterns writes a note for each repetition worth one: a thing
// bought before plans of a kind, where common sense agrees (one time is
// enough to say it, as a hypothesis) or the repetition is long enough to
// say it without.
func (m *Mind) notesFromPatterns(ctx context.Context, kinds []string) error {
	st := m.Opts.Store
	for _, kind := range kinds {
		ps, err := st.Patterns(ctx, kind)
		if err != nil {
			return err
		}
		total := 0
		support := map[string]int{}
		best := map[string]model.Pattern{}
		for _, p := range ps {
			if p.KindB == "" {
				total = p.Total
				continue
			}
			support[p.KindB] += p.Support
			if p.Support > best[p.KindB].Support {
				best[p.KindB] = p
			}
		}
		classes := make([]string, 0, len(support))
		for c := range support {
			classes = append(classes, c)
		}
		slices.Sort(classes)
		for _, class := range classes {
			n := support[class]
			j, err := st.LinkCache(ctx, class, kind)
			common := err == nil && j.Rel != "unrelated" && j.Plausibility >= 0.5
			if !common && (n < 3 || err == nil && j.Plausibility < 0.1) {
				continue
			}
			b := best[class].Bucket
			note := model.Note{ID: self.ID("prep", kind, class), Text: self.PrepText(kind, class, b), Scope: kind,
				Confidence: math.Round(self.Shrunk(n, max(total, n), 0.3, 1)*100) / 100,
				Evidence:   []string{fmt.Sprintf("%s %d번 중 %d번, 시작 전에 %s 샀다", kind, max(total, n), n, self.Object(class))},
				Rule:       &model.Rule{Kind: "prep", PlanKind: kind, Class: class, Bucket: b, Support: n, Total: max(total, n)}}
			if _, _, err := m.Self.Hold(ctx, note, fmt.Sprintf("밤 정리: %s %d번 중 %d번", kind, max(total, n), n)); err != nil {
				return err
			}
		}
	}
	return nil
}
