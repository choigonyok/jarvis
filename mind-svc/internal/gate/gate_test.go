package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

type fixture struct {
	g     *Gate
	st    *store.Memory
	cal   *act.MemoryCalendar
	note  *act.MemoryNotifier
	log   *obs.MemoryLog
	clock time.Time
}

func newFixture(p Policy) *fixture {
	f := &fixture{st: store.NewMemory(), cal: act.NewMemoryCalendar(), note: &act.MemoryNotifier{}, clock: time.Date(2026, 10, 12, 10, 0, 0, 0, seoul)}
	f.log = obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go f.log.Run(context.Background(), func(context.Context, obs.Observation) error { return nil })
	f.g = New(f.st, act.NewRegistry(act.CalendarActuator{Cal: f.cal}), f.note, f.log, p, seoul,
		func() time.Time { f.clock = f.clock.Add(time.Second); return f.clock }, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

func create(entity string) model.Intent {
	payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "저녁 " + entity, Date: "2026-10-17"}})
	return model.Intent{Skill: "schedule", Kind: "calendar.create", EntityID: entity, Payload: payload, Title: "캘린더에 추가", Confidence: 0.9, Reversible: true}
}

// The day's push budget holds other notices for the evening, never the
// morning brief: loops told at 07:00 must not send the 08:00 brief to the
// digest (2026-10-25: nine loops with one person did).
func TestBriefIsNotHeldByTheDaysBudget(t *testing.T) {
	ctx := context.Background()
	p := DefaultPolicy()
	p.DailyInterrupts = 2
	f := newFixture(p)
	for i := range 3 {
		in, err := f.g.Submit(ctx, model.Intent{Skill: "people", Kind: "info", Title: "고리 " + string(rune('a'+i)), Confidence: 1, Urgent: true})
		if err != nil {
			t.Fatal(err)
		}
		if want := "now"; i == 2 {
			want = "digest"
		} else if in.Tier != want {
			t.Fatalf("%d번째 알림은 %s 여야 합니다: %+v", i+1, want, in)
		}
	}
	brief, err := f.g.Submit(ctx, model.Intent{Skill: BriefSkill, Kind: "info", Title: "오늘 일정 1개 · 챙길 일 2개", Confidence: 1, Urgent: true})
	if err != nil || brief.Tier != "now" || brief.Decision != model.Notify {
		t.Fatalf("브리핑은 한도에 밀리지 않아야 합니다: %+v %v", brief, err)
	}
}

// answered is a kind's record on the ledger: so many cards, approved or not.
func answered(n int, result string) []model.Outcome {
	rows := make([]model.Outcome, n)
	for i := range rows {
		rows[i] = model.Outcome{ID: fmt.Sprint("intent:", i), Source: "card", Kind: "calendar.create", Skill: "schedule", Context: "calendar.create",
			P: 0.9, Result: result}
	}
	return rows
}

// Asked first, however sure, until the ledger has enough answers; after enough
// approvals, done and told; one undo and it is asked again, before the ledger
// has heard of it.
func TestTrustIsLearned(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	for i := range 3 {
		in, err := f.g.Submit(ctx, create("plan_"+string(rune('a'+i))))
		if err != nil || in.Decision != model.Ask || in.Status != model.Pending {
			t.Fatalf("%d번째는 물어야 합니다: %+v %v", i+1, in, err)
		}
		if _, err := f.g.Approve(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
	}
	if in, _ := f.g.Submit(ctx, create("plan_d")); in.Decision != model.Ask {
		t.Fatalf("답이 %d건 쌓이기 전에는 물어야 합니다: %+v", engine.MinCases, in)
	}
	f.g.SetBook(engine.NewBook(answered(12, model.ResultApproved)))
	in, _ := f.g.Submit(ctx, create("plan_e"))
	if in.Decision != model.Notify || in.Status != model.Executed {
		t.Fatalf("장부가 늘 맞았다고 하면 바로 하고 알려야 합니다: %+v", in)
	}
	if len(f.cal.All()) != 4 {
		t.Fatalf("캘린더에 4개: %v", f.cal.All())
	}
	if _, err := f.g.Undo(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.cal.All()) != 3 {
		t.Fatalf("되돌리면 빠져야 합니다: %v", f.cal.All())
	}
	again, _ := f.g.Submit(ctx, create("plan_f"))
	if again.Decision != model.Ask {
		t.Fatalf("되돌린 뒤에는 다시 물어야 합니다: %+v", again)
	}
}

// A kind the operator keeps turning down goes silent.
func TestWhatTheLedgerSaysAgainst(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	f.g.SetBook(engine.NewBook(answered(12, model.ResultRejected)))
	got, _ := f.g.Submit(ctx, create("plan_z"))
	if engine.Explored(got.ID) {
		t.Skip("이 id 는 탐색으로 묻습니다")
	}
	if got.Decision != model.Drop {
		t.Fatalf("계속 거절한 종류는 묻지 않습니다: %+v", got)
	}
}

// Deleting is asked however often it was approved; an unsure skill is
// asked quietly.
func TestFixedAndUnsure(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	for i := range 4 {
		saved, _ := f.cal.Put(ctx, model.CalendarEvent{Title: "저녁", Date: "2026-10-17"})
		payload, _ := json.Marshal(model.CalendarAction{EventID: saved.ID, Event: saved})
		in, _ := f.g.Submit(ctx, model.Intent{Skill: "schedule", Kind: "calendar.delete", EntityID: "p" + string(rune('a'+i)), Payload: payload, Confidence: 0.95, Reversible: true})
		if in.Decision != model.Ask {
			t.Fatalf("%d번째 삭제도 물어야 합니다: %+v", i+1, in)
		}
		if _, err := f.g.Approve(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
	}
	unsure := create("q")
	unsure.Confidence = 0.55
	got, _ := f.g.Submit(ctx, unsure)
	if got.Decision != model.Ask || got.Tier != "digest" {
		t.Fatalf("확신이 낮으면 조용히 물어야 합니다: %+v", got)
	}
}

// Past the day's budget, pushes become lines in the evening summary.
func TestInterruptionBudget(t *testing.T) {
	ctx := context.Background()
	p := DefaultPolicy()
	p.DailyInterrupts = 2
	f := newFixture(p)
	var tiers []string
	for range 3 {
		in, _ := f.g.Submit(ctx, model.Intent{Skill: "loops", Kind: "info", Title: "알림", Urgent: true, Confidence: 1})
		tiers = append(tiers, in.Tier)
	}
	if tiers[0] != "now" || tiers[1] != "now" || tiers[2] != "digest" {
		t.Fatalf("한도 뒤에는 요약으로: %v", tiers)
	}
	if n := len(f.note.All()); n != 3 {
		t.Fatalf("알림 3개(2개는 바로, 1개는 요약): %d", n)
	}
}

// A newer intent about the same entity replaces an unanswered one.
func TestNewerReplacesUnanswered(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	first, _ := f.g.Submit(ctx, create("plan_x"))
	second, _ := f.g.Submit(ctx, create("plan_x"))
	old, _ := f.st.Intent(ctx, first.ID)
	if old.Status != model.Superseded || second.Status != model.Pending {
		t.Fatalf("앞의 질문은 대체돼야 합니다: %s / %s", old.Status, second.Status)
	}
	if _, err := f.g.Approve(ctx, first.ID); err == nil {
		t.Fatal("대체된 제안은 승인할 수 없어야 합니다")
	}
}

// A malformed action never reaches anyone.
func TestPreviewRejectsBadPayload(t *testing.T) {
	f := newFixture(DefaultPolicy())
	in, _ := f.g.Submit(context.Background(), model.Intent{Skill: "schedule", Kind: "calendar.create", Payload: json.RawMessage(`{"event":{"title":"x","date":"내일"}}`), Confidence: 1, Reversible: true})
	if in.Status != model.Failed || len(f.note.All()) != 0 {
		t.Fatalf("미리보기에서 걸러져야 합니다: %+v", in)
	}
}

// A question that rings goes out as one mind.card, and an answer given here
// closes it; a newer intent closes the one it replaces.
func TestCardsGoOutAndClose(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	first, _ := f.g.Submit(ctx, create("plan_x"))
	second, _ := f.g.Submit(ctx, create("plan_x"))
	if !first.Card || !second.Card {
		t.Fatalf("카드로 나가야 합니다: %+v %+v", first, second)
	}
	if _, err := f.g.RejectWhy(ctx, second.ID, "이미 넣었어"); err != nil {
		t.Fatal(err)
	}
	f.log.Drain()
	all, _ := f.log.Read(ctx, 0, 100)
	var got []string
	for _, o := range all {
		got = append(got, o.Type+":"+o.Subject)
	}
	want := []string{CardRaised + ":" + first.ID, CardClosed + ":" + first.ID, CardRaised + ":" + second.ID, CardClosed + ":" + second.ID}
	if len(got) != len(want) {
		t.Fatalf("카드 이벤트: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("카드 이벤트: %v", got)
		}
	}
	rej, _ := f.st.Intent(ctx, second.ID)
	if rej.Error != "이미 넣었어" {
		t.Fatalf("거절 이유가 남아야 합니다: %+v", rej)
	}
	stat, _ := f.st.Policy(ctx, "schedule", "calendar.create")
	if stat.Shown != 2 || stat.Rejected != 1 || stat.Last != model.OutcomeRejected {
		t.Fatalf("승인율 통계: %+v", stat)
	}
	if n, err := f.g.Rebuild(ctx); err != nil || n != 2 {
		t.Fatalf("다시 세기: %d %v", n, err)
	}
	again, _ := f.st.Policy(ctx, "schedule", "calendar.create")
	if again.Shown != 2 || again.Rejected != 1 {
		t.Fatalf("다시 센 통계가 같아야 합니다: %+v", again)
	}
}

// An entry already on the calendar is linked, not asked about twice.
func TestAlreadyOnCalendarIsLinked(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	there, _ := f.cal.Put(ctx, model.CalendarEvent{Title: "저녁 plan_y 약속", Date: "2026-10-17"})
	in, _ := f.g.Submit(ctx, create("plan_y"))
	if in.Status != model.Executed || in.Card || len(f.cal.All()) != 1 || !strings.Contains(string(in.Result), there.ID) {
		t.Fatalf("이미 있는 일정은 연결만: %+v %v", in, f.cal.All())
	}
}

// spy runs the calendar action and records what the store said about the
// intent while it ran.
type spy struct {
	act.CalendarActuator
	st     *store.Memory
	id     *string
	during string
	runs   int
}

func (s *spy) Execute(ctx context.Context, kind string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	s.runs++
	if in, err := s.st.Intent(ctx, *s.id); err == nil {
		s.during = in.Status
	}
	return s.CalendarActuator.Execute(ctx, kind, payload)
}

// B-6: an approval claims the intent before running it, so a second approval
// - after the save that follows the run failed, or a double tap - cannot run
// it again.
func TestApprovalClaimsBeforeRunning(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	var id string
	sp := &spy{CalendarActuator: act.CalendarActuator{Cal: f.cal}, st: f.st, id: &id}
	f.g = New(f.st, act.NewRegistry(sp), f.note, f.log, DefaultPolicy(), seoul,
		func() time.Time { f.clock = f.clock.Add(time.Second); return f.clock }, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	in, err := f.g.Submit(ctx, create("plan_claim"))
	if err != nil || !in.Card {
		t.Fatalf("카드로 나가야 합니다: %+v %v", in, err)
	}
	id = in.ID
	if _, err := f.g.Approve(ctx, id); err != nil {
		t.Fatal(err)
	}
	if sp.during != model.Executing {
		t.Fatalf("실행하는 동안 저장된 상태는 executing 이어야 합니다: %q", sp.during)
	}
	if _, err := f.g.Approve(ctx, id); err == nil {
		t.Fatal("두 번째 승인은 거절돼야 합니다")
	}
	if sp.runs != 1 {
		t.Fatalf("한 번만 실행돼야 합니다: %d", sp.runs)
	}
}

// A trip moved for the weather is asked every time: a context kept fixed
// is not promoted, whatever its kind and however often it was approved.
func TestFixedContextIsAlwaysAsked(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	for i := range 4 {
		ev, _ := f.cal.Put(ctx, model.CalendarEvent{Title: "캠핑", Date: "2026-10-24"})
		payload, _ := json.Marshal(model.CalendarAction{EventID: ev.ID, Event: model.CalendarEvent{Title: "캠핑", Date: "2026-10-31"}})
		in, err := f.g.Submit(ctx, model.Intent{Skill: "rain", Kind: "calendar.update", Context: "rain", EntityID: "plan_" + string(rune('a'+i)),
			Payload: payload, Title: "옮길까요?", Confidence: 0.8, Value: 1, Reversible: true})
		if err != nil || in.Decision != model.Ask {
			t.Fatalf("%d번째도 물어야 합니다: %+v %v", i+1, in, err)
		}
		if _, err := f.g.Approve(ctx, in.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// 2026-10-26: the card waited while the assistant's own card for the same
// entry was approved. Approving it then links to that entry - it does not
// put the entry on the calendar a second time.
func TestApprovalLinksWhatWentOnMeanwhile(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	in, err := f.g.Submit(ctx, create("plan_z"))
	if err != nil || in.Status != model.Pending || !in.Card {
		t.Fatalf("카드로 물어야 합니다: %+v %v", in, err)
	}
	there, _ := f.cal.Put(ctx, model.CalendarEvent{Title: "저녁 plan_z", Date: "2026-10-17", Start: "20:00", End: "20:30"})
	got, err := f.g.Approve(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.cal.All()) != 1 {
		t.Fatalf("같은 일정을 또 넣으면 안 됩니다: %v", f.cal.All())
	}
	var res struct {
		CalendarID string `json:"calendarId"`
		Linked     bool   `json:"linked"`
	}
	_ = json.Unmarshal(got.Result, &res)
	if got.Status != model.Executed || res.CalendarID != there.ID || !res.Linked || len(got.Undo) != 0 {
		t.Fatalf("이미 있는 일정에 이어야 합니다: %+v", got)
	}
	if got.Preview != "2026-10-17 20:00 저녁 plan_z" || !strings.HasPrefix(got.Why, "이미 있어서 연결만") {
		t.Fatalf("무엇에 이었는지: %q / %q", got.Preview, got.Why)
	}
	if _, err := f.g.Undo(ctx, got.ID); err == nil {
		t.Fatal("이어 붙이기만 한 것은 되돌릴 게 없습니다")
	}
	stat, _ := f.st.Policy(ctx, "schedule", "calendar.create")
	if stat.Approved != 1 {
		t.Fatalf("승인은 승인으로 셉니다: %+v", stat)
	}
}

// A card the agent did not show - the conversation asks the same already -
// is put aside: not answerable, not one of the day's cards, nothing learned
// from it. Its card goes out carrying the change, which is how the agent
// told it from the conversation's.
func TestCoveredStandsAside(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	in, _ := f.g.Submit(ctx, create("plan_c"))
	if f.g.cardsToday(ctx) != 1 {
		t.Fatalf("울린 카드 하나: %d", f.g.cardsToday(ctx))
	}
	got, err := f.g.Covered(ctx, in.ID, "p-1793-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Covered() || got.Error != "대화에서 같은 카드를 먼저 올렸어요 (p-1793-1)" {
		t.Fatalf("대화의 카드에 맡긴 것: %+v", got)
	}
	if n := f.g.cardsToday(ctx); n != 0 {
		t.Fatalf("보이지 않은 카드는 오늘의 카드가 아닙니다: %d", n)
	}
	if _, err := f.g.Approve(ctx, in.ID); err == nil {
		t.Fatal("맡긴 제안은 승인할 수 없어야 합니다")
	}
	if _, err := f.g.Covered(ctx, in.ID, "p-1793-1"); err == nil {
		t.Fatal("두 번 맡길 수 없습니다")
	}
	stat, _ := f.st.Policy(ctx, "schedule", "calendar.create")
	if stat.Approved+stat.Rejected+stat.Undone != 0 || stat.Last != "" {
		t.Fatalf("배울 답이 없습니다: %+v", stat)
	}

	f.log.Drain()
	all, _ := f.log.Read(ctx, 0, 100)
	var raised Card
	var closed bool
	for _, o := range all {
		switch o.Type {
		case CardRaised:
			_ = json.Unmarshal(o.Data, &raised)
		case CardClosed:
			closed = o.Subject == in.ID
		}
	}
	if raised.Calendar == nil || raised.Calendar.Event.Title != "저녁 plan_c" || raised.Calendar.Event.Date != "2026-10-17" {
		t.Fatalf("카드에 바꿀 일정이 실려야 합니다: %+v", raised)
	}
	if !closed {
		t.Fatalf("닫혔다고 알려야 합니다: %+v", all)
	}
}

func answers(kind, skill string, n, approved int, p float64) []model.Outcome {
	var out []model.Outcome
	for i := 0; i < n; i++ {
		result := model.ResultRejected
		if i < approved {
			result = model.ResultApproved
		}
		out = append(out, model.Outcome{Source: "card", Kind: kind, Skill: skill, Context: kind, P: p, Result: result})
	}
	return out
}

func cardOf(t *testing.T, f *fixture) Card {
	t.Helper()
	f.log.Drain()
	all, err := f.log.Read(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var c Card
	found := false
	for _, o := range all {
		if o.Type == CardRaised {
			c, found = Card{}, true
			_ = json.Unmarshal(o.Data, &c)
		}
	}
	if !found {
		t.Fatal("카드가 없습니다")
	}
	return c
}

// A kind that says 90% and is rarely approved does not ring on its word.
func TestTheRecordDecidesWhatRings(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	in := create("p1")
	in.Skill, in.Confidence = "schedule", 0.9
	f.g.SetBook(engine.NewBook(answers("calendar.create", "schedule", 24, 5, 0.9)))
	got, err := f.g.Submit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != model.Ask || got.Tier != "digest" || !got.Card {
		t.Fatalf("카드는 남고 울리지 않아야 합니다: %+v", got)
	}
	if c := cardOf(t, f); !c.Quiet {
		t.Fatalf("%+v", c)
	}
}

// A kind whose record bears it out rings as it did, and the doubt is not
// taken off twice by the approval rate on top.
func TestAGoodRecordStillRings(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	f.g.SetBook(engine.NewBook(answers("calendar.create", "schedule", 20, 18, 0.9)))
	got, err := f.g.Submit(ctx, create("p1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != "now" {
		t.Fatalf("%+v", got)
	}
}

// Turned down again and again, a kind goes quiet altogether.
func TestAKindTurnedDownGoesSilent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	f.g.SetBook(engine.NewBook(answers("calendar.create", "schedule", 30, 0, 0.9)))
	silent, asked := 0, 0
	for i := range 60 {
		in := create("p" + string(rune('A'+i)))
		got, err := f.g.Submit(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		switch got.Decision {
		case model.Drop:
			silent++
			if got.Status != model.Dropped || got.Card {
				t.Fatalf("%+v", got)
			}
		case model.Ask:
			asked++
		}
	}
	if silent == 0 {
		t.Fatal("침묵해야 합니다")
	}
	if asked == 0 {
		t.Fatal("돌아올 길이 있어야 합니다: 가끔은 묻습니다")
	}
	if asked > silent/2 {
		t.Fatalf("대부분 침묵해야 합니다: 물음 %d, 침묵 %d", asked, silent)
	}
}

// What is always asked is asked whatever its record.
func TestFixedIsNeverSilenced(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	f.g.SetBook(engine.NewBook(answers("calendar.delete", "schedule", 30, 0, 0.9)))
	in := create("p1")
	in.Kind = "calendar.delete"
	payload, _ := json.Marshal(model.CalendarAction{EventID: "e1", Event: model.CalendarEvent{Title: "저녁", Date: "2026-10-17"}})
	in.Payload = payload
	_, _ = f.cal.Put(ctx, model.CalendarEvent{ID: "e1", Title: "저녁", Date: "2026-10-17"})
	got, err := f.g.Submit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != model.Ask {
		t.Fatalf("%+v", got)
	}
}

// With no ledger the gate is what it was.
func TestWithoutABookNothingChanges(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	got, err := f.g.Submit(ctx, create("p1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != model.Ask || got.Tier != "now" {
		t.Fatalf("%+v", got)
	}
}

// A slot comes graded and the gate carries it out; what cannot be undone is
// not done on a grade.
func TestAGradeIsCarriedOut(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	in := create("p1")
	in.Grade = string(engine.Silent)
	if got, _ := f.g.Submit(ctx, in); got.Decision != model.Drop {
		t.Fatalf("%+v", got)
	}
	in = create("p2")
	in.Grade = string(engine.Act)
	if got, _ := f.g.Submit(ctx, in); got.Decision != model.Notify {
		t.Fatalf("%+v", got)
	}
	in = create("p3")
	in.Grade, in.Reversible = string(engine.Act), false
	if got, _ := f.g.Submit(ctx, in); got.Decision != model.Ask {
		t.Fatalf("되돌릴 수 없으면 묻습니다: %+v", got)
	}
}

// The way out is narrow: an action with no row is not done, whoever raised it.
func TestAnActionWithNoRowIsNotDone(t *testing.T) {
	p := DefaultPolicy()
	delete(p.Levels, "calendar.create")
	f := newFixture(p)
	in, err := f.g.Submit(context.Background(), create("e1"))
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != model.Failed || in.Error != "등록되지 않은 행동입니다" {
		t.Fatalf("%+v", in)
	}
}

// What can be carried out has a row.
func TestEveryActionHasARow(t *testing.T) {
	f := newFixture(DefaultPolicy())
	if missing := f.g.Unlisted(); len(missing) != 0 {
		t.Fatalf("%v", missing)
	}
}

// One subject rings once: a second skill's card about the same plan waits
// beside the first, and the first is not taken down.
func TestOneSubjectRingsOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	first, err := f.g.Submit(ctx, create("plan_a"))
	if err != nil || first.Tier != "now" {
		t.Fatalf("%+v %v", first, err)
	}
	second := create("plan_a")
	second.Skill = "protect"
	got, err := f.g.Submit(ctx, second)
	if err != nil || got.Decision != model.Ask || got.Tier != "digest" {
		t.Fatalf("%+v %v", got, err)
	}
	still, err := f.st.Intent(ctx, first.ID)
	if err != nil || still.Status != model.Pending {
		t.Fatalf("%+v %v", still, err)
	}
}
