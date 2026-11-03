package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// The noes read again are mind's and the conversation's in the operator's own
// words, newest first, each once: not the one-tap answers, not a question of
// mind's the conversation's card answered (that card says it), not a card the
// agent never told mind of.
func TestTurnedDownIsMindsAndTheConversations(t *testing.T) {
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	entry := func(title string) json.RawMessage {
		b, _ := json.Marshal(CalendarAction{Event: CalendarEvent{Title: title}})
		return b
	}
	mine := []Intent{
		{Kind: "calendar.create", Title: "캘린더에 추가: 11/7(토) 19:00 민수와 저녁", Status: Rejected, Error: "저녁 약속은 넣지 마", CreatedAt: at.Add(-time.Hour)},
		{Kind: "calendar.create", Title: "캘린더에 추가: 치과", Status: Rejected, Error: "틀렸어", CreatedAt: at},
		{Kind: "calendar.create", Title: "캘린더에 추가: 쏘카 대여", Status: Rejected, Error: "차 예약은 일정 카드 필요없어", DecidedBy: ByConversation, CreatedAt: at},
		{Kind: "calendar.create", Title: "캘린더에 추가: 헬스", Status: Superseded, Error: "대화에서 같은 카드를 먼저 올렸어요 (p-3)", CreatedAt: at},
		{Kind: "review.place", Title: "회고", Status: Rejected, Error: "별로였어", CreatedAt: at},
	}
	held := []Intent{
		{Kind: HeldCalendarCreate, Title: "일정을 추가합니다", Payload: entry("쏘카 대여"), Status: Rejected, Error: " 차 예약은 일정 카드 필요없어 ", CreatedAt: at.Add(time.Minute)},
		{Kind: HeldCalendarCreate, Title: "일정을 추가합니다", Status: Rejected, Error: "손님의 말", CreatedAt: at.Add(2 * time.Minute)},
		{Kind: HeldCalendarCreate, Payload: entry("쏘카 대여"), Status: Rejected, Error: "차 예약은 일정 카드 필요없어", CreatedAt: at.Add(-2 * time.Hour)},
		{Kind: HeldCalendarCreate, Payload: entry("수영"), Status: Executed, Error: "", CreatedAt: at},
	}
	want := []Turned{
		{Title: "쏘카 대여", Reason: "차 예약은 일정 카드 필요없어", At: at.Add(time.Minute)},
		{Title: "11/7(토) 19:00 민수와 저녁", Reason: "저녁 약속은 넣지 마", At: at.Add(-time.Hour)},
	}
	if got := TurnedDown(mine, held, 12); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if got := TurnedDown(mine, held, 1); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("가장 최근 것부터 max 개: %+v", got)
	}
}
