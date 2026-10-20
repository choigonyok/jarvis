// Package reflect looks, at night, at what was predicted wrong - and only
// that. A model is shown the surprising days next to ordinary ones and
// asked what might set them apart, in terms of features a day can be
// checked for; each hypothesis is then tested against the days by count,
// not taken on the model's word: enough days, a real difference (Fisher's
// exact test, false discovery rate controlled across the night's
// hypotheses), shrunk toward the usual rate when the days are few, and set
// against the other hypotheses it overlaps with - a bus route taken only on
// late nights says nothing the late nights did not. What survives is a
// note, a hypothesis until the operator confirms it, and worded as what
// was seen, not as a cause.
package reflect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

type Reflector struct {
	St      store.Store
	LLM     llm.Client
	Predict *predict.Predictor
	Self    *self.Self
	Loc     *time.Location
	Now     func() time.Time
}

// Checks of a hypothesis.
const (
	window     = 60 * 24 * time.Hour // the days a hypothesis is tested on
	minSupport = 5                   // days the feature held, at least
	minEffect  = 0.25                // the rates must differ by this much
	fdr        = 0.1                 // false discovery rate across a night
	prior      = 1.0                 // pseudo-days of shrinkage toward the base
	overlap    = 0.6                 // Jaccard above which two features are one
)

// System is what the model is asked at night.
const System = `너는 개인 비서의 "성찰" 단계다. 예측이 틀린 날들과 평소의 날들이 주어진다. 틀린 날들을 평소와 가르는 것이 무엇일 수 있는지 가설을 세운다. 원인이라고 단정하지 말고 관찰로 쓴다.
text 에는 그 특징이 말하는 조건만 일반적으로 쓴다(특정 날짜·가맹점·노선 번호·횟수 같은 근거는 넣지 않는다. 근거는 따로 남는다). 각 가설은 하나의 특징만 말한다.
가설은 반드시 주어진 특징 목록 중 하나와 그 인자로 확인할 수 있어야 한다. 확인할 수 없는 가설(기분처럼 데이터에 없는 것)은 내지 않는다. 날씨는 날마다 적힌 만큼만 안다.
JSON 하나만: {"hypotheses":[{"text":"21시 이후 퇴근한 날은 운동을 거의 안 한다","feature":"late_leave","arg":"21:00"}]}
최대 4개.`

// Hypothesis is one the model proposed, and what testing it found.
type Hypothesis struct {
	Text    string `json:"text"`
	Feature string `json:"feature"`
	Arg     string `json:"arg"`

	With, Without [2]int  // days the feature held / did not: [days, with the target]
	P             float64 // Fisher's exact, two-sided
	Rate, Base    float64 // shrunk rate on feature days; rate on the others
	Kept          bool
	Why           string // why it was dropped, or what it rests on
	days          map[string]bool
}

// Result is a night's reflection on one target.
type Result struct {
	Target     string        `json:"target"`
	Surprises  int           `json:"surprises"`
	Hypotheses []*Hypothesis `json:"hypotheses"`
	Notes      []model.Note  `json:"notes"`
}

// Night reflects on the surprises not reflected on yet: the workout
// predictor's, and those of the engine's daily rules that have come true
// often enough to be shown - a candidate's misses are its own test, not a
// puzzle. At most a few things a night, those with the most surprises.
// Without surprises it asks no model.
func (r *Reflector) Night(ctx context.Context) ([]Result, error) {
	now := r.Now()
	scored, err := r.St.Predictions(ctx, now.Add(-14*24*time.Hour), now)
	if err != nil {
		return nil, err
	}
	by := map[string][]model.Prediction{}
	var order []string
	for _, p := range scored {
		if !p.Surprise || p.Reflected {
			continue
		}
		key := ""
		switch {
		case strings.HasPrefix(p.Target, "workout:"):
			key = "workout"
		case strings.HasPrefix(p.Target, "day:") && p.Key != "" && len(p.Basis) > 0:
			if n, err := r.St.Note(ctx, p.Basis[0]); err == nil && predict.Trusted(n) {
				key = p.Key
			}
		}
		if key == "" {
			continue
		}
		if by[key] == nil {
			order = append(order, key)
		}
		by[key] = append(by[key], p)
	}
	sort.SliceStable(order, func(i, j int) bool { return len(by[order[i]]) > len(by[order[j]]) })
	if len(order) > maxTargets {
		order = order[:maxTargets]
	}
	var out []Result
	var errs []error
	for _, key := range order {
		res, err := r.reflectOn(ctx, key, by[key])
		for _, p := range by[key] {
			p.Reflected = true
			if err := r.St.PutPrediction(ctx, p); err != nil {
				return out, err
			}
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, res)
	}
	return out, errors.Join(errs...)
}

// maxTargets is how many things a night reflects on, at most: each is a
// model call.
const maxTargets = 2

// dayOf is the day a surprise was about.
func (r *Reflector) dayOf(p model.Prediction) string {
	if strings.HasPrefix(p.Target, "workout:") {
		return strings.TrimPrefix(p.Target, "workout:")
	}
	if p.From != nil {
		return p.From.In(r.Loc).Format("2006-01-02")
	}
	return p.Due.In(r.Loc).Format("2006-01-02")
}

// target is what a reflection is about: the thing, in words, and whether a
// day had it.
type target struct {
	key, label string
	hit        func(predict.Day) bool
}

func (r *Reflector) target(ctx context.Context, key string) target {
	if key == "workout" {
		return target{key, "운동", func(d predict.Day) bool { return d.Workout }}
	}
	seen := map[string]string{}
	thing := func(name string) string {
		if c, ok := seen[name]; ok {
			return c
		}
		c, _ := r.St.ThingClass(ctx, name)
		seen[name] = c
		return c
	}
	return target{key, predict.Label(key), func(d predict.Day) bool {
		for _, o := range d.Occ {
			if slices.Contains(predict.Keys(o, thing), key) {
				return true
			}
		}
		return false
	}}
}

func (r *Reflector) reflectOn(ctx context.Context, key string, surprises []model.Prediction) (Result, error) {
	res := Result{Target: key, Surprises: len(surprises)}
	tg := r.target(ctx, key)
	end := dayStart(r.Now(), r.Loc)
	days, err := r.Predict.Days(ctx, end.Add(-window), end)
	if err != nil {
		return res, err
	}
	// The comparison is between days of the same sort as the surprises:
	// weekdays with weekdays.
	surprised := map[string]bool{}
	sortOf := map[bool]int{}
	missed := 0 // expected and did not come, against came unexpected
	for _, p := range surprises {
		d := r.dayOf(p)
		surprised[d] = true
		if t, err := time.ParseInLocation("2006-01-02", d, r.Loc); err == nil {
			sortOf[weekday(t.Weekday())]++
		}
		if p.Prob >= 0.5 {
			missed++
		} else {
			missed--
		}
	}
	wd := sortOf[true] >= sortOf[false]
	var pool []predict.Day
	for _, d := range days {
		if weekday(d.Weekday) == wd {
			pool = append(pool, d)
		}
	}
	if r.LLM == nil || !r.LLM.Ready() {
		return res, nil
	}
	answer, err := r.LLM.Complete(llm.WithPurpose(ctx, llm.Reflect), System, r.prompt(tg, pool, surprised, missed >= 0))
	if err != nil {
		return res, err
	}
	hs, err := parse(answer)
	if err != nil {
		return res, err
	}
	res.Hypotheses = hs
	r.test(hs, pool, tg.hit)
	scope := "운동"
	if key != "workout" {
		scope = tg.label
	}
	for _, h := range hs {
		if !h.Kept {
			continue
		}
		note := model.Note{ID: self.ID("rate", key, h.Feature, h.Arg), Text: h.Text, Scope: scope,
			Confidence: math.Round(math.Min(0.9, 1-h.P)*100) / 100,
			Evidence:   []string{fmt.Sprintf("%s: %d일 중 %d일 %s, 다른 날 %d일 중 %d일 (p=%.3f)", h.Text, h.With[0], h.With[1], tg.label, h.Without[0], h.Without[1], h.P)},
			Rule:       &model.Rule{Kind: "rate", Target: key, Feature: h.Feature, Arg: h.Arg, Rate: h.Rate, Base: h.Base, Days: h.With[0]}}
		saved, _, err := r.Self.Hold(ctx, note, "밤 성찰: 예측이 틀린 날 "+fmt.Sprint(len(surprises))+"일")
		if err != nil {
			return res, err
		}
		res.Notes = append(res.Notes, saved)
	}
	return res, nil
}

func weekday(w time.Weekday) bool { return w != time.Saturday && w != time.Sunday }

func dayStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// prompt shows the surprising days and the others, each as a line of what
// happened - times, merchants, places, plans; never message text.
func (r *Reflector) prompt(tg target, pool []predict.Day, surprised map[string]bool, missed bool) string {
	var b strings.Builder
	what := tg.label + "(그날 있었는지)"
	if tg.key == "workout" {
		what = "운동(그날 운동 기록이 있는지)"
	}
	b.WriteString("목표: " + what + "\n\n## 특징 목록 (가설은 이 중 하나로)\n")
	keys := make([]string, 0, len(predict.Features))
	for k := range predict.Features {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %s\n", k, predict.Features[k])
	}
	line := func(d predict.Day) string {
		var parts []string
		for _, o := range d.Occ {
			t := o.At.In(r.Loc).Format("15:04")
			switch o.Kind {
			case "purchase":
				parts = append(parts, fmt.Sprintf("%s %s(%s)", t, o.Title, o.Class))
			case "workout":
				parts = append(parts, t+" 운동 "+o.Title)
			case "visit":
				parts = append(parts, t+" "+o.Title+" 도착")
			}
		}
		for _, p := range d.Plans {
			parts = append(parts, fmt.Sprintf("일정 %s %s(%s)", p.State.Start, p.State.Title, p.State.Type))
		}
		if w := d.Weather; w != nil {
			// What fell, in the words the rain feature is tested by.
			sky := weather.Sky(w.Code)
			if w.Precip >= 0.1 {
				sky = fmt.Sprintf("%s %.1fmm", map[bool]string{true: "눈", false: "비"}[w.Snowy()], w.Precip)
			}
			parts = append(parts, "날씨 "+sky)
		}
		w := []string{"일", "월", "화", "수", "목", "금", "토"}[d.Weekday]
		mark := tg.label + " 있음"
		if tg.key == "workout" {
			mark = "운동함"
		}
		if !tg.hit(d) {
			mark = tg.label + " 없음"
			if tg.key == "workout" {
				mark = "운동 안 함"
			}
		}
		return fmt.Sprintf("%s(%s) %s: %s", d.Date, w, mark, strings.Join(parts, ", "))
	}
	head := "\n## 예측이 틀린 날 (있을 것으로 봤지만 없음)\n"
	switch {
	case tg.key == "workout":
		head = "\n## 예측이 틀린 날 (운동할 것으로 봤지만 안 함)\n"
	case !missed:
		head = "\n## 예측이 틀린 날 (없을 것으로 봤지만 있었음)\n"
	}
	b.WriteString(head)
	for _, d := range pool {
		if surprised[d.Date] {
			b.WriteString(line(d) + "\n")
		}
	}
	b.WriteString("\n## 같은 종류의 다른 날\n")
	n := 0
	for i := len(pool) - 1; i >= 0 && n < 20; i-- {
		if !surprised[pool[i].Date] {
			b.WriteString(line(pool[i]) + "\n")
			n++
		}
	}
	return b.String()
}

func parse(answer string) ([]*Hypothesis, error) {
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("가설을 읽지 못했습니다: %q", answer)
	}
	var out struct {
		Hypotheses []*Hypothesis `json:"hypotheses"`
	}
	if err := json.Unmarshal([]byte(answer[start:end+1]), &out); err != nil {
		return nil, err
	}
	var keep []*Hypothesis
	for _, h := range out.Hypotheses {
		if _, ok := predict.Features[h.Feature]; ok && strings.TrimSpace(h.Text) != "" && len(keep) < 4 {
			keep = append(keep, h)
		}
	}
	return keep, nil
}

// test checks each hypothesis against the days, then the survivors against
// each other.
func (r *Reflector) test(hs []*Hypothesis, pool []predict.Day, hit func(predict.Day) bool) {
	for _, h := range hs {
		h.days = map[string]bool{}
		for _, d := range pool {
			held := predict.Holds(h.Feature, h.Arg, d, r.Loc)
			if held {
				h.days[d.Date] = true
				h.With[0]++
				if hit(d) {
					h.With[1]++
				}
			} else {
				h.Without[0]++
				if hit(d) {
					h.Without[1]++
				}
			}
		}
		if h.Without[0] > 0 {
			h.Base = float64(h.Without[1]) / float64(h.Without[0])
		}
		h.Rate = self.Shrunk(h.With[1], h.With[0], h.Base, prior)
		h.P = Fisher(h.With[1], h.With[0]-h.With[1], h.Without[1], h.Without[0]-h.Without[1])
		switch {
		case h.With[0] < minSupport:
			h.Why = fmt.Sprintf("해당하는 날이 %d일뿐(최소 %d일)", h.With[0], minSupport)
		case math.Abs(h.Rate-h.Base) < minEffect:
			h.Why = fmt.Sprintf("차이가 작음(%.0f%% vs %.0f%%)", h.Rate*100, h.Base*100)
		default:
			h.Kept = true
		}
	}
	// Benjamini-Hochberg over the night's tests.
	var tested []*Hypothesis
	for _, h := range hs {
		if h.Kept {
			tested = append(tested, h)
		}
	}
	sort.Slice(tested, func(i, j int) bool { return tested[i].P < tested[j].P })
	m := float64(len(hs))
	cut := -1
	for i, h := range tested {
		if h.P <= float64(i+1)/m*fdr {
			cut = i
		}
	}
	for i, h := range tested {
		if i > cut {
			h.Kept, h.Why = false, fmt.Sprintf("다중 비교 보정 뒤 유의하지 않음(p=%.3f)", h.P)
		}
	}
	// Confounding: of two features that hold on mostly the same days, the
	// one that tells nothing once the other is known goes.
	for _, a := range hs {
		for _, b := range hs {
			if a == b || !a.Kept || !b.Kept || jaccard(a.days, b.days) < overlap {
				continue
			}
			ea, eb := r.explains(a, b, pool, hit), r.explains(b, a, pool, hit)
			if ea && (!eb || rank(a) < rank(b)) {
				b.Kept, b.Why = false, fmt.Sprintf("교란: '%s'와 같은 날에 겹치고, 그것을 알면 더 설명하는 게 없음", a.Text)
			}
		}
	}
}

// explains is whether a accounts for b: among the days b held, the rate
// with a and without a differ, while among the days a held, b makes no
// difference.
func (r *Reflector) explains(a, b *Hypothesis, pool []predict.Day, hit func(predict.Day) bool) bool {
	rate := func(keep func(predict.Day) bool) (float64, int) {
		n, k := 0, 0
		for _, d := range pool {
			if keep(d) {
				n++
				if hit(d) {
					k++
				}
			}
		}
		if n == 0 {
			return math.NaN(), 0
		}
		return float64(k) / float64(n), n
	}
	withA := func(d predict.Day) bool { return a.days[d.Date] }
	withB := func(d predict.Day) bool { return b.days[d.Date] }
	// Within a's days and outside them, does b still matter?
	bInA, n1 := rate(func(d predict.Day) bool { return withA(d) && withB(d) })
	notBInA, n2 := rate(func(d predict.Day) bool { return withA(d) && !withB(d) })
	bOut, n3 := rate(func(d predict.Day) bool { return !withA(d) && withB(d) })
	notBOut, n4 := rate(func(d predict.Day) bool { return !withA(d) && !withB(d) })
	bMatters := false
	if n1 > 0 && n2 > 0 && math.Abs(bInA-notBInA) >= minEffect {
		bMatters = true
	}
	if n3 > 0 && n4 > 0 && math.Abs(bOut-notBOut) >= minEffect {
		bMatters = true
	}
	return !bMatters
}

// rank orders features by how directly a day's shape shows in them: when
// two coincide entirely, the plainer account is kept.
func rank(h *Hypothesis) int {
	return map[string]int{"late_leave": 0, "evening": 1, "plan_type": 2, "rain": 3, "weekday": 4, "spent_over": 5, "merchant": 6}[h.Feature]
}

func jaccard(a, b map[string]bool) float64 {
	inter, union := 0, 0
	for k := range a {
		union++
		if b[k] {
			inter++
		}
	}
	for k := range b {
		if !a[k] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// Fisher is the two-sided p of Fisher's exact test on the table
// [[a b] [c d]] (predict.Fisher).
func Fisher(a, b, c, d int) float64 { return predict.Fisher(a, b, c, d) }
