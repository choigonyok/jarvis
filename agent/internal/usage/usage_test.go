package usage

import (
	"path/filepath"
	"testing"
	"time"
)

const event = `{"status":"allowed","resetsAt":1791429000,"rateLimitType":"five_hour",
 "unifiedWindows":{"five_hour":{"utilization":0.17,"resetsAt":1791429000},"seven_day":{"utilization":0.12,"resetsAt":1791990000}}}`

func TestObserveSnapshotAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	tr := New(path)
	if !tr.Stale(time.Now(), time.Hour) {
		t.Fatal("nothing read yet must be stale")
	}
	tr.Observe([]byte(event))
	u := tr.Snapshot(time.Unix(1791400000, 0))
	if u.FiveHour == nil || u.FiveHour.Utilization != 0.17 || u.SevenDay.Utilization != 0.12 || u.Status != "allowed" {
		t.Fatalf("%+v", u)
	}
	if tr.Stale(time.Now(), time.Hour) {
		t.Fatal("just read must not be stale")
	}
	// Past the 5-hour reset the old number no longer holds.
	if u := tr.Snapshot(time.Unix(1791430000, 0)); u.FiveHour.Utilization != 0 || u.SevenDay.Utilization != 0.12 {
		t.Fatalf("after reset: %+v %+v", u.FiveHour, u.SevenDay)
	}
	// Survives a restart.
	if u := New(path).Snapshot(time.Unix(1791400000, 0)); u.FiveHour == nil || u.FiveHour.Utilization != 0.17 {
		t.Fatalf("reloaded: %+v", u)
	}
	// An event without windows changes nothing.
	tr.Observe([]byte(`{"status":"allowed"}`))
	if u := tr.Snapshot(time.Unix(1791400000, 0)); u.FiveHour.Utilization != 0.17 {
		t.Fatal("empty event overwrote the reading")
	}
}
