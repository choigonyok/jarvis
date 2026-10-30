package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
	"github.com/choigonyok/jarvis/input-svc/internal/workout/book"
)

// The training log. A save is a record of what changed, and the sessions are
// this service's read model of those records: the change and its record land
// in one transaction, so the screen reads what it just saved.

// kindWorkout is what changed in the log.
const kindWorkout = "workout.changes"

func (s *Server) readWorkout(w http.ResponseWriter, r *http.Request) {
	b, err := s.views.Workout.Load(r.Context())
	if err != nil {
		s.log.Error("기록을 불러오지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 불러오지 못했습니다."})
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(b.Revision, 10)))
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) changeWorkout(w http.ResponseWriter, r *http.Request) {
	var c book.Changes
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&c); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
		return
	}
	guest := asGuest(r)
	rev, err := s.views.Workout.Apply(r.Context(), c, func(ctx context.Context, tx pgx.Tx, rev int64, c book.Changes) error {
		e, err := workoutRecord(c, rev, guest, s.now())
		if err != nil {
			return err
		}
		return s.store.PutIn(ctx, tx, e)
	})
	if err != nil {
		s.log.Error("기록을 저장하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 저장하지 못했습니다."})
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(rev, 10)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": rev})
}

// workoutRecord is a save as a record, named for the revision it made. What
// the guest saved is the guest's, on the same book.
func workoutRecord(c book.Changes, rev int64, guest bool, now time.Time) (envelope.Envelope, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return envelope.Envelope{}, err
	}
	actor := "operator"
	if guest {
		actor = envelope.Guest
	}
	return envelope.Envelope{
		Kind: kindWorkout, Source: "workout", SourceKey: fmt.Sprintf("workout:%d", rev), Actor: actor, Channel: "web",
		Subject: "workout", OccurredAt: now, Tier: "durable", SchemaVersion: 1, Payload: payload,
	}, nil
}
