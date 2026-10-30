package checks

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/record"
)

// Said when it changes and on the beat: a row that stays as it is costs one
// record an hour, and a silence means something.
func TestAResultIsSaidOnceUntilItChanges(t *testing.T) {
	w, mem := record.NewMemory(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	now := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)
	r := &Reporter{Source: "t", Records: w, Now: func() time.Time { return now }}
	said := func(want int) {
		t.Helper()
		for i := 0; i < 100 && len(mem.All()) < want; i++ {
			time.Sleep(5 * time.Millisecond)
		}
		if got := len(mem.All()); got != want {
			t.Fatalf("%d건, %d건이어야 합니다", got, want)
		}
	}
	r.Report(Result{ID: "a", State: OK, Summary: "좋아요"})
	said(1)
	now = now.Add(time.Minute)
	r.Report(Result{ID: "a", State: OK, Summary: "좋아요"})
	said(1)
	r.Report(Result{ID: "a", State: Warn, Summary: "주의"})
	said(2)
	now = now.Add(Beat)
	r.Report(Result{ID: "a", State: Warn, Summary: "주의"})
	said(3)
	r.Report(Result{ID: "b", State: Warn, Summary: "주의"})
	said(4)
	if got := mem.All()[3]; got.Kind != Kind || got.Subject != "b" || got.Tier != record.Raw {
		t.Fatalf("%+v", got)
	}
	var none *Reporter
	none.Report(Result{ID: "a"})
}
