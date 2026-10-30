package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
	"slices"
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
	if ms, _ := st.RoomMessagesFrom(ctx, "r", time.Time{}, time.Time{}, 1); len(ms) != 1 || ms[0].Ref != "sim:1" {
		t.Fatalf("가장 오래된 것부터: %+v", ms)
	}
	if ms, _ := st.RoomMessagesFrom(ctx, "r", time.Time{}, at.Add(time.Minute), 10); len(ms) != 1 || ms[0].Ref != "sim:1" {
		t.Fatalf("before 는 배타적: %+v", ms)
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
	held := later
	held.ID, held.Status, held.Tries = "mf_held", model.FactHeld, 5
	_ = st.PutFact(ctx, held)
	if hs, err := st.HeldFacts(ctx); err != nil || len(hs) != 1 || hs[0].ID != "mf_held" {
		t.Fatalf("보류한 것만: %+v %v", hs, err)
	}
	if due, _ := st.DueFacts(ctx, at.Add(48*time.Hour), 10); len(due) != 1 || due[0].ID != "mf_2" {
		t.Fatalf("보류한 것은 저절로 다시 나오지 않습니다: %+v", due)
	}
	held.Status = model.FactDropped
	_ = st.PutFact(ctx, held)

	// G-M1: a ledger reads facts by relation, with no limit and in any
	// letter case - old entries must not fall out behind newer facts.
	for i, rel := range []string{"GAVE_TO", "received_from", "LIKES"} {
		g := f
		g.ID, g.Relation, g.CreatedAt = fmt.Sprintf("mf_rel_%d", i), rel, at.Add(time.Duration(i+10)*time.Minute)
		_ = st.PutFact(ctx, g)
	}
	byRel, err := st.FactsWith(ctx, "GAVE_TO", "RECEIVED_FROM")
	if err != nil || len(byRel) != 2 || byRel[0].ID != "mf_rel_1" || byRel[1].ID != "mf_rel_0" {
		t.Fatalf("관계로 고른 사실, 최근 것부터: %+v %v", byRel, err)
	}

	_ = st.PutAlias(ctx, model.Alias{Name: "지수", Kind: "Person", Node: "n1", At: at})
	_ = st.PutAlias(ctx, model.Alias{Name: "지수", Node: "n2", At: at.Add(time.Minute)})
	as, err := st.Aliases(ctx)
	if err != nil || len(as) != 1 || as[0].Node != "n2" || as[0].Seen != 2 || as[0].Kind != "Person" {
		t.Fatalf("이름은 마지막 노드로, 본 횟수는 늘고, 종류는 남아야 합니다: %+v %v", as, err)
	}
}

// messages is searching message text and reading around a message: the
// words found anywhere in a message (a particle stuck on, any case), every
// word required, newest first, narrowed by room and time, at most as many as
// asked and no more read than allowed.
func messages(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	put := func(ref, room, roomName, sender, text string, min int) {
		t.Helper()
		if err := st.PutMessage(ctx, model.Message{Ref: ref, Source: "kakao", Room: room, RoomName: roomName, Sender: sender, SentAt: at.Add(time.Duration(min) * time.Minute), Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	// A notice keeps being one: mind's own output, read back as context only.
	if err := st.PutMessage(ctx, model.Message{Ref: "chat:n1", Source: "chat", Room: "chat:owner", RoomName: "jarvis",
		Sender: model.NoticeSender, SentAt: at, Text: "리마인드 · 장갑 챙기기", Notice: true}); err != nil {
		t.Fatal(err)
	}
	if m, err := st.Message(ctx, "chat:n1"); err != nil || !m.Notice || m.Sender != model.NoticeSender {
		t.Fatalf("알림 표시가 남아야 합니다: %+v %v", m, err)
	}
	if ms, _ := st.RoomMessages(ctx, "chat:owner", time.Time{}, time.Time{}, 10); len(ms) != 1 || !ms[0].Notice {
		t.Fatalf("방의 메시지로 읽어도 알림입니다: %+v", ms)
	}
	put("srch:1", "kakao:uni", "대학 동기방", "지수", "요즘 다들 갓생 산다던데", 0)
	put("srch:2", "kakao:uni", "대학 동기방", "민지", "갓생이 뭐야?", 1)
	put("srch:3", "kakao:uni", "대학 동기방", "지수", "갓생은 부지런하고 생산적으로 사는 거", 2)
	put("srch:4", "kakao:uni", "대학 동기방", "민지", "아하 Godsaeng", 3)
	put("srch:5", "kakao:work", "회사 팀방", "팀장", "이번 주 갓생 프로젝트 회의", 4)

	ids := func(ms []model.Message) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Ref)
		}
		return fmt.Sprint(out)
	}
	got, err := st.SearchMessages(ctx, MessageQuery{Terms: []string{"갓생"}, Limit: 10, Scan: 1000})
	if err != nil || ids(got) != "[srch:5 srch:3 srch:2 srch:1]" {
		t.Fatalf("최신 먼저, 조사가 붙어도: %s %v", ids(got), err)
	}
	if got[0].Text != "이번 주 갓생 프로젝트 회의" || got[0].RoomName != "회사 팀방" {
		t.Fatalf("원문과 방: %+v", got[0])
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"갓생", "부지런"}, Limit: 10, Scan: 1000}); ids(got) != "[srch:3]" {
		t.Fatalf("모든 낱말: %s", ids(got))
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"godsaeng"}, Limit: 10, Scan: 1000}); ids(got) != "[srch:4]" {
		t.Fatalf("대소문자 무시: %s", ids(got))
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"갓생"}, Room: "동기", Limit: 10, Scan: 1000}); ids(got) != "[srch:3 srch:2 srch:1]" {
		t.Fatalf("방 이름으로 좁히기: %s", ids(got))
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"갓생"}, Room: "kakao:work", Limit: 10, Scan: 1000}); ids(got) != "[srch:5]" {
		t.Fatalf("방 id 로 좁히기: %s", ids(got))
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"갓생"}, Since: at.Add(90 * time.Second), Limit: 10, Scan: 1000}); ids(got) != "[srch:5 srch:3]" {
		t.Fatalf("이후만: %s", ids(got))
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"갓생"}, Limit: 2, Scan: 1000}); ids(got) != "[srch:5 srch:3]" {
		t.Fatalf("개수 상한: %s", ids(got))
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"갓생"}, Room: "kakao:uni", Limit: 10, Scan: 2}); ids(got) != "[srch:3]" {
		t.Fatalf("읽는 양 상한(최신 2개만 읽음): %s", ids(got))
	}
	if got, _ := st.SearchMessages(ctx, MessageQuery{Terms: []string{"  "}, Limit: 10, Scan: 1000}); len(got) != 0 {
		t.Fatalf("빈 검색어: %s", ids(got))
	}

	around, err := st.MessageContext(ctx, "srch:3", 1, 5)
	if err != nil || ids(around) != "[srch:2 srch:3 srch:4]" {
		t.Fatalf("앞뒤(같은 방만): %s %v", ids(around), err)
	}
	if around, _ := st.MessageContext(ctx, "srch:1", 5, 0); ids(around) != "[srch:1]" {
		t.Fatalf("맨 앞: %s", ids(around))
	}
	if _, err := st.MessageContext(ctx, "srch:none", 1, 1); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("없는 메시지: %v", err)
	}
}

// ledger is what a rebuild leaves: the rows it was given, and no others.
func ledger(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	// The classification cache: a model's answer gives way, the operator's
	// does not.
	if _, err := st.Class(ctx, "merchant.category", "상호"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("없는 분류: %v", err)
	}
	if err := st.PutClass(ctx, model.Class{Kind: "merchant.category", Key: "상호", Value: "식비", P: 0.9, By: model.ClassByLLM, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutClass(ctx, model.Class{Kind: "merchant.category", Key: "상호", Value: "교통", P: 0.7, By: model.ClassByLLM, At: at}); err != nil {
		t.Fatal(err)
	}
	if c, err := st.Class(ctx, "merchant.category", "상호"); err != nil || c.Value != "교통" || c.P != 0.7 {
		t.Fatalf("%+v %v", c, err)
	}
	if err := st.PutClass(ctx, model.Class{Kind: "merchant.category", Key: "상호", Value: "쇼핑", P: 1, By: model.ClassByOperator, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutClass(ctx, model.Class{Kind: "merchant.category", Key: "상호", Value: "식비", P: 0.9, By: model.ClassByLLM, At: at}); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Class(ctx, "merchant.category", "상호"); c.Value != "쇼핑" || c.By != model.ClassByOperator {
		t.Fatalf("운영자의 답을 덮었습니다: %+v", c)
	}
	base := 0.7
	if err := st.ReplaceOutcomes(ctx, []model.Outcome{
		{ID: "intent:a", Source: "card", Skill: "schedule", Context: "calendar.create", Kind: "calendar.create", Rule: "이유", P: 0.8, Result: model.ResultRejected, Manual: true, At: at},
		{ID: "prediction:b", Source: "prediction", P: 0.6, Base: &base, Result: model.ResultHit, At: at.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceOutcomes(ctx, []model.Outcome{
		{ID: "intent:a", Source: "card", Skill: "schedule", Context: "calendar.create", Kind: "calendar.create", Rule: "이유", P: 0.8, Result: model.ResultRejected, Manual: true, At: at},
		{ID: "prediction:c", Source: "prediction", P: 0.6, Base: &base, Result: model.ResultHit, At: at.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Outcomes(ctx)
	if err != nil || len(rows) != 2 || rows[0].ID != "intent:a" || !rows[0].Manual || rows[1].ID != "prediction:c" || *rows[1].Base != 0.7 {
		t.Fatalf("장부: %+v %v", rows, err)
	}

	end := at.Add(24 * time.Hour)
	if err := st.ReplaceFactRows(ctx, []model.FactRow{
		{ID: "f1", Subject: "self", Predicate: "LIVES_IN", Object: "서울", Text: "서울에 산다", From: at, To: &end, P: 0.7, Single: true},
		{ID: "f2", Subject: "self", Predicate: "LIVES_IN", Object: "부산", From: end, P: 0.9, Single: true},
	}); err != nil {
		t.Fatal(err)
	}
	facts, err := st.FactRows(ctx)
	if err != nil || len(facts) != 2 || facts[0].To == nil || !facts[0].To.Equal(end) || facts[1].To != nil || !facts[1].Single {
		t.Fatalf("사실: %+v %v", facts, err)
	}

	// The table the graph is made from: records and what was adopted stay
	// through a rebuild of the checked rows, and come first, oldest kind first.
	if err := st.PutFactRow(ctx, model.FactRow{ID: "record:a", Subject: "self", Predicate: "결제", Object: "m1", Text: "결제", From: at, P: 1,
		Origin: model.FactRecord, Body: json.RawMessage(`{"id":"record:a"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutFactRow(ctx, model.FactRow{ID: "edge:z", Subject: "나", Predicate: "VISITED", Object: "강릉", Text: "옛 사실", From: at, To: &end, P: 1,
		Origin: model.FactLegacy, Body: json.RawMessage(`{"id":"edge:z"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceFactRows(ctx, []model.FactRow{{ID: "f3", Subject: "self", Predicate: "LIKES", Object: "커피", From: at, P: 0.8,
		Origin: model.FactChecked, Body: json.RawMessage(`{"id":"f3"}`)}, {ID: "f4", Subject: "self", Predicate: "LIKES", Object: "차", From: at, P: 0.8}}); err != nil {
		t.Fatal(err)
	}
	index, err := st.IndexRows(ctx, 0, 10)
	if err != nil || len(index) != 3 || index[0].ID != "edge:z" || index[1].ID != "record:a" || index[2].ID != "f3" || index[0].Origin != model.FactLegacy {
		t.Fatalf("색인: %+v %v", index, err)
	}
	if page, err := st.IndexRows(ctx, 2, 10); err != nil || len(page) != 1 || page[0].ID != "f3" {
		t.Fatalf("다음 쪽: %+v %v", page, err)
	}
	if facts, err := st.FactRows(ctx); err != nil || len(facts) != 2 {
		t.Fatalf("확인한 사실만: %+v %v", facts, err)
	}
}

func TestMemory(t *testing.T) {
	messages(t, NewMemory())
	contract(t, NewMemory())
	facts(t, NewMemory())
	policy(t, NewMemory())
	ledger(t, NewMemory())
	occurrences(t, NewMemory())
	judge(t, NewMemory())
	self(t, NewMemory())
	replies(t, NewMemory())
	usage(t, NewMemory())
	reviews(t, NewMemory())
}

func reviews(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 12, 21, 0, 0, 0, time.UTC)
	_ = st.AddObservations(ctx, []model.Observation{
		{SubjectType: "캠핑장", Subject: "솔숲캠핑장", Dimension: "소음", Polarity: -1, Score: 2, Context: map[string]string{"with": "가족"}, Source: "int_a", At: at},
		{SubjectType: "캠핑장", Subject: "솔숲캠핑장", Dimension: "뷰·자연", Polarity: 1, Score: 2, Source: "int_a", At: at},
		{SubjectType: "캠핑장", Subject: "솔숲캠핑장", Dimension: "소음", Polarity: -1, Score: 3, Source: "int_b", At: at},
	})
	attrs, _ := st.Attributes(ctx, "캠핑장", "솔숲캠핑장")
	byDim := map[string]model.Attribute{}
	for _, a := range attrs {
		byDim[a.Dimension] = a
	}
	if len(attrs) != 2 || byDim["소음"].Value != -1 || byDim["소음"].Count != 2 || byDim["뷰·자연"].Value != 1 {
		t.Fatalf("속성: %+v", attrs)
	}
	noise, _ := st.ObservationsOn(ctx, "소음")
	if len(noise) != 2 || noise[0].Context["with"] != "가족" {
		t.Fatalf("관찰: %+v", noise)
	}
	_ = st.ForgetObservations(ctx, "int_b")
	if noise, _ := st.ObservationsOn(ctx, "소음"); len(noise) != 1 {
		t.Fatalf("되돌린 회고의 답은 빠짐: %+v", noise)
	}
	_ = st.PutPreference(ctx, model.Preference{Dimension: "소음", Polarity: -1, Status: "estimated", Subjects: []string{"a", "b"}, UpdatedAt: at})
	_ = st.PutPreference(ctx, model.Preference{Dimension: "소음", Polarity: -1, Status: "confirmed", Subjects: []string{"a", "b"}, UpdatedAt: at})
	if ps, _ := st.Preferences(ctx); len(ps) != 1 || ps[0].Status != "confirmed" {
		t.Fatalf("선호: %+v", ps)
	}
}

func usage(t *testing.T, st Store) {
	ctx := context.Background()
	_ = st.AddUsage(ctx, model.Usage{Day: "2026-10-14", Purpose: "judge", Calls: 1, USD: 0.001, In: 100, Out: 20})
	_ = st.AddUsage(ctx, model.Usage{Day: "2026-10-15", Purpose: "judge", Calls: 1, USD: 0.002, In: 100, Out: 20})
	_ = st.AddUsage(ctx, model.Usage{Day: "2026-10-15", Purpose: "judge", Calls: 1, USD: 0.002, In: 100, Out: 20})
	us, _ := st.Usages(ctx, "2026-10-15")
	if len(us) != 1 || us[0].Calls != 2 || us[0].In != 200 || us[0].USD < 0.0039 {
		t.Fatalf("사용량: %+v", us)
	}
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

// One address at a time is the database's rule, not only the rebuild's.
func exclusion(t *testing.T, st Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	err := st.ReplaceFactRows(ctx, []model.FactRow{
		{ID: "a", Subject: "self", Predicate: "LIVES_IN", Object: "서울", From: at, P: 1, Single: true},
		{ID: "b", Subject: "self", Predicate: "LIVES_IN", Object: "부산", From: at.Add(time.Hour), P: 1, Single: true},
	})
	if err == nil {
		t.Fatal("겹치는 주소는 거절해야 합니다")
	}
	if facts, _ := st.FactRows(ctx); len(facts) == 0 {
		t.Fatal("실패한 교체는 있던 것을 지우면 안 됩니다")
	}
	// Many-valued facts may overlap.
	if err := st.ReplaceFactRows(ctx, []model.FactRow{
		{ID: "a", Subject: "self", Predicate: "LIKES", Object: "커피", From: at, P: 1},
		{ID: "b", Subject: "self", Predicate: "LIKES", Object: "차", From: at.Add(time.Hour), P: 1},
	}); err != nil {
		t.Fatal(err)
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
	resetTables(t, dsn, `truncate mind_messages, mind_rooms, mind_entities, mind_changes, mind_timers, mind_intents, mind_cursors, mind_facts, mind_aliases, mind_policy_stats, mind_occurrences, mind_links, mind_thing_classes, mind_link_cache, mind_pattern_counts, mind_notes, mind_note_versions, mind_predictions, mind_expectations, mind_reply_examples, mind_llm_usage, mind_observations, mind_preferences, mind.outcomes, mind.facts, mind.classes restart identity cascade`)
	key := make([]byte, 32)
	st := NewPostgres(pool, key)
	contract(t, st)
	facts(t, st)
	policy(t, st)
	ledger(t, st)
	exclusion(t, st)
	occurrences(t, st)
	judge(t, st)
	self(t, st)
	replies(t, st)
	usage(t, st)
	reviews(t, st)
	// 068 brings a fact given up on back as pending; the body it writes must
	// read back here.
	ctx := context.Background()
	gave := model.Fact{ID: "mf_gaveup", Room: "r", Subject: model.Party{Name: "나", Self: true}, Relation: "LIKES",
		Object: model.Party{Name: "뿌링클"}, Text: "뿌링클을 좋아한다", Status: "failed", Tries: 12, Error: "끊김",
		NextTry: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err := st.PutFact(ctx, gave); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update mind_facts set status = 'pending', next_try = now(),
		body = jsonb_set(jsonb_set(jsonb_set(body, '{status}', '"pending"'), '{tries}', '0'), '{nextTry}', to_jsonb(now()))
		where status = 'failed'`); err != nil {
		t.Fatal(err)
	}
	back, err := st.Fact(ctx, "mf_gaveup")
	// now() is the database's clock, which may run a little ahead of this one.
	if err != nil || back.Status != model.FactPending || back.Tries != 0 || back.NextTry.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("068 의 되살림: %+v %v", back, err)
	}
	if due, _ := st.DueFacts(ctx, time.Now().Add(2*time.Second), 50); !slices.ContainsFunc(due, func(f model.Fact) bool { return f.ID == "mf_gaveup" }) {
		t.Fatalf("되살린 사실은 바로 다시 보냅니다: %+v", due)
	}
	// Last: its messages must not count in the others' rooms.
	messages(t, st)
	var sealed bool
	var body []byte
	_ = pool.QueryRow(context.Background(), `select sealed, body from mind_messages where ref = 'sim:1'`).Scan(&sealed, &body)
	if !sealed || string(body) == "강릉 갈까?" {
		t.Fatal("키가 있으면 원문이 봉해져야 합니다")
	}
}

// resetTables empties tables as the owner when scripts/dbtest gives one
// (JARVIS_TEST_ADMIN_DSN): the service's own role may not TRUNCATE, and the
// code under test runs as that role.
func resetTables(t *testing.T, dsn, sql string) {
	t.Helper()
	if admin := os.Getenv("JARVIS_TEST_ADMIN_DSN"); admin != "" {
		dsn = admin
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}
