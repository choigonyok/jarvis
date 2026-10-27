package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// What is about to matter, worked out from the world model, the notes and
// what just happened - each a few rules over bounded windows:
//
//   people  the morning of a plan with someone, what is still open with them
//   gift    an anniversary close enough that a wished-for gift must be
//           ordered now to arrive in time
//   salary  pay arrived, and the notes say it is put to work within days
//   late    a payment far from where a plan is, too close to its start

// Assets reads the allocation (assets-svc, read only).
type Assets interface {
	Allocation(ctx context.Context) ([]Drift, error)
}

// Drift is one bucket of the allocation: where it is against its target.
type Drift struct {
	Label   string  `json:"label"`
	Current float64 `json:"current"`
	Target  float64 `json:"target"`
}

var weekdayNames = []string{"일", "월", "화", "수", "목", "금", "토"}

func md(t time.Time) string { return fmt.Sprintf("%d/%d", int(t.Month()), t.Day()) }

// people: today's plans with someone, and what is open with that person -
// "오늘 민수 만나는 날. 9/14에 「그 책 빌려주기」 하기로 하셨어요."
func (m *Mind) people(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now().In(loc)
	today := now.Format("2006-01-02")
	plans, err := st.PlansOn(ctx, today, today)
	if err != nil {
		return err
	}
	loops, err := st.Entities(ctx, model.KindLoop, "", true)
	if err != nil {
		return err
	}
	rooms, err := st.Rooms(ctx)
	if err != nil {
		return err
	}
	// A one-to-one room is named for the person: what was said there is
	// with them, named or not.
	alone := map[string]string{}
	for _, r := range rooms {
		if !strings.ContainsAny(r.Name, ",·&") && !strings.HasSuffix(r.Room, ":owner") && r.Name != "" {
			alone[r.Room] = r.Name
		}
	}
	with := func(e model.Entity) []string {
		if len(e.State.People) > 0 {
			return e.State.People
		}
		if n := alone[e.Room]; n != "" {
			return []string{n}
		}
		return nil
	}
	// One notice a person, with everything open with them. One a loop was
	// nine pushes at 07:00 for one friend, and they spent the day's budget
	// before the 08:00 brief (2026-10-25).
	type open struct {
		id   string
		line string
		ev   []model.Evidence
	}
	for _, p := range plans {
		if p.State.Status != model.Confirmed || p.State.Date != today {
			continue
		}
		byWho := map[string][]open{}
		var order []string
		for _, l := range loops {
			if l.State.PlanID == p.ID || l.State.Type == "정산" {
				continue
			}
			who := ""
			for _, a := range with(l) {
				for _, b := range with(p) {
					if samePerson(a, b) {
						who = b
					}
				}
			}
			if who == "" {
				continue
			}
			var ev []model.Evidence
			said := l.CreatedAt.In(loc)
			if cs, err := st.Changes(ctx, l.ID, 50); err == nil && len(cs) > 0 {
				first := cs[len(cs)-1]
				ev, said = first.Evidence, first.At.In(loc)
			}
			line := fmt.Sprintf("%s에 「%s」 하기로 하셨어요.", md(said), l.State.Title)
			if l.State.Owner == "them" {
				line = fmt.Sprintf("%s에 %s가 「%s」 하기로 했어요.", md(said), who, l.State.Title)
			}
			if _, ok := byWho[who]; !ok {
				order = append(order, who)
			}
			byWho[who] = append(byWho[who], open{id: l.ID, line: line, ev: ev})
		}
		for _, who := range order {
			opens := byWho[who]
			in := model.Intent{Skill: "people", Kind: "info", Topic: "meet:" + p.ID + ":" + who,
				Reason: "만나는 사람에게 걸린 열린 고리", Confidence: 1, Urgent: true}
			if len(opens) == 1 {
				in.EntityID, in.Evidence = opens[0].id, opens[0].ev
				in.Title, in.Body = "오늘 "+who+" 만나는 날. "+opens[0].line, p.State.Title
			} else {
				in.Title = fmt.Sprintf("오늘 %s 만나는 날. 하기로 한 것 %d가지", who, len(opens))
				lines := []string{p.State.Title}
				for _, o := range opens {
					lines = append(lines, "· "+o.line)
					in.Evidence = append(in.Evidence, o.ev...)
				}
				in.Body = strings.Join(lines, "\n")
			}
			if _, err := m.Gate.Submit(ctx, in); err != nil {
				return err
			}
		}
	}
	return nil
}

func samePerson(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && b != "" && (a == b || strings.HasSuffix(a, b) || strings.HasSuffix(b, a))
}

// shipping is the longest a gift may take to arrive, when nothing better is
// known: an online order from an unknown seller.
const shipping = 6

// gift: an anniversary or birthday with someone who said what they want.
// The card comes the first morning the days left are within shipping plus
// a two-day margin - "기념일 8일 남음. 향수는 배송 최대 6일이라 이번 주 주문
// 필요" - grounded on what they said.
func (m *Mind) gift(ctx context.Context) error {
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	plans, err := st.PlansOn(ctx, today.Format("2006-01-02"), today.AddDate(0, 0, 14).Format("2006-01-02"))
	if err != nil {
		return err
	}
	facts, err := st.Facts(ctx, 500)
	if err != nil {
		return err
	}
	for _, p := range plans {
		if p.State.Status != model.Confirmed || p.State.Type != "기념일" || len(p.State.People) == 0 {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", p.State.Date, loc)
		if err != nil {
			continue
		}
		left := int(math.Round(day.Sub(today).Hours() / 24))
		if left > shipping+2 || left < 1 {
			continue
		}
		for _, f := range facts {
			if !wish(f) || !slicesHasPerson(p.State.People, f.Subject.Name) {
				continue
			}
			item := f.Object.Name
			urgency := "이번 주 주문 필요"
			if left <= shipping {
				urgency = "지금 주문해도 늦을 수 있어요"
			}
			payload, _ := json.Marshal(map[string]string{"item": item, "class": "선물", "planId": p.ID, "site": "coupang.com"})
			said := f.CreatedAt.In(loc)
			if _, err := m.Gate.Submit(ctx, model.Intent{
				Skill: "gift", Kind: "cart.add", EntityID: p.ID, Topic: "gift:" + p.ID + ":" + item, Context: "gift", Payload: payload,
				Title:  fmt.Sprintf("%s %d일 남음. %s 배송 최대 %d일이라 %s. 장바구니에 담을까요?", p.State.Type, left, self.Topic(item), shipping, urgency),
				Body:   fmt.Sprintf("%s · %s에 %s", p.State.Title, md(said), f.Text),
				Reason: "기념일에 맞춰 도착하려면 지금 주문해야 해요", Confidence: 0.85, Value: 1, Evidence: f.Evidence, Reversible: true,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// wish is a fact that someone wants something.
func wish(f model.Fact) bool {
	if f.Status == model.FactDropped || f.Object.Name == "" {
		return false
	}
	r := strings.ToUpper(f.Relation)
	return r == "WANTS" || r == "WISHES" || r == "WANTS_GIFT" || strings.Contains(f.Text, "갖고 싶") || strings.Contains(f.Text, "받고 싶")
}

func slicesHasPerson(people []string, name string) bool {
	for _, p := range people {
		if samePerson(p, name) {
			return true
		}
	}
	return false
}

// salary: pay arrived. If the notes say it is put to work within days, the
// card says how the allocation stands and what the plan was - a plan only:
// the assets side reads and never orders.
func (m *Mind) salary(ctx context.Context, o model.Occurrence) error {
	if o.Kind != "deposit" || o.Class != "급여" {
		return nil
	}
	notes, err := m.Opts.Store.Notes(ctx)
	if err != nil {
		return err
	}
	for _, n := range notes {
		if n.Status == model.Rejected || n.Rule == nil || n.Rule.Kind != "salary" {
			continue
		}
		gap := ""
		if m.Opts.Assets != nil {
			if rows, err := m.Opts.Assets.Allocation(ctx); err == nil {
				worst, label := 0.0, ""
				for _, r := range rows {
					d := points(r.Target) - points(r.Current)
					if d > worst {
						worst, label = d, r.Label
					}
				}
				if worst >= 1 {
					gap = fmt.Sprintf("%s 비중 목표보다 %.0f%%p 낮음. ", label, worst)
				}
			}
		}
		at := o.At.In(m.Opts.Loc)
		payload, _ := json.Marshal(map[string]string{"plan": n.Rule.Plan, "noteId": n.ID})
		if _, err := m.Gate.Submit(ctx, model.Intent{
			Skill: "salary", Kind: "note", Topic: "salary:" + at.Format("2006-01"), Context: "salary", Payload: payload,
			Title:  fmt.Sprintf("월급 들어왔어요. %s이번 달 적립 %s 어떠세요?", gap, n.Rule.Plan),
			Body:   n.Text,
			Reason: "월급이 들어오면 며칠 안에 적립하셨어요", Confidence: 0.8, Value: 0.9, Reversible: true,
			Evidence: []model.Evidence{{Ref: o.Ref, Quote: o.Quote}},
		}); err != nil {
			return err
		}
	}
	return nil
}

// points is a share as percentage points: 0.17 or 17 both as 17.
func points(f float64) float64 {
	if math.Abs(f) <= 1 {
		return f * 100
	}
	return f
}

// late: a payment (or photo) somewhere a plan is a known trip away from,
// too close to the plan's start to make it - "민수에게 '10분 정도 늦을 것
// 같아 미안!' 보낼까요?". The message is a draft; sending it is the
// operator's.
func (m *Mind) late(ctx context.Context, o model.Occurrence) error {
	if (o.Kind != "purchase" && o.Kind != "photo" && o.Kind != "visit") || o.Area == "" && o.Place == "" {
		return nil // nowhere known: no grounds to say anything
	}
	if o.Kind == "visit" {
		if o.Until == nil {
			return nil // arrived, not leaving
		}
		o.At = *o.Until // leaving the region is setting out
	}
	st, loc := m.Opts.Store, m.Opts.Loc
	now := m.Opts.Now()
	if now.Sub(o.At) > 30*time.Minute {
		return nil // old news
	}
	day := o.At.In(loc).Format("2006-01-02")
	plans, err := st.PlansOn(ctx, day, day)
	if err != nil {
		return err
	}
	notes, err := st.Notes(ctx)
	if err != nil {
		return err
	}
	for _, p := range plans {
		if p.State.Status != model.Confirmed || p.State.Start == "" || len(p.State.People) == 0 {
			continue
		}
		start, err := time.ParseInLocation("2006-01-02 15:04", p.State.Date+" "+p.State.Start, loc)
		if err != nil || start.Before(o.At) || start.Sub(o.At) > 2*time.Hour {
			continue
		}
		for _, n := range notes {
			r := n.Rule
			if n.Status == model.Rejected || r == nil || r.Kind != "travel" || r.Minutes <= 0 {
				continue
			}
			if !world.SamePlace(o.Area, o.Place, r.From, "") || !world.SamePlace(p.State.Area, p.State.Place, r.To, "") {
				continue
			}
			eta := o.At.Add(time.Duration(r.Minutes) * time.Minute)
			behind := eta.Sub(start)
			if behind < 5*time.Minute {
				continue
			}
			mins := int(math.Ceil(behind.Minutes()/10) * 10)
			who := p.State.People[0]
			text := fmt.Sprintf("%d분 정도 늦을 것 같아 미안!", mins)
			payload, _ := json.Marshal(map[string]string{"to": who, "text": text, "purpose": fmt.Sprintf("%s 약속에 %d분 정도 늦는다고 미안하다고", p.State.Start, mins), "planId": p.ID})
			if _, err := m.Gate.Submit(ctx, model.Intent{
				Skill: "late", Kind: "message.draft", EntityID: p.ID, Topic: "late:" + p.ID, Context: "late", Payload: payload,
				Title:  fmt.Sprintf("%s에게 '%s' 보낼까요?", who, text),
				Body:   fmt.Sprintf("%s %s · %s %s에서 출발하면 %s쯤 도착 (%s)", p.State.Start, p.State.Title, o.At.In(loc).Format("15:04"), firstNonEmpty(o.Area, o.Place), eta.In(loc).Format("15:04"), n.Text),
				Reason: "약속 시각보다 늦게 도착할 것 같아요", Confidence: 0.8, Value: 1, Reversible: true,
				Evidence: []model.Evidence{{Ref: o.Ref, Quote: o.Quote}},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// AssetsHTTP is assets-svc's portfolio, read for its allocation.
type AssetsHTTP struct {
	URL, Token string
}

func (a AssetsHTTP) Allocation(ctx context.Context) ([]Drift, error) {
	var out struct {
		Allocation *struct {
			Rows []Drift `json:"rows"`
		} `json:"allocation"`
		Problems []string `json:"problems"`
	}
	if err := getJSON(ctx, strings.TrimRight(a.URL, "/")+"/portfolio", a.Token, &out); err != nil {
		return nil, err
	}
	// Half the accounts is no allocation: a broker that timed out reads as
	// "유망주 0%", and a plan from that would be wrong by exactly that much.
	for _, p := range out.Problems {
		if strings.HasPrefix(p, "업비트:") || strings.HasPrefix(p, "한국투자증권:") || strings.HasPrefix(p, "금현물:") {
			return nil, fmt.Errorf("잔고를 다 읽지 못했습니다: %s", p)
		}
	}
	if out.Allocation == nil {
		return nil, nil
	}
	return out.Allocation.Rows, nil
}

// StaticAssets is a fixed allocation (evaluation).
type StaticAssets []Drift

func (s StaticAssets) Allocation(context.Context) ([]Drift, error) { return s, nil }

func getJSON(ctx context.Context, url, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
