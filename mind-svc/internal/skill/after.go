package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// After owns what comes once a plan is over: what was paid during it is
// split with the people who were there, and its photos are gathered for
// them. It looks when the plan probably ends (or when it is said to be
// over), and only at what is linked to the plan for sure - a payment that
// merely fell inside its hours is not grounds enough.
type After struct{}

func (After) Name() string                                { return "after" }
func (After) Wants(e model.Entity) bool                   { return e.Kind == model.KindPlan }
func (After) WantsLinks() bool                            { return true }
func (After) OnTick(context.Context, View, string) Result { return Result{} }

// recent: a plan that ended longer ago than this (a calendar read for the
// first time) is not looked back on.
const recent = 12 * time.Hour

func (After) OnChange(ctx context.Context, v View, e model.Entity, changes []model.Change) Result {
	st := e.State
	if st.Status == model.Cancelled || st.Status == model.Proposed {
		return Result{CancelTimers: true, Withdraw: true}
	}
	if st.Date == "" || (st.Start == "" && st.EndDate == "") {
		return Result{}
	}
	_, end, ok := v.Span(ctx, e)
	if !ok {
		return Result{}
	}
	now := v.Now()
	at := end
	if st.Status == model.Done || at.Before(now) {
		if now.Sub(end) > recent && st.Status != model.Done {
			return Result{}
		}
		at = now
	}
	return Result{CancelTimers: true, Timers: []model.Timer{{Skill: "after", EntityID: e.ID, Name: "end", FireAt: at}}}
}

func (a After) OnTimer(ctx context.Context, v View, e model.Entity, t model.Timer) Result {
	if t.Name != "end" || (e.State.Status != model.Confirmed && e.State.Status != model.Done) {
		return Result{}
	}
	during := a.during(ctx, v, e)
	var r Result
	if in, ok := settle(e, during); ok {
		r.Intents = append(r.Intents, in)
	}
	if in, ok := photos(e, during, v.Loc()); ok {
		r.Intents = append(r.Intents, in)
	}
	return r
}

// seen is an occurrence linked to the plan, and whether for sure.
type seen struct {
	model.Occurrence
	Sure bool // a confirmed link; otherwise it only fell in the plan's hours
}

// during is what happened during the plan. A suggestion rests on the sure
// ones; the others may only ride along.
func (After) during(ctx context.Context, v View, e model.Entity) []seen {
	links, _ := v.Links(ctx, e.ID)
	var out []seen
	for _, l := range links {
		if l.To != e.ID || l.Rel != model.During || l.State == model.Rejected {
			continue
		}
		if o, err := v.Occurrence(ctx, l.From); err == nil {
			out = append(out, seen{o, l.State == model.Confirmed})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Settle is what a settle.request carries: the split, and (filled in by the
// actuator before anyone sees it) a draft message for each person.
type Settle struct {
	PlanID    string            `json:"planId"`
	Title     string            `json:"title"`
	Date      string            `json:"date"`
	Total     int64             `json:"total"`
	PerPerson int64             `json:"perPerson"`
	People    []string          `json:"people"`
	Drafts    map[string]string `json:"drafts,omitempty"`
}

// settle splits a meal or a get-together's bill with the people who were
// there, when the operator paid it.
func settle(e model.Entity, during []seen) (model.Intent, bool) {
	st := e.State
	if st.Type != "식사" && st.Type != "회식" && st.Type != "모임" || len(st.People) == 0 {
		return model.Intent{}, false
	}
	var total int64
	var ev []model.Evidence
	var where []string
	for _, o := range during {
		if o.Kind != "purchase" || o.Amount <= 0 || !o.Sure {
			continue
		}
		total += o.Amount
		ev = append(ev, model.Evidence{Ref: o.Ref, Quote: fmt.Sprintf("%s %s원", o.Title, won(o.Amount))})
		where = append(where, fmt.Sprintf("%s %s %s원", o.At.Format("15:04"), o.Title, won(o.Amount)))
	}
	n := int64(len(st.People) + 1)
	if total < 20000 || total/n < 5000 {
		return model.Intent{}, false
	}
	per := (total/n + 50) / 100 * 100
	payload, _ := json.Marshal(Settle{PlanID: e.ID, Title: st.Title, Date: st.Date, Total: total, PerPerson: per, People: st.People})
	return model.Intent{
		Kind: "settle.request", EntityID: e.ID, Topic: "settle:" + e.ID, Context: "settle", Payload: payload,
		Title:  fmt.Sprintf("1인 %s원. 정산 메시지 보낼까요?", won(per)),
		Body:   fmt.Sprintf("%s · 내가 낸 %s원을 %d명이 나누면 1인 %s원\n%s", st.Title, won(total), n, won(per), strings.Join(where, ", ")),
		Reason: "함께한 일정 중에 내가 계산했어요", Confidence: 0.85, Evidence: ev, Reversible: true,
	}, true
}

// Album is what a photos.album carries: every photo of the plan, the few
// picked to send first, and who they are for.
type Album struct {
	PlanID string   `json:"planId"`
	Name   string   `json:"name"`
	All    []string `json:"all"`    // asset ids
	Picked []string `json:"picked"` // the ones to send
	To     string   `json:"to"`
}

// photos gathers a date's or a trip's photos for the person it was with.
func photos(e model.Entity, during []seen, loc *time.Location) (model.Intent, bool) {
	st := e.State
	if st.Type != "데이트" && st.Type != "여행" && st.Type != "캠핑" || len(st.People) == 0 {
		return model.Intent{}, false
	}
	var shots []model.Occurrence
	sure := 0
	for _, o := range during {
		if o.Kind == "photo" && strings.HasPrefix(o.ID, "photo:") {
			shots = append(shots, o.Occurrence)
			if o.Sure {
				sure++
			}
		}
	}
	// The photos taken in its hours ride along, but only photos known to
	// be of it - its place, its people - are grounds to bring it up.
	if len(shots) < 5 || sure < 3 {
		return model.Intent{}, false
	}
	all := make([]string, len(shots))
	for i, o := range shots {
		all[i] = strings.TrimPrefix(o.ID, "photo:")
	}
	picked := pick(shots, 5)
	to := st.People[0]
	day, _ := time.ParseInLocation("2006-01-02", st.Date, loc)
	name := fmt.Sprintf("%d/%d %s", int(day.Month()), day.Day(), st.Title)
	payload, _ := json.Marshal(Album{PlanID: e.ID, Name: name, All: all, Picked: picked, To: to})
	return model.Intent{
		Kind: "photos.album", EntityID: e.ID, Topic: "photos:" + e.ID, Context: "photos", Payload: payload,
		Title:  fmt.Sprintf("오늘 사진 %d장. 잘 나온 %d장 골라뒀어요. %s에게 보낼까요?", len(shots), len(picked), to),
		Body:   fmt.Sprintf("%s 동안 찍은 사진 %d장", st.Title, len(shots)),
		Reason: "함께한 일정의 사진이에요", Confidence: 0.8, Reversible: true,
		Evidence: []model.Evidence{{Ref: shots[0].Ref, Quote: fmt.Sprintf("%s~%s 사진", shots[0].At.In(loc).Format("15:04"), shots[len(shots)-1].At.In(loc).Format("15:04"))}},
	}, true
}

// pick chooses n photos to send: favourites first, then spread over the
// day so they are not five of the same moment.
func pick(shots []model.Occurrence, n int) []string {
	var out []string
	for _, o := range shots {
		if o.Class == "favorite" && len(out) < n {
			out = append(out, strings.TrimPrefix(o.ID, "photo:"))
		}
	}
	rest := n - len(out)
	if rest <= 0 {
		return out
	}
	var others []model.Occurrence
	for _, o := range shots {
		if o.Class != "favorite" {
			others = append(others, o)
		}
	}
	for i := 0; i < rest && i < len(others); i++ {
		o := others[i*len(others)/rest]
		out = append(out, strings.TrimPrefix(o.ID, "photo:"))
	}
	return out
}

// won is 96000 as "96,000".
func won(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
