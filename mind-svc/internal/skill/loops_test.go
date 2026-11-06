package skill

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// What the assistant promised in its own conversation ("알려 드릴게요") is
// told by the reminder it became: not again at 09:00 as "Jarvis가 하기로 한
// 것", nor in the morning's list as the operator's to-do. Someone else's
// promise passed on there still is.
func TestAssistantsPromiseIsNotToldAgain(t *testing.T) {
	ctx := context.Background()
	jarvis := model.Entity{ID: "loop_a", Kind: model.KindLoop, Room: model.OwnerRoom, State: model.State{
		Title: "캠핑 전날 장갑 챙기라고 알림", Status: model.Open, Owner: "them", Due: "2026-10-23"}}
	friend := model.Entity{ID: "loop_b", Kind: model.KindLoop, Room: model.OwnerRoom, State: model.State{
		Title: "돈 보내기", Status: model.Open, Owner: "them", People: []string{"지수"}, Due: "2026-10-23"}}
	v := view{plans: []model.Entity{jarvis, friend}, now: time.Date(2026, 10, 23, 9, 0, 0, 0, seoul)}
	due := model.Timer{Skill: "loops", Name: "due"}

	if r := (Loops{}).OnChange(ctx, v, jarvis, nil); !r.CancelTimers || len(r.Timers) != 0 {
		t.Fatalf("자비스의 약속에는 기한 타이머를 두지 않아야 합니다: %+v", r)
	}
	if r := (Loops{}).OnTimer(ctx, v, jarvis, due); len(r.Intents) != 0 {
		t.Fatalf("이미 잡혀 있던 타이머도 말하지 않아야 합니다: %+v", r.Intents)
	}
	if r := (Loops{}).OnTimer(ctx, v, friend, due); len(r.Intents) != 1 || !strings.Contains(r.Intents[0].Title, "지수가 하기로 한 것") {
		t.Fatalf("다른 사람의 약속은 그대로 알려야 합니다: %+v", r.Intents)
	}
	brief := (Brief{}).OnTick(ctx, v, "morning")
	if len(brief.Intents) != 1 || strings.Contains(brief.Intents[0].Body, "장갑") || !strings.Contains(brief.Intents[0].Body, "돈 보내기") {
		t.Fatalf("아침 목록에는 운영자 쪽 일만: %+v", brief.Intents)
	}
}

// The brief is the operator's day: the partner's own entries are not in it,
// and a loop long past its deadline is counted, not read out again - the
// 13th's to-do was in every brief for a fortnight (2026-10-27).
func TestBriefIsTheOperatorsDay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, seoul)
	mine := model.Entity{ID: "plan_a", Kind: model.KindPlan, State: model.State{Title: "승전과 톤 세팅", Status: model.Confirmed, Date: "2026-10-27", Start: "19:00"}}
	theirs := model.Entity{ID: "plan_b", Kind: model.KindPlan, State: model.State{Title: "독서모임", Status: model.Confirmed, Date: "2026-10-27", Start: "21:00", Owner: "partner"}}
	fresh := model.Entity{ID: "loop_a", Kind: model.KindLoop, State: model.State{Title: "은주에게 연락하기", Status: model.Open, Owner: "me", Due: "2026-10-26"}}
	stale := model.Entity{ID: "loop_b", Kind: model.KindLoop, State: model.State{Title: "재택근무 신청", Status: model.Open, Owner: "me", Due: "2026-10-13"}}
	later := model.Entity{ID: "loop_c", Kind: model.KindLoop, State: model.State{Title: "선물 고르기", Status: model.Open, Owner: "me", Due: "2026-11-02"}}
	v := view{plans: []model.Entity{mine, theirs, fresh, stale, later}, now: now}

	r := (Brief{}).OnTick(ctx, v, "morning")
	if len(r.Intents) != 1 {
		t.Fatalf("브리핑은 하나: %+v", r.Intents)
	}
	in := r.Intents[0]
	if !strings.HasPrefix(in.Title, "오늘 일정 1개 · 챙길 일 1개") {
		t.Fatalf("상대의 일정과 오래된 할 일은 세지 않아야 합니다: %q", in.Title)
	}
	for _, word := range []string{"독서모임", "재택근무 신청", "선물 고르기"} {
		if strings.Contains(in.Body, word) {
			t.Fatalf("%q 는 브리핑에 없어야 합니다: %s", word, in.Body)
		}
	}
	if !strings.Contains(in.Body, "은주에게 연락하기") || !strings.Contains(in.Body, "기한 지난 할 일 1개 더") {
		t.Fatalf("사흘 안의 할 일은 이름으로, 더 오래된 것은 수로: %s", in.Body)
	}
}

// A loop is told about once for its deadline: a note added later is the same
// loop, and its timer stays as it was - not set again for "now" when the time
// has gone by (2026-11-06: a second push 32 minutes after the first, when the
// order list was added). A deadline moved is told again.
func TestLoopIsToldOncePerDeadline(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 17, 59, 0, 0, seoul)
	v := view{now: now}
	made := model.State{Title: "책 돌려받기", Status: model.Open, Owner: "them", People: []string{"지수"}, Due: "2026-11-06"}
	e := model.Entity{ID: "loop_a", Kind: model.KindLoop, Room: "kakao:1", State: made}

	r := (Loops{}).OnChange(ctx, v, e, []model.Change{{After: made}})
	if !r.CancelTimers || len(r.Timers) != 1 || !r.Timers[0].FireAt.Equal(now) {
		t.Fatalf("새 고리는 기한 타이머를 건다(지난 시각이면 지금): %+v", r)
	}

	noted := made
	noted.Note = "두 권, 문 앞에 두기로"
	e.State = noted
	if r := (Loops{}).OnChange(ctx, v, e, []model.Change{{Before: &made, After: noted}}); r.CancelTimers || len(r.Timers) != 0 || len(r.Intents) != 0 {
		t.Fatalf("메모만 바뀐 고리는 타이머를 건드리지 않아야 합니다: %+v", r)
	}

	moved := noted
	moved.Due = "2026-11-08"
	e.State = moved
	r = (Loops{}).OnChange(ctx, v, e, []model.Change{{Before: &noted, After: moved}})
	if !r.CancelTimers || len(r.Timers) != 1 || !r.Timers[0].FireAt.Equal(time.Date(2026, 11, 8, 9, 0, 0, 0, seoul)) {
		t.Fatalf("기한이 바뀌면 그 기한으로 다시 건다: %+v", r)
	}

	reopened := moved
	reopened.Status = model.Open
	done := moved
	done.Status = model.Done
	if r := (Loops{}).OnChange(ctx, v, e, []model.Change{{Before: &done, After: reopened}}); len(r.Timers) != 1 {
		t.Fatalf("다시 열린 고리는 다시 건다: %+v", r)
	}
}

// The operator's own promise is not pushed: it is offered as a reminder on a
// card - when and in what words, both theirs to change - and rings only as
// the reminder it becomes (the operator's rule, 2026-11-06). Money owed is
// still a line at its deadline.
func TestOperatorsPromiseIsOfferedAsAReminder(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 17, 59, 0, 0, seoul)
	v := view{now: now}
	made := model.State{Title: "빵 사 가기", Status: model.Open, Owner: "me", People: []string{"지수"}, Due: "2026-11-06", Note: "소금빵 둘"}
	e := model.Entity{ID: "loop_b", Kind: model.KindLoop, Room: "kakao:1", State: made}

	r := (Loops{}).OnChange(ctx, v, e, []model.Change{{After: made}})
	if len(r.Timers) != 0 || len(r.Intents) != 1 {
		t.Fatalf("운영자의 약속은 푸시 타이머가 아니라 카드 하나: %+v", r)
	}
	in := r.Intents[0]
	if in.Kind != KindRemind || in.EntityID != "loop_b" || !in.Reversible || in.Topic != "" {
		t.Fatalf("리마인드 제안 카드여야 합니다: %+v", in)
	}
	if len(in.Fields) != 2 || in.Fields[0].Key != "at" || in.Fields[0].Value != "11/6(금) 18:30" || len(in.Fields[0].Options) != 3 || in.Fields[1].Value != "빵 사 가기" {
		t.Fatalf("시각(다음 30분부터 세 칸)과 문구를 고를 수 있어야 합니다: %+v", in.Fields)
	}
	var p struct {
		Loop, Text, Body, At string
		Choices              map[string]string
	}
	if err := json.Unmarshal(in.Payload, &p); err != nil || p.At != "2026-11-06T18:30" || p.Choices["11/6(금) 19:30"] != "2026-11-06T19:30" || p.Body != "소금빵 둘" || p.Loop != "loop_b" {
		t.Fatalf("카드의 값: %+v (%v)", p, err)
	}

	// A timed deadline: three hours before it.
	timed := made
	timed.Due = "2026-11-07T19:00"
	e.State = timed
	r = (Loops{}).OnChange(ctx, v, e, []model.Change{{Before: &made, After: timed}})
	if len(r.Intents) != 1 || r.Intents[0].Fields[0].Value != "11/7(토) 16:00" {
		t.Fatalf("시각이 있는 기한은 세 시간 전: %+v", r.Intents)
	}

	// A timer kept from before: a card then too, not a push.
	if r := (Loops{}).OnTimer(ctx, v, e, model.Timer{Name: "due"}); len(r.Intents) != 1 || r.Intents[0].Kind != KindRemind {
		t.Fatalf("남아 있던 타이머도 카드로: %+v", r)
	}

	// Long past: not offered.
	stale := made
	stale.Due = "2026-11-01"
	e.State = stale
	if r := (Loops{}).OnChange(ctx, v, e, []model.Change{{After: stale}}); len(r.Intents) != 0 || len(r.Timers) != 0 {
		t.Fatalf("하루 넘게 지난 기한은 묻지 않는다: %+v", r)
	}

	owed := model.State{Title: "모임비 받기", Status: model.Open, Owner: "me", Type: "정산", Due: "2026-11-09"}
	e = model.Entity{ID: "loop_c", Kind: model.KindLoop, Room: "kakao:1", State: owed}
	if r := (Loops{}).OnChange(ctx, v, e, []model.Change{{After: owed}}); len(r.Timers) != 1 || len(r.Intents) != 0 {
		t.Fatalf("정산은 그대로 기한에 한 줄: %+v", r)
	}
}
