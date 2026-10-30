package ledger

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

var (
	seoul, _ = time.LoadLocation("Asia/Seoul")
	day      = time.Date(2026, 10, 28, 7, 0, 0, 0, seoul)
)

func ptr[T any](v T) *T { return &v }

func one(t *testing.T, in Inputs) model.Outcome {
	t.Helper()
	in.Loc = seoul
	if in.Now.IsZero() {
		in.Now = day.AddDate(0, 0, 30)
	}
	rows := Build(in)
	if len(rows) != 1 {
		t.Fatalf("%d 줄", len(rows))
	}
	return rows[0]
}

func purchase(id string, at time.Time, class string) model.Occurrence {
	return model.Occurrence{ID: id, Kind: "purchase", At: at, Title: "가게", Class: class}
}

// The 10/28 payment that reached the assistant on the 30th did happen.
func TestAPredictionIsScoredByWhatHappened(t *testing.T) {
	from := day.Add(-7 * time.Hour)
	pr := model.Prediction{ID: "p", Key: "purchase/식비", Prob: 0.9, Base: 0.7, From: &from,
		Due: day.Add(17 * time.Hour), Outcome: ptr(false), CreatedAt: day}
	o := one(t, Inputs{Predictions: []model.Prediction{pr}, Occurrences: []model.Occurrence{purchase("s", day.Add(13*time.Hour), "식비")}})
	if o.Result != model.ResultHit || o.Live == nil || *o.Live != false {
		t.Fatalf("늦게 온 결제도 맞음입니다: %+v", o)
	}
	if hit, ok := o.Scored(); !hit || !ok || o.Base == nil || *o.Base != 0.7 {
		t.Fatalf("%+v", o)
	}

	// Another class, the day after: it did not happen.
	o = one(t, Inputs{Predictions: []model.Prediction{pr}, Occurrences: []model.Occurrence{
		purchase("a", day.Add(13*time.Hour), "교통"), purchase("b", day.Add(30*time.Hour), "식비")}})
	if o.Result != model.ResultMiss || o.Live != nil {
		t.Fatalf("창 밖의 결제는 아닙니다: %+v", o)
	}
}

func TestAPredictionNotDueIsOpen(t *testing.T) {
	pr := model.Prediction{ID: "p", Key: "purchase/식비", Prob: 0.9, Due: day.Add(17 * time.Hour), CreatedAt: day}
	if o := one(t, Inputs{Predictions: []model.Prediction{pr}, Now: day}); o.Result != model.ResultOpen {
		t.Fatalf("%+v", o)
	}
}

// A prediction with no key is known only by how it was scored.
func TestAPredictionWithoutAKeyKeepsItsScore(t *testing.T) {
	pr := model.Prediction{ID: "p", Target: "workout:2026-10-28", Prob: 0.7, Due: day, Outcome: ptr(true), CreatedAt: day}
	o := one(t, Inputs{Predictions: []model.Prediction{pr}})
	if o.Result != model.ResultHit || o.Context != "workout" {
		t.Fatalf("%+v", o)
	}
}

func TestHowACardEnded(t *testing.T) {
	cases := []struct {
		name string
		in   model.Intent
		want string
	}{
		{"승인", model.Intent{Status: model.Executed, DecidedBy: "user"}, model.ResultApproved},
		{"승인했지만 실행 실패", model.Intent{Status: model.Failed, DecidedBy: "user"}, model.ResultApproved},
		{"거절", model.Intent{Status: model.Rejected, DecidedBy: "user"}, model.ResultRejected},
		{"되돌림", model.Intent{Status: model.Undone}, model.ResultUndone},
		{"대화가 먼저 물음", model.Intent{Status: model.Superseded, DecidedBy: model.ByConversation}, model.ResultCovered},
		{"답이 없어 거둠", model.Intent{Status: model.Superseded, Error: "3일 동안 답이 없어 거둠"}, model.ResultIgnored},
		{"일정이 바뀌어 대체됨", model.Intent{Status: model.Superseded, Error: "일정이 바뀜"}, model.ResultSuperseded},
		{"일주일 넘게 답 없음", model.Intent{Status: model.Pending}, model.ResultIgnored},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.ID, c.in.Decision, c.in.CreatedAt, c.in.Card = "i", model.Ask, day, true
			o := one(t, Inputs{Intents: []model.Intent{c.in}})
			if o.Result != c.want || o.Source != "card" {
				t.Fatalf("%+v", o)
			}
		})
	}
	// Answered tomorrow, it is not ignored yet.
	o := one(t, Inputs{Intents: []model.Intent{{ID: "i", Decision: model.Ask, Status: model.Pending, CreatedAt: day}}, Now: day.AddDate(0, 0, 1)})
	if o.Result != model.ResultOpen {
		t.Fatalf("%+v", o)
	}
}

// Ignored says nothing about the judgement: it is not a refusal.
func TestOnlyAnsweredOnesAreScored(t *testing.T) {
	if _, ok := (model.Outcome{Result: model.ResultIgnored}).Scored(); ok {
		t.Fatal("무시는 거절이 아닙니다")
	}
	rows := []model.Outcome{
		{Source: "card", Skill: "schedule", Context: "calendar.create", P: 0.8, Result: model.ResultRejected},
		{Source: "card", Skill: "schedule", Context: "calendar.create", P: 0.8, Result: model.ResultIgnored},
		{Source: "prediction", Context: "purchase", P: 0.5, Result: model.ResultHit},
	}
	cases := Cases(rows)
	if len(cases) != 2 || cases[0].Group != "schedule/calendar.create" || cases[0].Hit || cases[1].Group != "purchase" || !cases[1].Hit {
		t.Fatalf("%+v", cases)
	}
}

// Approved with the answer changed went through, and was still wrong.
func TestACorrectedAnswerCountsAgainst(t *testing.T) {
	in := model.Intent{ID: "i", Decision: model.Ask, Status: model.Executed, DecidedBy: "user", CreatedAt: day, Result: json.RawMessage(`{"corrected":true}`)}
	o := one(t, Inputs{Intents: []model.Intent{in}})
	if hit, ok := o.Scored(); o.Result != model.ResultCorrected || hit || !ok {
		t.Fatalf("%+v", o)
	}
	in.Result = json.RawMessage(`{"corrected":false}`)
	if o := one(t, Inputs{Intents: []model.Intent{in}}); o.Result != model.ResultApproved {
		t.Fatalf("%+v", o)
	}
}

func TestDoneAndToldStandsAfterADay(t *testing.T) {
	in := model.Intent{ID: "i", Decision: model.Notify, Status: model.Executed, CreatedAt: day}
	if o := one(t, Inputs{Intents: []model.Intent{in}, Now: day.Add(time.Hour)}); o.Result != model.ResultOpen {
		t.Fatalf("%+v", o)
	}
	if o := one(t, Inputs{Intents: []model.Intent{in}}); o.Result != model.ResultKept || o.Source != "told" {
		t.Fatalf("%+v", o)
	}
	in.Status = model.Undone
	if o := one(t, Inputs{Intents: []model.Intent{in}}); o.Result != model.ResultUndone {
		t.Fatalf("%+v", o)
	}
}

func TestANoticeJudgesNothing(t *testing.T) {
	for _, decision := range []string{model.Execute, model.Notify} {
		o := one(t, Inputs{Intents: []model.Intent{{ID: "i", Decision: decision, Status: model.Executed, Kind: "info", CreatedAt: day}}})
		if _, ok := o.Scored(); ok || o.Source != "notice" {
			t.Fatalf("%+v", o)
		}
	}
}

func calendarIntent(status, decision string) model.Intent {
	payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "은주와 합주", Date: "2026-11-02", Start: "15:00"}})
	return model.Intent{ID: "i", Kind: "calendar.create", Decision: decision, Status: status, Payload: payload, CreatedAt: day}
}

// Refused and then put on the calendar by hand: the operator wanted the entry.
func TestWhatTheOperatorDidByHand(t *testing.T) {
	entry := model.Occurrence{ID: "calendar:1", Kind: "calendar", Title: "기타 합주 (은주)", At: time.Date(2026, 11, 2, 15, 0, 0, 0, seoul)}
	o := one(t, Inputs{Intents: []model.Intent{calendarIntent(model.Rejected, model.Ask)}, Occurrences: []model.Occurrence{entry}})
	if !o.Manual || o.Result != model.ResultRejected {
		t.Fatalf("%+v", o)
	}
	other := entry
	other.Title = "치과"
	if o := one(t, Inputs{Intents: []model.Intent{calendarIntent(model.Rejected, model.Ask)}, Occurrences: []model.Occurrence{other}}); o.Manual {
		t.Fatalf("같은 시각의 다른 일입니다: %+v", o)
	}
	// Silence held to account: the gate dropped it, and it happened anyway.
	o = one(t, Inputs{Intents: []model.Intent{calendarIntent(model.Dropped, model.Drop)}, Occurrences: []model.Occurrence{entry}})
	if !o.Manual || o.Source != "quiet" {
		t.Fatalf("%+v", o)
	}
	if c := Count([]model.Outcome{o}); c.Manual != 1 || c.ManualQuiet != 1 || c.Quiet != 1 {
		t.Fatalf("%+v", c)
	}
}

// A card replaced by a later one, or an entry another card of the plan put
// there, is the assistant's doing, not the operator's.
func TestReplacedCardsAreNotDoneByHand(t *testing.T) {
	entry := model.Occurrence{ID: "calendar:1", Kind: "calendar", Title: "기타 합주 (은주)", At: time.Date(2026, 11, 2, 15, 0, 0, 0, seoul)}
	replaced := calendarIntent(model.Superseded, model.Ask)
	if o := one(t, Inputs{Intents: []model.Intent{replaced}, Occurrences: []model.Occurrence{entry}}); o.Manual {
		t.Fatalf("%+v", o)
	}
	refused := calendarIntent(model.Rejected, model.Ask)
	refused.EntityID = "plan_1"
	approved := calendarIntent(model.Executed, model.Ask)
	approved.ID, approved.EntityID, approved.DecidedBy = "j", "plan_1", "user"
	rows := Build(Inputs{Intents: []model.Intent{refused, approved}, Occurrences: []model.Occurrence{entry}, Now: day.AddDate(0, 1, 0), Loc: seoul})
	for _, o := range rows {
		if o.Manual {
			t.Fatalf("같은 일정의 다른 카드로 들어갔습니다: %+v", o)
		}
	}
}

func TestAPurchaseDoneByHand(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"item": "참나무 장작", "class": "연료/캠핑"})
	in := model.Intent{ID: "i", Kind: "cart.add", Decision: model.Ask, Status: model.Rejected, Payload: payload, CreatedAt: day}
	thing := func(name string) string {
		if name == "캠핑 땔감 10kg" {
			return "연료/캠핑"
		}
		return ""
	}
	bought := model.Occurrence{ID: "s#1", Kind: "purchase.item", Title: "캠핑 땔감 10kg", At: day.AddDate(0, 0, 3)}
	if o := one(t, Inputs{Intents: []model.Intent{in}, Occurrences: []model.Occurrence{bought}, Thing: thing}); !o.Manual {
		t.Fatalf("같은 분류의 구매입니다: %+v", o)
	}
	late := bought
	late.At = day.AddDate(0, 0, 30)
	if o := one(t, Inputs{Intents: []model.Intent{in}, Occurrences: []model.Occurrence{late}, Thing: thing}); o.Manual {
		t.Fatalf("한 달 뒤의 구매는 다른 구매입니다: %+v", o)
	}
}

func TestBuildIsInOrderOfJudgement(t *testing.T) {
	rows := Build(Inputs{Now: day.AddDate(0, 1, 0), Intents: []model.Intent{{ID: "b", Decision: model.Drop, Status: model.Dropped, CreatedAt: day.Add(time.Hour)}},
		Predictions: []model.Prediction{{ID: "a", Target: "x", Due: day, CreatedAt: day}}})
	if len(rows) != 2 || rows[0].ID != "prediction:a" {
		t.Fatalf("%+v", rows)
	}
}

func fact(id, relation, object string, created time.Time) model.Fact {
	return model.Fact{ID: id, Subject: model.Party{Self: true}, Relation: relation, Object: model.Party{Name: object}, Confidence: 0.7, CreatedAt: created}
}

// A move ends the old address at the new one's start.
func TestASingleFactHoldsUntilTheNext(t *testing.T) {
	rows := Intervals([]model.Fact{
		fact("f2", "LIVES_IN", "부산", day.AddDate(0, 1, 0)),
		fact("f1", "LIVES_IN", "서울", day),
		fact("f3", "LIVES_IN", "서울", day.AddDate(0, 2, 0)),
	}, seoul)
	if len(rows) != 3 || rows[0].ID != "f1" || rows[0].To == nil || !rows[0].To.Equal(rows[1].From) || rows[2].To != nil {
		t.Fatalf("%+v", rows)
	}
	if !rows[0].Single || rows[0].Subject != "self" {
		t.Fatalf("%+v", rows[0])
	}
}

func TestTheSameSaidAgainIsOneFact(t *testing.T) {
	a, b := fact("f1", "LIVES_IN", "서울", day), fact("f2", "LIVES_IN", "서울", day.AddDate(0, 0, 3))
	b.Confidence = 0.9
	rows := Intervals([]model.Fact{a, b}, seoul)
	if len(rows) != 1 || rows[0].ID != "f1" || rows[0].P != 0.9 || !rows[0].From.Equal(day) {
		t.Fatalf("%+v", rows)
	}
}

func TestSaidAtOneMomentTheLaterStands(t *testing.T) {
	a, b := fact("f1", "LIVES_IN", "서울", day), fact("f2", "LIVES_IN", "부산", day)
	rows := Intervals([]model.Fact{a, b}, seoul)
	if len(rows) != 1 || rows[0].Object != "부산" {
		t.Fatalf("%+v", rows)
	}
}

func TestManyValuedFactsStandSideBySide(t *testing.T) {
	rows := Intervals([]model.Fact{fact("f1", "LIKES", "커피", day), fact("f2", "LIKES", "차", day.AddDate(0, 0, 1))}, seoul)
	if len(rows) != 2 || rows[0].To != nil || rows[0].Single {
		t.Fatalf("%+v", rows)
	}
}

func TestWhatMemoryEndedIsClosed(t *testing.T) {
	a := fact("f1", "LIKES", "매운 음식", day)
	a.Memory = &model.Stored{Edge: "e1"}
	b := fact("f2", "DISLIKES", "매운 음식", day.AddDate(0, 0, 5))
	b.Ends = []model.Ended{{ID: "e1", Fact: "매운 음식을 좋아한다"}}
	rows := Intervals([]model.Fact{a, b}, seoul)
	if len(rows) != 2 || rows[0].To == nil || !rows[0].To.Equal(b.CreatedAt) {
		t.Fatalf("%+v", rows)
	}
}

func TestADroppedFactIsNotAFact(t *testing.T) {
	a := fact("f1", "LIVES_IN", "서울", day)
	a.Status = model.FactDropped
	if rows := Intervals([]model.Fact{a}, seoul); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

func TestValidAtSaysWhenItBegan(t *testing.T) {
	a := fact("f1", "WORKS_AT", "회사", day)
	a.ValidAt = "2026-01-15"
	rows := Intervals([]model.Fact{a}, seoul)
	if rows[0].From.Format("2006-01-02") != "2026-01-15" {
		t.Fatalf("%+v", rows)
	}
}

// A row says how it goes into memory's graph: on the edge and the nodes memory
// put it on, and ended where it ended. A fact memory turned down stays a fact
// and stays out of the graph.
func TestARowSaysHowItGoesIntoTheGraph(t *testing.T) {
	a := fact("f1", "LIVES_IN", "망원동", day)
	a.Status = model.FactStored
	a.Memory = &model.Stored{Edge: "edge-1", EdgeFact: "나는 망원동에 산다",
		Source: model.MemoryNode{ID: "n-me", Name: "나"}, Target: model.MemoryNode{ID: "n-1", Name: "망원동"}}
	a.Subject = model.Party{Self: true}
	b := fact("f2", "LIVES_IN", "합정동", day.AddDate(0, 1, 0))
	b.Subject = model.Party{Self: true}
	held := fact("f3", "LIKES", "커피", day)
	held.Status = model.FactHeld
	rows := Intervals([]model.Fact{a, b, held}, seoul)
	if len(rows) != 3 {
		t.Fatalf("%+v", rows)
	}
	var got struct {
		Origin, Edge, Fact, InvalidAt string
		Source                        struct {
			ID   string
			Self bool
		}
		Target struct{ ID, Name string }
	}
	if err := json.Unmarshal(rows[0].Body, &got); err != nil {
		t.Fatalf("%s %v", rows[0].Body, err)
	}
	if got.Origin != model.FactChecked || got.Edge != "edge-1" || got.Fact != "나는 망원동에 산다" || !got.Source.Self ||
		got.Target.ID != "n-1" || got.InvalidAt == "" {
		t.Fatalf("%s", rows[0].Body)
	}
	for _, r := range rows {
		if r.ID == "f3" && len(r.Body) != 0 {
			t.Fatalf("기억이 받지 않은 사실은 그래프에 넣지 않습니다: %s", r.Body)
		}
	}
}

// What memory ended is ended here too: by a later fact, or by its own word.
func TestWhatMemoryEndedIsEnded(t *testing.T) {
	a := fact("f1", "WANTS", "자전거", day)
	a.Memory = &model.Stored{Edge: "edge-1"}
	b := fact("f2", "BOUGHT", "자전거", day.AddDate(0, 1, 0))
	b.Memory = &model.Stored{Edge: "edge-2", Ended: []model.Ended{{ID: "edge-1", InvalidAt: "2026-11-20"}}}
	c := fact("f3", "GOAL", "완주", day)
	c.Memory = &model.Stored{Edge: "edge-3", InvalidAt: "2026-12-31T00:00:00+09:00"}
	byID := map[string]model.FactRow{}
	for _, r := range Intervals([]model.Fact{a, b, c}, seoul) {
		byID[r.ID] = r
	}
	if r := byID["f1"]; r.To == nil || r.To.In(seoul).Format("2006-01-02") != "2026-11-20" {
		t.Fatalf("%+v", r)
	}
	if r := byID["f3"]; r.To == nil || r.To.In(seoul).Format("2006-01-02") != "2026-12-31" {
		t.Fatalf("%+v", r)
	}
	if r := byID["f2"]; r.To != nil {
		t.Fatalf("%+v", r)
	}
}

// A fact whose edge left the graph stays a fact and does not go back in.
func TestAFactGoneFromTheGraphStaysOut(t *testing.T) {
	a := fact("f1", "LIKES", "커피", day)
	a.Memory = &model.Stored{Edge: "edge-1", Gone: true}
	rows := Intervals([]model.Fact{a}, seoul)
	if len(rows) != 1 || len(rows[0].Body) != 0 {
		t.Fatalf("%+v", rows)
	}
}
