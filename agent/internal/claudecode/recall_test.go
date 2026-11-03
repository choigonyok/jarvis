package claudecode

import (
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// A short message is searched with the exchange it follows; a long one, or
// one after a long pause, alone.
func TestAShortMessageIsSearchedWithWhatItFollows(t *testing.T) {
	now := time.Date(2026, 11, 3, 21, 0, 0, 0, time.Local)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }
	turns := []*thread.Turn{
		{Role: thread.RoleUser, Text: "어제 얘기", AtISO: at(3 * time.Hour)},
		{Role: thread.RoleUser, Text: "토요일에 양평 캠핑장 예약할까?", AtISO: at(10 * time.Minute)},
		{Role: thread.RoleAgent, Paragraphs: []string{"소나무숲 캠핑장이", "토요일 자리가 있어요."}, AtISO: at(9 * time.Minute)},
		{Role: thread.RoleAgent, ProposalID: "p-1", AtISO: at(9 * time.Minute)},
		{Role: thread.RoleAgent, Origin: &thread.Origin{Kind: thread.OriginJob}, Paragraphs: []string{"작업 소식"}, AtISO: at(5 * time.Minute)},
		{Role: thread.RoleUser, Text: "응 그거 해줘", AtISO: at(time.Minute)},
	}
	if got := withExchange("응 그거 해줘", turns, now); got != "응 그거 해줘 / 소나무숲 캠핑장이 토요일 자리가 있어요. / 토요일에 양평 캠핑장 예약할까?" {
		t.Fatalf("%q", got)
	}
	if got := withExchange("응 그거 해줘", turns[:1], now); got != "응 그거 해줘" {
		t.Fatalf("오래 쉰 뒤의 말은 그것만: %q", got)
	}
	long := "다음 주 토요일 양평 캠핑장 예약하고 장작도 같이 주문해 줄 수 있어?"
	r := runner(t, "")
	if got := r.recallQuery(long, now); got != long {
		t.Fatalf("긴 말은 그것만: %q", got)
	}
}
