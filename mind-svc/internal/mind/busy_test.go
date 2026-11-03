package mind

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
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
		if err := m.StateSaid(ctx, queue.Message{Kind: KindState, Payload: []byte(`{"state":` + state + `}`)}); err != nil {
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
	if err := m.StateSaid(ctx, queue.Message{Kind: KindState, Payload: []byte(`잘못`)}); err == nil {
		t.Fatal("읽지 못하는 것은 받지 않습니다")
	}
}
