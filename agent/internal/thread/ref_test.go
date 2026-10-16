package thread

import (
	"strings"
	"testing"
	"time"
)

// 2: a quote keeps who said it, the day and time, and only the start of a
// long message.
func TestRefTo(t *testing.T) {
	refZone = time.FixedZone("KST", 9*3600)
	// The database may write the stamp in UTC; the quote still says Seoul time.
	r := RefTo(&Turn{ID: "a-1", Role: RoleAgent, At: "22:31", AtISO: "2026-10-16T13:31:05.123456+00:00",
		Paragraphs: []string{"알람시계 T872", strings.Repeat("가", 700)}})
	if r.ID != "a-1" || r.Role != RoleAgent || r.At != "10-16 22:31" {
		t.Fatalf("누가·언제: %+v", r)
	}
	if !strings.HasPrefix(r.Text, "알람시계 T872\n\n") || len([]rune(r.Text)) > 601 {
		t.Fatalf("앞부분만: %d자", len([]rune(r.Text)))
	}
}
