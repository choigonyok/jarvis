package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/input-svc/internal/chat/store"
	"github.com/choigonyok/jarvis/input-svc/internal/chat/turn"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
)

// A person marks one of the assistant's answers: good, or wrong - and why, if
// they say. Like everything a person does, it is a record first (chat.feedback,
// the durable tier: what the conversation's evaluation is made from); the turn
// keeps the latest mark so the screen shows it, and the conversation hears of
// it before its next turn. What they wrote is sealed like their words.

// maxNote is how long a reason may be, in characters.
const maxNote = 500

func (s *Server) postFeedback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mark string `json:"mark"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	switch {
	case in.Mark != turn.Good && in.Mark != turn.Bad && in.Mark != "":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mark 는 good, bad, 또는 빈 값(지우기)이어야 합니다."})
		return
	case utf8.RuneCountInString(in.Note) > maxNote:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "이유는 500자까지입니다."})
		return
	case in.Mark == "" && in.Note != "":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "표시를 지울 때는 이유를 받지 않습니다."})
		return
	}
	thread, guest := "", asGuest(r)
	if guest {
		thread = guestThread
	}
	now := s.now()
	var fb *turn.Feedback
	if in.Mark != "" {
		fb = &turn.Feedback{Mark: in.Mark, Note: in.Note, At: now.Format(time.RFC3339Nano)}
	}
	id := r.PathValue("id")
	saved, err := s.views.Chat.Mark(r.Context(), thread, id, fb, s.views.TZ, func(ctx context.Context, tx pgx.Tx, t turn.Turn) error {
		e, err := s.feedbackRecord(t, in.Mark, in.Note, now, guest)
		if err != nil {
			return err
		}
		return s.store.PutIn(ctx, tx, e)
	})
	if errors.Is(err, store.ErrNoTurn) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "표시할 수 있는 답이 아닙니다."})
		return
	}
	if err != nil {
		s.log.Error("답에 표시하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "표시하지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// feedbackRecord is a mark as a record: which answer and what mark in the open,
// the reason sealed.
func (s *Server) feedbackRecord(t turn.Turn, mark, note string, at time.Time, guest bool) (envelope.Envelope, error) {
	body := map[string]string{"turn": t.ID, "thread": t.Thread, "mark": mark}
	if note != "" {
		sealed, err := s.views.Seal.Seal([]byte(note))
		if err != nil {
			return envelope.Envelope{}, err
		}
		body["sealed"] = base64.StdEncoding.EncodeToString(sealed)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return envelope.Envelope{}, err
	}
	actor := "operator"
	if guest {
		actor = envelope.Guest
	}
	return envelope.Envelope{
		Kind: record.KindFeedback, Source: "web", SourceKey: "feedback:" + t.ID + ":" + at.Format(time.RFC3339Nano),
		Actor: actor, Channel: "console", Subject: "chat:" + t.Thread, OccurredAt: at, Tier: "durable",
		SchemaVersion: 1, Payload: payload,
	}, nil
}
