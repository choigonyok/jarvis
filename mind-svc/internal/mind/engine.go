package mind

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
)

// The prediction engine's place in the day (predict/mine.go, engine.go):
// at night it finds the rules in what happened - of every source - and
// grades each by how its predictions went; in the morning every rule says
// what it expects today; an occurrence starts what usually follows it; what
// comes true is matched as it happens, and every prediction is scored when
// its time is up. What a rule that has come true expects is said to the
// conversation (Situation); a monthly payment that did not come is said in
// the evening. A candidate - a rule not yet borne out - says nothing to
// anyone.

// engineNight finds the rules and grades them by their record.
func (m *Mind) engineNight(ctx context.Context) error {
	mined, err := m.Predict.Mine(ctx)
	if err != nil {
		return err
	}
	if err := m.Predict.Keep(ctx, mined); err != nil {
		return err
	}
	m.forgetTracks()
	return m.Predict.Grade(ctx)
}

// engineMorning makes the rules' predictions for today.
func (m *Mind) engineMorning(ctx context.Context) error {
	_, err := m.Predict.Expect(ctx)
	return err
}

// scored hears predictions whose time came: a monthly payment, pay or bill
// a trusted cycle expected and that did not come is a line for the evening -
// a subscription ended, or the card it is paid with changed.
func (m *Mind) scored(ctx context.Context, prs ...model.Prediction) {
	for _, pr := range prs {
		if pr.Outcome == nil || *pr.Outcome || !strings.HasPrefix(pr.Target, "cycle:") || pr.Prob < 0.75 || len(pr.Basis) == 0 {
			continue
		}
		n, err := m.Opts.Store.Note(ctx, pr.Basis[0])
		if err != nil || !predict.Trusted(n) || n.Rule.Kind != predict.Cycle || n.Rule.Gap < 20 {
			continue
		}
		switch predict.Source(n.Rule.Key) {
		case "purchase", "deposit", "bill", "transfer", "asset":
		default:
			continue
		}
		label := predict.Label(n.Rule.Key)
		last := n.Rule.Last
		if t, err := time.ParseInLocation("2006-01-02", last, m.Opts.Loc); err == nil {
			last = md(t)
		}
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "predict", Kind: "info", Topic: "missed:" + n.Rule.Key + ":" + n.Rule.Last,
			Title:  fmt.Sprintf("이번엔 %s 없었어요", predict.Subject(label)),
			Body:   fmt.Sprintf("%s (마지막 %s). 해지했거나 결제 수단이 바뀌었는지 확인해 보세요.", n.Text, last),
			Reason: "기대가 빗나감", Confidence: pr.Prob, Value: 0.3,
		}); err != nil {
			m.Opts.Logger.Warn("빗나간 기대를 알리지 못했습니다", "err", err)
		}
	}
}

// Notes is the notes with how their predictions went, as of now - the
// candidates too only when all is asked.
func (m *Mind) Notes(ctx context.Context, all bool) ([]model.Note, error) {
	notes, err := m.Opts.Store.Notes(ctx)
	if err != nil {
		return nil, err
	}
	tracks := m.cachedTracks(ctx)
	out := []model.Note{}
	for _, n := range notes {
		if n.Status == model.Candidate && !all {
			continue
		}
		if t, ok := tracks[n.ID]; ok {
			n.Track = &t
		}
		out = append(out, n)
	}
	return out, nil
}

// tracks remembers the rules' records for a while: the conversation reads
// the situation every turn, and a record moves once a prediction is scored.
type tracks struct {
	mu sync.Mutex
	at time.Time
	by map[string]model.Track
}

const tracksFresh = 10 * time.Minute

func (m *Mind) cachedTracks(ctx context.Context) map[string]model.Track {
	t := &m.tracks
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by != nil && m.Opts.Now().Sub(t.at) < tracksFresh {
		return t.by
	}
	by, err := m.Predict.Tracks(ctx)
	if err != nil {
		m.Opts.Logger.Warn("예측 기록을 읽지 못했습니다", "err", err)
		return t.by
	}
	t.by, t.at = by, m.Opts.Now()
	return by
}

func (m *Mind) forgetTracks() {
	m.tracks.mu.Lock()
	m.tracks.by = nil
	m.tracks.mu.Unlock()
}

// expectLines is what the rules that have come true expect of today, for
// the conversation: open predictions whose window holds today, likely
// enough to be worth saying, from a rule shown or confirmed (the workout
// predictor once its own record is good). What already happened is not
// expected any more.
func (m *Mind) expectLines(ctx context.Context) string {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now().In(loc)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	prs, err := st.Predictions(ctx, now, now.AddDate(0, 0, 8))
	if err != nil {
		return ""
	}
	notes := map[string]model.Note{}
	if ns, err := st.Notes(ctx); err == nil {
		for _, n := range ns {
			notes[n.ID] = n
		}
	}
	type item struct {
		key, text string
		prob      float64
	}
	var items []item
	for _, pr := range prs {
		if pr.ScoredAt != nil || pr.Prob < 0.6 || pr.From != nil && !pr.From.Before(end) {
			continue
		}
		if !m.sayable(ctx, pr, notes) {
			continue
		}
		met := false
		if es, err := st.Expectations(ctx, pr.ID); err == nil {
			for _, e := range es {
				met = met || e.State == "met"
			}
		}
		if met {
			continue
		}
		key := pr.Key
		if key == "" {
			key = pr.Target
		}
		text := fmt.Sprintf("%s %.0f%%", expectLabel(pr, notes), pr.Prob*100)
		if !pr.Due.Before(end) {
			text += fmt.Sprintf(" (%s까지)", md(pr.Due.In(loc)))
		}
		items = append(items, item{key, text, pr.Prob})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].prob > items[j].prob })
	var parts []string
	var seen []string
	for _, it := range items {
		if slices.Contains(seen, it.key) || len(parts) == 5 {
			continue
		}
		seen = append(seen, it.key)
		parts = append(parts, it.text)
	}
	if len(parts) == 0 {
		return ""
	}
	return "- 평소대로라면 오늘 있을 일(예측이 맞아 온 습관만 · 확률일 뿐이니 사실처럼 말하지 말 것): " + strings.Join(parts, " · ")
}

// sayable is whether a prediction may reach the operator: made from a rule
// that has come true (shown or confirmed); the workout predictor, which
// rests on no note, once its own record beats the plain rate.
func (m *Mind) sayable(ctx context.Context, pr model.Prediction, notes map[string]model.Note) bool {
	if strings.HasPrefix(pr.Target, "workout:") {
		t := m.cachedTracks(ctx)["workout"]
		return t.N >= 5 && t.Skill() >= 0.1
	}
	if len(pr.Basis) == 0 {
		return false
	}
	n, ok := notes[pr.Basis[0]]
	return ok && predict.IsEngine(n.Rule) && predict.Trusted(n)
}

// expectLabel is what a prediction expects, in words.
func expectLabel(pr model.Prediction, notes map[string]model.Note) string {
	if strings.HasPrefix(pr.Target, "workout:") {
		return "운동"
	}
	if len(pr.Basis) > 0 {
		if n, ok := notes[pr.Basis[0]]; ok && n.Rule != nil {
			switch {
			case n.Rule.Kind == predict.After:
				return predict.Label(n.Rule.Anchor) + " 뒤 " + predict.Label(n.Rule.Key)
			case n.Rule.Usual != "":
				return predict.Label(n.Rule.Key) + "(보통 " + n.Rule.Usual + ")"
			}
		}
	}
	if pr.Key != "" {
		return predict.Label(pr.Key)
	}
	return pr.Text
}
