package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/prefs"
)

// Questions before suggestions: after a night at a campsite or a meal at a
// new restaurant, the evening asks how it was - a form card, a tap or two.
// The answers are data (internal/prefs): what the place is like, and, once
// several places agree, what the operator is like, which is asked about
// once more before it is believed.

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
}

// reviews asks tonight's questions: campsites and trips that ended today,
// stays of a night or more at a campsite region, and restaurants paid at for
// the first time today.
func (m *Mind) reviews(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	var cands []candidate

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
		cands = append(cands, candidate{kind, ps.Place, p.ID, ps.People, sp.Start})
	}
	stays, err := st.Occurrences(ctx, today.AddDate(0, 0, -3), now, "visit")
	if err != nil {
		return err
	}
	for _, v := range stays {
		if v.Class == "camp" && v.Until != nil && v.Until.Sub(v.At) >= 12*time.Hour && !v.Until.Before(today) {
			cands = append(cands, candidate{"캠핑장", v.Title, "", nil, v.At})
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
		cands = append(cands, candidate{"식당", o.Title, "", nil, o.At})
	}

	asked := 0
	seen := map[string]bool{}
	for _, c := range cands {
		key := c.subjectType + ":" + c.subject
		if seen[key] || asked >= questionsADay {
			continue
		}
		seen[key] = true
		if never, _ := st.Cursor(ctx, "review:never:"+key); never > 0 {
			continue
		}
		in, err := m.Gate.Submit(ctx, reviewIntent(c, now))
		if err != nil {
			return err
		}
		if in.Card {
			asked++
		}
	}
	return nil
}

func delivery(merchant string) bool {
	for _, w := range []string{"배달의민족", "배민", "우아한형제", "쿠팡이츠", "요기요", "땡겨요"} {
		if strings.Contains(merchant, w) {
			return true
		}
	}
	return false
}

func reviewIntent(c candidate, now time.Time) model.Intent {
	season := map[time.Month]string{12: "겨울", 1: "겨울", 2: "겨울", 3: "봄", 4: "봄", 5: "봄", 6: "여름", 7: "여름", 8: "여름", 9: "가을", 10: "가을", 11: "가을"}[c.at.Month()]
	ctxt := map[string]string{"season": season, "weekday": weekdayNames[c.at.Weekday()]}
	if len(c.with) > 0 {
		ctxt["with"] = strings.Join(c.with, ",")
	}
	payload, _ := json.Marshal(prefs.Review{SubjectType: c.subjectType, Subject: c.subject, Context: ctxt, PlanID: c.planID})
	when := fmt.Sprintf("%d/%d", int(c.at.Month()), c.at.Day())
	return model.Intent{
		Skill: "review", Kind: "review.place", EntityID: c.planID, Topic: "review:" + c.subjectType + ":" + c.subject, Context: "review",
		Payload: payload, Title: fmt.Sprintf("%s (%s) 어땠어요?", c.subject, when),
		Body:   "한두 번 누르면 돼요. 이 장소의 특징과 내 취향을 따로 배워서 다음에 고를 때 써요.",
		Reason: "다녀온 곳 회고", Confidence: 0.9, Value: 1, Reversible: true,
		Fields: []model.Field{
			{Key: "score", Label: "점수", Kind: "select", Options: []string{"1", "2", "3", "4", "5"}, Required: true},
			{Key: "bad", Label: "아쉬웠던 점", Kind: "multi", Options: prefs.Bad(c.subjectType)},
			{Key: "good", Label: "좋았던 점", Kind: "multi", Options: prefs.Good(c.subjectType)},
			{Key: "note", Label: "한 줄 (선택)", Kind: "text"},
		},
	}
}

// answered is what came of a review or a confirmation card.
func (m *Mind) answered(ctx context.Context, in model.Intent) {
	switch in.Kind {
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
				_ = m.Opts.Store.SetCursor(ctx, "review:never:"+r.SubjectType+":"+r.Subject, 1)
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
// preferences: confirmed ones, and estimates, each with its grounds.
func (m *Mind) PreferencesText(ctx context.Context) string {
	ps, err := m.Opts.Store.Preferences(ctx)
	if err != nil {
		return ""
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
	return strings.Join(lines, "\n")
}

func orWhy(p model.Preference) string {
	if p.Why != "" {
		return p.Why
	}
	return prefs.Why(p)
}
