// Package observe is what every sensor does with what it saw, and no more:
// the content goes to the store, and the log gets that it was seen. The
// sensors share it with mind's own ways in - the console's simulator, a
// replay - so a message typed in or read again is taken exactly as one a
// collector handed on.
package observe

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Message stores a message's text and logs that it was seen.
//
// The observation's key is not the message's ref: a collector already left
// the message itself in the stream under that key (message.received,
// gocommon/collect), and the way in keeps one record per (source, key). Under
// the same key this observation would be dropped as a repeat, no room would
// come due, and nothing said in KakaoTalk would be read.
func Message(ctx context.Context, st store.Store, log obs.Log, msg model.Message) error {
	if err := st.PutMessage(ctx, msg); err != nil {
		return err
	}
	data, _ := json.Marshal(obs.MessageRef{Ref: msg.Ref, Room: msg.Room})
	return log.Append(ctx, obs.Observation{
		Source: msg.Source, Type: model.KindMessageObserved, Subject: msg.Room,
		Data: data, Key: MessageKey(msg.Ref), OccurredAt: msg.SentAt,
	})
}

// MessageKey is the record key of the observation that a message was seen:
// its ref, set apart from the record the collector left under the ref itself.
func MessageKey(ref string) string { return "observed:" + ref }

// Occurrence records an occurrence read from a structured source and logs
// that it was seen. Seeing the same one again (a payment's items filled in
// later) updates it.
func Occurrence(ctx context.Context, st store.Store, log obs.Log, o model.Occurrence) error {
	if err := st.PutOccurrence(ctx, o); err != nil {
		return err
	}
	data, _ := json.Marshal(obs.OccurrenceRef{ID: o.ID, Kind: o.Kind})
	return log.Append(ctx, obs.Observation{
		Source: strings.SplitN(o.ID, ":", 2)[0], Type: model.KindOccurrenceObserved, Subject: o.ID,
		Data: data, Key: "occurrence:" + o.ID + ":" + o.At.UTC().Format(time.RFC3339), OccurredAt: o.At,
	})
}
