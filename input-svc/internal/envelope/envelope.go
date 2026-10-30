// Package envelope reads what producers left in the outbox: a Record, or the
// event shape the services have sent since before there were records.
//
// It checks the envelope and nothing inside the payload: what a kind holds is
// that kind's business, and a kind nobody registered is let in. The event
// shape is upcast here so that nothing past this point knows it existed; it
// goes when the last producer sends Records of its own.
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

// Notify is the wording a sender attached to an event. It stays in the
// payload under this name.
type Notify struct {
	Tier     string `json:"tier"`
	Level    string `json:"level,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	URL      string `json:"url,omitempty"`
	Audience string `json:"audience,omitempty"`
}

// event is the old shape as sent: the event's own fields, or the notification
// shape (kind, tier, title...) of the services that came first.
type event struct {
	Source     string          `json:"source"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject"`
	Data       json.RawMessage `json:"data"`
	Notify     *Notify         `json:"notify"`
	Key        string          `json:"key"`
	OccurredAt *time.Time      `json:"occurredAt"`

	Kind     string `json:"kind"`
	Tier     string `json:"tier"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	Audience string `json:"audience"`
}

// Parse reads one outbox row, or says why it will never be a record. id is
// the row's: it is the idempotency key of an event sent without one. now is
// when an event that did not say when it happened is taken to have.
func Parse(raw []byte, id int64, now time.Time) (Envelope, error) {
	var probe struct {
		SourceKey string `json:"source_key"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Envelope{}, errors.New("JSON 을 읽지 못했습니다")
	}
	if probe.SourceKey == "" {
		return upcast(raw, id, now)
	}
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

// upcast turns the event shape into a record. Its data stays what it was,
// with the wording beside it; what is for the guest goes to the guest.
func upcast(raw []byte, id int64, now time.Time) (Envelope, error) {
	var in event
	if err := json.Unmarshal(raw, &in); err != nil {
		return Envelope{}, errors.New("이벤트를 읽지 못했습니다")
	}
	if in.Type == "" && in.Kind != "" {
		in.Type = in.Kind
		if in.Notify == nil && in.Title != "" {
			in.Notify = &Notify{Tier: in.Tier, Level: in.Level, Title: in.Title, Body: in.Body, URL: in.URL, Audience: in.Audience}
		}
	}
	switch {
	case in.Source == "" || in.Type == "":
		return Envelope{}, errors.New("source 와 type 이 필요합니다")
	case len(in.Data) > 0 && !json.Valid(in.Data):
		return Envelope{}, errors.New("data 는 JSON 이어야 합니다")
	case in.Notify != nil && (in.Notify.Title == "" || !validTier(in.Notify.Tier)):
		return Envelope{}, errors.New("notify 에는 title 과 tier(now, digest, log)가 필요합니다")
	}
	payload, err := merge(in.Data, in.Notify)
	if err != nil {
		return Envelope{}, err
	}
	e := Envelope{
		Kind: in.Type, Source: in.Source, SourceKey: in.Key, Actor: in.Source, Subject: in.Subject,
		OccurredAt: now, Tier: rawTier, SchemaVersion: 1, Payload: payload,
	}
	if e.SourceKey == "" {
		e.SourceKey = fmt.Sprintf("outbox:%d", id)
	}
	if in.OccurredAt != nil && !in.OccurredAt.IsZero() {
		e.OccurredAt = *in.OccurredAt
	}
	if in.Notify != nil && in.Notify.Audience == Guest {
		e.Actor = Guest
	}
	return e, e.Check()
}

func validTier(t string) bool { return t == "now" || t == "digest" || t == "log" }

// merge is data with the wording beside it. Data that is not an object, which
// no service sends, is kept under "value".
func merge(data json.RawMessage, notify *Notify) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
			fields = map[string]json.RawMessage{"value": data}
		}
	}
	if notify != nil {
		b, err := json.Marshal(notify)
		if err != nil {
			return nil, err
		}
		fields["notify"] = b
	}
	return json.Marshal(fields)
}

// guestCommands are the kinds the guest's agent writes: the guest's own
// reminders and lists, which mind keeps apart by whose record it is, and how
// the guest's cards were answered and what the guest's tools were refused.
var guestCommands = []string{"reminder.", "checklist.", "intent.", "policy."}

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
