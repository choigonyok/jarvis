package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// contract is what every Store must do, run against each implementation.
func contract(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)

	// Entities: optimistic versions, history with before/after.
	e, c, err := st.Put(ctx, model.Entity{ID: "plan_1", Kind: model.KindPlan, Room: "r", State: model.State{Title: "강릉", Status: model.Proposed}}, model.Change{Actor: "interpreter", At: at, Evidence: []model.Evidence{{Ref: "sim:1", Quote: "강릉 갈까"}}, Confidence: 0.8})
	if err != nil || e.Version != 1 || c.Before != nil || c.ID == 0 {
		t.Fatalf("만들기: %+v %+v %v", e, c, err)
	}
	stale := e
	e.State.Status = model.Confirmed
	e, c, err = st.Put(ctx, e, model.Change{Actor: "interpreter", At: at.Add(time.Minute)})
	if err != nil || e.Version != 2 || c.Before == nil || c.Before.Status != model.Proposed || c.After.Status != model.Confirmed {
		t.Fatalf("고치기: %+v %+v %v", e, c, err)
	}
	if _, _, err := st.Put(ctx, stale, model.Change{At: at}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("낡은 버전은 충돌이어야 합니다: %v", err)
	}
	if _, _, err := st.Put(ctx, model.Entity{ID: "plan_1", Kind: model.KindPlan}, model.Change{At: at}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("같은 id 를 새로 만들면 충돌이어야 합니다: %v", err)
	}
	got, err := st.Entity(ctx, "plan_1")
	if err != nil || got.State.Status != model.Confirmed || !got.CreatedAt.Equal(at) {
		t.Fatalf("읽기: %+v %v", got, err)
	}
	cs, _ := st.Changes(ctx, "plan_1", 10)
	if len(cs) != 2 || cs[0].Version != 2 || len(cs[1].Evidence) != 1 {
		t.Fatalf("이력: %+v", cs)
	}
	e.State.Status = model.Cancelled
	_, _, _ = st.Put(ctx, e, model.Change{At: at.Add(2 * time.Minute)})
	if live, _ := st.Entities(ctx, model.KindPlan, "r", true); len(live) != 0 {
		t.Fatalf("취소된 건 살아 있지 않습니다: %+v", live)
	}

	// Messages and rooms.
	for i, text := range []string{"강릉 갈까?", "그러자"} {
		m := model.Message{Ref: "sim:" + string(rune('1'+i)), Source: "sim", Room: "r", RoomName: "지수", IsMine: i == 1, SentAt: at.Add(time.Duration(i) * time.Minute), Text: text}
		if err := st.PutMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		_ = st.PutMessage(ctx, m) // idempotent
	}
	msg, err := st.Message(ctx, "sim:1")
	if err != nil || msg.Text != "강릉 갈까?" {
		t.Fatalf("메시지: %+v %v", msg, err)
	}
	rooms, _ := st.Rooms(ctx)
	if len(rooms) != 1 || rooms[0].Messages != 2 || rooms[0].Uninterpreted != 2 || rooms[0].OnlyMine {
		t.Fatalf("방: %+v", rooms)
	}
	_ = st.MarkRoom(ctx, "r", at)
	rooms, _ = st.Rooms(ctx)
	if rooms[0].Uninterpreted != 1 {
		t.Fatalf("해석한 뒤 남은 것: %+v", rooms)
	}
	ms, _ := st.RoomMessages(ctx, "r", at, time.Time{}, 10)
	if len(ms) != 1 || ms[0].Text != "그러자" {
		t.Fatalf("새 메시지: %+v", ms)
	}

	// Timers.
	_ = st.SetTimer(ctx, model.Timer{Skill: "schedule", EntityID: "plan_1", Name: "prep", FireAt: at.Add(time.Hour)})
	_ = st.SetTimer(ctx, model.Timer{Skill: "schedule", EntityID: "plan_1", Name: "prep", FireAt: at.Add(2 * time.Hour)})
	if due, _ := st.DueTimers(ctx, at.Add(90*time.Minute)); len(due) != 0 {
		t.Fatalf("옮긴 타이머는 아직: %+v", due)
	}
	due, _ := st.DueTimers(ctx, at.Add(3*time.Hour))
	if len(due) != 1 {
		t.Fatalf("타이머: %+v", due)
	}
	_ = st.FireTimer(ctx, due[0].ID, at.Add(3*time.Hour))
	if due, _ := st.DueTimers(ctx, at.Add(4*time.Hour)); len(due) != 0 {
		t.Fatalf("울린 타이머는 다시 안 울립니다: %+v", due)
	}
	_ = st.CancelTimers(ctx, "schedule", "plan_1")
	if all, _ := st.Timers(ctx); len(all) != 0 {
		t.Fatalf("지운 타이머: %+v", all)
	}

	// Intents.
	in := model.Intent{ID: "int_1", Skill: "schedule", Kind: "calendar.create", EntityID: "plan_1", Status: model.Pending, CreatedAt: at}
	_ = st.PutIntent(ctx, in)
	in.Status = model.Executed
	_ = st.PutIntent(ctx, in)
	_ = st.PutIntent(ctx, model.Intent{ID: "int_2", Kind: "info", Status: model.Executed, CreatedAt: at.Add(time.Hour)})
	all, _ := st.Intents(ctx, 10)
	if len(all) != 2 || all[0].ID != "int_2" {
		t.Fatalf("의도는 최근 순: %+v", all)
	}
	got1, _ := st.Intent(ctx, "int_1")
	if got1.Status != model.Executed {
		t.Fatalf("의도 갱신: %+v", got1)
	}
	if for1, _ := st.IntentsFor(ctx, "plan_1"); len(for1) != 1 {
		t.Fatalf("엔티티별: %+v", for1)
	}
	if since, _ := st.IntentsSince(ctx, at.Add(30*time.Minute)); len(since) != 1 {
		t.Fatalf("이후: %+v", since)
	}

	// Cursors.
	if v, _ := st.Cursor(ctx, "sensor:kakao"); v != 0 {
		t.Fatal("없는 커서는 0")
	}
	_ = st.SetCursor(ctx, "sensor:kakao", 3215)
	if v, _ := st.Cursor(ctx, "sensor:kakao"); v != 3215 {
		t.Fatalf("커서: %d", v)
	}
}

// facts is the evidence ledger and the alias table, for every Store.
func facts(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	f := model.Fact{
		ID: "mf_1", Room: "r", Subject: model.Party{Name: "지수", Kind: "Person"}, Relation: "DISLIKES",
		Object: model.Party{Name: "매운 음식", Kind: "Thing"}, Text: "지수는 매운 음식을 못 먹는다",
		Evidence: []model.Evidence{{Ref: "sim:1", Quote: "매운 거 못 먹어"}}, Confidence: 0.9,
		Status: model.FactPending, NextTry: at, CreatedAt: at,
	}
	if err := st.PutFact(ctx, f); err != nil {
		t.Fatal(err)
	}
	later := f
	later.ID, later.CreatedAt, later.NextTry = "mf_2", at.Add(time.Minute), at.Add(time.Hour)
	_ = st.PutFact(ctx, later)
	due, err := st.DueFacts(ctx, at.Add(time.Minute), 10)
	if err != nil || len(due) != 1 || due[0].ID != "mf_1" || due[0].Evidence[0].Quote != "매운 거 못 먹어" {
		t.Fatalf("때가 된 것만 나와야 합니다: %+v %v", due, err)
	}
	f.Status, f.Memory = model.FactStored, &model.Stored{Edge: "e1", Source: model.MemoryNode{ID: "n1", Name: "지수"}}
	_ = st.PutFact(ctx, f)
	if due, _ := st.DueFacts(ctx, at.Add(2*time.Hour), 10); len(due) != 1 || due[0].ID != "mf_2" {
		t.Fatalf("넣은 것은 다시 나오지 않아야 합니다: %+v", due)
	}
	got, err := st.Fact(ctx, "mf_1")
	if err != nil || got.Memory == nil || got.Memory.Source.ID != "n1" {
		t.Fatalf("기억의 판단이 남아야 합니다: %+v %v", got, err)
	}
	if all, _ := st.Facts(ctx, 10); len(all) != 2 || all[0].ID != "mf_2" {
		t.Fatalf("최근 것부터: %+v", all)
	}
	if _, err := st.Fact(ctx, "mf_x"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("없는 사실: %v", err)
	}

	_ = st.PutAlias(ctx, model.Alias{Name: "지수", Kind: "Person", Node: "n1", At: at})
	_ = st.PutAlias(ctx, model.Alias{Name: "지수", Node: "n2", At: at.Add(time.Minute)})
	as, err := st.Aliases(ctx)
	if err != nil || len(as) != 1 || as[0].Node != "n2" || as[0].Seen != 2 || as[0].Kind != "Person" {
		t.Fatalf("이름은 마지막 노드로, 본 횟수는 늘고, 종류는 남아야 합니다: %+v %v", as, err)
	}
}

func TestMemory(t *testing.T) {
	contract(t, NewMemory())
	facts(t, NewMemory())
	policy(t, NewMemory())
	occurrences(t, NewMemory())
	judge(t, NewMemory())
	self(t, NewMemory())
	replies(t, NewMemory())
}

func replies(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	_ = st.AddReplySituation(ctx, model.ReplySituation{Kind: "reconnect", Room: "kakao:1", InRef: "kakao:10", GapDays: 90, At: at})
	_ = st.AddReplySituation(ctx, model.ReplySituation{Kind: "reconnect", Room: "kakao:1", InRef: "kakao:10", GapDays: 90, At: at})
	if ex, _ := st.ReplyExamples(ctx, "reconnect", 5); len(ex) != 0 {
		t.Fatalf("답하기 전엔 예시가 아님: %+v", ex)
	}
	_ = st.AnswerReplySituations(ctx, "kakao:1", "kakao:11")
	ex, _ := st.ReplyExamples(ctx, "reconnect", 5)
	if len(ex) != 1 || ex[0].OutRef != "kakao:11" || ex[0].GapDays != 90 {
		t.Fatalf("예시: %+v", ex)
	}
}

// self: notes with their versions, predictions and expectations.
func self(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	n := model.Note{ID: "note_1", Text: "캠핑 며칠 전 장작을 산다", Status: model.Hypothesis, Confidence: 0.5, Version: 1, CreatedAt: at, UpdatedAt: at,
		Rule: &model.Rule{Kind: "prep", PlanKind: "캠핑", Class: "장작", Bucket: "d3", Support: 1, Total: 1}}
	_ = st.PutNote(ctx, n)
	n.Version, n.Status, n.Reason, n.UpdatedAt = 2, model.Confirmed, "운영자가 확인", at.Add(time.Hour)
	_ = st.PutNote(ctx, n)
	got, err := st.Note(ctx, "note_1")
	if err != nil || got.Status != model.Confirmed || got.Rule.Class != "장작" {
		t.Fatalf("노트: %+v %v", got, err)
	}
	vs, _ := st.NoteVersions(ctx, "note_1")
	if len(vs) != 2 || vs[0].Status != model.Hypothesis || vs[1].Reason != "운영자가 확인" {
		t.Fatalf("버전: %+v", vs)
	}
	if all, _ := st.Notes(ctx); len(all) != 1 {
		t.Fatalf("노트 목록: %+v", all)
	}
	pr := model.Prediction{ID: "pred_1", Target: "workout:2026-10-08", Text: "오늘 운동", Prob: 0.65, Due: at.Add(20 * time.Hour), CreatedAt: at}
	_ = st.PutPrediction(ctx, pr)
	if due, _ := st.DuePredictions(ctx, at); len(due) != 0 {
		t.Fatalf("아직 기한 전: %+v", due)
	}
	if due, _ := st.DuePredictions(ctx, at.Add(21*time.Hour)); len(due) != 1 {
		t.Fatalf("기한 지남: %+v", due)
	}
	scored := at.Add(21 * time.Hour)
	pr.ScoredAt = &scored
	_ = st.PutPrediction(ctx, pr)
	if due, _ := st.DuePredictions(ctx, at.Add(22*time.Hour)); len(due) != 0 {
		t.Fatalf("채점한 것은 빼야: %+v", due)
	}
	if day, _ := st.Predictions(ctx, at, at.Add(24*time.Hour)); len(day) != 1 {
		t.Fatalf("기간 예측: %+v", day)
	}
	_ = st.PutExpectation(ctx, model.Expectation{PredictionID: "pred_1", Key: "workout", Due: pr.Due, State: "open"})
	if open, _ := st.OpenExpectations(ctx, "workout"); len(open) != 1 {
		t.Fatalf("열린 기대: %+v", open)
	}
	_ = st.PutExpectation(ctx, model.Expectation{PredictionID: "pred_1", Key: "workout", Due: pr.Due, State: "met", MetBy: "workout:x"})
	if open, _ := st.OpenExpectations(ctx, "workout"); len(open) != 0 {
		t.Fatalf("채운 기대는 닫힘: %+v", open)
	}
	if all, _ := st.Expectations(ctx, "pred_1"); len(all) != 1 || all[0].MetBy != "workout:x" {
		t.Fatalf("기대: %+v", all)
	}
}

// judge: the common-sense caches and the pattern counts.
func judge(t *testing.T, st Store) {
	ctx := context.Background()
	if _, err := st.ThingClass(ctx, "참나무 장작 10kg"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("없는 분류: %v", err)
	}
	_ = st.PutThingClass(ctx, "참나무 장작 10kg", "장작")
	if c, _ := st.ThingClass(ctx, "참나무 장작 10kg"); c != "장작" {
		t.Fatalf("분류: %q", c)
	}
	_ = st.PutLinkCache(ctx, "캠핑", model.Judgement{Class: "장작", Rel: model.PrepFor, Plausibility: 0.9, Why: "화로"})
	if j, err := st.LinkCache(ctx, "장작", "캠핑"); err != nil || j.Plausibility < 0.89 || j.Rel != model.PrepFor {
		t.Fatalf("상식: %+v %v", j, err)
	}
	at := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	_ = st.CountPattern(ctx, model.Pattern{KindA: "캠핑", Rel: model.PrepFor, Total: 1, LastSeen: at})
	_ = st.CountPattern(ctx, model.Pattern{KindA: "캠핑", Rel: model.PrepFor, KindB: "장작", Bucket: "d3", Support: 1, LastSeen: at})
	_ = st.CountPattern(ctx, model.Pattern{KindA: "캠핑", Rel: model.PrepFor, KindB: "장작", Bucket: "d3", Support: 1, LastSeen: at.Add(time.Hour)})
	ps, _ := st.Patterns(ctx, "캠핑")
	if len(ps) != 2 || ps[1].Support != 2 || ps[0].Total != 1 || !ps[1].LastSeen.Equal(at.Add(time.Hour)) {
		t.Fatalf("패턴: %+v", ps)
	}
	_ = st.ResetPatterns(ctx)
	if ps, _ := st.Patterns(ctx, ""); len(ps) != 0 {
		t.Fatalf("비워야: %+v", ps)
	}
}

// occurrences and links: idempotent by id, read by time window and kind,
// links from either end and by when they changed.
func occurrences(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 3, 12, 40, 0, 0, time.UTC)
	o := model.Occurrence{ID: "spending:1#1", Kind: "purchase.item", At: at, Title: "참나무 장작 10kg", Class: "연료/캠핑", Amount: 20000, Ref: "spending:1"}
	_ = st.PutOccurrence(ctx, o)
	o.Amount = 21000
	_ = st.PutOccurrence(ctx, o)
	_ = st.PutOccurrence(ctx, model.Occurrence{ID: "photo:a", Kind: "photo", At: at.Add(time.Hour), Area: "가평"})
	got, _ := st.Occurrences(ctx, at, at.Add(2*time.Hour))
	if len(got) != 2 || got[0].Amount != 21000 {
		t.Fatalf("발생: %+v", got)
	}
	if only, _ := st.Occurrences(ctx, at, at.Add(2*time.Hour), "photo"); len(only) != 1 || only[0].ID != "photo:a" {
		t.Fatalf("종류로: %+v", only)
	}
	if none, _ := st.Occurrences(ctx, at.Add(2*time.Hour), at.Add(3*time.Hour)); len(none) != 0 {
		t.Fatalf("구간 밖: %+v", none)
	}
	for i, ps := range []model.State{{Title: "캠핑", Date: "2026-10-06", EndDate: "2026-10-07"}, {Title: "저녁", Date: "2026-10-09"}, {Title: "언젠가"}} {
		_, _, _ = st.Put(ctx, model.Entity{ID: fmt.Sprintf("plan_on%d", i), Kind: model.KindPlan, State: ps}, model.Change{Actor: "t", At: at})
	}
	on, _ := st.PlansOn(ctx, "2026-10-07", "2026-10-08")
	if len(on) != 1 || on[0].State.Title != "캠핑" {
		t.Fatalf("그날 걸친 일정: %+v", on)
	}
	l := model.Link{From: "spending:1#1", To: "plan_x", Rel: model.PrepFor, Confidence: 0.6, State: model.Estimated, By: "judge", CreatedAt: at, UpdatedAt: at}
	_ = st.PutLink(ctx, l)
	l.State, l.UpdatedAt = model.Confirmed, at.Add(time.Hour)
	_ = st.PutLink(ctx, l)
	of, _ := st.LinksOf(ctx, "plan_x")
	if len(of) != 1 || of[0].State != model.Confirmed {
		t.Fatalf("링크: %+v", of)
	}
	if since, _ := st.LinksSince(ctx, at.Add(30*time.Minute)); len(since) != 1 {
		t.Fatalf("바뀐 링크: %+v", since)
	}
}

// policy: counts per (skill, context), the last answer, and a reset.
func policy(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	for _, o := range []string{model.OutcomeShown, model.OutcomeApproved, model.OutcomeShown, model.OutcomeRejected, model.OutcomeShown} {
		if err := st.CountPolicy(ctx, "schedule", "calendar.create", o, at); err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.Policy(ctx, "schedule", "calendar.create")
	if err != nil || p.Shown != 3 || p.Approved != 1 || p.Rejected != 1 || p.Last != model.OutcomeRejected {
		t.Fatalf("승인율 통계: %+v %v", p, err)
	}
	if none, _ := st.Policy(ctx, "x", "y"); none.Shown != 0 || none.Rate() != 0.5 {
		t.Fatalf("처음 보는 맥락: %+v", none)
	}
	all, _ := st.Policies(ctx)
	if len(all) != 1 {
		t.Fatalf("목록: %+v", all)
	}
	_ = st.ResetPolicy(ctx)
	if all, _ := st.Policies(ctx); len(all) != 0 {
		t.Fatalf("비워야 합니다: %+v", all)
	}
}

// Against a throwaway Postgres with migrations 035 and 038 applied:
// MIND_TEST_DATABASE_URL=postgres://... go test ./internal/store/
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("MIND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIND_TEST_DATABASE_URL 이 없어 건너뜁니다")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), `truncate mind_messages, mind_rooms, mind_entities, mind_changes, mind_timers, mind_intents, mind_cursors, mind_facts, mind_aliases, mind_policy_stats, mind_occurrences, mind_links, mind_thing_classes, mind_link_cache, mind_pattern_counts, mind_notes, mind_note_versions, mind_predictions, mind_expectations, mind_reply_examples restart identity cascade`); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	st := NewPostgres(pool, key)
	contract(t, st)
	facts(t, st)
	policy(t, st)
	occurrences(t, st)
	judge(t, st)
	self(t, st)
	replies(t, st)
	var sealed bool
	var body []byte
	_ = pool.QueryRow(context.Background(), `select sealed, body from mind_messages where ref = 'sim:1'`).Scan(&sealed, &body)
	if !sealed || string(body) == "강릉 갈까?" {
		t.Fatal("키가 있으면 원문이 봉해져야 합니다")
	}
}
