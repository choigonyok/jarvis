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
