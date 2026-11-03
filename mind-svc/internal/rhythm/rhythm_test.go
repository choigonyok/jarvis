package rhythm

import (
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

// card is a card mind rang with at hour h, n days ago, answered after `after`
// (0: never).
func card(now time.Time, daysAgo, h int, after time.Duration) model.Intent {
	at := time.Date(now.Year(), now.Month(), now.Day(), h, 10, 0, 0, seoul).AddDate(0, 0, -daysAgo)
	in := model.Intent{Card: true, Tier: "now", Decision: model.Ask, CreatedAt: at}
	if after > 0 {
		d := at.Add(after)
		in.DecidedAt, in.DecidedBy = &d, "user"
	}
	return in
}

// The operator answers in the evening and leaves the afternoon's cards for
// later: the afternoon is slow, the evening is where the digest is heard.
func TestTheHoursTheOperatorAnswersAt(t *testing.T) {
	now := time.Date(2026, 11, 3, 23, 0, 0, 0, seoul)
	var ins []model.Intent
	for d := 1; d <= 12; d++ {
		ins = append(ins, card(now, d, 15, 0))              // the afternoon: never in time
		ins = append(ins, card(now, d, 20, 10*time.Minute)) // the evening: at once
		ins = append(ins, card(now, d, 10, 3*time.Hour))    // the morning: found later, not heard
	}
	// Not rung: kept for the digest, raised in the conversation, answered by policy.
	quiet := card(now, 1, 15, time.Minute)
	quiet.Tier = "digest"
	chat := card(now, 1, 15, time.Minute)
	chat.Holder = "chat"
	ins = append(ins, quiet, chat, card(now, 90, 15, time.Minute))
	p := Build(ins, seoul, now)
	if p.Cards != 36 || p.Answered != 12 {
		t.Fatalf("울린 카드만 셉니다: %d %d", p.Cards, p.Answered)
	}
	if slow, why := p.Slow(time.Date(2026, 11, 4, 15, 30, 0, 0, seoul)); !slow || !strings.Contains(why, "15시") {
		t.Fatalf("오후는 늦게 답하는 때: %v %q", slow, why)
	}
	if slow, _ := p.Slow(time.Date(2026, 11, 4, 20, 30, 0, 0, seoul)); slow {
		t.Fatal("저녁은 바로 답하는 때입니다")
	}
	if h, ok := p.Evening(); !ok || h.Hour != 20 {
		t.Fatalf("저녁 요약은 20시가 가장 잘 들립니다: %+v %v", h, ok)
	}
	if p := Build(ins[:9], seoul, now); p.Known() {
		t.Fatal("카드가 적으면 아무것도 말하지 않습니다")
	} else if slow, _ := p.Slow(time.Date(2026, 11, 4, 15, 30, 0, 0, seoul)); slow {
		t.Fatal("모르면 늦다고 하지 않습니다")
	}
}
