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

// move is a change to a plan already on the calendar: another question than
// its add.
func move(entity string) model.Intent {
	in := create(entity)
	in.Kind = "calendar.update"
	in.Payload, _ = json.Marshal(model.CalendarAction{EventID: "ev1", Event: model.CalendarEvent{Title: "저녁", Date: "2026-10-18"}})
	return in
}

// The day's push budget holds other notices for the evening, never the
// morning brief: loops told at 07:00 must not send the 08:00 brief to the
// digest.
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
	second, _ := f.g.Submit(ctx, move("plan_x"))
	old, _ := f.st.Intent(ctx, first.ID)
	if old.Status != model.Superseded || second.Status != model.Pending || second.ID == first.ID {
		t.Fatalf("앞의 질문은 대체돼야 합니다: %s / %s", old.Status, second.Status)
	}
	if _, err := f.g.Approve(ctx, first.ID); err == nil {
		t.Fatal("대체된 제안은 승인할 수 없어야 합니다")
	}
}

// A plan that moves again before anyone answered is one question: the card
// stays where it is and says what the plan says now. It rings once, counts
// once, and an answer does what it says now (45 entries took 81 cards).
func TestAnOpenCardIsToldWhatChanged(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	first := create("plan_x")
	first.Evidence = []model.Evidence{{Ref: "m1", Quote: "토요일에 보자"}}
	asked, err := f.g.Submit(ctx, first)
	if err != nil || !asked.Card {
		t.Fatalf("%+v %v", asked, err)
	}
	moved := create("plan_x")
	moved.Title = "캘린더에 추가: 19:00 저녁"
	moved.Payload, _ = json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "저녁 plan_x", Date: "2026-10-17", Start: "19:00"}})
	told, err := f.g.Submit(ctx, moved)
	if err != nil {
		t.Fatal(err)
	}
	if told.ID != asked.ID || told.Status != model.Pending || !told.CreatedAt.Equal(asked.CreatedAt) || told.Tier != asked.Tier {
		t.Fatalf("같은 카드여야 합니다: %+v / %+v", told, asked)
	}
	if len(told.Evidence) != 1 {
		t.Fatalf("새 근거가 없으면 있던 근거를 둡니다: %+v", told.Evidence)
	}

	f.log.Drain()
	all, _ := f.log.Read(ctx, 0, 1000)
	var raised, updated, closed int
	var said Card
	for _, o := range all {
		switch o.Type {
		case model.KindCardRaised:
			raised++
		case model.KindCardUpdated:
			updated++
			_ = json.Unmarshal(o.Data, &said)
		case model.KindCardClosed:
			closed++
		}
	}
	if raised != 1 || updated != 1 || closed != 0 {
		t.Fatalf("올림 %d 고침 %d 닫음 %d", raised, updated, closed)
	}
	if said.IntentID != asked.ID || said.Title != moved.Title {
		t.Fatalf("%+v", said)
	}
	if stat, _ := f.st.Policy(ctx, "schedule", told.ContextKey()); stat.Shown != 1 {
		t.Fatalf("한 번 물은 것은 한 번입니다: %+v", stat)
	}
	done, err := f.g.Approve(ctx, asked.ID)
	if err != nil || done.Status != model.Executed {
		t.Fatalf("%+v %v", done, err)
	}
	if evs, _ := f.cal.Range(ctx, "2026-10-17", "2026-10-17"); len(evs) != 1 || evs[0].Start != "19:00" {
		t.Fatalf("지금 말하는 대로 들어갑니다: %+v", evs)
	}
}

// What comes of the newer intent decides: a move that is done without asking
// leaves no question, and the card goes the way it always went.
func TestACardNotAskedAgainCloses(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	asked, _ := f.g.Submit(ctx, create("plan_x"))
	bad := create("plan_x")
	bad.Payload = []byte(`{}`)
	if _, err := f.g.Submit(ctx, bad); err != nil {
		t.Fatal(err)
	}
	old, _ := f.st.Intent(ctx, asked.ID)
	if old.Status != model.Superseded {
		t.Fatalf("못 묻게 됐으면 앞의 카드를 닫습니다: %s", old.Status)
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
	second, _ := f.g.Submit(ctx, move("plan_x"))
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
	want := []string{model.KindCardRaised + ":" + first.ID, model.KindCardClosed + ":" + first.ID, model.KindCardRaised + ":" + second.ID, model.KindCardClosed + ":" + second.ID}
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
	stat, _ := f.st.Policy(ctx, "schedule", "calendar.update")
	if stat.Shown != 1 || stat.Rejected != 1 || stat.Last != model.OutcomeRejected {
		t.Fatalf("승인율 통계: %+v", stat)
	}
	if n, err := f.g.Rebuild(ctx); err != nil || n != 2 {
		t.Fatalf("다시 세기: %d %v", n, err)
	}
	again, _ := f.st.Policy(ctx, "schedule", "calendar.update")
	if again.Shown != 1 || again.Rejected != 1 {
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

// A card that waited while the assistant's own card for the same entry was
// approved links to that entry when it is approved - it does not put the
// entry on the calendar a second time.
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
	var closed bool
	for _, o := range all {
		if o.Type == model.KindCardClosed {
			closed = o.Subject == in.ID
		}
	}
	if !closed {
		t.Fatalf("닫혔다고 알려야 합니다: %+v", all)
	}
}

// addAt is a card about a plan of room, whatever the plan is called.
func addAt(t *testing.T, f *fixture, entity, room, title, date, start string) model.Intent {
	t.Helper()
	if _, _, err := f.st.Put(context.Background(), model.Entity{ID: entity, Kind: model.KindPlan, Room: room,
		State: model.State{Title: title, Status: model.Confirmed, Date: date, Start: start}}, model.Change{}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: title, Date: date, Start: start}})
	return model.Intent{Skill: ScheduleSkill, Kind: "calendar.create", EntityID: entity, Payload: payload, Title: "캘린더에 추가: " + title, Confidence: 0.9, Reversible: true}
}

// A card the conversation raised is the question: mind's own, open about the
// same entry, stands aside, and none is raised while it speaks. Another entry is
// asked as before.
func TestACardInTheConversationIsTheQuestion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	open, _ := f.g.Submit(ctx, addAt(t, f, "plan_a", "kakao:1", "지수와 합주", "2026-11-17", "15:00"))
	other, _ := f.g.Submit(ctx, addAt(t, f, "plan_b", "kakao:1", "치과", "2026-11-18", "15:00"))
	held, err := f.g.Asked(ctx, "p-1", "calendar.create_event", "기타 합주 (지수)", model.CalendarAction{
		Event: model.CalendarEvent{Title: "기타 합주 (지수)", Date: "2026-11-17", Start: "15:00", End: "17:00"}})
	if err != nil || held.ID != "card:p-1" || held.Status != model.Asked {
		t.Fatalf("%+v %v", held, err)
	}
	if in, _ := f.st.Intent(ctx, open.ID); !in.Covered() || !strings.Contains(in.Error, "p-1") {
		t.Fatalf("같은 일정의 mind 카드는 비켜섭니다: %+v", in)
	}
	if in, _ := f.st.Intent(ctx, other.ID); in.Status != model.Pending {
		t.Fatalf("다른 일정은 그대로: %+v", in)
	}
	if stat, _ := f.st.Policy(ctx, ScheduleSkill, "calendar.create"); stat.Rejected != 0 {
		t.Fatalf("아무도 답하지 않았습니다: %+v", stat)
	}

	// Read a minute later, in another room or the same.
	again, _ := f.g.Submit(ctx, addAt(t, f, "plan_c", "kakao:2", "지수와 합주", "2026-11-17", "15:00"))
	if !again.Covered() || again.Card {
		t.Fatalf("대화의 카드가 있으면 올리지 않습니다: %+v", again)
	}
	if in, _ := f.st.Intent(ctx, again.ID); !in.Covered() {
		t.Fatalf("기록에 남아야 다시 묻지 않습니다: %+v", in)
	}

	// Two days on, it no longer speaks.
	f.clock = f.clock.Add(askedWithin + time.Minute)
	late, _ := f.g.Submit(ctx, addAt(t, f, "plan_d", "kakao:2", "지수와 합주", "2026-11-17", "15:00"))
	if late.Status != model.Pending || !late.Card {
		t.Fatalf("%+v", late)
	}
}

// What was said to the assistant is a plan read from the words of the card, under
// a title of the interpreter's. There the day and the hour say it: the same day
// with nothing against it is one, and two hours apart it is not.
func TestToldToTheAssistantTheHourSaysIt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	if _, err := f.g.Asked(ctx, "p-1", "calendar.create_event", "강남 고기집", model.CalendarAction{
		Event: model.CalendarEvent{Title: "강남 고기집", Date: "2026-11-17", Start: "19:00"}}); err != nil {
		t.Fatal(err)
	}
	same, _ := f.g.Submit(ctx, addAt(t, f, "plan_a", model.OwnerRoom, "회식", "2026-11-17", "19:30"))
	if !same.Covered() {
		t.Fatalf("같은 방, 같은 시각: %+v", same)
	}
	apart, _ := f.g.Submit(ctx, addAt(t, f, "plan_b", model.OwnerRoom, "치과", "2026-11-17", "15:00"))
	if apart.Status != model.Pending {
		t.Fatalf("시각이 떨어진 일은 따로 묻습니다: %+v", apart)
	}
	room, _ := f.g.Submit(ctx, addAt(t, f, "plan_c", "kakao:1", "회식", "2026-11-17", "19:30"))
	if room.Status != model.Pending {
		t.Fatalf("다른 방에서는 시각만으로 잇지 않습니다: %+v", room)
	}
}

// Two changes to one entry are two questions; the same change is one.
func TestAMoveAskedInTheConversation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	entry, _ := f.cal.Put(ctx, model.CalendarEvent{Title: "합주", Date: "2026-11-17", Start: "15:00"})
	if _, err := f.g.Asked(ctx, "p-1", "calendar.update_event", "합주 16시로", model.CalendarAction{
		EventID: entry.ID, Event: model.CalendarEvent{Start: "16:00"}}); err != nil {
		t.Fatal(err)
	}
	move := func(entity, start string) model.Intent {
		payload, _ := json.Marshal(model.CalendarAction{EventID: entry.ID, Event: model.CalendarEvent{Title: "합주", Date: "2026-11-17", Start: start}})
		return model.Intent{Skill: ScheduleSkill, Kind: "calendar.update", EntityID: entity, Payload: payload, Title: "합주 옮기기", Confidence: 0.9, Reversible: true}
	}
	if in, _ := f.g.Submit(ctx, move("plan_a", "16:00")); !in.Covered() {
		t.Fatalf("같은 옮김: %+v", in)
	}
	if in, _ := f.g.Submit(ctx, move("plan_b", "18:00")); in.Status != model.Pending {
		t.Fatalf("다른 옮김은 묻습니다: %+v", in)
	}
}

// A card in the conversation that added an entry does not speak for changing
// it after: approved at 11:56, it does not keep the later moves of '은주와
// 저녁's place - 망원 at 12:00, 모퉁이맥주 from KakaoTalk at 17:30 - off the
// calendar. Adding the same again is still its question; a move the memo says
// already is no question at all.
func TestAChangeAfterTheConversationAddedItIsAsked(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	entry, _ := f.cal.Put(ctx, model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Memo: "퇴근 후 · 망원", Owner: "shared"})
	if _, err := f.g.Asked(ctx, "p-1", model.HeldCalendarCreate, "일정을 추가합니다", model.CalendarAction{
		Event: model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.Answered(ctx, HeldAnswer{Card: "p-1", Kind: model.HeldCalendarCreate, Approved: true, At: f.clock}); err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{"calendar", model.OwnerRoom} {
		entity := "plan_" + strings.ReplaceAll(room, ":", "_")
		if _, _, err := f.st.Put(ctx, model.Entity{ID: entity, Kind: model.KindPlan, Room: room,
			State: model.State{Title: "은주와 저녁", Status: model.Confirmed, Date: "2026-11-04", Place: "모퉁이맥주", CalendarID: entry.ID}}, model.Change{}); err != nil {
			t.Fatal(err)
		}
		change := func(kind, place string) model.Intent {
			was := model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Place: "망원"}
			payload, _ := json.Marshal(model.CalendarAction{EventID: entry.ID, Was: &was, Event: model.CalendarEvent{Title: "은주와 저녁", Date: "2026-11-04", Place: place, Owner: "shared"}})
			return model.Intent{Skill: ScheduleSkill, Kind: kind, EntityID: entity, Payload: payload, Title: "캘린더 수정: 은주와 저녁", Confidence: 0.85, Reversible: true}
		}
		moved, _ := f.g.Submit(ctx, change("calendar.update", "모퉁이맥주"))
		if moved.Status != model.Pending || !moved.Card {
			t.Fatalf("%s: 추가한 카드는 그 뒤의 수정을 대신하지 않습니다: %+v", room, moved)
		}
		said, _ := f.g.Submit(ctx, change("calendar.update", "망원"))
		if said.Status != model.Executed || said.Card || !strings.HasPrefix(said.Why, "캘린더에 이미 그렇게") {
			t.Fatalf("%s: 메모에 이미 있는 장소는 묻지 않습니다: %+v", room, said)
		}
		gone, _ := f.g.Submit(ctx, change("calendar.delete", ""))
		if gone.Status != model.Pending || !gone.Card {
			t.Fatalf("%s: 지우는 것도 따로 묻습니다: %+v", room, gone)
		}
		_ = f.g.Withdraw(ctx, entity, ScheduleSkill)
	}
	again, _ := f.g.Submit(ctx, addAt(t, f, "plan_again", "kakao:1", "은주와 저녁", "2026-11-04", ""))
	if !again.Covered() {
		t.Fatalf("같은 일정을 또 추가하는 것은 대화의 카드가 물었습니다: %+v", again)
	}
}

// With no card in the conversation, mind's is the only question.
func TestNoCardInTheConversationAsksAsBefore(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	in, _ := f.g.Submit(ctx, addAt(t, f, "plan_a", model.OwnerRoom, "캠프", "2026-11-17", ""))
	if in.Status != model.Pending || !in.Card {
		t.Fatalf("%+v", in)
	}
}

// What the rules leave open goes to the judgement, once it is there.
func TestWhatTheRulesCannotSayIsJudged(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	a := model.State{Title: "지수 생일 저녁", Date: "2026-11-17", Start: "19:00"}
	b := model.State{Title: "저녁 약속", Date: "2026-11-17", Start: "21:30"}
	if f.g.Same(ctx, a, b) {
		t.Fatal("판단이 없으면 둘입니다")
	}
	asked := 0
	f.g.Judge = func(context.Context, model.State, model.State) bool { asked++; return true }
	if !f.g.Same(ctx, a, b) || asked != 1 {
		t.Fatalf("판단을 따릅니다: %d", asked)
	}
	b.Date = "2026-11-18"
	if f.g.Same(ctx, a, b) || asked != 1 {
		t.Fatalf("규칙이 다르다면 묻지 않습니다: %d", asked)
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
		if o.Type == model.KindCardRaised {
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

// Two plans for one entry - the same day at the same place, read in two rooms
// - are one question: the newer card closes the older.
func TestAlikeCalendarCardIsOneQuestion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	alike := func(entity, title, place string) model.Intent {
		payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: title, Date: "2026-10-18", Place: place}})
		return model.Intent{Skill: "schedule", Kind: "calendar.create", EntityID: entity, Payload: payload, Title: "캘린더에 추가: " + title, Confidence: 0.9, Reversible: true}
	}
	first, _ := f.g.Submit(ctx, alike("plan_a", "쉼오지캠프 2박 (하늘존별)", "쉼오지캠프"))
	other, _ := f.g.Submit(ctx, alike("plan_o", "민수와 저녁", "강남"))
	second, _ := f.g.Submit(ctx, alike("plan_b", "쉼오지캠프 2박3일", "쉼오지캠프"))
	old, _ := f.st.Intent(ctx, first.ID)
	if old.Status != model.Superseded || !strings.Contains(old.Error, "같은 일정") || old.DecidedBy != "policy" || second.Status != model.Pending {
		t.Fatalf("앞의 카드는 닫히고 새 카드가 서야 합니다: %s %q / %s", old.Status, old.Error, second.Status)
	}
	if kept, _ := f.st.Intent(ctx, other.ID); kept.Status != model.Pending {
		t.Fatalf("다른 일정의 카드는 그대로여야 합니다: %s", kept.Status)
	}
	stat, _ := f.st.Policy(ctx, "schedule", "calendar.create")
	if stat.Rejected != 0 {
		t.Fatalf("닫힌 카드는 거절로 배우지 않습니다: %+v", stat)
	}
	f.log.Drain()
	all, _ := f.log.Read(ctx, 0, 100)
	closed := false
	for _, o := range all {
		if o.Type == model.KindCardClosed && strings.Contains(string(o.Data), first.ID) {
			closed = true
		}
	}
	if !closed {
		t.Fatal("닫힌 카드는 대화에서도 닫혀야 합니다")
	}
}

// One stay asked about under two names: the same first and last day and a word
// in common are one question, and approving both would put it on twice.
func TestCardsAboutOneStayAreOneQuestion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	stay := func(entity, title string) model.Intent {
		payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: title, Date: "2027-01-01", EndDate: "2027-01-03"}})
		return model.Intent{Skill: "schedule", Kind: "calendar.create", EntityID: entity, Payload: payload, Title: "캘린더에 추가: " + title, Confidence: 0.9, Reversible: true}
	}
	first, _ := f.g.Submit(ctx, stay("plan_a", "지수와 캠핑 2박3일"))
	second, _ := f.g.Submit(ctx, stay("plan_b", "숲속 캠핑 2박3일 (4번 사이트)"))
	old, _ := f.st.Intent(ctx, first.ID)
	if old.Status != model.Superseded || second.Status != model.Pending {
		t.Fatalf("앞의 카드는 닫히고 새 카드가 서야 합니다: %s / %s", old.Status, second.Status)
	}
}

// One morning's plans asked four cards and none closed another: a question
// about an entry closes the other plans' questions about it, and a question
// about one outing closes the other parts'. Another day's stays.
func TestCardsAboutOneOutingAreOneQuestion(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	plan := func(id, title string) {
		if _, _, err := f.st.Put(ctx, model.Entity{ID: id, Kind: model.KindPlan, Room: "r", State: model.State{Title: title, Status: model.Confirmed, Date: "2026-10-28", People: []string{"지수"}}}, model.Change{Actor: "t", Evidence: []model.Evidence{}}); err != nil {
			t.Fatal(err)
		}
	}
	plan("plan_a", "지수와 합주 (연습실 D)")
	plan("plan_b", "지수와 점심")
	ask := func(entity, kind, eventID, title, date, start string) model.Intent {
		payload, _ := json.Marshal(model.CalendarAction{EventID: eventID, Event: model.CalendarEvent{Title: title, Date: date, Start: start}})
		return model.Intent{Skill: "schedule", Kind: kind, EntityID: entity, Payload: payload, Title: title, Confidence: 0.9, Reversible: true}
	}
	entry, err := f.cal.Put(ctx, model.CalendarEvent{Title: "지수와 톤 세팅", Date: "2026-10-28", Start: "10:00"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := f.g.Submit(ctx, ask("plan_o", "calendar.create", "", "지수와 커피", "2026-11-02", "12:00"))
	first, _ := f.g.Submit(ctx, ask("plan_a", "calendar.update", entry.ID, "지수와 합주 (연습실 D)", "2026-10-28", "12:00"))
	if first.Status != model.Pending {
		t.Fatalf("%+v", first)
	}
	second, _ := f.g.Submit(ctx, ask("plan_c", "calendar.update", entry.ID, "지수와 톤 세팅", "2026-10-28", "11:00"))
	if old, _ := f.st.Intent(ctx, first.ID); old.Status != model.Superseded || !strings.Contains(old.Error, second.ID) {
		t.Fatalf("같은 캘린더 항목의 앞 카드는 닫혀야 합니다: %s %q", old.Status, old.Error)
	}
	third, _ := f.g.Submit(ctx, ask("plan_b", "calendar.create", "", "지수와 점심", "2026-10-28", "13:00"))
	if third.Status != model.Pending {
		t.Fatalf("%+v", third)
	}
	if lunch, _ := f.st.Intent(ctx, second.ID); lunch.Status != model.Superseded {
		t.Fatalf("같은 사람과 이어지는 일정의 카드는 하나로: %s", lunch.Status)
	}
	if kept, _ := f.st.Intent(ctx, other.ID); kept.Status != model.Pending {
		t.Fatalf("다른 날의 카드는 그대로: %s", kept.Status)
	}
}

// The floor on confidence is held to the confidence the grade went by: a
// kind that says 90% and is approved about half the time is asked, though
// it would be done and told on its word.
func TestTheFloorIsTheCorrectedConfidence(t *testing.T) {
	ctx := context.Background()
	p := DefaultPolicy()
	p.Levels["calendar.create"] = Tell
	f := newFixture(p)
	if got, _ := f.g.Submit(ctx, create("p0")); got.Decision != model.Notify {
		t.Fatalf("기록이 없으면 말한 확신대로: %+v", got)
	}
	f.g.SetBook(engine.NewBook(answers("calendar.create", "schedule", 24, 10, 0.9)))
	got, err := f.g.Submit(ctx, create("p1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != model.Ask || !strings.HasPrefix(got.Why, "확신도 ") || strings.HasPrefix(got.Why, "확신도 90%") {
		t.Fatalf("고친 확신이 바닥 아래면 물음: %+v", got)
	}
}

// A notice is answered with one tap, and the next of its kind is held to
// what was said of the ones before: mostly not needed, it waits for the
// evening; never needed, it is not sent but for the odd one. The brief is
// what was asked for, whatever was said.
func TestANoticeIsHeldToWhatWasSaidOfItsKind(t *testing.T) {
	ctx := context.Background()
	f := newFixture(DefaultPolicy())
	notice := func(topic string) model.Intent {
		return model.Intent{Skill: "life", Kind: "info", Topic: topic, Title: "생수 살 때 됐어요", Confidence: 1, Urgent: true}
	}
	first, err := f.g.Submit(ctx, notice("restock:생수:2026-10-01"))
	if err != nil || first.Decision != model.Notify {
		t.Fatalf("기록이 없으면 그대로: %+v %v", first, err)
	}
	heard, err := f.g.Heard(ctx, first.ID, false)
	if err != nil || heard.Heard != model.HeardUseless || heard.HeardAt == nil {
		t.Fatalf("한 번 누른 답: %+v %v", heard, err)
	}
	if _, err := f.g.Heard(ctx, "int_none", true); err == nil {
		t.Fatal("없는 알림")
	}
	heard_ := func(n, useful int) []model.Outcome {
		var out []model.Outcome
		for i := range n {
			result := model.ResultRejected
			if i < useful {
				result = model.ResultApproved
			}
			out = append(out, model.Outcome{Source: "notice", Kind: engine.NoticeKind("restock"), Skill: "life", Context: "restock", P: engine.NoticeStated, Result: result})
		}
		return out
	}
	f.g.SetBook(engine.NewBook(heard_(12, 4)))
	if got, _ := f.g.Submit(ctx, notice("restock:휴지:2026-10-02")); got.Decision != model.Digest || got.Tier != "digest" || !strings.Contains(got.Why, "필요 없다고") {
		t.Fatalf("대개 필요 없던 종류는 저녁 요약으로: %+v", got)
	}
	f.g.SetBook(engine.NewBook(heard_(30, 0)))
	sent, dropped := 0, 0
	for i := range 40 {
		got, _ := f.g.Submit(ctx, notice(fmt.Sprintf("restock:물건%d:2026-10-03", i)))
		switch got.Decision {
		case model.Drop:
			dropped++
			if got.Status != model.Dropped || got.Tier != "" {
				t.Fatalf("%+v", got)
			}
		default:
			sent++
		}
	}
	if dropped == 0 || sent == 0 || sent > dropped/2 {
		t.Fatalf("늘 필요 없던 종류는 보내지 않되 가끔은: 보냄 %d, 안 보냄 %d", sent, dropped)
	}
	// Another kind of notice, and the brief, are not held to it.
	other := notice("missed:netflix:2026-10-15")
	other.Skill = "predict"
	if got, _ := f.g.Submit(ctx, other); got.Decision != model.Notify {
		t.Fatalf("다른 종류는 그대로: %+v", got)
	}
	brief := notice("restock:brief")
	brief.Skill = BriefSkill
	if got, _ := f.g.Submit(ctx, brief); got.Decision != model.Notify {
		t.Fatalf("브리핑은 그대로: %+v", got)
	}
}
