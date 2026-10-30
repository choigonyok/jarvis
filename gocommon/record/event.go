package record

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Notify is wording for a person a record may carry: how a service says
// something is worth telling. It stays in the payload under this name, and it
// is mind's to pass on.
type Notify struct {
	Tier     string `json:"tier"`
	Level    string `json:"level,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	URL      string `json:"url,omitempty"`
	Audience string `json:"audience,omitempty"`
}

// event is what a service reports, in either of the shapes they speak: the
// event's own fields, or the notification shape (kind, tier, title...).
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

// GuestActor is the actor of what is the guest's: input-svc puts it in the
// guest's tables.
const GuestActor = "guest"

// FromEvent is a report as a record's envelope, ready for the outbox. Its data
// stays what it was, with the wording beside it, and what is for the guest is
// the guest's. A report without a key gets one of its own: it is not the same
// report as any other.
func FromEvent(raw []byte, now time.Time) ([]byte, error) {
	var in event
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errors.New("이벤트를 읽지 못했습니다")
	}
	if in.Type == "" && in.Kind != "" {
		in.Type = in.Kind
		if in.Notify == nil && in.Title != "" {
			in.Notify = &Notify{Tier: in.Tier, Level: in.Level, Title: in.Title, Body: in.Body, URL: in.URL, Audience: in.Audience}
		}
	}
	switch {
	case in.Source == "" || in.Type == "":
		return nil, errors.New("source 와 type 이 필요합니다")
	case len(in.Data) > 0 && !json.Valid(in.Data):
		return nil, errors.New("data 는 JSON 이어야 합니다")
	case in.Notify != nil && (in.Notify.Title == "" || !validTier(in.Notify.Tier)):
		return nil, errors.New("notify 에는 title 과 tier(now, digest, log)가 필요합니다")
	}
	payload, err := merge(in.Data, in.Notify)
	if err != nil {
		return nil, err
	}
	e := envelope{
		Kind: in.Type, Source: in.Source, SourceKey: in.Key, Actor: in.Source, Subject: in.Subject,
		OccurredAt: now, Tier: Raw, SchemaVersion: 1, Payload: payload,
	}
	if e.SourceKey == "" {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		e.SourceKey = "ev-" + hex.EncodeToString(b)
	}
	if in.OccurredAt != nil && !in.OccurredAt.IsZero() {
		e.OccurredAt = *in.OccurredAt
	}
	if in.Notify != nil && in.Notify.Audience == GuestActor {
		e.Actor = GuestActor
	}
	return json.Marshal(e)
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
