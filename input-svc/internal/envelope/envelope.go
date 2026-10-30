// Package envelope reads what producers left in the outbox: a Record.
//
// It checks the envelope and nothing inside the payload: what a kind holds is
// that kind's business, and a kind nobody registered is let in.
package envelope

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Envelope is a record ready to store.
type Envelope struct {
	Kind          string          `json:"kind"`
	Source        string          `json:"source"`
	SourceKey     string          `json:"source_key"`
	Actor         string          `json:"actor"`
	Channel       string          `json:"channel"`
	Subject       string          `json:"subject"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Tier          string          `json:"tier"`
	SchemaVersion int             `json:"schema_version"`
	TraceID       string          `json:"trace_id"`
	CausationID   string          `json:"causation_id"`
	Payload       json.RawMessage `json:"payload"`
}

// Guest is the actor of what is the guest's: it goes to the guest's tables.
const Guest = "guest"

// IsGuest is whether the record belongs in the guest's tables.
func (e Envelope) IsGuest() bool { return e.Actor == Guest }

const (
	rawTier     = "raw"
	durableTier = "durable"
	maxShort    = 80
	maxName     = 200
)

// Check says what is wrong with the envelope, if anything.
func (e Envelope) Check() error {
	var missing []string
	for _, f := range []struct{ name, value string }{
		{"kind", e.Kind}, {"source", e.Source}, {"source_key", e.SourceKey}, {"actor", e.Actor},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if e.OccurredAt.IsZero() {
		missing = append(missing, "occurred_at")
	}
	switch {
	case len(missing) > 0:
		return fmt.Errorf("기록에 %s 가 없습니다", strings.Join(missing, ", "))
	case e.Tier != rawTier && e.Tier != durableTier:
		return fmt.Errorf("기록의 층이 %q 입니다", e.Tier)
	case len(e.Kind) > maxShort || len(e.Source) > maxShort:
		return errors.New("kind, source 가 너무 깁니다")
	case len(e.SourceKey) > maxName || len(e.Subject) > maxName:
		return errors.New("source_key, subject 가 너무 깁니다")
	case len(e.Payload) > 0 && !json.Valid(e.Payload):
		return errors.New("payload 는 JSON 이어야 합니다")
	}
	return nil
}

// Notify is the wording a sender attached to a record. It stays in the
// payload under this name.
type Notify struct {
	Tier     string `json:"tier"`
	Level    string `json:"level,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	URL      string `json:"url,omitempty"`
	Audience string `json:"audience,omitempty"`
}

// Parse reads one outbox row, or says why it will never be a record.
func Parse(raw []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return Envelope{}, fmt.Errorf("기록의 봉투를 읽지 못했습니다: %w", err)
	}
	if e.SchemaVersion == 0 {
		e.SchemaVersion = 1
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}
	return e, e.Check()
}

// guestCommands are the kinds the guest writes: the guest's own reminders, lists
// and jobs, which mind keeps apart by whose record it is, what they changed on
// the shared calendar, what they said, and how their cards were answered and
// what their tools were refused.
var guestCommands = []string{"reminder.", "checklist.", "intent.", "policy.", "jobs.", "manual.", "chat.message"}

// ForGuest is what a record from the guest's token may be: the guest's own
// reminders and lists, or a notification to the guest. Whatever it said about
// itself, it is the guest's.
func ForGuest(e Envelope) (Envelope, error) {
	for _, prefix := range guestCommands {
		if strings.HasPrefix(e.Kind, prefix) {
			e.Actor = Guest
			return e, nil
		}
	}
	var p struct {
		Notify *Notify `json:"notify"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil || p.Notify == nil {
		return e, errors.New("손님은 알림만 보낼 수 있습니다.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &fields); err != nil {
		return e, errors.New("손님은 알림만 보낼 수 있습니다.")
	}
	p.Notify.Audience = Guest
	b, err := json.Marshal(p.Notify)
	if err != nil {
		return e, err
	}
	fields["notify"] = b
	e.Payload, err = json.Marshal(fields)
	if err != nil {
		return e, err
	}
	e.Actor = Guest
	return e, nil
}
