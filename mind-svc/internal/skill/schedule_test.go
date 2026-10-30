package skill

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// An entry like one turned down lately is not asked again: the same title
// on another day, or one that carries the word the reason was about. An
// entry that shares neither is asked as before (2026-10-25: "롤체 약속은
// 캘린더에 추가하지마", and "은주와 롤체" was asked twice more the next day).
func TestEntryLikeOneTurnedDownIsNotAskedAgain(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, seoul)
	at := now.Add(-24 * time.Hour)
	payload, _ := json.Marshal(model.CalendarAction{Event: model.CalendarEvent{Title: "은주와 롤체", Date: "2026-10-25", Start: "23:20", Owner: "me"}})
	v := view{now: now, intents: []model.Intent{{
		ID: "int_old", Kind: "calendar.create", EntityID: "plan_old", Payload: payload, Status: model.Rejected,
		Error: "롤체 약속은 캘린더에 추가하지마", CreatedAt: at, DecidedAt: &at,
	}}}
	plan := func(id, title string) model.Entity {
		return model.Entity{ID: id, Kind: model.KindPlan, State: model.State{Title: title, Status: model.Confirmed, Date: "2026-10-26", Start: "18:00"}}
	}
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_same", "은주와 롤체"), nil); ok {
		t.Fatal("거절한 것과 같은 제목은 다시 묻지 않아야 합니다")
	}
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_kind", "롤체 대회 같이 보기"), nil); ok {
		t.Fatal("거절 사유가 말한 것(롤체)이 든 일정도 묻지 않아야 합니다")
	}
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_other", "은주와 저녁"), nil); !ok {
		t.Fatal("다른 일정은 그대로 물어야 합니다")
	}

	// A no without a word in common only covers that title.
	v.intents[0].Error = "틀렸어"
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_kind2", "롤체 대회 같이 보기"), nil); !ok {
		t.Fatal("이유가 제목의 말을 짚지 않으면 같은 제목만 거릅니다")
	}

	// Long ago, it is forgotten.
	old := now.Add(-40 * 24 * time.Hour)
	v.intents[0].Error, v.intents[0].CreatedAt, v.intents[0].DecidedAt = "롤체 약속은 캘린더에 추가하지마", old, &old
	if _, ok := (Schedule{}).add(ctx, v, plan("plan_later", "은주와 롤체"), nil); !ok {
		t.Fatal("한 달이 지난 거절은 더 보지 않습니다")
	}
}

func TestAlikeWords(t *testing.T) {
	if got := words("롤체 약속은 캘린더에 추가하지마."); len(got) != 4 || got[0] != "롤체" || got[1] != "약속" || got[2] != "캘린더" || got[3] != "추가하지마" {
		t.Fatalf("words: %v", got)
	}
	if alike("승전과 톤 세팅 만남", "은주 일정은 넣지 마", "은주와 저녁") {
		t.Fatal("거절한 제목에 없는 말로는 거르지 않습니다")
	}
	if !alike("은주와 롤체", "은주 일정은 넣지 마", "은주와 저녁") {
		t.Fatal("거절한 제목과 이유가 함께 짚은 말(은주)은 거릅니다")
	}
}

// A day gone by is not asked about: read late, the card came for what had
// already happened.
func TestADayGoneByIsNotAskedAbout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, seoul)
	v := view{now: now}
	past := model.Entity{ID: "plan_a", State: model.State{Title: "저녁", Date: "2026-10-25", Status: model.Confirmed}}
	if _, ok := (Schedule{}).add(ctx, v, past, nil); ok {
		t.Fatal("지난 날은 묻지 않습니다")
	}
	today := past
	today.State.Date = "2026-10-26"
	if _, ok := (Schedule{}).add(ctx, v, today, nil); !ok {
		t.Fatal("오늘은 묻습니다")
	}
}
