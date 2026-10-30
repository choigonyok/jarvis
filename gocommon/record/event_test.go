package record

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var evNow = time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)

type read struct {
	Kind          string          `json:"kind"`
	Source        string          `json:"source"`
	SourceKey     string          `json:"source_key"`
	Actor         string          `json:"actor"`
	Subject       string          `json:"subject"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Tier          string          `json:"tier"`
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

func fromEvent(t *testing.T, v any) read {
	t.Helper()
	raw, _ := json.Marshal(v)
	out, err := FromEvent(raw, evNow)
	if err != nil {
		t.Fatal(err)
	}
	var r read
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTheEventShapeBecomesARecord(t *testing.T) {
	at := evNow.Add(-time.Minute)
	e := fromEvent(t, map[string]any{"source": "spending", "type": "occurrence.observed", "subject": "tx:1", "key": "occurrence:1",
		"data": map[string]any{"id": "x"}, "occurredAt": at, "notify": map[string]any{"tier": "now", "title": "결제", "body": "1원"}})
	if e.Kind != "occurrence.observed" || e.Source != "spending" || e.SourceKey != "occurrence:1" || e.Actor != "spending" ||
		e.Subject != "tx:1" || !e.OccurredAt.Equal(at) || e.Tier != Raw || e.SchemaVersion != 1 {
		t.Fatalf("%+v", e)
	}
	var p struct {
		ID     string `json:"id"`
		Notify Notify `json:"notify"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil || p.ID != "x" || p.Notify.Title != "결제" {
		t.Fatalf("%s %v", e.Payload, err)
	}
}

// The services that came first send the notification shape.
func TestTheNotificationShapeBecomesARecord(t *testing.T) {
	e := fromEvent(t, map[string]any{"source": "status", "kind": "status.fail", "tier": "now", "title": "멈춤", "level": "warn", "url": "/status"})
	if e.Kind != "status.fail" || !strings.HasPrefix(e.SourceKey, "ev-") || !e.OccurredAt.Equal(evNow) {
		t.Fatalf("%+v", e)
	}
	var p struct {
		Notify Notify `json:"notify"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	if p.Notify.Title != "멈춤" || p.Notify.Level != "warn" || p.Notify.URL != "/status" {
		t.Fatalf("%s", e.Payload)
	}
}

func TestWhatIsForTheGuestIsTheGuests(t *testing.T) {
	e := fromEvent(t, map[string]any{"source": "context", "type": "location.guest.enter", "notify": map[string]any{"tier": "log", "title": "도착", "audience": "guest"}})
	if e.Actor != GuestActor {
		t.Fatalf("%+v", e)
	}
	e = fromEvent(t, map[string]any{"source": "context", "type": "location.enter", "notify": map[string]any{"tier": "log", "title": "도착", "audience": "owner"}})
	if e.Actor != "context" {
		t.Fatalf("%+v", e)
	}
}

func TestABadEventIsSaidToBe(t *testing.T) {
	for name, m := range map[string]map[string]any{
		"no source": {"type": "a"},
		"no type":   {"source": "a"},
		"bad tier":  {"source": "a", "type": "b", "notify": map[string]any{"tier": "loud", "title": "x"}},
		"no title":  {"source": "a", "type": "b", "notify": map[string]any{"tier": "now"}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(m)
			if _, err := FromEvent(raw, evNow); err == nil {
				t.Fatal("거절해야 합니다")
			}
		})
	}
	if _, err := FromEvent([]byte("not json"), evNow); err == nil {
		t.Fatal("JSON 이 아닙니다")
	}
}
