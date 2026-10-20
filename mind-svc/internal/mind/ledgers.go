package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/prefs"
)

// The rest of a life's long run, each read from what is already kept: the
// conversation's facts (a contract's end, a licence's expiry, the day the
// recycling goes out, why a stock was bought, a friend who moved), the
// card's charges (what comes back every month, what a meal was) and the
// review cards (the places worth going back to). Nothing here is stored
// apart from a cursor that says a reminder was given.

// Relations of these ledgers (interpret's prompt, rules 11, 15-19).
const (
	ExpiresOn       = "EXPIRES_ON"
	ChoreOn         = "CHORE_ON"
	InvestedBecause = "INVESTED_BECAUSE"
	LivesIn         = "LIVES_IN"
)

// --- deadlines: contracts, subscriptions, licences, passports ------------------

// deadlineMarks: a deadline is brought up three months, a month and a
// week ahead - each once.
var deadlineMarks = []int{90, 30, 7}

// deadlines, each morning: what the operator said ends on a day ("전세 계약
// 2027-03-01 만기", "정보처리기사 2026-12 만료") is brought up ahead of it.
func (m *Mind) deadlines(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	facts, err := st.FactsOn(ctx, ExpiresOn, time.Time{}, m.Opts.Now().Add(time.Minute))
	if err != nil {
		return err
	}
	// 같은 것의 만기가 다시 말해지면(연장) 나중 것만 본다. FactsOn 은 오래된
	// 것부터이므로 뒤에 온 것이 덮는다.
	latest := map[string]model.Fact{}
	var order []string
	for _, f := range facts {
		if !f.Subject.Self || f.Status == model.FactFailed || f.ValidAt == "" {
			continue
		}
		key := strings.Join(strings.Fields(strings.ToLower(firstNonEmpty(f.Object.Name, f.Text))), "")
		if _, seen := latest[key]; !seen {
			order = append(order, key)
		}
		latest[key] = f
	}
	for _, key := range order {
		f := latest[key]
		due, err := time.ParseInLocation("2006-01-02", f.ValidAt[:min(10, len(f.ValidAt))], loc)
		if err != nil {
			continue
		}
		days := int(math.Round(due.Sub(today).Hours() / 24))
		mark := 0
		for _, k := range deadlineMarks {
			if days >= 0 && days <= k {
				mark = k
			}
		}
		if mark == 0 {
			continue
		}
		cursor := fmt.Sprintf("deadline:%s:%d", f.ID, mark)
		if done, _ := st.Cursor(ctx, cursor); done > 0 {
			continue
		}
		what := firstNonEmpty(f.Object.Name, f.Text)
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: cursor,
			Title: fmt.Sprintf("%s 만기까지 %d일 (%s)", what, days, md(due)),
			Body:  f.Text, Reason: "기한이 있는 일", Confidence: 1, Urgent: days <= 7, Evidence: f.Evidence,
		}); err != nil {
			return err
		}
		if err := st.SetCursor(ctx, cursor, 1); err != nil {
			return err
		}
	}
	return nil
}

// --- fixed costs ---------------------------------------------------------------

// Fixed is a charge that comes back every month: a subscription, a phone
// bill, insurance, rent paid by card.
type Fixed struct {
	Merchant string `json:"merchant"`
	Amount   int64  `json:"amount"` // the latest
	Day      int    `json:"day"`    // of the month, usually
	Months   int    `json:"months"` // months in a row seen
	Last     string `json:"last"`
	Before   int64  `json:"before,omitempty"` // the amount before, when it changed
}

// FixedCosts is the monthly charges of the last half year: the same
// merchant in three or more months in a row, about the same day and the
// same amount.
func (m *Mind) FixedCosts(ctx context.Context) ([]Fixed, error) {
	loc := m.Opts.Loc
	now := m.Opts.Now()
	pays, err := m.Opts.Store.Occurrences(ctx, now.AddDate(0, -6, 0), now, "purchase")
	if err != nil {
		return nil, err
	}
	by := map[string][]model.Occurrence{}
	for _, p := range pays {
		by[p.Title] = append(by[p.Title], p)
	}
	out := []Fixed{}
	for merchant, ps := range by {
		sort.Slice(ps, func(i, j int) bool { return ps[i].At.Before(ps[j].At) })
		months := map[string]model.Occurrence{}
		for _, p := range ps {
			months[p.At.In(loc).Format("2006-01")] = p // the month's latest
		}
		if len(months) < 3 || len(months) != len(ps) {
			continue // not monthly, or more than once in a month: shopping, not a bill
		}
		keys := make([]string, 0, len(months))
		for k := range months {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		run := 1
		for i := 1; i < len(keys); i++ {
			a, _ := time.Parse("2006-01", keys[i-1])
			b, _ := time.Parse("2006-01", keys[i])
			if a.AddDate(0, 1, 0).Equal(b) {
				run++
			} else {
				run = 1
			}
		}
		if run < 3 {
			continue
		}
		var amounts []int64
		var days []int
		for _, k := range keys {
			amounts = append(amounts, months[k].Amount)
			days = append(days, months[k].At.In(loc).Day())
		}
		if slices.Max(days)-slices.Min(days) > 4 {
			continue
		}
		mid := median64(amounts[:len(amounts)-1])
		steady := 0
		for _, a := range amounts[:len(amounts)-1] {
			if math.Abs(float64(a-mid)) <= 0.05*float64(mid) {
				steady++
			}
		}
		if steady < len(amounts)-1 {
			continue
		}
		last := months[keys[len(keys)-1]]
		f := Fixed{Merchant: merchant, Amount: last.Amount, Day: days[len(days)/2], Months: run, Last: last.At.In(loc).Format("2006-01-02")}
		if math.Abs(float64(last.Amount-mid)) > 0.05*float64(mid) {
			f.Before = mid
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	return out, nil
}

// fixedChanges, each night: a monthly charge that changed its amount this
// time is a line in the evening ("넷플릭스 13,500원 → 17,000원").
func (m *Mind) fixedChanges(ctx context.Context) error {
	fixed, err := m.FixedCosts(ctx)
	if err != nil {
		return err
	}
	now := m.Opts.Now()
	for _, f := range fixed {
		last, err := time.ParseInLocation("2006-01-02", f.Last, m.Opts.Loc)
		if err != nil || f.Before == 0 || now.Sub(last) > 3*24*time.Hour {
			continue
		}
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: "fixed:" + f.Merchant + ":" + f.Last[:7],
			Title:  fmt.Sprintf("%s %s → %s (매달 %d일쯤)", f.Merchant, money(f.Before), money(f.Amount), f.Day),
			Body:   fmt.Sprintf("%d달째 이어지는 고정비가 이번에 바뀌었어요", f.Months),
			Reason: "고정비", Confidence: 1,
		}); err != nil {
			return err
		}
	}
	return nil
}

func median64(xs []int64) int64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s) == 0 {
		return 0
	}
	return s[len(s)/2]
}

// --- warranties and sizes --------------------------------------------------------

// Warranty is how long a thing is usually guaranteed (the consumer dispute
// standard's one year for most appliances and electronics) - a guess, said
// as one, since the receipt is not read.
const Warranty = 365 * 24 * time.Hour

var electronic = []string{"가전", "디지털", "컴퓨터", "노트북", "휴대폰", "전자", "태블릿", "카메라", "모니터", "청소기", "이어폰", "TV"}

// warranties, each morning: something electronic bought for 100,000 won or
// more whose usual year is two weeks from ending - worth a look before it
// does.
func (m *Mind) warranties(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now()
	items, err := st.Occurrences(ctx, now.Add(-Warranty-15*24*time.Hour), now.Add(-Warranty+15*24*time.Hour), "purchase.item")
	if err != nil {
		return err
	}
	for _, it := range items {
		class, _ := st.ThingClass(ctx, it.Title)
		if it.Amount < 100000 || !containsAny(it.Class+" "+class+" "+it.Title, electronic) {
			continue
		}
		end := it.At.Add(Warranty)
		left := end.Sub(now)
		if left < 0 || left > 14*24*time.Hour {
			continue
		}
		cursor := "warranty:" + it.ID
		if done, _ := st.Cursor(ctx, cursor); done > 0 {
			continue
		}
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: cursor,
			Title:  fmt.Sprintf("%s 보증이 곧 끝나요 (%s까지, 보통 1년)", it.Title, md(end.In(loc))),
			Body:   fmt.Sprintf("%s에 %s에 산 것. 고장이나 불편한 데가 있으면 그 전에 맡기세요", md(it.At.In(loc)), money(it.Amount)),
			Reason: "가진 물건", Confidence: 1,
		}); err != nil {
			return err
		}
		if err := st.SetCursor(ctx, cursor, 1); err != nil {
			return err
		}
	}
	return nil
}

var sizeRE = []*regexp.Regexp{
	regexp.MustCompile(`(\d{3})\s?mm`),
	regexp.MustCompile(`사이즈\s*[:：]?\s*([0-9A-Za-z]{1,4})`),
	regexp.MustCompile(`(?:^|[\s,(/])((?:[2-4]?X)?[SML]|XXL|XXXL)(?:$|[\s,)/])`),
}

// Sizes is the sizes a purchased item's name says: 270mm, XL, 사이즈 95.
func Sizes(name string) []string {
	var out []string
	for i, re := range sizeRE {
		for _, m := range re.FindAllStringSubmatch(name, -1) {
			s := m[1]
			if i == 0 {
				s += "mm"
			}
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// --- people: a move --------------------------------------------------------------

// moved, each night: someone the operator knows moved (a fact that lives-in
// somewhere new, ending the old one) - a housewarming may be due, with what
// went between them before.
func (m *Mind) moved(ctx context.Context) error {
	st := m.Opts.Store
	now := m.Opts.Now()
	facts, err := st.FactsOn(ctx, LivesIn, now.Add(-48*time.Hour), now.Add(time.Minute))
	if err != nil {
		return err
	}
	for _, f := range facts {
		if f.Subject.Self || len(f.Ends) == 0 || f.Status == model.FactFailed {
			continue
		}
		who := f.Subject.Name
		body := "집들이 선물을 챙길까요?"
		if lines := m.ledgerWith(ctx, who); len(lines) > 0 {
			body += " 지난번: " + strings.Join(lines, " · ")
		}
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: "moved:" + who + ":" + f.Object.Name,
			Title: fmt.Sprintf("%s님이 %s(으)로 이사했대요", who, f.Object.Name), Body: body,
			Reason: "사람의 근황", Confidence: 1, Evidence: f.Evidence,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ledgerWith is what went between the operator and someone (GAVE_TO,
// RECEIVED_FROM), as said.
func (m *Mind) ledgerWith(ctx context.Context, who string) []string {
	facts, err := m.Opts.Store.FactsWith(ctx, GaveTo, ReceivedFrom)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range facts {
		r := strings.ToUpper(f.Relation)
		if (r == GaveTo || r == ReceivedFrom) && f.Subject.Self && samePerson(f.Object.Name, who) {
			out = append(out, f.Text)
		}
	}
	return out
}

// --- chores: the day before --------------------------------------------------------

var weekdayRE = regexp.MustCompile(`([월화수목금토일])요일`)

// chore: arriving home in the evening, the day before a chore's day ("분리수거
// 는 목요일") - said once that evening.
func (m *Mind) chore(ctx context.Context, o model.Occurrence) error {
	if o.Kind != "visit" || o.Class != "home" || o.Until != nil {
		return nil
	}
	loc := m.Opts.Loc
	at := o.At.In(loc)
	now := m.Opts.Now()
	if now.Sub(o.At) > 30*time.Minute || at.Hour() < 16 {
		return nil
	}
	tomorrow := weekdayNames[at.AddDate(0, 0, 1).Weekday()]
	facts, err := m.Opts.Store.FactsOn(ctx, ChoreOn, time.Time{}, now.Add(time.Minute))
	if err != nil {
		return err
	}
	for _, f := range facts {
		if !f.Subject.Self || f.Status == model.FactFailed {
			continue
		}
		days := weekdayRE.FindAllStringSubmatch(f.Text+" "+f.Object.Name, -1)
		hit := false
		for _, d := range days {
			hit = hit || strings.HasPrefix(tomorrow, d[1])
		}
		if !hit {
			continue
		}
		what := firstNonEmpty(f.Object.Name, f.Text)
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: "chore:" + what + ":" + at.Format("2006-01-02"),
			Title: fmt.Sprintf("내일(%s) %s 날이에요", tomorrow, what), Body: f.Text,
			Reason: "집안일", Confidence: 1, Urgent: true, Evidence: f.Evidence,
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- investments: why it was bought ----------------------------------------------

// investMemo: a holding sold - with why it was bought, as said then, next to
// what came of it.
func (m *Mind) investMemo(ctx context.Context, o model.Occurrence) error {
	if o.Kind != "asset" || o.Class != "매도" {
		return nil
	}
	facts, err := m.Opts.Store.FactsOn(ctx, InvestedBecause, time.Time{}, m.Opts.Now().Add(time.Minute))
	if err != nil {
		return err
	}
	names := append([]string{o.Title}, o.Things...)
	for _, f := range facts {
		if !f.Subject.Self || f.Status == model.FactFailed {
			continue
		}
		match := false
		for _, n := range names {
			if n != "" && (strings.Contains(f.Object.Name, n) || strings.Contains(n, f.Object.Name)) {
				match = true
			}
		}
		if !match {
			continue
		}
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "life", Kind: "info", Topic: "memo:" + o.ID,
			Title:  fmt.Sprintf("%s 매도 · 살 때 이유: %s", o.Title, f.Text),
			Body:   fmt.Sprintf("%s (%s에 하신 말)", o.Quote, md(f.CreatedAt.In(m.Opts.Loc))),
			Reason: "투자 판단 메모", Confidence: 1, Evidence: f.Evidence,
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- the operator's own map ---------------------------------------------------------

// Place is somewhere reviewed, with what came of going there.
type Place struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Score   float64  `json:"score"` // the reviews' average
	Reviews int      `json:"reviews"`
	Visits  int      `json:"visits"` // stays in its region and payments there
	Spent   int64    `json:"spent"`
	Good    []string `json:"good,omitempty"` // what it is known for
	Bad     []string `json:"bad,omitempty"`
	Last    string   `json:"last"`
}

// Places is the operator's own map: every place reviewed, best first.
func (m *Mind) Places(ctx context.Context) ([]Place, error) {
	st := m.Opts.Store
	scores, err := st.ObservationsOn(ctx, "")
	if err != nil {
		return nil, err
	}
	by := map[string]*Place{}
	var order []string
	for _, o := range scores {
		if !prefs.Places[o.SubjectType] || o.Score == 0 {
			continue
		}
		k := o.SubjectType + ":" + o.Subject
		p := by[k]
		if p == nil {
			p = &Place{Type: o.SubjectType, Name: o.Subject}
			by[k] = p
			order = append(order, k)
		}
		p.Score += float64(o.Score)
		p.Reviews++
		if d := o.At.In(m.Opts.Loc).Format("2006-01-02"); d > p.Last {
			p.Last = d
		}
	}
	occ, err := st.Occurrences(ctx, m.Opts.Now().AddDate(-2, 0, 0), m.Opts.Now(), "visit", "purchase")
	if err != nil {
		return nil, err
	}
	out := make([]Place, 0, len(order))
	for _, k := range order {
		p := *by[k]
		p.Score = math.Round(p.Score/float64(p.Reviews)*10) / 10
		for _, o := range occ {
			if o.Title == p.Name || o.Place == p.Name {
				p.Visits++
				if o.Kind == "purchase" {
					p.Spent += o.Amount
				}
			}
		}
		attrs, _ := st.Attributes(ctx, p.Type, p.Name)
		for _, a := range attrs {
			switch {
			case a.Value > 0:
				p.Good = append(p.Good, a.Dimension)
			case a.Value < 0:
				p.Bad = append(p.Bad, a.Dimension)
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// --- meals ---------------------------------------------------------------------------

// Meal is a meal as the card saw it: where, how much, delivered or not, at
// home or not.
type Meal struct {
	At       time.Time `json:"at"`
	Merchant string    `json:"merchant"`
	Amount   int64     `json:"amount"`
	Delivery bool      `json:"delivery"`
	Home     bool      `json:"home"`
	Slot     string    `json:"slot"` // 아침 | 점심 | 저녁 | 밤
}

// Meals is the last days' meals, from food charges and the stays at home.
func (m *Mind) Meals(ctx context.Context, days int) ([]Meal, error) {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now()
	from := now.AddDate(0, 0, -days)
	pays, err := st.Occurrences(ctx, from, now, "purchase")
	if err != nil {
		return nil, err
	}
	stays, err := st.Occurrences(ctx, from.Add(-24*time.Hour), now, "visit")
	if err != nil {
		return nil, err
	}
	out := []Meal{}
	for _, p := range pays {
		d := delivery(p.Title)
		if p.Class != "식비" && !d {
			continue
		}
		meal := Meal{At: p.At, Merchant: p.Title, Amount: p.Amount, Delivery: d}
		switch h := p.At.In(loc).Hour(); {
		case h < 10:
			meal.Slot = "아침"
		case h < 15:
			meal.Slot = "점심"
		case h < 21:
			meal.Slot = "저녁"
		default:
			meal.Slot = "밤"
		}
		for _, v := range stays {
			if v.Class == "home" && !p.At.Before(v.At) && (v.Until == nil || p.At.Before(*v.Until)) {
				meal.Home = true
			}
		}
		out = append(out, meal)
	}
	return out, nil
}

// --- portfolio out of band ------------------------------------------------------

// AssetsBand is assets-svc's event: a bucket of the portfolio has left the
// band around its target.
const AssetsBand = "assets.band"

// portfolioBand raises one rebalance question for the whole portfolio. The
// gate's ledger keeps it quiet after it was raised or turned down, as it does
// any topic.
//
// With orders to place (assets-svc checks the plan), it is a card that
// places exactly those orders when approved (act.RebalanceActuator) - asked
// every time. Without (nothing tradable, the service unreachable), it stays
// a question pointing at the 자산 screen, where the plan can be placed by hand.
// now: this is the moment for it (band waited for the evening at home), so
// that question is a push and not a line in the evening's summary.
func (m *Mind) portfolioBand(ctx context.Context, o obs.Observation, now bool) (model.Intent, error) {
	var n struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	_ = json.Unmarshal(o.Notify, &n)
	title := firstNonEmpty(n.Title, "자산 비중이 목표 범위를 벗어났어요")
	in := model.Intent{
		Skill: "assets", Kind: "info", Topic: "rebalance",
		Title:  title + " - 리밸런싱을 검토해 보세요",
		Body:   strings.TrimSpace(n.Body + "\n자산 탭의 목표 비중에서 매매 계획을 볼 수 있어요."),
		Reason: "목표 비중의 허용 범위를 벗어남", Confidence: 0.8,
		Link: "/assets?focus=allocation", Urgent: now,
	}
	if m.Opts.Orders != nil {
		if orders, err := m.Opts.Orders.Check(ctx); err == nil && len(orders) > 0 {
			lines := make([]string, len(orders))
			for i, x := range orders {
				lines[i] = x.Line()
			}
			in.Kind = "assets.rebalance"
			in.Title = title + " - 리밸런싱할까요?"
			in.Body = strings.TrimSpace(n.Body + "\n" + strings.Join(lines, " · "))
			in.Payload, _ = json.Marshal(act.RebalancePlan{PlanID: o.Key, Orders: orders})
		} else if err != nil {
			m.Opts.Logger.Warn("리밸런싱 주문을 확인하지 못해 질문으로만 올립니다", "err", err)
		}
	}
	return m.Gate.Submit(ctx, in)
}
