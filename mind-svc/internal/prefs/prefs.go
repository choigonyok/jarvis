// Package prefs turns reviews into what places are like and what the
// operator is like - in two layers, since one answer holds both: "this
// campsite was noisy" is the campsite's, "I mind noise" is the operator's.
//
// Answers are kept as they were given (observations). A subject's
// attributes are their average. A preference is never drawn from one
// answer: the same dimension, the same way, at two or more different
// places makes it an estimate; a high score at a place known for the
// opposite counts against it; it is asked about once, and an estimate
// turned down is not asked again. What the operator says outright ("원래
// 시끄러운 거 싫어") is confirmed at once. The model only maps free text
// onto dimensions; every rule is here.
package prefs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Dimensions: shared by campsites, lodgings, cafés and restaurants so that
// evidence gathers across them, plus the campsite's own. A good dimension
// can be known of a place before going, is not another's overlap, and is
// answered in a tap.
var (
	Common = []string{"소음", "청결", "가격 대비", "접근성", "공간", "응대"}
	Camp   = []string{"사이트 간격", "화장실·샤워장", "뷰·자연"}
	Other  = "기타"
)

// options are the card's choices, as dimension and way.
var options = map[string]struct {
	dim string
	pol int
}{
	"소음·매너": {"소음", -1}, "화장실·샤워장": {"화장실·샤워장", -1}, "사이트 간격": {"사이트 간격", -1}, "청결": {"청결", -1},
	"가격 대비": {"가격 대비", -1}, "접근성": {"접근성", -1}, "예약·응대": {"응대", -1}, "붐빔": {"공간", -1},
	"뷰": {"뷰·자연", +1}, "조용함": {"소음", +1}, "깨끗함": {"청결", +1}, "사이트 넓음": {"사이트 간격", +1},
	"가까움": {"접근성", +1}, "가성비": {"가격 대비", +1}, "친절함": {"응대", +1}, "여유로움": {"공간", +1},
}

// Places are the subjects whose answers teach preferences: what a place is
// like, and so what the operator is like. A purchase, a workout, a decision
// or a week is reviewed too, but its answers stay its own.
var Places = map[string]bool{"캠핑장": true, "숙소": true, "식당": true, "카페": true}

// Bad and Good are the card's choices for a kind of subject, before the
// dimensions the answers grew (Options).
func Bad(subjectType string) []string {
	if subjectType == "캠핑장" {
		return []string{"소음·매너", "화장실·샤워장", "사이트 간격", "청결", "가격 대비", "접근성", "예약·응대"}
	}
	return []string{"소음·매너", "청결", "가격 대비", "접근성", "붐빔", "예약·응대"}
}

func Good(subjectType string) []string {
	if subjectType == "캠핑장" {
		return []string{"뷰", "조용함", "깨끗함", "사이트 넓음", "가까움", "가성비"}
	}
	return []string{"조용함", "깨끗함", "가까움", "가성비", "친절함", "여유로움"}
}

// System maps a free sentence onto the dimensions.
const System = `회고에 적은 자유 서술을 차원으로 바꾼다. 차원 목록 밖의 것은 "기타"로 두고 detail 에 짧게 적는다.
polarity 는 좋았으면 1, 아쉬웠으면 -1. 서술에 없는 차원은 넣지 않는다.
JSON 하나만: {"items":[{"dimension":"소음","polarity":-1},{"dimension":"기타","polarity":-1,"detail":"벌레가 많음"}]}`

type Prefs struct {
	St  store.Store
	LLM llm.Client
	Now func() time.Time
}

// Answers is a review card's form as the operator left it.
type Answers map[string]string

// Review is what a review intent carries.
type Review struct {
	SubjectType string            `json:"subjectType"`
	Subject     string            `json:"subject"`
	Context     map[string]string `json:"context,omitempty"`
	PlanID      string            `json:"planId,omitempty"`
	Answers     Answers           `json:"answers,omitempty"`
}

// Record keeps a review's answers as observations, and returns the
// preferences that moved because of them.
func (p *Prefs) Record(ctx context.Context, source string, r Review) ([]model.Preference, error) {
	score, _ := strconv.Atoi(strings.TrimSpace(r.Answers["score"]))
	at := p.Now()
	base := model.Observation{SubjectType: r.SubjectType, Subject: r.Subject, Score: score, Context: r.Context, Source: source, At: at}
	if !Places[r.SubjectType] {
		return nil, p.St.AddObservations(ctx, Others(base, r.Answers))
	}
	obs := []model.Observation{base} // the score alone
	seen := map[string]bool{}
	grown, err := p.promoted(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"bad", "good"} {
		for _, choice := range split(r.Answers[key]) {
			o, ok := options[choice]
			for _, g := range grown {
				if Key(g.Label) == Key(choice) { // "벌레 많음" is "벌레가 많음"
					o, ok = struct {
						dim string
						pol int
					}{g.Label, g.Polarity}, true
				}
			}
			if !ok || seen[o.dim] {
				continue
			}
			seen[o.dim] = true
			x := base
			x.Dimension, x.Polarity = o.dim, o.pol
			obs = append(obs, x)
		}
	}
	if note := strings.TrimSpace(r.Answers["note"]); note != "" {
		items := p.mapText(ctx, note)
		if len(items) == 0 {
			x := base
			x.Dimension, x.Detail = Other, note
			obs = append(obs, x)
		}
		for _, it := range items {
			if seen[it.Dimension] && it.Dimension != Other {
				continue
			}
			seen[it.Dimension] = true
			x := base
			x.Dimension, x.Polarity, x.Detail = it.Dimension, it.Polarity, it.Detail
			obs = append(obs, x)
		}
	}
	if err := p.St.AddObservations(ctx, obs); err != nil {
		return nil, err
	}
	// A score moves the preferences on what the subject is known for too:
	// rated high at a place known to be noisy says something about noise.
	if attrs, err := p.St.Attributes(ctx, r.SubjectType, r.Subject); err == nil {
		for _, a := range attrs {
			seen[a.Dimension] = true
		}
	}
	// "기타" said the same way at three or more places is a dimension of its
	// own from now on; the observations stay as they were, and are read
	// under it.
	if grown, err = p.promoted(ctx); err != nil {
		return nil, err
	}
	for _, o := range obs {
		if o.Dimension == Other && o.Detail != "" {
			for _, g := range grown {
				if Key(g.Label) == Key(o.Detail) {
					seen[g.Label] = true
				}
			}
		}
	}
	var moved []model.Preference
	for d := range seen {
		if d == Other {
			continue
		}
		ps, err := p.Recompute(ctx, d)
		if err != nil {
			return moved, err
		}
		moved = append(moved, ps...)
	}
	return moved, nil
}

type item struct {
	Dimension string `json:"dimension"`
	Polarity  int    `json:"polarity"`
	Detail    string `json:"detail"`
}

func (p *Prefs) mapText(ctx context.Context, text string) []item {
	if p.LLM == nil || !p.LLM.Ready() {
		return nil
	}
	all := append(append([]string{}, Common...), Camp...)
	answer, err := p.LLM.Complete(llm.WithPurpose(ctx, "review"), System, fmt.Sprintf("차원 목록: %s, 기타\n서술: %s", strings.Join(all, ", "), text))
	if err != nil {
		return nil
	}
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return nil
	}
	var out struct {
		Items []item `json:"items"`
	}
	if json.Unmarshal([]byte(answer[start:end+1]), &out) != nil {
		return nil
	}
	var keep []item
	for _, it := range out.Items {
		if it.Dimension != Other && !slices.Contains(all, it.Dimension) {
			it.Detail, it.Dimension = strings.TrimSpace(it.Dimension+" "+it.Detail), Other
		}
		if it.Polarity > 0 {
			it.Polarity = 1
		} else if it.Polarity < 0 {
			it.Polarity = -1
		}
		keep = append(keep, it)
	}
	return keep
}

// Thresholds of the rules.
const (
	// places: different subjects needed for an estimate.
	places = 2
	// prior pseudo-places toward "nothing to say" when estimating confidence.
	prior = 1.0
)

// Recompute redraws the preferences on one dimension from its observations:
// each way it was felt, at how many different places (and in what company,
// which becomes the scope when all of them share it), against the places
// that went the other way.
func (p *Prefs) Recompute(ctx context.Context, dimension string) ([]model.Preference, error) {
	obs, err := p.St.ObservationsOn(ctx, dimension)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(Common, dimension) && !slices.Contains(Camp, dimension) {
		others, err := p.St.ObservationsOn(ctx, Other)
		if err != nil {
			return nil, err
		}
		for _, o := range others {
			if Key(o.Detail) == Key(dimension) {
				obs = append(obs, o)
			}
		}
	}
	existing, err := p.St.Preferences(ctx)
	if err != nil {
		return nil, err
	}
	scores, err := p.St.ObservationsOn(ctx, "")
	if err != nil {
		return nil, err
	}
	var moved []model.Preference
	for _, pol := range []int{-1, +1} {
		subjects := map[string]bool{}
		withs := map[string]int{}
		for _, o := range obs {
			if o.Polarity != pol {
				continue
			}
			key := o.SubjectType + ":" + o.Subject
			if !subjects[key] {
				subjects[key] = true
				withs[o.Context["with"]]++
			}
		}
		// Against it: a place felt the other way on this dimension, or one
		// known for this dimension's bad side (its attributes) that was rated
		// high anyway - or known for its good side and rated low.
		counter := map[string]bool{}
		for _, o := range obs {
			if o.Polarity == -pol && !subjects[o.SubjectType+":"+o.Subject] {
				counter[o.SubjectType+":"+o.Subject] = true
			}
		}
		for _, sc := range scores {
			key := sc.SubjectType + ":" + sc.Subject
			if counter[key] || sc.Score == 0 {
				continue
			}
			known := 0.0
			attrs, err := p.St.Attributes(ctx, sc.SubjectType, sc.Subject)
			if err != nil {
				return moved, err
			}
			for _, a := range attrs {
				if a.Dimension == dimension {
					known = a.Value
				}
			}
			if pol < 0 && known < 0 && sc.Score >= 4 || pol > 0 && known > 0 && sc.Score <= 2 {
				counter[key] = true
			}
		}
		n, c := len(subjects), len(counter)
		if n < places || n <= c {
			continue
		}
		scope := ""
		if len(withs) == 1 {
			for w := range withs {
				if w != "" {
					scope = With(w) + " 갈 때"
				}
			}
		}
		pref := model.Preference{Dimension: dimension, Polarity: pol, Scope: scope, Status: "estimated", Subjects: names(subjects), Counter: names(counter),
			Confidence: math.Round(float64(n)/(float64(n+c)+prior)*100) / 100, UpdatedAt: p.Now()}
		pref.Why = Why(pref)
		if old, ok := find(existing, dimension, pol, scope); ok {
			if old.Status != "estimated" {
				continue // the operator's word stands
			}
			if slices.Equal(old.Subjects, pref.Subjects) && slices.Equal(old.Counter, pref.Counter) {
				continue
			}
		}
		if err := p.St.PutPreference(ctx, pref); err != nil {
			return moved, err
		}
		moved = append(moved, pref)
	}
	return moved, nil
}

// With is company with its particle: 가족과, 엄마·아빠와.
func With(names string) string {
	r := []rune(strings.ReplaceAll(names, ",", "·"))
	if len(r) == 0 {
		return ""
	}
	last := r[len(r)-1]
	if last >= 0xAC00 && last <= 0xD7A3 && (last-0xAC00)%28 != 0 {
		return string(r) + "과"
	}
	return string(r) + "와"
}

// Key is a free description brought to one form, to tell when two say the
// same: spaces and trailing endings dropped ("벌레가 많음" = "벌레 많음").
func Key(detail string) string {
	var words []string
	for _, w := range strings.Fields(detail) {
		r := []rune(w)
		if len(r) > 1 && strings.ContainsRune("가이은는을를", r[len(r)-1]) {
			w = string(r[:len(r)-1])
		}
		words = append(words, w)
	}
	k := strings.Join(words, "")
	for _, suf := range []string{"했음", "함", "음", "다", "요"} {
		if r := []rune(k); len(r) > len([]rune(suf))+1 {
			k = strings.TrimSuffix(k, suf)
		}
	}
	return k
}

// promote: places that must say the same "기타" before it becomes a
// dimension.
const promote = 3

// Promoted is whether a "기타" description has been said of enough
// different places to be a dimension.
func (p *Prefs) Promoted(ctx context.Context, detail string) (bool, error) {
	others, err := p.St.ObservationsOn(ctx, Other)
	if err != nil {
		return false, err
	}
	places := map[string]bool{}
	for _, o := range others {
		if Key(o.Detail) == Key(detail) {
			places[o.SubjectType+":"+o.Subject] = true
		}
	}
	return len(places) >= promote, nil
}

// Grown is a dimension that grew out of "기타": what was written (the
// words said most often), and the way it was said.
type Grown struct {
	Label    string
	Polarity int
	Places   int
}

// promoted is every "기타" said the same way at enough different places.
func (p *Prefs) promoted(ctx context.Context) ([]Grown, error) {
	others, err := p.St.ObservationsOn(ctx, Other)
	if err != nil {
		return nil, err
	}
	type acc struct {
		places map[string]bool
		pol    int
		words  map[string]int
	}
	by := map[string]*acc{}
	var keys []string
	for _, o := range others {
		k := Key(o.Detail)
		if k == "" {
			continue
		}
		a := by[k]
		if a == nil {
			a = &acc{places: map[string]bool{}, words: map[string]int{}}
			by[k] = a
			keys = append(keys, k)
		}
		a.places[o.SubjectType+":"+o.Subject] = true
		a.pol += o.Polarity
		a.words[strings.TrimSpace(o.Detail)]++
	}
	var out []Grown
	for _, k := range keys {
		a := by[k]
		if len(a.places) < promote {
			continue
		}
		label, best := "", 0
		for w, n := range a.words {
			if n > best || n == best && w < label {
				label, best = w, n
			}
		}
		pol := -1
		if a.pol > 0 {
			pol = 1
		}
		out = append(out, Grown{Label: label, Polarity: pol, Places: len(a.places)})
	}
	return out, nil
}

// Retirement: a dimension stops being offered on the card when it went
// unchosen for months while reviews kept coming, or when choosing it says
// nothing about the score. Its observations stay, so it can come back.
const (
	unusedFor      = 120 * 24 * time.Hour
	reviewsToJudge = 8
	unrelated      = 0.3
)

// Options are the card's choices for a kind of subject: the starting set
// less what was retired, and what grew out of "기타". Retired lists what
// was taken off, with why.
func (p *Prefs) Options(ctx context.Context, subjectType string) (bad, good []string, retired map[string]string, err error) {
	scores, err := p.St.ObservationsOn(ctx, "")
	if err != nil {
		return nil, nil, nil, err
	}
	retired = map[string]string{}
	now := p.Now()
	recent, mean, n := 0, 0.0, 0
	first := now
	byVisit := map[string]int{}
	for _, sc := range scores {
		if !Places[sc.SubjectType] || sc.Score == 0 {
			continue
		}
		if now.Sub(sc.At) < unusedFor {
			recent++
		}
		if sc.At.Before(first) {
			first = sc.At
		}
		mean += float64(sc.Score)
		n++
		byVisit[sc.Source] = sc.Score
	}
	if n > 0 {
		mean /= float64(n)
	}
	judge := func(dim string) error {
		if _, done := retired[dim]; done {
			return nil
		}
		obs, err := p.St.ObservationsOn(ctx, dim)
		if err != nil {
			return err
		}
		last := time.Time{}
		sum := map[int]float64{}
		cnt := map[int]int{}
		for _, o := range obs {
			if !Places[o.SubjectType] {
				continue
			}
			if o.At.After(last) {
				last = o.At
			}
			if s, ok := byVisit[o.Source]; ok {
				sum[o.Polarity] += float64(s)
				cnt[o.Polarity]++
			}
		}
		if recent >= reviewsToJudge && now.Sub(first) >= unusedFor && now.Sub(last) >= unusedFor {
			retired[dim] = fmt.Sprintf("최근 넉 달 회고 %d번에 한 번도 고르지 않음", recent)
			return nil
		}
		if cnt[-1]+cnt[1] >= reviewsToJudge {
			says := false
			for _, pol := range []int{-1, 1} {
				if cnt[pol] >= 3 && math.Abs(sum[pol]/float64(cnt[pol])-mean) >= unrelated {
					says = true
				}
			}
			if !says {
				retired[dim] = fmt.Sprintf("%d번 골랐지만 점수와 상관이 없음", cnt[-1]+cnt[1])
			}
		}
		return nil
	}
	keep := func(choices []string) ([]string, error) {
		var out []string
		for _, c := range choices {
			if err := judge(options[c].dim); err != nil {
				return nil, err
			}
			if _, gone := retired[options[c].dim]; !gone {
				out = append(out, c)
			}
		}
		return out, nil
	}
	if bad, err = keep(Bad(subjectType)); err != nil {
		return nil, nil, nil, err
	}
	if good, err = keep(Good(subjectType)); err != nil {
		return nil, nil, nil, err
	}
	grown, err := p.promoted(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, g := range grown {
		if err := judge(g.Label); err != nil {
			return nil, nil, nil, err
		}
		if _, gone := retired[g.Label]; gone {
			continue
		}
		if g.Polarity < 0 {
			bad = append(bad, g.Label)
		} else {
			good = append(good, g.Label)
		}
	}
	return bad, good, retired, nil
}

// Others are the answers to a review of something that is not a place - a
// purchase, a workout, a decision, a week - as observations of that
// subject: the score, and each answer under its own name. They teach no
// preference about places.
func Others(base model.Observation, a Answers) []model.Observation {
	base.Detail = strings.TrimSpace(a["note"])
	out := []model.Observation{base}
	ways := map[string]map[string]int{
		"again":     {"또 살래요": 1, "다른 걸로": 0, "안 살래요": -1},
		"condition": {"좋았음": 1, "보통": 0, "피곤했음": -1},
		"reason":    {"이유가 맞았음": 1, "반쯤": 0, "빗나감": -1},
	}
	names := map[string]string{"again": "재구매", "condition": "컨디션", "reason": "판단 이유"}
	asks := map[string]string{"물건": "again", "운동": "condition", "결정": "reason"}
	for _, key := range []string{"again", "condition", "reason"} {
		if asks[base.SubjectType] != key {
			continue
		}
		if pol, ok := ways[key][strings.TrimSpace(a[key])]; ok {
			x := base
			x.Dimension, x.Polarity, x.Detail = names[key], pol, a[key]
			out = append(out, x)
		}
	}
	for _, t := range []struct {
		key, dim string
		pol      int
	}{{"good", "좋았던 일", 1}, {"bad", "아쉬웠던 일", -1}, {"next", "다음 주", 0}} {
		if v := strings.TrimSpace(a[t.key]); v != "" && base.SubjectType == "주" {
			x := base
			x.Dimension, x.Polarity, x.Detail = t.dim, t.pol, v
			out = append(out, x)
		}
	}
	return out
}

// Decide is the operator's answer to a confirmation card.
func (p *Prefs) Decide(ctx context.Context, dimension string, polarity int, scope, answer string) error {
	ps, err := p.St.Preferences(ctx)
	if err != nil {
		return err
	}
	pref, ok := find(ps, dimension, polarity, scope)
	if !ok {
		return fmt.Errorf("그런 추정이 없습니다")
	}
	pref.UpdatedAt = p.Now()
	switch {
	case strings.HasPrefix(answer, "네"):
		pref.Status, pref.Confidence = "confirmed", 1
	case strings.Contains(answer, "때만"):
		// Only in that company: the estimate becomes a scoped confirmation.
		pref.Status, pref.Confidence = "rejected", 0
		if err := p.St.PutPreference(ctx, pref); err != nil {
			return err
		}
		scoped := pref
		scoped.Scope, scoped.Status, scoped.Confidence = strings.TrimSpace(strings.TrimSuffix(answer, "만")), "confirmed", 1
		scoped.Why = Why(scoped)
		return p.St.PutPreference(ctx, scoped)
	default:
		pref.Status, pref.Confidence = "rejected", 0
	}
	return p.St.PutPreference(ctx, pref)
}

// Say records a preference the operator stated outright: confirmed, however
// little evidence there is.
func (p *Prefs) Say(ctx context.Context, dimension string, polarity int, quote string) error {
	pref := model.Preference{Dimension: dimension, Polarity: polarity, Status: "confirmed", Confidence: 1,
		Why: "직접 말함: “" + quote + "”", UpdatedAt: p.Now()}
	return p.St.PutPreference(ctx, pref)
}

// Stated maps a sentence the operator said about themselves onto a
// dimension and way, by its words: "시끄러운 거 싫어" is noise, minded.
func Stated(relation, text string) (string, int, bool) {
	pol := 0
	switch strings.ToUpper(relation) {
	case "DISLIKES", "HATES", "AVOIDS":
		pol = -1
	case "LIKES", "PREFERS", "LOVES":
		pol = +1
	default:
		return "", 0, false
	}
	words := map[string][]string{
		"소음": {"시끄러", "소음", "조용"}, "청결": {"더러", "청결", "깨끗"}, "공간": {"붐비", "사람 많", "북적"},
		"사이트 간격": {"사이트 간격", "다닥다닥"}, "화장실·샤워장": {"화장실", "샤워장"}, "뷰·자연": {"뷰", "경치", "자연"},
		"가격 대비": {"가성비", "비싼"}, "접근성": {"멀리", "가까운", "접근"}, "응대": {"불친절", "친절"},
	}
	for dim, ws := range words {
		for _, w := range ws {
			if strings.Contains(text, w) {
				// "조용한 곳이 좋아" (LIKES quiet) is minding noise as much as
				// "시끄러운 거 싫어": seeking the good side of a dimension whose
				// word names the bad side is the same preference.
				if (w == "조용" || w == "깨끗" || w == "가까운" || w == "친절" || w == "가성비") && pol > 0 {
					return dim, -1, true
				}
				return dim, pol, true
			}
		}
	}
	return "", 0, false
}

// Why is the grounds of a preference, as a card or an answer shows it:
// "캠핑장 3곳 중 3곳에서 소음이 아쉬웠음".
func Why(p model.Preference) string {
	way := "아쉬웠음"
	if p.Polarity > 0 {
		way = "좋았음"
	}
	s := fmt.Sprintf("%d곳에서 %s이(가) %s (%s)", len(p.Subjects), p.Dimension, way, strings.Join(short(p.Subjects), ", "))
	if len(p.Counter) > 0 {
		s += fmt.Sprintf(", 반대로 %d곳", len(p.Counter))
	}
	if p.Scope != "" {
		s += " · " + p.Scope
	}
	return s
}

// Text is a preference in a sentence: "소음에 민감하다".
func Text(p model.Preference) string {
	t := p.Dimension + "이(가) 좋은 곳을 찾는다"
	if p.Polarity < 0 {
		t = p.Dimension + "에 민감하다"
	}
	if p.Scope != "" {
		t = p.Scope + " " + t
	}
	return t
}

func find(ps []model.Preference, dim string, pol int, scope string) (model.Preference, bool) {
	for _, p := range ps {
		if p.Dimension == dim && p.Polarity == pol && p.Scope == scope {
			return p, true
		}
	}
	return model.Preference{}, false
}

func names(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func short(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		_, name, _ := strings.Cut(k, ":")
		out[i] = name
	}
	return out
}

func split(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
