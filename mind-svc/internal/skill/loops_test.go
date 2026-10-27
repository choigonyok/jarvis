package skill

import (
	"context"
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
