package mind

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
)

// Photos come in as occurrences (photos-svc, sensor), tied to a plan by the
// hours they fell in. What fell in no plan's hours is asked about before it is
// forgotten: which plan were these? The answer ties them for sure, and what the
// operator says of them goes to long-term memory, so a later "그때 사진" finds them.
// The rest of what photos are good for is the conversation's: it searches them
// by what is in them (photos-svc) and asks here when a place's were taken.

const (
	photosEvery = 15 * time.Minute
	// photosBack is how recent a batch may be to be asked about, photosGap the
	// pause that ends one, and photosQuiet how long after its last photo: not
	// while the phone is still out.
	photosBack  = 72 * time.Hour
	photosGap   = 90 * time.Minute
	photosQuiet = time.Hour
	// A few photos are not worth a question, and with no plan near to choose
	// from it takes more.
	photosMin, photosMinLoose = 3, 6
	// photosShown is how many a card shows.
	photosShown = 4
	// noPlan is the choice for photos of no plan.
	noPlan = "일정 아님"
)

// every runs step on a timer, first after one period.
func (m *Mind) every(ctx context.Context, period time.Duration, step func(context.Context) error) {
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := step(ctx); err != nil && ctx.Err() == nil {
				m.Opts.Logger.Warn("정해 둔 일을 하지 못했습니다", "err", err)
			}
		}
	}
}

// photoAsk is what a photos.place card carries.
type photoAsk struct {
	Photos []string `json:"photos"`
	// Plans is which plan each choice means.
	Plans   map[string]string `json:"plans"`
	Day     string            `json:"day"` // "11/2(월)"
	Answers map[string]string `json:"answers,omitempty"`
}

// askPhotos raises one question a day about a batch of photos that fell in no
// plan, in the waking hours.
func (m *Mind) askPhotos(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now().In(loc)
	if now.Hour() < 9 || now.Hour() >= 22 {
		return nil
	}
	today, _ := dayNumber(now)
	if last, err := st.Cursor(ctx, "photos:asked"); err != nil || last >= today {
		return err
	}
	batches, err := m.unplaced(ctx, now)
	if err != nil {
		return err
	}
	for _, b := range batches {
		key := "photos:batch:" + b[0].ID
		if done, err := st.Cursor(ctx, key); err != nil || done > 0 {
			continue
		}
		in, ok, err := m.photoQuestion(ctx, b)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := st.SetCursor(ctx, key, 1); err != nil {
			return err
		}
		if err := st.SetCursor(ctx, "photos:asked", today); err != nil {
			return err
		}
		_, err = m.Gate.Submit(ctx, in)
		return err
	}
	return nil
}

func dayNumber(t time.Time) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(t.Format("20060102"), "%d", &n)
	return n, err
}

// unplaced is the recent photos tied to no plan, in batches taken together,
// oldest first; a batch still being taken waits.
func (m *Mind) unplaced(ctx context.Context, now time.Time) ([][]model.Occurrence, error) {
	st := m.Opts.Store
	photos, err := st.Occurrences(ctx, now.Add(-photosBack), now, model.Photo)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(photos, func(a, b model.Occurrence) int { return a.At.Compare(b.At) })
	var batches [][]model.Occurrence
	var cur []model.Occurrence
	flush := func() {
		if len(cur) > 0 && now.Sub(cur[len(cur)-1].At) >= photosQuiet {
			batches = append(batches, cur)
		}
		cur = nil
	}
	for _, o := range photos {
		links, err := st.LinksOf(ctx, o.ID)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(links, func(l model.Link) bool { return l.Rel == model.During && l.State != model.Rejected }) {
			continue
		}
		if len(cur) > 0 && o.At.Sub(cur[len(cur)-1].At) > photosGap {
			flush()
		}
		cur = append(cur, o)
	}
	flush()
	return batches, nil
}

// photoQuestion is the card for one batch: the plans near it to choose from, or
// with none near, what it was.
func (m *Mind) photoQuestion(ctx context.Context, batch []model.Occurrence) (model.Intent, bool, error) {
	loc := m.Opts.Loc
	first, last := batch[0].At.In(loc), batch[len(batch)-1].At.In(loc)
	plans, err := m.Opts.Store.PlansOn(ctx, first.AddDate(0, 0, -1).Format("2006-01-02"), last.AddDate(0, 0, 1).Format("2006-01-02"))
	if err != nil {
		return model.Intent{}, false, err
	}
	// Nearest first: a day can hold more plans than a card shows, and the one
	// the photos belong to is the one just before or after them.
	slices.SortStableFunc(plans, func(a, b model.Entity) int {
		return cmp.Compare(farFrom(a.State, first, loc), farFrom(b.State, first, loc))
	})
	choices := map[string]string{}
	var options []string
	for _, p := range plans {
		st := p.State
		if st.Status != model.Confirmed && st.Status != model.Done || st.Declined != "" || st.Owner == "partner" {
			continue
		}
		label := skill.Day(st, loc) + " " + st.Title
		if _, dup := choices[label]; !dup && len(options) < 6 {
			choices[label] = p.ID
			options = append(options, label)
		}
	}
	if len(batch) < photosMin || len(options) == 0 && len(batch) < photosMinLoose {
		return model.Intent{}, false, nil
	}
	day := skill.Day(model.State{Date: first.Format("2006-01-02")}, loc)
	ids := make([]string, len(batch))
	for i, o := range batch {
		ids[i] = strings.TrimPrefix(o.ID, "photo:")
	}
	payload, _ := json.Marshal(photoAsk{Photos: ids, Plans: choices, Day: day})
	when := first.Format("15:04")
	if last.Sub(first) >= time.Minute {
		when += "~" + last.Format("15:04")
	}
	if place := batch[0].Area; place != "" {
		when += " · " + place
	}
	in := model.Intent{
		Skill: "photos", Kind: model.PhotosPlace, Topic: "photos:place:" + batch[0].ID, Context: model.PhotosPlace,
		Payload: payload, Title: fmt.Sprintf("%s 사진 %d장, 어느 일정이에요?", day, len(batch)), Body: when,
		Reason: "찍은 시간에 맞는 일정이 없어요", Confidence: 0.9, Value: 1, Reversible: true, Photos: spread(ids, photosShown),
	}
	note := model.Field{Key: "note", Label: "무슨 사진인가요 (선택)", Kind: "text"}
	if len(options) == 0 {
		in.Title = fmt.Sprintf("%s 사진 %d장, 뭐 찍은 거예요?", day, len(batch))
		note.Label, note.Required = "무슨 사진인가요", true
		in.Fields = []model.Field{note}
		return in, true, nil
	}
	in.Fields = []model.Field{{Key: "plan", Label: "어느 일정", Kind: "select", Options: append(options, noPlan), Required: true}, note}
	return in, true, nil
}

// farFrom is how far a plan lay from a moment: none while it was on, and a plan
// with no hour comes after the ones with an hour close by.
func farFrom(st model.State, at time.Time, loc *time.Location) time.Duration {
	when, timed, ok := skill.When(st, loc)
	if !ok {
		return math.MaxInt64
	}
	if day := at.In(loc).Format("2006-01-02"); st.EndDate != "" && st.Date <= day && day <= st.EndDate {
		return 0
	}
	if !timed {
		return when.Sub(at).Abs() + 6*time.Hour
	}
	return when.Sub(at).Abs()
}

// spread is n of ids evenly apart: a batch, not five of one moment.
func spread(ids []string, n int) []string {
	if len(ids) <= n {
		return ids
	}
	out := make([]string, n)
	for i := range n {
		out[i] = ids[i*len(ids)/n]
	}
	return out
}

// photosAnswered ties the photos to the plan named, for sure, and what was said
// of them goes to long-term memory.
func (m *Mind) photosAnswered(ctx context.Context, in model.Intent) {
	if in.Status != model.Executed {
		return
	}
	var a photoAsk
	if json.Unmarshal(in.Payload, &a) != nil {
		return
	}
	now := m.Opts.Now()
	chosen := a.Answers["plan"]
	note := strings.TrimSpace(a.Answers["note"])
	title := chosen
	if id := a.Plans[chosen]; id != "" {
		for _, photo := range a.Photos {
			l := model.Link{From: "photo:" + photo, To: id, Rel: model.During, Confidence: 1, State: model.Confirmed, By: "user",
				Why: "사진 카드에서 고름", CreatedAt: now, UpdatedAt: now}
			if err := m.Opts.Store.PutLink(ctx, l); err != nil {
				m.Opts.Logger.Warn("사진을 일정에 잇지 못했습니다", "intent", in.ID, "err", err)
				return
			}
		}
	} else if note == "" {
		return // no plan, and nothing said of them
	} else {
		title = ""
	}
	// A plan chosen names its own day.
	day := a.Day
	if strings.HasPrefix(title, day) {
		day = ""
	}
	text := strings.TrimSpace(fmt.Sprintf("%s %s 사진 %d장", day, title, len(a.Photos)))
	if note != "" {
		text += ": " + note
	}
	object := firstNonEmpty(title, note)
	f := model.Fact{ID: "mf_" + in.ID, Room: model.OwnerRoom, Subject: model.Party{Name: "나", Self: true}, Relation: "PHOTOGRAPHED",
		Object: model.Party{Name: object, Kind: "Event"}, Text: text, Evidence: []model.Evidence{{Ref: "intent:" + in.ID, Quote: text}},
		Confidence: 1, Why: "사진 카드에 답함", Status: model.FactPending, NextTry: now, CreatedAt: now}
	if err := m.Opts.Store.PutFact(ctx, f); err != nil {
		m.Opts.Logger.Warn("사진에 대한 말을 기억으로 보내지 못했습니다", "intent", in.ID, "err", err)
	}
}

// PhotoWindow is when photos of a place were taken: the days a plan or a stay
// there was on, and the photos known to be of it.
type PhotoWindow struct {
	Title  string   `json:"title"`
	From   string   `json:"from"` // YYYY-MM-DD
	To     string   `json:"to"`
	Place  string   `json:"place,omitempty"`
	Photos []string `json:"photos,omitempty"` // ids
}

// photoWindows bounds how many stretches a place's photos are looked for in,
// and how far back.
const (
	photoWindows = 5
	photoYears   = 3
)

// PhotoWindows is when photos of what q names were taken, the latest first: its
// plans and its stays that have happened, by title or place.
func (m *Mind) PhotoWindows(ctx context.Context, q string) ([]PhotoWindow, error) {
	st, loc := m.Opts.Store, m.Opts.Loc
	want := photoWord(q)
	if want == "" {
		return []PhotoWindow{}, nil
	}
	now := m.Opts.Now().In(loc)
	today := now.Format("2006-01-02")
	hits := func(words ...string) bool {
		for _, w := range words {
			if w = photoWord(w); w != "" && (strings.Contains(w, want) || strings.Contains(want, w)) {
				return true
			}
		}
		return false
	}
	plans, err := st.Entities(ctx, model.KindPlan, "", false)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []PhotoWindow
	for _, p := range plans {
		s := p.State
		if s.Status == model.Cancelled || s.Status == model.Proposed || s.Declined != "" || s.Date == "" || s.Date > today || !hits(s.Title, s.Place, s.Area) {
			continue
		}
		w := PhotoWindow{Title: s.Title, From: s.Date, To: firstNonEmpty(s.EndDate, s.Date), Place: s.Place}
		links, err := st.LinksOf(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, l := range links {
			if l.Rel == model.During && l.State == model.Confirmed && strings.HasPrefix(l.From, "photo:") {
				w.Photos = append(w.Photos, strings.TrimPrefix(l.From, "photo:"))
			}
		}
		seen[w.From] = true
		out = append(out, w)
	}
	stays, err := st.Occurrences(ctx, now.AddDate(-photoYears, 0, 0), now, model.Visit)
	if err != nil {
		return nil, err
	}
	for _, o := range stays {
		day := o.At.In(loc).Format("2006-01-02")
		if seen[day] || !hits(o.Title, o.Place, o.Area) {
			continue
		}
		w := PhotoWindow{Title: o.Title, From: day, To: day, Place: firstNonEmpty(o.Place, o.Title)}
		if o.Until != nil {
			w.To = o.Until.In(loc).Format("2006-01-02")
		}
		seen[day] = true
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b PhotoWindow) int { return strings.Compare(b.From, a.From) })
	return out[:min(len(out), photoWindows)], nil
}

// photoWord is words as they are compared: no spaces, no case.
func photoWord(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
