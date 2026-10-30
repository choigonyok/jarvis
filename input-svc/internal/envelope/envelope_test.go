package envelope

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)

func TestARecordComesThroughAsItIs(t *testing.T) {
	at := now.Add(-time.Hour)
	raw, _ := json.Marshal(map[string]any{
		"kind": "guard.query", "source": "agent", "source_key": "q-1", "actor": "guard", "channel": "browser",
		"subject": "coupang.com", "occurred_at": at, "tier": "durable", "schema_version": 2, "trace_id": "job:9",
		"causation_id": "q-0", "payload": map[string]any{"a": "b"},
	})
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != "guard.query" || e.Source != "agent" || e.SourceKey != "q-1" || e.Actor != "guard" || e.Channel != "browser" ||
		e.Subject != "coupang.com" || !e.OccurredAt.Equal(at) || e.Tier != "durable" || e.SchemaVersion != 2 ||
		e.TraceID != "job:9" || e.CausationID != "q-0" || string(e.Payload) != `{"a":"b"}` {
		t.Fatalf("%+v", e)
	}
	if e.IsGuest() {
		t.Fatal("손님 것이 아닙니다")
	}
}

func TestAnEnvelopeThatIsWrongIsSaidToBe(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"kind": "k", "source": "s", "source_key": "x", "actor": "a", "occurred_at": now, "tier": "raw"}
	}
	for name, change := range map[string]func(m map[string]any){
		"kind":     func(m map[string]any) { delete(m, "kind") },
		"source":   func(m map[string]any) { delete(m, "source") },
		"actor":    func(m map[string]any) { delete(m, "actor") },
		"occurred": func(m map[string]any) { delete(m, "occurred_at") },
		"tier":     func(m map[string]any) { m["tier"] = "forever" },
		"long":     func(m map[string]any) { m["kind"] = strings.Repeat("k", 200) },
	} {
		t.Run(name, func(t *testing.T) {
			m := base()
			change(m)
			raw, _ := json.Marshal(m)
			if _, err := Parse(raw); err == nil {
				t.Fatal("거절해야 합니다")
			}
		})
	}
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("JSON 이 아닙니다")
	}
}

// A kind nobody registered is let in: the door is wide, the way out is not.
func TestAnUnknownKindIsLetIn(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"kind": "처음.보는.종류", "source": "s", "source_key": "x", "actor": "a", "occurred_at": now, "tier": "raw"})
	if _, err := Parse(raw); err != nil {
		t.Fatal(err)
	}
}

func TestWhatIsForTheGuestIsTheGuests(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"kind": "location.guest.enter", "source": "context", "source_key": "x", "actor": Guest, "occurred_at": now, "tier": "raw"})
	e, err := Parse(raw)
	if err != nil || !e.IsGuest() {
		t.Fatalf("%+v %v", e, err)
	}
}

// What is not a record is not read as anything else.
func TestTheEventShapeIsNotARecord(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"source": "spending", "type": "a.b", "key": "k", "data": map[string]any{"id": "x"}})
	if _, err := Parse(raw); err == nil {
		t.Fatal("거절해야 합니다")
	}
}

// The guest's token says what it says: to the guest, and only as a notice.
func TestTheGuestCanOnlyNotifyTheGuest(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"kind": "approval.pending", "source": "agent", "source_key": "k", "actor": "agent", "occurred_at": now, "tier": "raw",
		"payload": map[string]any{"notify": map[string]any{"tier": "now", "title": "카드"}}})
	e, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if e.IsGuest() {
		t.Fatal("아직은 손님 것이 아닙니다")
	}
	g, err := ForGuest(e)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Notify Notify `json:"notify"`
	}
	_ = json.Unmarshal(g.Payload, &p)
	if !g.IsGuest() || p.Notify.Audience != Guest {
		t.Fatalf("%+v %s", g, g.Payload)
	}
	raw, _ = json.Marshal(map[string]any{"kind": "mind.card", "source": "agent", "source_key": "k2", "actor": "agent", "occurred_at": now, "tier": "raw",
		"payload": map[string]any{"a": 1}})
	e, err = Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ForGuest(e); err == nil {
		t.Fatal("알림이 아닌 것은 안 됩니다")
	}
}

// The guest's own reminders and lists come this way too, whoever they claim
// to be from, and they are the guest's.
func TestTheGuestAsksForTheirOwn(t *testing.T) {
	for _, kind := range []string{"reminder.set", "checklist.make", "intent.answer", "policy.denied"} {
		raw, _ := json.Marshal(map[string]any{"kind": kind, "source": "agent", "source_key": "k", "actor": "operator", "occurred_at": now, "tier": "raw"})
		e, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		g, err := ForGuest(e)
		if err != nil || !g.IsGuest() {
			t.Fatalf("%s: %+v %v", kind, g, err)
		}
	}
	for _, kind := range []string{"note.decided", "link.decided", "timer.fired", "payment.requested"} {
		raw, _ := json.Marshal(map[string]any{"kind": kind, "source": "agent", "source_key": "k", "actor": "operator", "occurred_at": now, "tier": "raw"})
		e, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ForGuest(e); err == nil {
			t.Fatalf("%s 는 손님이 낼 수 없습니다", kind)
		}
	}
}
