package mind

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// The operator's own promise comes as a card offering a reminder, and
// approving it - with the time and the words the operator left - is what
// keeps one; nothing is pushed for the loop itself. The loop's note changed
// afterwards asks nothing again.
func TestPromiseIsOfferedAndApprovedAsAReminder(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 11, 6, 17, 59, 0, 0, seoul), nil)
	ctx := context.Background()
	e, err := f.m.World.Create(ctx, model.KindLoop, "kakao:1", model.State{
		Title: "빵 사 가기", Status: model.Open, Owner: "me", People: []string{"지수"}, Due: "2026-11-06", Note: "소금빵 둘",
	}, world.Meta{Actor: "interpreter"})
	if err != nil {
		t.Fatal(err)
	}
	f.m.Attn.Settle(ctx)

	var card model.Intent
	for _, in := range f.intents("") {
		if in.Kind == skill.KindRemind {
			card = in
		}
		if in.Kind == "info" {
			t.Fatalf("운영자의 약속은 푸시하지 않아야 합니다: %+v", in)
		}
	}
	if card.ID == "" || !card.Card || card.Status != model.Pending {
		t.Fatalf("리마인드 제안 카드가 떠야 합니다: %+v", f.intents(""))
	}
	if !strings.Contains(card.Preview, "11/6(금) 18:30에 폰으로 알립니다: 빵 사 가기") {
		t.Fatalf("미리보기: %q", card.Preview)
	}

	// The note grows before anyone answers: no second card, no push.
	if _, _, err := f.m.World.Update(ctx, e.ID, func(s *model.State) bool { s.Note = "소금빵 둘, 우유 하나"; return true }, world.Meta{Actor: "interpreter"}); err != nil {
		t.Fatal(err)
	}
	f.m.Attn.Settle(ctx)
	if n := len(f.intents("")); n != 1 {
		t.Fatalf("메모가 바뀌어도 다시 묻지 않아야 합니다: %d개", n)
	}

	done, err := f.m.Gate.ApproveWith(ctx, card.ID, map[string]string{"at": "11/6(금) 19:30", "text": "빵 사 가세요"})
	if err != nil || done.Status != model.Executed {
		t.Fatalf("승인: %+v %v", done, err)
	}
	rs, err := f.m.Moments.List(ctx, false)
	if err != nil || len(rs) != 1 {
		t.Fatalf("리마인드 하나: %+v %v", rs, err)
	}
	r := rs[0]
	if r.Text != "빵 사 가세요" || r.Body != "소금빵 둘" || r.At == nil || !r.At.Equal(f.at(19, 30)) || r.Source != moments.Asked || r.Topic != "loop:"+e.ID {
		t.Fatalf("고른 시각과 문구로: %+v", r)
	}

	// Undone, it is cancelled.
	if _, err := f.m.Gate.Undo(ctx, card.ID); err != nil {
		t.Fatal(err)
	}
	if rs, _ := f.m.Moments.List(ctx, false); len(rs) != 0 {
		t.Fatalf("되돌리면 리마인드도 취소: %+v", rs)
	}
}

// A reminder for a place on the map goes by the fixes: one outside, then one
// inside after it starts to count, is the arrival - no region named for it,
// and nothing polled.
func TestAreaReminderGoesByTheFixes(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 11, 6, 18, 38, 0, 0, seoul), nil)
	ctx := context.Background()
	city := &moments.Area{Name: "가상시", Rings: [][][2]float64{{{128.00, 36.00}, {128.20, 36.00}, {128.20, 36.15}, {128.00, 36.15}}}}
	from, until := f.at(19, 30), f.at(23, 30)
	if _, err := f.m.Moments.Set(ctx, moments.Reminder{Text: "빵 사 가세요", Place: "가상", Area: city, Move: moments.Arrive, After: &from, Until: &until}); err != nil {
		t.Fatal(err)
	}
	fix := func(h, m int, lat, lon float64) {
		f.clk.Pin(f.at(h, m))
		data, _ := json.Marshal(map[string]float64{"lat": lat, "lon": lon, "acc": 12})
		f.handle(t, obs.Observation{Source: "context", Type: model.KindLocationFix, Data: data, OccurredAt: f.clk.Now()})
	}
	fix(19, 40, 36.40, 128.30) // on the way, outside
	fix(20, 10, 36.25, 128.15)
	if n := len(f.notice.All()); n != 0 {
		t.Fatalf("밖에서는 울리지 않는다: %d", n)
	}
	fix(20, 31, 36.10, 128.07) // in
	all := f.notice.All()
	if len(all) != 1 || all[0].Title != "빵 사 가세요" || !strings.Contains(all[0].Body, "가상에 도착하셔서") {
		t.Fatalf("들어온 순간 한 번: %+v", all)
	}
	fix(20, 40, 36.10, 128.072)
	if n := len(f.notice.All()); n != 1 {
		t.Fatalf("한 번만: %d", n)
	}
}
