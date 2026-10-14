package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// JudgeSystem is the common-sense question. It is asked about a kind of
// thing and a kind of plan, never about anyone's particular purchase: the
// model knows that firewood is for camping, not that this firewood was for
// that trip - the rest is the event's evidence, judged by rule.
const JudgeSystem = `상품과 일정 종류가 주어진다. 그 상품이 그 종류의 일정을 위해 사는 물건일 가능성을 상식으로만 판단하라. 특정인의 사정은 모른다고 가정한다.
- class: 상품을 짧은 일반 분류로(브랜드·용량·수량을 빼고). "참나무 장작 10kg" → "장작", "삼다수 2L 12병" → "생수", "마이프로틴 웨이 1kg" → "프로틴". 분류가 주어지면 그대로 쓴다.
- rel: prep_for(그 일정을 위해 미리 사는 것) | used_during(그 일정 중에 쓰지만 평소에도 사는 것) | unrelated
- plausibility: 0~1. 그 상품을 사는 사람 가운데 그 일정 때문에 사는 비율에 가깝게.
- why: 한 줄.
JSON 하나만: {"class":"장작","rel":"prep_for","plausibility":0.9,"why":"장작은 캠핑 화로용 대표 준비물"}`

// Judge answers the common-sense question, once per (class, plan kind):
// asked of the model the first time, read from the cache after. Its cost
// grows with the combinations never seen before, not with the purchases.
type Judge struct {
	St  store.Store
	LLM llm.Client
}

// ErrNoModel is a question that needs the model when there is none.
var ErrNoModel = errors.New("상식 판정에 모델이 필요합니다")

// Of is the common sense about item and planKind.
func (j *Judge) Of(ctx context.Context, item, planKind string) (model.Judgement, error) {
	class, err := j.St.ThingClass(ctx, item)
	if err == nil {
		if got, err := j.St.LinkCache(ctx, class, planKind); err == nil {
			return got, nil
		}
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Judgement{}, err
	}
	if j.LLM == nil || !j.LLM.Ready() {
		return model.Judgement{}, ErrNoModel
	}
	user := fmt.Sprintf("상품: %q / 일정 종류: %s", item, planKind)
	if class != "" {
		user = fmt.Sprintf("상품: %q (분류: %s) / 일정 종류: %s", item, class, planKind)
	}
	answer, err := j.LLM.Complete(llm.WithPurpose(ctx, llm.Judge), JudgeSystem, user)
	if err != nil {
		return model.Judgement{}, err
	}
	got, err := parseJudgement(answer)
	if err != nil {
		return model.Judgement{}, err
	}
	if class != "" {
		got.Class = class
	}
	if got.Class == "" {
		got.Class = item
	}
	if err := j.St.PutThingClass(ctx, item, got.Class); err != nil {
		return got, err
	}
	return got, j.St.PutLinkCache(ctx, planKind, got)
}

func parseJudgement(answer string) (model.Judgement, error) {
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return model.Judgement{}, fmt.Errorf("판정을 읽지 못했습니다: %q", answer)
	}
	var j model.Judgement
	if err := json.Unmarshal([]byte(answer[start:end+1]), &j); err != nil {
		return j, err
	}
	j.Class = strings.TrimSpace(j.Class)
	if !slices.Contains([]string{model.PrepFor, model.UsedDuring, "unrelated"}, j.Rel) {
		j.Rel = "unrelated"
	}
	j.Plausibility = math.Min(math.Max(j.Plausibility, 0), 1)
	return j, nil
}

// Thresholds of the event's judgement.
const (
	// window: a purchase is for a plan only in the two weeks before it.
	window = 14 * 24 * time.Hour
	// common: common sense this sure links on its own (estimated).
	common = 0.5
	// weak: below common, repetition must carry it - this many plans of the
	// kind with the thing bought before them. Below weak, never: a thing
	// bought before every trip by chance is not for the trips.
	weak, repeats = 0.1, 3
	// first is the corroboration of a first link: no repetition, no
	// conversation, no sign it was used - an estimate (0.9 × 1 × 1 × ⅔ ≈ 0.6).
	first = 2.0 / 3
)

// Bucket is how long before a plan a thing was bought, coarsely.
func Bucket(gap time.Duration) string {
	switch d := gap.Hours() / 24; {
	case d <= 1:
		return "d1"
	case d <= 3:
		return "d3"
	case d <= 7:
		return "d7"
	}
	return "d14"
}

// prep judges whether item (a purchase.item occurrence) was bought for plan
// p and links it if so:
//
//	confidence = common sense × time fit × exclusivity × corroboration
//
// Time fit: bought days before, so it is there in time. Exclusivity: shared
// with the other plans it could be for. Corroboration: a first time is an
// estimate; repetition (pattern counts) makes it surer, and makes a thing
// common sense is unsure of (water) linkable at all.
func (l *Linker) prep(ctx context.Context, item model.Occurrence, p model.Entity, rivals int) error {
	if l.Judge == nil || p.State.Type == "" || p.State.Type == "기타" {
		return nil
	}
	sp, ok := l.World.Span(ctx, p, l.Now(), l.Loc)
	if !ok {
		return nil
	}
	gap := sp.Start.Sub(item.At)
	if gap <= 0 || gap > window {
		return nil
	}
	j, err := l.Judge.Of(ctx, item.Title, p.State.Type)
	if errors.Is(err, ErrNoModel) {
		return nil
	}
	if err != nil {
		return err
	}
	if j.Rel == "unrelated" || j.Plausibility < weak {
		return nil
	}
	support, total := l.repeated(ctx, p.State.Type, j.Class)
	if j.Plausibility < common && support < repeats {
		return nil
	}
	fit := 1.0
	if gap < 24*time.Hour {
		fit = 0.7 // may not arrive in time
	}
	excl := 1 / float64(max(rivals, 1))
	corroboration := first
	why := fmt.Sprintf("상식 %.2f(%s) · 일정 %s 전", j.Plausibility, j.Why, days(gap))
	if support >= 2 {
		corroboration = math.Min(1, first+float64(support)/float64(max(total, support))/3)
		why += fmt.Sprintf(" · 반복 %d/%d", support, total)
	}
	common := j.Plausibility
	if common < 0.5 {
		common = 0.5 // repetition carries what common sense doubts
	}
	conf := math.Round(common*fit*excl*corroboration*100) / 100
	rel := j.Rel
	return l.put(ctx, model.Link{From: item.ID, To: p.ID, Rel: rel, Confidence: conf, State: model.Estimated,
		Evidence: []string{item.Ref}, Why: why, By: "judge"})
}

// repeated is how often plans of this kind had a thing of this class
// bought before them (any bucket), out of how many such plans.
func (l *Linker) repeated(ctx context.Context, kind, class string) (support, total int) {
	ps, err := l.St.Patterns(ctx, kind)
	if err != nil {
		return 0, 0
	}
	for _, p := range ps {
		switch {
		case p.KindB == "":
			total = p.Total
		case p.KindB == class && p.Rel == model.PrepFor:
			support += p.Support
		}
	}
	return support, total
}

func days(d time.Duration) string {
	if n := int(math.Round(d.Hours() / 24)); n >= 1 {
		return fmt.Sprintf("%d일", n)
	}
	return fmt.Sprintf("%d시간", int(d.Hours()))
}
