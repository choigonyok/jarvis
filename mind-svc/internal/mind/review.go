package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
	"github.com/choigonyok/jarvis/mind-svc/internal/prefs"
)

// Questions before suggestions: after a night at a campsite or a meal at a
// new restaurant, the evening asks how it was - a form card, a tap or two.
// The answers are data (internal/prefs): what the place is like, and, once
// several places agree, what the operator is like, which is asked about
// once more before it is believed.
//
// The same card asks of other things, whose answers stay their own: on
// Sunday, the week; a big decision three months on, against its reasons
// then; a purchase two weeks on, whether it would be bought again; a
// workout, how hard it felt (at most every third day). Places come first,
// and two questions a day at most, whatever they are about.

const (
	reviewAt = 21 * 60 // 21:00, with the evening summary
	// questionsADay is how many review questions a day may ask.
	questionsADay = 2
	// reviewExpires: an unanswered question is withdrawn after this long.
	reviewExpires = 3 * 24 * time.Hour
)

// candidate is something worth a review question.
type candidate struct {
	subjectType, subject string
	planID               string
	with                 []string
	at                   time.Time
	body                 string           // what the card says it is about
	evidence             []model.Evidence // the words it rests on (a decision's)
}

// workoutEvery: a workout is asked about at most this often.
const workoutEvery = 3 * 24 * time.Hour

// reviews asks tonight's questions: campsites and trips that ended today,
// stays of a night or more at a campsite region, and restaurants paid at for
// the first time today.
func (m *Mind) reviews(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	var cands []candidate
	if now.Weekday() == time.Sunday {
		c, err := m.weekCandidate(ctx, today)
		if err != nil {
			return err
		}
		cands = append(cands, c)
	}

	plans, err := st.PlansOn(ctx, today.AddDate(0, 0, -2).Format("2006-01-02"), today.Format("2006-01-02"))
	if err != nil {
		return err
	}
	for _, p := range plans {
		ps := p.State
		if (ps.Status != model.Confirmed && ps.Status != model.Done) || ps.Place == "" || (ps.Type != "캠핑" && ps.Type != "여행") {
			continue
		}
		sp, ok := m.World.Span(ctx, p, now, loc)
		if !ok || sp.End.After(now) || now.Sub(sp.End) > 30*time.Hour {
			continue
		}
		kind := "캠핑장"
		if ps.Type == "여행" {
			kind = "숙소"
		}
		cands = append(cands, candidate{subjectType: kind, subject: ps.Place, planID: p.ID, with: ps.People, at: sp.Start})
	}
	stays, err := st.Occurrences(ctx, today.AddDate(0, 0, -3), now, "visit")
	if err != nil {
		return err
	}
	for _, v := range stays {
		if v.Class == "camp" && v.Until != nil && v.Until.Sub(v.At) >= 12*time.Hour && !v.Until.Before(today) {
			cands = append(cands, candidate{subjectType: "캠핑장", subject: v.Title, at: v.At})
		}
	}
	meals, err := st.Occurrences(ctx, today, now, "purchase")
	if err != nil {
		return err
	}
	for _, o := range meals {
		if o.Class != "식비" || o.Amount < 15000 || delivery(o.Title) {
			continue
		}
		before, err := st.Occurrences(ctx, today.AddDate(0, 0, -180), today, "purchase")
		if err != nil {
			return err
		}
		if slices.ContainsFunc(before, func(b model.Occurrence) bool { return b.Title == o.Title }) {
			continue // not new
		}
		cands = append(cands, candidate{subjectType: "식당", subject: o.Title, at: o.At})
	}

	// A big decision three months on: was it right, were the reasons?
	decisions, err := st.FactsOn(ctx, Decided, today.AddDate(0, -3, -3), today.AddDate(0, -3, 1))
	if err != nil {
		return err
	}
	for _, f := range decisions {
		if !f.Subject.Self || f.Status == "failed" {
			continue
		}
		body := fmt.Sprintf("%s에 정하신 일이에요: %s", md(f.CreatedAt.In(loc)), f.Text)
		if len(f.Evidence) > 0 && !strings.Contains(f.Text, f.Evidence[0].Quote) {
			body += " (“" + f.Evidence[0].Quote + "”)"
		}
		cands = append(cands, candidate{subjectType: "결정", subject: firstNonEmpty(f.Object.Name, f.Text), at: f.CreatedAt,
			body: body, evidence: f.Evidence})
	}

	// A purchase two weeks on: would it be bought again?
	items, err := st.Occurrences(ctx, today.AddDate(0, 0, -16), today.AddDate(0, 0, -13), "purchase.item")
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Amount < 30000 || strings.Contains(it.Class, "식") {
			continue
		}
		cands = append(cands, candidate{subjectType: "물건", subject: it.Title, at: it.At,
			body: fmt.Sprintf("%s에 %s에 사신 것", md(it.At.In(loc)), money(it.Amount))})
	}

	// A workout today: how hard did it feel? Not every day.
	works, err := st.Occurrences(ctx, today, now, "workout")
	if err != nil {
		return err
	}
	if last, _ := st.Cursor(ctx, "review:last:운동"); len(works) > 0 && now.Sub(time.UnixMilli(last)) >= workoutEvery {
		w := works[len(works)-1]
		cands = append(cands, candidate{subjectType: "운동", subject: w.Title, at: w.At, body: strings.Join(w.Things, ", ")})
	}

	asked := 0
	seen := map[string]bool{}
	for _, c := range cands {
		key := c.subjectType + ":" + placeKey(c.subject)
		if seen[key] || asked >= questionsADay {
			continue
		}
		seen[key] = true
		if never, _ := st.Cursor(ctx, "review:never:"+neverKey(c.subjectType, c.subject)); never > 0 {
			continue
		}
		// One trip, one question: the calendar's place, the region stayed
		// at and the trip's town are the same nights, not three
		// (2026-10-24~26: 양평, 메리지아레캠핑장, 메리지아레 캠핑장 (양평)).
		if stay(c.subjectType) && m.tripAsked(ctx, c.at.In(loc)) {
			continue
		}
		intent, err := m.reviewIntent(ctx, c, now)
		if err != nil {
			return err
		}
		in, err := m.Gate.Submit(ctx, intent)
		if err != nil {
			return err
		}
		if in.Card {
			asked++
			if c.subjectType == "운동" {
				_ = st.SetCursor(ctx, "review:last:운동", now.UnixMilli())
			}
			if stay(c.subjectType) {
				_ = st.SetCursor(ctx, "review:trip:"+c.at.In(loc).Format("2006-01-02"), 1)
			}
		}
	}
	return nil
}

// stay is a subject type that is somewhere slept: a campsite, a lodging.
func stay(subjectType string) bool { return subjectType == "캠핑장" || subjectType == "숙소" }

// tripAsked is whether a stay starting the day before, on, or after day was
// already asked about.
func (m *Mind) tripAsked(ctx context.Context, day time.Time) bool {
	for d := -1; d <= 1; d++ {
		if n, _ := m.Opts.Store.Cursor(ctx, "review:trip:"+day.AddDate(0, 0, d).Format("2006-01-02")); n > 0 {
			return true
		}
	}
	return false
}

// placeKey is a place's name as the ledger keys it: the calendar's
// "메리지아레 캠핑장 (양평)" and the region's "메리지아레캠핑장" are one place.
func placeKey(subject string) string {
	s := strings.TrimSpace(subject)
	for {
		i := strings.LastIndex(s, "(")
		if i <= 0 || !strings.HasSuffix(s, ")") {
			break
		}
		s = strings.TrimSpace(s[:i])
	}
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// Decided is the relation a big decision is remembered by (with the
// reasons it was made for, in its text).
const Decided = "DECIDED"

// neverKey is what "don't ask again" covers: one place, one decision, one
// thing - or every week, every workout.
func neverKey(subjectType, subject string) string {
	if subjectType == "주" || subjectType == "운동" {
		return subjectType
	}
	return subjectType + ":" + placeKey(subject)
}

// weekCandidate is the week ending today, with what it held.
func (m *Mind) weekCandidate(ctx context.Context, today time.Time) (candidate, error) {
	st := m.Opts.Store
	from := today.AddDate(0, 0, -6)
	y, w := today.ISOWeek()
	c := candidate{subjectType: "주", subject: fmt.Sprintf("%d-W%02d", y, w), at: from}
	occ, err := st.Occurrences(ctx, from, today.AddDate(0, 0, 1))
	if err != nil {
		return c, err
	}
	var spent int64
	works, photos := 0, 0
	for _, o := range occ {
		switch o.Kind {
		case "purchase":
			spent += o.Amount
		case "workout":
			works++
		case "photo":
			photos++
		}
	}
	plans, err := st.PlansOn(ctx, from.Format("2006-01-02"), today.Format("2006-01-02"))
	if err != nil {
		return c, err
	}
	n := 0
	for _, p := range plans {
		if p.State.Status == model.Confirmed || p.State.Status == model.Done {
			n++
		}
	}
	c.body = fmt.Sprintf("%s~%s · 일정 %d개 · 운동 %d번 · 지출 %s · 사진 %d장", md(from), md(today), n, works, money(spent), photos)
	return c, nil
}

func delivery(merchant string) bool { return predict.IsDelivery(merchant) }

func (m *Mind) reviewIntent(ctx context.Context, c candidate, now time.Time) (model.Intent, error) {
	season := map[time.Month]string{12: "겨울", 1: "겨울", 2: "겨울", 3: "봄", 4: "봄", 5: "봄", 6: "여름", 7: "여름", 8: "여름", 9: "가을", 10: "가을", 11: "가을"}[c.at.Month()]
	ctxt := map[string]string{"season": season, "weekday": weekdayNames[c.at.Weekday()]}
	if len(c.with) > 0 {
		ctxt["with"] = strings.Join(c.with, ",")
	}
	payload, _ := json.Marshal(prefs.Review{SubjectType: c.subjectType, Subject: c.subject, Context: ctxt, PlanID: c.planID})
	when := fmt.Sprintf("%d/%d", int(c.at.Month()), c.at.Day())
	in := model.Intent{
		Skill: "review", Kind: "review.place", EntityID: c.planID, Topic: "review:" + c.subjectType + ":" + placeKey(c.subject), Context: "review",
		Payload: payload, Title: fmt.Sprintf("%s (%s) 어땠어요?", c.subject, when),
		Body:   "한두 번 누르면 돼요. 이 장소의 특징과 내 취향을 따로 배워서 다음에 고를 때 써요.",
		Reason: "다녀온 곳 회고", Confidence: 0.9, Value: 1, Reversible: true, Evidence: c.evidence,
	}
	score := model.Field{Key: "score", Label: "점수", Kind: "select", Options: []string{"1", "2", "3", "4", "5"}, Required: true}
	note := model.Field{Key: "note", Label: "한 줄 (선택)", Kind: "text"}
	switch c.subjectType {
	case "주":
		in.Title, in.Body, in.Reason = "이번 주 어땠어요?", c.body, "일요일 회고"
		score.Label = "이번 주 점수"
		in.Fields = []model.Field{score,
			{Key: "good", Label: "좋았던 일", Kind: "text"}, {Key: "bad", Label: "아쉬웠던 일", Kind: "text"},
			{Key: "next", Label: "다음 주에 하나만", Kind: "text"}}
	case "결정":
		in.Title, in.Body, in.Reason = fmt.Sprintf("석 달 전 '%s', 지금 보면 어때요?", c.subject), c.body, "큰 결정 석 달 뒤"
		score.Label = "잘한 결정이었나 (5 아주 잘함)"
		in.Fields = []model.Field{score, {Key: "reason", Label: "그때 이유는", Kind: "select", Options: []string{"이유가 맞았음", "반쯤", "빗나감"}}, note}
	case "물건":
		in.Title, in.Body, in.Reason = fmt.Sprintf("%s, 2주 써보니 어때요?", c.subject), c.body, "산 지 2주"
		score.Label = "만족도"
		in.Fields = []model.Field{score, {Key: "again", Label: "다시 산다면", Kind: "select", Options: []string{"또 살래요", "다른 걸로", "안 살래요"}}, note}
	case "운동":
		in.Title, in.Body, in.Reason = fmt.Sprintf("오늘 %s, 얼마나 힘들었어요?", c.subject), c.body, "운동 체감 강도"
		score.Label = "체감 강도 (1 가벼움 · 5 한계)"
		in.Topic = "review:운동:" + now.Format("2006-01-02")
		in.Fields = []model.Field{score, {Key: "condition", Label: "컨디션", Kind: "select", Options: []string{"좋았음", "보통", "피곤했음"}}, note}
	default:
		bad, good, _, err := m.Prefs.Options(ctx, c.subjectType)
		if err != nil {
			return in, err
		}
		in.Fields = []model.Field{score,
			{Key: "bad", Label: "아쉬웠던 점", Kind: "multi", Options: bad},
			{Key: "good", Label: "좋았던 점", Kind: "multi", Options: good},
			note}
	}
	return in, nil
}

// answered is what came of a review or a confirmation card.
func (m *Mind) answered(ctx context.Context, in model.Intent) {
	switch in.Kind {
	case act.KindCategory:
		m.chargeAnswered(ctx, in)
	case "review.place":
		var r prefs.Review
		if json.Unmarshal(in.Payload, &r) != nil {
			return
		}
		switch in.Status {
		case model.Executed:
			moved, err := m.Prefs.Record(ctx, in.ID, r)
			if err != nil {
				m.Opts.Logger.Warn("회고를 기록하지 못했습니다", "err", err)
				return
			}
			for _, p := range moved {
				if p.Status == "estimated" {
					_, _ = m.Gate.Submit(ctx, confirmIntent(p))
				}
			}
		case model.Undone:
			_ = m.Opts.Store.ForgetObservations(ctx, in.ID)
		case model.Rejected:
			if strings.Contains(in.Error, "묻지 마") || strings.Contains(in.Error, "묻지마") {
				_ = m.Opts.Store.SetCursor(ctx, "review:never:"+neverKey(r.SubjectType, r.Subject), 1)
			}
		}
	case "preference.confirm":
		var p struct {
			Dimension string            `json:"dimension"`
			Polarity  int               `json:"polarity"`
			Scope     string            `json:"scope"`
			Answers   map[string]string `json:"answers"`
		}
		if json.Unmarshal(in.Payload, &p) != nil {
			return
		}
		answer := p.Answers["answer"]
		if in.Status == model.Rejected {
			answer = "이번만 그랬어" // turned down: not to be asked again
		}
		if in.Status == model.Executed || in.Status == model.Rejected {
			if err := m.Prefs.Decide(ctx, p.Dimension, p.Polarity, p.Scope, answer); err != nil {
				m.Opts.Logger.Warn("취향 답을 기록하지 못했습니다", "err", err)
			}
		}
	}
}

// confirmIntent asks once whether an estimated preference is so.
func confirmIntent(p model.Preference) model.Intent {
	payload, _ := json.Marshal(map[string]any{"dimension": p.Dimension, "polarity": p.Polarity, "scope": p.Scope, "text": prefs.Text(p)})
	options := []string{"네, 기억해 줘", "이번만 그랬어"}
	if p.Scope == "" {
		options = append(options, "가족과 갈 때만", "친구와 갈 때만")
	}
	return model.Intent{
		Skill: "prefs", Kind: "preference.confirm", Topic: fmt.Sprintf("pref:%s:%d:%s", p.Dimension, p.Polarity, p.Scope), Context: "prefs",
		Payload: payload, Title: fmt.Sprintf("%s 맞나요?", prefs.Text(p)), Body: prefs.Why(p),
		Reason: "여러 곳에서 같은 답을 하셨어요", Confidence: 0.85, Value: 0.9, Reversible: true,
		Fields: []model.Field{{Key: "answer", Label: "답", Kind: "select", Options: options, Required: true}},
	}
}

// expireQuestions withdraws questions nobody answered in three days.
func (m *Mind) expireQuestions(ctx context.Context) error {
	now := m.Opts.Now()
	recent, err := m.Opts.Store.IntentsSince(ctx, now.Add(-14*24*time.Hour))
	if err != nil {
		return err
	}
	for _, in := range recent {
		if in.Status != model.Pending || (in.Kind != "review.place" && in.Kind != "preference.confirm") || now.Sub(in.CreatedAt) < reviewExpires {
			continue
		}
		in.Status, in.Error = model.Superseded, "3일 동안 답이 없어 거둠"
		if err := m.Opts.Store.PutIntent(ctx, in); err != nil {
			return err
		}
		m.Gate.Close(ctx, in)
	}
	return nil
}

// statedPreferences confirms the preferences the operator said outright -
// a fact about themselves, liking or disliking something that names a
// dimension ("원래 시끄러운 거 싫어").
func (m *Mind) statedPreferences(ctx context.Context) error {
	st := m.Opts.Store
	last, err := st.Cursor(ctx, "prefs:facts")
	if err != nil {
		return err
	}
	facts, err := st.Facts(ctx, 200)
	if err != nil {
		return err
	}
	newest := last
	for _, f := range facts {
		at := f.CreatedAt.UnixMilli()
		if at <= last || !f.Subject.Self {
			continue
		}
		newest = max(newest, at)
		if dim, pol, ok := prefs.Stated(f.Relation, f.Text+" "+f.Object.Name); ok {
			quote := f.Text
			if len(f.Evidence) > 0 {
				quote = f.Evidence[0].Quote
			}
			if err := m.Prefs.Say(ctx, dim, pol, quote); err != nil {
				return err
			}
		}
	}
	return st.SetCursor(ctx, "prefs:facts", newest)
}

// PreferencesText is what the conversation is told of the operator's
// preferences: confirmed ones, and estimates, each with its grounds. "" when
// there are none or they could not be read; PreferencesRead says which.
func (m *Mind) PreferencesText(ctx context.Context) string {
	text, _ := m.PreferencesRead(ctx)
	return text
}

// PreferencesRead is PreferencesText with the error kept: the agent keeps
// the list in its instructions, and "none" would change them where
// "unreadable" must not.
func (m *Mind) PreferencesRead(ctx context.Context) (string, error) {
	ps, err := m.Opts.Store.Preferences(ctx)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, p := range ps {
		switch p.Status {
		case "confirmed":
			lines = append(lines, fmt.Sprintf("- %s (확정 · %s)", prefs.Text(p), orWhy(p)))
		case "estimated":
			lines = append(lines, fmt.Sprintf("- %s (추정 %.0f%% · %s)", prefs.Text(p), p.Confidence*100, orWhy(p)))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func orWhy(p model.Preference) string {
	if p.Why != "" {
		return p.Why
	}
	return prefs.Why(p)
}
