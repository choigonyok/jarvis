package book

import (
	"encoding/json"
	"strings"
	"testing"
)

// A session says who made it when that is known, and says nothing when it is
// not: one from before is read as it always was, with no by to misread.
func TestASessionSaysWhoMadeItOnlyWhenKnown(t *testing.T) {
	old := `{"date":"2026-10-01","id":"w-old","exercises":[],"startedAt":1790000000000}`
	var s Session
	if err := json.Unmarshal([]byte(old), &s); err != nil || s.By != "" {
		t.Fatalf("예전 세션: %+v %v", s, err)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"by"`) {
		t.Fatalf("모르는 주인은 말하지 않습니다: %s", out)
	}
	s.By = "guest"
	if out, _ = json.Marshal(s); !strings.Contains(string(out), `"by":"guest"`) {
		t.Fatalf("아는 주인은 말합니다: %s", out)
	}
}
