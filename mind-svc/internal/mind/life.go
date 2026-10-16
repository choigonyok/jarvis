package mind

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/link"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// The long run of a life, from what is already kept: what was given and
// received at whose wedding (facts the interpreter checked against the
// conversation), where the operator wants to go and what they want (facts
// too), and what they own and run out of (purchased items). Nothing new is
// stored here; each is read from its ledger when it is needed.

// Relations of the life ledgers (interpret's prompt, rules 13-14).
const (
	GaveTo       = "GAVE_TO"
	ReceivedFrom = "RECEIVED_FROM"
	WantsToVisit = "WANTS_TO_VISIT"
	WantsToBuy   = "WANTS_TO_BUY"
	Goal         = "GOAL"
)

// ceremony: a wedding, a funeral, a first birthday with someone - and what
// went between the operator and them before ("이 분 결혼식 때 10만 원
// 받으셨어요").
func (m *Mind) ceremony(ctx context.Context, e model.Entity) error {
	st := e.State
	if st.Type != "경조사" || st.Status != model.Confirmed {
		return nil
	}
	people := st.People
	if len(people) == 0 {
		people = namesIn(st.Title)
	}
	facts, err := m.Opts.Store.FactsWith(ctx, GaveTo, ReceivedFrom)
	if err != nil {
		return err
	}
	var lines []string
	var ev []model.Evidence
	who := ""
	for _, f := range facts {
		r := strings.ToUpper(f.Relation)
		if (r != GaveTo && r != ReceivedFrom) || f.Status == model.FactFailed || !f.Subject.Self {
			continue
		}
		for _, p := range people {
			if samePerson(f.Object.Name, p) {
				lines = append(lines, f.Text)
				ev = append(ev, f.Evidence...)
				who = p
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	if done, _ := m.Opts.Store.Cursor(ctx, "ledger:told:"+e.ID); done > 0 {
		return nil // once per plan
	}
	if err := m.Opts.Store.SetCursor(ctx, "ledger:told:"+e.ID, 1); err != nil {
		return err
	}
	day := st.Date
	if d, err := time.ParseInLocation("2006-01-02", st.Date, m.Opts.Loc); err == nil {
		day = md(d)
	}
	_, err = m.Gate.Submit(ctx, model.Intent{
		Skill: "life", Kind: "info", EntityID: e.ID, Topic: "ledger:" + e.ID,
		Title:  fmt.Sprintf("%s(%s) · %s님과 주고받은 기록: %s", st.Title, day, who, lines[0]),
		Body:   strings.Join(lines, "\n"),
		Reason: "경조사 장부", Confidence: 1, Evidence: ev,
	})
	return err
}

// namesIn is the names a title begins with ("민수 결혼식" → 민수).
func namesIn(title string) []string {
	f := strings.Fields(title)
	if len(f) == 0 {
		return nil
	}
	name := strings.TrimSuffix(strings.TrimSuffix(f[0], "의"), "님")
	if r := []rune(name); len(r) >= 2 && len(r) <= 4 {
		return []string{name}
	}
	return nil
}

// wishes is what the operator said they want to do, see or have.
func (m *Mind) wishes(ctx context.Context, relations ...string) ([]model.Fact, error) {
	facts, err := m.Opts.Store.FactsWith(ctx, relations...)
	if err != nil {
		return nil, err
	}
	var out []model.Fact
	for _, f := range facts {
		if f.Subject.Self && f.Status != model.FactFailed && slices.Contains(relations, strings.ToUpper(f.Relation)) && f.Object.Name != "" {
			out = append(out, f)
		}
	}
	return out, nil
}

// weekend: on a Friday morning, an empty weekend and a place the operator
// said they want to go - the one least recently brought up.
func (m *Mind) weekend(ctx context.Context) error {
	now := m.Opts.Now().In(m.Opts.Loc)
	if now.Weekday() != time.Friday {
		return nil
	}
	sat := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, m.Opts.Loc)
	plans, err := m.Opts.Store.PlansOn(ctx, sat.Format("2006-01-02"), sat.AddDate(0, 0, 1).Format("2006-01-02"))
	if err != nil {
		return err
	}
	for _, p := range plans {
		if p.State.Status == model.Confirmed {
			return nil // not empty
		}
	}
	ws, err := m.wishes(ctx, WantsToVisit)
	if err != nil || len(ws) == 0 {
		return err
	}
	type cand struct {
		f    model.Fact
		last int64
	}
	var cs []cand
	for _, f := range ws {
		last, _ := m.Opts.Store.Cursor(ctx, "wish:last:"+f.Object.Name)
		cs = append(cs, cand{f, last})
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].last < cs[j].last })
	pick := cs[0].f
	if err := m.Opts.Store.SetCursor(ctx, "wish:last:"+pick.Object.Name, now.Unix()); err != nil {
		return err
	}
	_, err = m.Gate.Submit(ctx, model.Intent{
		Skill: "life", Kind: "info", Topic: "wish:" + pick.Object.Name + ":" + sat.Format("2006-01-02"),
		Title:  fmt.Sprintf("이번 주말이 비어 있어요. 가고 싶다던 %s 어때요?", pick.Object.Name),
		Body:   fmt.Sprintf("%s에 말씀하셨어요: %s", md(pick.CreatedAt.In(m.Opts.Loc)), pick.Text),
		Reason: "버킷리스트", Confidence: 1, Evidence: pick.Evidence,
	})
	return err
}

// nearWish: arriving in a region where a place the operator wants to go
// is - said the moment it is near.
func (m *Mind) nearWish(ctx context.Context, o model.Occurrence) error {
	if o.Kind != "visit" || o.Until != nil || m.Opts.Now().Sub(o.At) > time.Hour {
		return nil
	}
	ws, err := m.wishes(ctx, WantsToVisit)
	if err != nil {
		return err
	}
	for _, f := range ws {
		if !world.SamePlace(o.Area, o.Place, "", f.Object.Name) {
			continue
		}
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: "wishnear:" + f.Object.Name + ":" + o.At.In(m.Opts.Loc).Format("2006-01-02"),
			Title: fmt.Sprintf("가고 싶다던 %s, 지금 근처예요", f.Object.Name),
			Body:  f.Text, Reason: "버킷리스트", Confidence: 1, Urgent: true, Evidence: f.Evidence,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Thing is a class of thing the operator owns, by its purchases.
type Thing struct {
	Class     string    `json:"class"`
	Items     []string  `json:"items"`
	Bought    []string  `json:"bought"` // days, oldest first
	CycleDays int       `json:"cycleDays,omitempty"`
	Next      string    `json:"next,omitempty"` // when it usually runs out
	Last      time.Time `json:"last"`
	Sizes     []string  `json:"sizes,omitempty"` // as the items' names say: 270mm, XL
}

// Things is what was bought in the last half year, by class, with how
// often the consumables come round. Items not yet classified are, a few at
// a time (common sense, cached by name).
func (m *Mind) Things(ctx context.Context, classify int) ([]Thing, error) {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now()
	items, err := st.Occurrences(ctx, now.AddDate(0, -6, 0), now, "purchase.item")
	if err != nil {
		return nil, err
	}
	by := map[string]*Thing{}
	var order []string
	for _, it := range items {
		class, err := st.ThingClass(ctx, it.Title)
		if err != nil && classify > 0 && m.Link.Judge != nil {
			classify--
			if j, jerr := m.Link.Judge.Of(ctx, it.Title, "일상"); jerr == nil {
				class, err = j.Class, nil
			} else if jerr == link.ErrNoModel {
				classify = 0
			}
		}
		if err != nil || class == "" {
			continue
		}
		t := by[class]
		if t == nil {
			t = &Thing{Class: class}
			by[class] = t
			order = append(order, class)
		}
		if !slices.Contains(t.Items, it.Title) {
			t.Items = append(t.Items, it.Title)
			for _, sz := range Sizes(it.Title) {
				if !slices.Contains(t.Sizes, sz) {
					t.Sizes = append(t.Sizes, sz)
				}
			}
		}
		day := it.At.In(loc).Format("2006-01-02")
		if !slices.Contains(t.Bought, day) {
			t.Bought = append(t.Bought, day)
		}
		if it.At.After(t.Last) {
			t.Last = it.At
		}
	}
	out := make([]Thing, 0, len(order))
	for _, c := range order {
		t := *by[c]
		sort.Strings(t.Bought)
		if len(t.Bought) >= 3 {
			first, _ := time.ParseInLocation("2006-01-02", t.Bought[0], loc)
			last, _ := time.ParseInLocation("2006-01-02", t.Bought[len(t.Bought)-1], loc)
			cycle := int(math.Round(last.Sub(first).Hours() / 24 / float64(len(t.Bought)-1)))
			if cycle >= 5 && cycle <= 90 {
				t.CycleDays = cycle
				t.Next = last.AddDate(0, 0, cycle).Format("2006-01-02")
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// restock: a consumable that comes round about now and has not been bought
// - "생수 살 때 됐어요 (보통 21일마다, 마지막 10/1)", a line in the evening.
func (m *Mind) restock(ctx context.Context) error {
	things, err := m.Things(ctx, 10)
	if err != nil {
		return err
	}
	now := m.Opts.Now().In(m.Opts.Loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, m.Opts.Loc)
	for _, t := range things {
		if t.CycleDays == 0 {
			continue
		}
		next, err := time.ParseInLocation("2006-01-02", t.Next, m.Opts.Loc)
		if err != nil || today.Before(next.AddDate(0, 0, -2)) || today.After(next.AddDate(0, 0, t.CycleDays/2)) {
			continue
		}
		last := t.Last.In(m.Opts.Loc)
		body := strings.Join(t.Items, ", ")
		// What the two-week question said of them: one not to buy again is
		// said so.
		for _, it := range t.Items {
			attrs, _ := m.Opts.Store.Attributes(ctx, "물건", it)
			for _, a := range attrs {
				if a.Dimension == "재구매" && a.Value < 0 {
					body += fmt.Sprintf(" · '%s'는 다시 안 산다고 하셨어요", it)
				}
			}
		}
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: "restock:" + t.Class + ":" + last.Format("2006-01-02"),
			Title:  fmt.Sprintf("%s 살 때 됐어요 (보통 %d일마다, 마지막 %s)", t.Class, t.CycleDays, md(last)),
			Body:   body,
			Reason: "가진 물건", Confidence: 1,
		}); err != nil {
			return err
		}
	}
	return nil
}

// deliveries keeps a note of how often food is delivered and when, from
// the card payments to the delivery apps (and where the phone was) over
// the last four weeks - the first step on meals, since the apps' own
// orders cannot be read.
func (m *Mind) deliveries(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now()
	pays, err := st.Occurrences(ctx, now.AddDate(0, 0, -28), now, "purchase")
	if err != nil {
		return err
	}
	stays, err := st.Occurrences(ctx, now.AddDate(0, 0, -29), now, "visit")
	if err != nil {
		return err
	}
	days := map[time.Weekday]int{}
	slots := map[string]int{}
	n, home := 0, 0
	var refs []string
	for _, p := range pays {
		if !delivery(p.Title) {
			continue
		}
		n++
		t := p.At.In(loc)
		days[t.Weekday()]++
		slot := "저녁"
		switch h := t.Hour(); {
		case h < 15:
			slot = "점심"
		case h >= 21:
			slot = "밤"
		}
		slots[slot]++
		refs = append(refs, p.Ref)
		for _, v := range stays {
			if v.Class == "home" && !p.At.Before(v.At) && (v.Until == nil || p.At.Before(*v.Until)) {
				home++
				break
			}
		}
	}
	if n < 3 {
		return nil
	}
	top := func(m map[string]int) string {
		best, k := 0, ""
		for key, v := range m {
			if v > best || v == best && key < k {
				best, k = v, key
			}
		}
		return k
	}
	var dayNames []string
	for d, c := range days {
		if c*3 >= n { // a weekday with a third or more of them
			dayNames = append(dayNames, weekdayNames[d])
		}
	}
	slices.Sort(dayNames)
	text := fmt.Sprintf("배달은 최근 4주 %d번(주 %.1f회), 주로 %s", n, float64(n)/4, top(slots))
	if len(dayNames) > 0 {
		text += ", " + strings.Join(dayNames, "·") + "요일에 많다"
	}
	if home > 0 {
		text += fmt.Sprintf(" (집에서 %d번)", home)
	}
	if len(refs) > 5 {
		refs = refs[len(refs)-5:]
	}
	_, _, err = m.Self.Hold(ctx, model.Note{ID: "note_delivery", Text: text, Scope: "식사", Confidence: 0.7, Evidence: refs,
		Rule: &model.Rule{Kind: "pattern", Target: "delivery", Days: n}}, "밤 정리: 지난 4주 배달 결제")
	return err
}
