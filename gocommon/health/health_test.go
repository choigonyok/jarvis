package health

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBackoffGrowsFourfoldUpToTheCap(t *testing.T) {
	want := []time.Duration{time.Minute, 4 * time.Minute, 16 * time.Minute, 64 * time.Minute, 4 * time.Hour, 4 * time.Hour}
	for i, w := range want {
		if got := Backoff(i+1, time.Minute, 4*time.Hour); got != w {
			t.Errorf("try %d: %v, want %v", i+1, got, w)
		}
	}
	if got := Backoff(0, time.Second, time.Minute); got != time.Second {
		t.Errorf("before any try: %v", got)
	}
}

// A loop goes stale by age alone: one that is wedged records nothing at all,
// and that is the silent stop the contract is for.
func TestALoopGoesStaleWithoutARoundThatGotThrough(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	l := NewLoops()
	l.now = func() time.Time { return now }
	l.Add("ingest", "카드 알림 수집", 10*time.Minute)

	now = now.Add(9 * time.Minute)
	if _, stale := l.List()[0].Stalled(now); stale {
		t.Fatal("시작하고 9분: 아직 멈춘 게 아닙니다")
	}
	now = now.Add(2 * time.Minute)
	if d, stale := l.List()[0].Stalled(now); !stale || d != 11*time.Minute {
		t.Fatalf("한 번도 지나가지 못한 채 11분: %v %v", d, stale)
	}

	l.Fail("ingest", errors.New("메시지 a1: 끊김"))
	l.OK("ingest")
	got := l.List()[0]
	if _, stale := got.Stalled(now); stale || got.Failing != 0 || got.LastError == "" {
		t.Fatalf("지나간 뒤에는 멈춤이 아니고, 지난 오류는 남습니다: %+v", got)
	}
	l.Fail("ingest", errors.New("또"))
	l.Fail("ingest", errors.New("또"))
	if got := l.List()[0]; got.Failing != 2 {
		t.Fatalf("연달아 실패 %d", got.Failing)
	}
	if _, stale := (Loop{StartedAt: now.Add(-time.Hour)}).Stalled(now); stale {
		t.Fatal("StaleSec 이 없으면 나이로 판단하지 않습니다")
	}
}

func TestAnUnknownLoopIsAddedWhenFirstHeardOf(t *testing.T) {
	l := NewLoops()
	l.OK("x")
	since := time.Now()
	l.Waiting("x", &since)
	if got := l.List(); len(got) != 1 || got[0].Behind == nil || got[0].LastOK == nil {
		t.Fatalf("%+v", got)
	}
	l.Waiting("x", nil)
	if l.List()[0].Behind != nil {
		t.Fatal("기다리는 것이 없으면 비웁니다")
	}
	var none *Loops
	none.OK("x")
	none.Fail("x", errors.New("e"))
	if none.List() != nil {
		t.Fatal("nil 은 아무것도 하지 않습니다")
	}
}

func TestWriteSaysOKOrDown(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, http.StatusOK, Report{Held: &Held{Count: 2, Where: "/spending"}})
	var r Report
	if err := json.NewDecoder(rec.Body).Decode(&r); err != nil || r.Status != "ok" || r.Held.Count != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	rec = httptest.NewRecorder()
	Write(rec, http.StatusServiceUnavailable, Report{})
	_ = json.NewDecoder(rec.Body).Decode(&r)
	if rec.Code != http.StatusServiceUnavailable || r.Status != "down" {
		t.Fatalf("%d %+v", rec.Code, r)
	}
}
