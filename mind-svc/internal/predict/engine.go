package predict

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
)

// How the engine's rules are judged by their record.
const (
	// trackMax: a rule's record is its latest predictions, this many.
	trackMax = 60
	// showSkill, showAccuracy: a candidate is shown once its predictions
	// (tried on the days before it, and made since) did this much better
	// than the plain rate, and called this share right.
	showSkill    = 0.15
	showAccuracy = 0.6
	showN        = 6
	// hideN: a rule shown goes back to testing when this many of its own
	// predictions did worse than the plain rate.
	hideN = 6
	// offN, offSkill: a rule stops predicting when this many did this badly.
	offN     = 10
	offSkill = -0.1
	// offRest: how long a rule that kept missing rests before it is tried
	// again (when the days still show it).
	offRest = 45 * 24 * time.Hour
)

// Why a rule stopped predicting.
const (
	reasonGone    = "밤 정리: 요즘 기록에선 보이지 않아 예측을 멈춤"
	reasonFailing = "예측이 계속 빗나가 예측을 멈춤"
)

// minLive is how many of its own predictions a rule must have made before
// it is shown: a daily rule makes one a day, a cycle one a round.
func minLive(kind string) int {
	switch kind {
	case Daily:
		return 3
	case After:
		return 2
	}
	return 1
}

// Keep reconciles the night's rules with the notes: a rule found for the
// first time is a candidate; one already known takes the new numbers (a new
// version only when its sentence or its standing moved); one the operator
// rejected stays rejected; one not found any more stops predicting.
func (p *Predictor) Keep(ctx context.Context, mined []model.Note) error {
	now := p.Now()
	notes, err := p.St.Notes(ctx)
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for _, n := range mined {
		found[n.ID] = true
		old, err := p.St.Note(ctx, n.ID)
		switch {
		case errors.Is(err, model.ErrNotFound):
			since := now
			n.Rule.Since = &since
			n.Status, n.Version, n.CreatedAt, n.UpdatedAt = model.Candidate, 1, now, now
			n.Confidence = confidence(*n.Rule.Test, model.Track{}, 0.5)
			n.Reason = "밤 정리: 새로 찾음, 예측으로 시험하는 중"
			if err := p.St.PutNote(ctx, n); err != nil {
				return err
			}
			continue
		case err != nil:
			return err
		case old.Status == model.Rejected:
			continue
		}
		r := *n.Rule
		reason := ""
		if old.Rule != nil {
			r.Except, r.Since, r.Off = old.Rule.Except, old.Rule.Since, old.Rule.Off
			if r.Off && (old.Reason == reasonGone || now.Sub(old.UpdatedAt) >= offRest) {
				since := now
				r.Off, r.Since, reason = false, &since, "밤 정리: 다시 보여 다시 시험하는 중"
			}
		}
		upd := old
		upd.Text, upd.Scope, upd.Rule = n.Text, n.Scope, &r
		upd.Evidence = append(n.Evidence, liveLines(old.Evidence)...)
		// A sentence shown to the operator keeps its versions; a candidate's
		// numbers move every night and nobody reads them.
		if reason == "" && upd.Text != old.Text && old.Status != model.Candidate {
			reason = "밤 정리: 숫자가 바뀜"
		}
		if reason != "" {
			upd.Version, upd.Reason, upd.UpdatedAt = old.Version+1, reason, now
		}
		if err := p.St.PutNote(ctx, upd); err != nil {
			return err
		}
	}
	for _, n := range notes {
		if !IsEngine(n.Rule) || found[n.ID] || n.Status == model.Rejected || n.Rule.Off {
			continue
		}
		n.Rule.Off = true
		if n.Status == model.Hypothesis {
			n.Status = model.Candidate
		}
		n.Version, n.Reason, n.UpdatedAt = n.Version+1, reasonGone, now
		if err := p.St.PutNote(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// Grade moves each of the engine's rules by how its predictions went: a
// candidate whose predictions came true - more than knowing only how often
// the thing happens would have said - is shown, a hypothesis for the
// operator to confirm; one shown whose predictions stopped coming true goes
// back to testing; one that keeps missing stops predicting. Its confidence
// is its record. A rule the operator confirmed is never hidden or stopped
// by its record; it only shows it.
func (p *Predictor) Grade(ctx context.Context) error {
	tracks, err := p.Tracks(ctx)
	if err != nil {
		return err
	}
	notes, err := p.St.Notes(ctx)
	if err != nil {
		return err
	}
	now := p.Now()
	for _, n := range notes {
		if !IsEngine(n.Rule) || n.Status == model.Rejected {
			continue
		}
		live := tracks[n.ID]
		test := model.Track{}
		if n.Rule.Test != nil {
			test = *n.Rule.Test
		}
		total := test.Plus(live)
		status, off, reason := n.Status, n.Rule.Off, ""
		switch {
		case status == model.Candidate && !off && live.N >= minLive(n.Rule.Kind) && total.N >= showN &&
			total.Skill() >= showSkill && total.Accuracy() >= showAccuracy:
			status, reason = model.Hypothesis, "예측이 맞아 와서 보여 드림: "+Words(total)
		case status == model.Hypothesis && live.N >= hideN && live.Skill() < 0:
			status, reason = model.Candidate, "최근 예측이 평소 빈도로 짐작한 것만 못해 다시 시험하는 중: "+Words(live)
		}
		if !off && status != model.Confirmed && live.N >= offN && live.Skill() < offSkill {
			off, reason = true, reasonFailing+": "+Words(live)
			if status == model.Hypothesis {
				status = model.Candidate
			}
		}
		conf := confidence(test, live, n.Confidence)
		lt := live
		upd := n
		upd.Status, upd.Confidence, upd.Track = status, conf, &lt
		r := *n.Rule
		r.Off = off
		upd.Rule = &r
		upd.Evidence = append(withoutLive(n.Evidence), liveLines([]string{liveLine(live)})...)
		if reason == "" && n.Status != model.Candidate && math.Abs(conf-n.Confidence) >= 0.05 {
			reason = "예측 기록: " + Words(total)
		}
		if reason != "" {
			upd.Version, upd.Reason, upd.UpdatedAt = n.Version+1, reason, now
		}
		if err := p.St.PutNote(ctx, upd); err != nil {
			return err
		}
	}
	return nil
}

// confidence is a rule's record as a share of calls right, from the days it
// was tried on and the predictions since; before either, what it had.
func confidence(test, live model.Track, had float64) float64 {
	t := test.Plus(live)
	if t.N == 0 {
		return had
	}
	return round2(t.Accuracy())
}

// Words is a record said: "예측 12번 중 10번 맞음 · 평소 빈도로 짐작할
// 때보다 오차 31% 적음".
func Words(t model.Track) string {
	s := fmt.Sprintf("예측 %d번 중 %d번 맞음", t.N, t.Right)
	if t.N == 0 {
		return s
	}
	switch sk := t.Skill(); {
	case sk >= 0.05:
		s += fmt.Sprintf(" · 평소 빈도로 짐작할 때보다 오차 %.0f%% 적음", sk*100)
	case sk <= -0.05:
		s += fmt.Sprintf(" · 평소 빈도로 짐작할 때보다 오차 %.0f%% 많음", -sk*100)
	default:
		s += " · 평소 빈도로 짐작한 것과 비슷"
	}
	return s
}

const livePrefix = "실제 예측: "

func liveLine(t model.Track) string {
	if t.N == 0 {
		return ""
	}
	return livePrefix + Words(t)
}

// liveLines is the record's line among a note's evidence, kept across the
// night's new numbers.
func liveLines(ev []string) []string {
	var out []string
	for _, e := range ev {
		if strings.HasPrefix(e, livePrefix) {
			out = append(out, e)
		}
	}
	return out
}

func withoutLive(ev []string) []string {
	var out []string
	for _, e := range ev {
		if !strings.HasPrefix(e, livePrefix) {
			out = append(out, e)
		}
	}
	return out
}

// RuleOf is the rule a prediction is counted under: the note it was made
// from, or - for the workout predictor, which reads the days themselves -
// its target's kind.
func RuleOf(pr model.Prediction) string {
	kind, _, _ := strings.Cut(pr.Target, ":")
	if kind == "workout" || len(pr.Basis) == 0 {
		return kind
	}
	return pr.Basis[0]
}

// baseOf is what the plain rate said; a prediction from before the engine
// said nothing to measure against, so it counts as no better and no worse.
func baseOf(pr model.Prediction) float64 {
	if pr.Base > 0 {
		return pr.Base
	}
	return pr.Prob
}

// Tracks is how each rule's predictions went: the scored predictions of the
// last year, by the rule they were made from (RuleOf), counted from the
// rule's Since, the latest trackMax of them.
func (p *Predictor) Tracks(ctx context.Context) (map[string]model.Track, error) {
	now := p.Now()
	prs, err := p.St.Predictions(ctx, now.AddDate(-1, 0, 0), now.Add(time.Minute))
	if err != nil {
		return nil, err
	}
	notes, err := p.St.Notes(ctx)
	if err != nil {
		return nil, err
	}
	since := map[string]time.Time{}
	for _, n := range notes {
		if n.Rule != nil && n.Rule.Since != nil {
			since[n.ID] = *n.Rule.Since
		}
	}
	by := map[string][]model.Prediction{}
	for _, pr := range prs {
		if pr.ScoredAt == nil || pr.Outcome == nil {
			continue
		}
		id := RuleOf(pr)
		if s, ok := since[id]; ok && pr.CreatedAt.Before(s) {
			continue
		}
		by[id] = append(by[id], pr)
	}
	out := map[string]model.Track{}
	for id, list := range by {
		if len(list) > trackMax {
			list = list[len(list)-trackMax:]
		}
		var t model.Track
		for _, pr := range list {
			t = t.Add(pr.Prob, baseOf(pr), *pr.Outcome)
		}
		out[id] = t
	}
	return out, nil
}

// Trusted is whether a rule's predictions may be said to the operator (or
// to the conversation): shown or confirmed - never a candidate.
func Trusted(n model.Note) bool {
	return (n.Status == model.Hypothesis || n.Status == model.Confirmed) && n.Rule != nil && !n.Rule.Off
}

// --- predicting --------------------------------------------------------------

// Expect makes today's predictions from the engine's rules, at the morning:
// each daily rule says how likely its thing is today; each cycle whose
// window has opened says how likely it comes round before the window ends.
// Candidates predict too - in silence, to be scored.
func (p *Predictor) Expect(ctx context.Context) ([]model.Prediction, error) {
	now := p.Now()
	today := dayStart(now, p.Loc)
	notes, err := p.St.Notes(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Prediction
	var errs []error
	for _, n := range notes {
		r := n.Rule
		if !IsEngine(r) || r.Off || n.Status == model.Rejected {
			continue
		}
		var pr model.Prediction
		switch r.Kind {
		case Daily:
			if len(r.Rates) != 7 {
				continue
			}
			prob, basis, why := p.rated(ctx, r.Key, r.Rates[today.Weekday()], today, notes)
			text := fmt.Sprintf("오늘 %s %.0f%%", Label(r.Key), prob*100)
			if why != "" {
				text += " (" + why + ")"
			}
			pr = model.Prediction{Target: "day:" + r.Key + ":" + today.Format(dateFmt), Text: text, Prob: prob, Base: r.Base,
				Key: r.Key, From: &today, Due: today.AddDate(0, 0, 1).Add(-time.Minute), Basis: append([]string{n.ID}, basis...)}
		case Cycle:
			last, err := time.ParseInLocation(dateFmt, r.Last, p.Loc)
			if err != nil || r.Gap <= 0 {
				continue
			}
			start, end := last.AddDate(0, 0, r.Gap-r.Tol), last.AddDate(0, 0, r.Gap+r.Tol)
			if today.Before(start) || today.After(end) {
				continue
			}
			base := windowBase(r.Base, r.Tol)
			prob := self.Shrunk(r.Support, r.Total, base, 1)
			pr = model.Prediction{Target: "cycle:" + r.Key + ":" + r.Last, Prob: prob, Base: base, Key: r.Key, From: &start,
				Text: fmt.Sprintf("%d/%d까지 %s %.0f%% (보통 %d일마다)", int(end.Month()), end.Day(), Label(r.Key), prob*100, r.Gap),
				Due:  end.AddDate(0, 0, 1).Add(-time.Minute), Basis: []string{n.ID}}
		default:
			continue
		}
		saved, err := p.Put(ctx, pr, r.Key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, saved)
	}
	return out, errors.Join(errs...)
}

// rated moves a day's share by the rate notes about the same thing that
// hold today (what the night's reflection found: "21시 이후 퇴근한 날은
// …"), as the workout prediction does.
func (p *Predictor) rated(ctx context.Context, key string, prob float64, day time.Time, notes []model.Note) (float64, []string, string) {
	var today []Day
	var basis, why []string
	for _, n := range notes {
		r := n.Rule
		if n.Status == model.Rejected || r == nil || r.Kind != "rate" || r.Target != key {
			continue
		}
		if today == nil {
			var err error
			if today, err = p.Days(ctx, day, day.AddDate(0, 0, 1)); err != nil || len(today) == 0 {
				return prob, nil, ""
			}
		}
		if !Holds(r.Feature, r.Arg, today[0], p.Loc) {
			continue
		}
		w := n.Confidence
		if n.Status == model.Confirmed {
			w = 1
		}
		prob += w * (r.Rate - prob)
		basis = append(basis, n.ID)
		why = append(why, n.Text)
	}
	return prob, basis, strings.Join(why, " · ")
}

// Follow makes the predictions an occurrence starts: of each after-rule
// whose anchor it is, the thing that usually follows within hours.
func (p *Predictor) Follow(ctx context.Context, o model.Occurrence) ([]model.Prediction, error) {
	keys := Keys(o, p.thingOf(ctx))
	if len(keys) == 0 {
		return nil, nil
	}
	notes, err := p.St.Notes(ctx)
	if err != nil {
		return nil, err
	}
	now := p.Now()
	var out []model.Prediction
	for _, n := range notes {
		r := n.Rule
		if r == nil || r.Kind != After || r.Off || n.Status == model.Rejected || !slices.Contains(keys, r.Anchor) {
			continue
		}
		from, due := o.At.Add(afterGap), o.At.Add(time.Duration(r.Hours)*time.Hour)
		if !due.After(now) {
			continue // over already (a late reading of an old one)
		}
		prob := self.Shrunk(r.Support, r.Total, r.Base, 2)
		pr, err := p.Put(ctx, model.Prediction{Target: "after:" + n.ID + ":" + o.ID, Prob: prob, Base: r.Base, Key: r.Key, From: &from, Due: due,
			Text:  fmt.Sprintf("%s 뒤 %s 안에 %s %.0f%%", Label(r.Anchor), hoursWord(r.Hours), Label(r.Key), prob*100),
			Basis: []string{n.ID}}, r.Key)
		if err != nil {
			return out, err
		}
		out = append(out, pr)
	}
	return out, nil
}

// catchUp meets, at once, an expectation whose thing already happened
// between its start and now - a delivery at 1 a.m. for a prediction made at
// seven - which no occurrence arriving later would.
func (p *Predictor) catchUp(ctx context.Context, pr model.Prediction, keys []string) error {
	if pr.From == nil {
		return nil
	}
	to := p.Now()
	if pr.Due.Before(to) {
		to = pr.Due
	}
	if !pr.From.Before(to) {
		return nil
	}
	thing := p.thingOf(ctx)
	for _, k := range keys {
		if strings.Contains(k, ":") {
			continue // a prep's class key: matched as items come
		}
		occ, err := p.St.Occurrences(ctx, *pr.From, to.Add(time.Second), Source(k))
		if err != nil {
			return err
		}
		for _, o := range occ {
			if !slices.Contains(Keys(o, thing), k) {
				continue
			}
			if err := p.St.PutExpectation(ctx, model.Expectation{PredictionID: pr.ID, Key: k, From: *pr.From, Due: pr.Due, State: "met", MetBy: o.ID}); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
