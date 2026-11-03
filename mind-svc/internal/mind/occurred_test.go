package mind

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// flaky is a store whose reads of an occurrence fail until told not to.
type flaky struct {
	*store.Memory
	down bool
}

func (f *flaky) Occurrence(ctx context.Context, id string) (model.Occurrence, error) {
	if f.down {
		return model.Occurrence{}, errors.New("db down")
	}
	return f.Memory.Occurrence(ctx, id)
}

// An occurrence that cannot be read now is tried again, not taken for one that
// is not there; one that is not there is nothing to hear.
func TestAnOccurrenceNotReadIsTriedAgain(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.New()
	clk.Pin(time.Date(2026, 11, 3, 12, 0, 0, 0, seoul))
	st := &flaky{Memory: store.NewMemory()}
	m := New(Options{Store: st, Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(), Notifier: &act.MemoryNotifier{},
		Loc: seoul, Now: clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"}, Policy: gate.DefaultPolicy(), Mode: "eval"})
	ctx := context.Background()
	if err := st.PutOccurrence(ctx, pay("spending:1", "합정식당", "식비", 12000, clk.Now())); err != nil {
		t.Fatal(err)
	}
	st.down = true
	if err := m.occurred(ctx, "spending:1"); err == nil {
		t.Fatal("읽지 못한 발생은 다시 와야 합니다")
	}
	st.down = false
	if err := m.occurred(ctx, "spending:1"); err != nil {
		t.Fatal(err)
	}
	if err := m.occurred(ctx, "spending:없음"); err != nil {
		t.Fatalf("없는 발생은 들을 것이 없습니다: %v", err)
	}
}
