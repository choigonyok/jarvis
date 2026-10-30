package mind

import (
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
)

// Behind by how long says how bad it is, and no key says it whatever else.
func TestHowReadingStands(t *testing.T) {
	now := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)
	st := func(ready bool, behind time.Duration) Status {
		s := Status{LLM: LLMState{Ready: ready}}
		if behind > 0 {
			at := now.Add(-behind)
			s.OldestUnread = &at
		}
		return s
	}
	for _, c := range []struct {
		s    Status
		want string
	}{
		{st(true, 0), checks.OK},
		{st(true, 40 * time.Minute), checks.Warn},
		{st(true, 3 * time.Hour), checks.Fail},
		{st(false, 0), checks.Fail},
	} {
		if got := reading(c.s, now); got.State != c.want || got.Summary == "" {
			t.Fatalf("%+v", got)
		}
	}
}

func TestHowRememberingStands(t *testing.T) {
	now := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)
	at := now.Add(-2 * time.Hour)
	if got := remembering(Status{Memory: true, Facts: map[string]int{"pending": 1}, OldestPending: &at}, now); got.State != checks.Warn {
		t.Fatalf("%+v", got)
	}
	if got := remembering(Status{Memory: false}, now); got.State != checks.Fail {
		t.Fatalf("%+v", got)
	}
	if got := remembering(Status{Memory: true}, now); got.State != checks.OK || got.ObservedAt != nil {
		t.Fatalf("%+v", got)
	}
}
