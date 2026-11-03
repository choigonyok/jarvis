package link

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// The judgement reads the noes by number, the entry, and what was said just now;
// it names one of them or none, and an answer it cannot give is none.
func TestTurnedJudgeNamesOneOrNone(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	past := []model.Turned{
		{Title: "쏘카 대여", Reason: "차 예약은\n일정 카드 필요없어", At: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)},
		{Title: "민수와 저녁", Reason: "저녁 약속은 넣지 마", At: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)},
	}
	ev := model.CalendarEvent{Title: "그린카 대여", Date: "2026-11-24", Start: "10:00", Memo: "강남역\n3번 출구"}
	l := &sameLLM{answer: `판단: {"turned":1}`}
	j := &TurnedJudge{LLM: l, Log: quiet}
	if n := j.Of(ctx, past, ev, "그린카  예약했어"); n != 1 || l.purpose != llm.Judge {
		t.Fatalf("%d %s", n, l.purpose)
	}
	want := "반려하며 남긴 말:\n1. '쏘카 대여' 카드 (11/3): 차 예약은 일정 카드 필요없어\n2. '민수와 저녁' 카드 (10/30): 저녁 약속은 넣지 마\n" +
		"올리려는 일정: '그린카 대여' 2026-11-24 10:00 메모: 강남역 3번 출구\n운영자가 방금 한 말: “그린카 예약했어”"
	if l.user != want {
		t.Fatalf("%q", l.user)
	}
	if !strings.Contains(TurnedPrompt(past, model.CalendarEvent{Title: "캠핑", Date: "2026-11-24", EndDate: "2026-11-25"}, ""), "'캠핑' 2026-11-24 ~2026-11-25\n운영자가 방금 한 말: 없음") {
		t.Fatal(TurnedPrompt(past, model.CalendarEvent{Title: "캠핑", Date: "2026-11-24", EndDate: "2026-11-25"}, ""))
	}
	for _, bad := range []*sameLLM{{answer: `{"turned":3}`}, {answer: `{"turned":-1}`}, {answer: "모르겠어요"}, {err: errors.New("down")}} {
		if n := (&TurnedJudge{LLM: bad, Log: quiet}).Of(ctx, past, ev, ""); n != 0 {
			t.Fatalf("%+v: %d", bad, n)
		}
	}
	none := &sameLLM{answer: `{"turned":0}`}
	if n := (&TurnedJudge{LLM: none, Log: quiet}).Of(ctx, nil, ev, ""); n != 0 || none.asked != 0 {
		t.Fatal("반려한 것이 없으면 묻지 않습니다")
	}
}
