package mind

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// What context-svc says of the operator's state is mind's to weigh: driving
// before a meeting before a focus, nothing once it ends or has gone unsaid
// too long.
func TestMindHearsTheOperatorsState(t *testing.T) {
	at := time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC)
	m, _, clk := testMind(t, at)
	ctx := context.Background()
	say := func(state string) {
		t.Helper()
		if err := m.StateSaid(ctx, queue.Message{Kind: model.KindContextState, Payload: []byte(`{"state":` + state + `}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if m.Busy() != "" {
		t.Fatal("들은 것이 없으면 바쁘지 않습니다")
	}
	say(`{"driving":true,"meeting":true}`)
	if m.Busy() != "운전 중" {
		t.Fatal(m.Busy())
	}
	say(`{"focus":true}`)
	if m.Busy() != "집중 모드" {
		t.Fatal(m.Busy())
	}
	clk.Pin(at.Add(busyLapse + time.Minute))
	if m.Busy() != "" {
		t.Fatal("끝났다는 말 없이 오래되면 믿지 않습니다")
	}
	say(`{}`)
	if m.Busy() != "" {
		t.Fatal(m.Busy())
	}
	if err := m.StateSaid(ctx, queue.Message{Kind: model.KindContextState, Payload: []byte(`잘못`)}); err == nil {
		t.Fatal("읽지 못하는 것은 받지 않습니다")
	}
}

// An hour the operator leaves mind's cards for later is one the gate weighs a
// buzz at as it does while they drive - and what context-svc says comes first.
func TestAnHourAnsweredLateIsBusy(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	at := time.Date(2026, 11, 3, 15, 30, 0, 0, seoul)
	m, st, clk := testMind(t, at)
	ctx := context.Background()
	for d := 1; d <= 12; d++ {
		for _, h := range []int{15, 20} {
			raised := time.Date(2026, 11, 3, h, 5, 0, 0, seoul).AddDate(0, 0, -d)
			in := model.Intent{ID: fmt.Sprintf("in-%d-%d", d, h), Card: true, Tier: "now", Decision: model.Ask, Status: model.Pending, CreatedAt: raised}
			if h == 20 {
				answered := raised.Add(5 * time.Minute)
				in.DecidedAt, in.DecidedBy, in.Status = &answered, "user", model.Executed
			}
			if err := st.PutIntent(ctx, in); err != nil {
				t.Fatal(err)
			}
		}
	}
	if why := m.BusyOrSlow(); !strings.Contains(why, "15시") {
		t.Fatalf("오후 세 시는 답이 늦는 때입니다: %q", why)
	}
	if err := m.StateSaid(ctx, queue.Message{Kind: model.KindContextState, Payload: []byte(`{"state":{"driving":true}}`)}); err != nil {
		t.Fatal(err)
	}
	if why := m.BusyOrSlow(); why != "운전 중" {
		t.Fatalf("들은 상태가 먼저입니다: %q", why)
	}
	if err := m.StateSaid(ctx, queue.Message{Kind: model.KindContextState, Payload: []byte(`{"state":{}}`)}); err != nil {
		t.Fatal(err)
	}
	clk.Pin(time.Date(2026, 11, 4, 20, 30, 0, 0, seoul))
	if why := m.BusyOrSlow(); why != "" {
		t.Fatalf("저녁은 바로 답하는 때: %q", why)
	}
}
