package rules

import (
	"testing"
	"time"
)

func TestAfterRun(t *testing.T) {
	if e := AfterRun(OK, true, 2, 0, ""); e.Retry || e.Fail || e.Attempts != 0 {
		t.Fatalf("decided run: %+v", e)
	}
	if e := AfterRun(Yielded, false, 2, 0, ""); !e.Retry || e.Fail || e.Attempts != 2 || e.After != YieldAfter {
		t.Fatalf("yield: %+v", e)
	}
	// Each run in a row that gave way doubles the wait, up to an hour.
	for yields, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour} {
		if e := AfterRun(Yielded, false, 0, yields, ""); e.After != want || e.Attempts != 0 {
			t.Fatalf("yield after %d: %+v, want %v", yields, e, want)
		}
	}
	if e := AfterRun(OK, false, 0, 0, ""); !e.Retry || e.Attempts != 1 || e.Note == "" {
		t.Fatalf("undecided: %+v", e)
	}
	if e := AfterRun(Error, true, 1, 0, "timeout"); !e.Retry || e.Attempts != 2 || e.Note != "timeout" {
		t.Fatalf("error: %+v", e)
	}
	if e := AfterRun(Error, false, MaxAttempts-1, 0, "x"); !e.Fail {
		t.Fatalf("third failure must stop the job: %+v", e)
	}
}

func TestClampNext(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	if got := ClampNext(now, now.Add(time.Second)); !got.Equal(now.Add(MinGap)) {
		t.Fatal(got)
	}
	if got := ClampNext(now, now.Add(2*time.Hour)); !got.Equal(now.Add(2 * time.Hour)) {
		t.Fatal(got)
	}
	if got := ClampNext(now, now.Add(90*24*time.Hour)); !got.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Fatal(got)
	}
}

func TestHost(t *testing.T) {
	for in, want := range map[string]string{
		"https://web.joongna.com/product/form": "web.joongna.com",
		" WEB.JOONGNA.COM ":                    "web.joongna.com",
		"nid.naver.com?x=1":                    "nid.naver.com",
	} {
		if got := Host(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
