package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/input-svc/internal/chat/store"
	"github.com/choigonyok/jarvis/input-svc/internal/chat/turn"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
)

// The conversation. What was said is a record like everything else; the turns
// are this service's read model of them, kept so the console can page through
// a thread. A turn and its record land in one transaction, and the turn's
// words go in the record sealed.

// kindTurn is a turn said or changed.
const kindTurn = "chat.turn"

// guestThread is the one thread the guest's token opens.
const guestThread = "guest"

// threadName is "" (the operator) or a short lowercase name. Anything else is
// refused rather than stored: a typo would quietly start a second, empty
// conversation.
var threadName = regexp.MustCompile(`^[a-z0-9-]{0,32}$`)

// turnID is the shape of a stored turn's id ("a-<epoch ms>-<seq>", "u-…").
var turnID = regexp.MustCompile(`^[a-z]+-[0-9]+-[0-9]+$`)

func (s *Server) listTurns(w http.ResponseWriter, r *http.Request) {
	thread := r.URL.Query().Get("thread")
	if !threadName.MatchString(thread) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "thread 이름이 올바르지 않습니다."})
		return
	}
	if asGuest(r) && thread != guestThread {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "손님은 손님 대화만 읽을 수 있습니다."})
		return
	}
	// limit= (with before=) is the console's page: the newest turns, and
	// the ones above them as it scrolls up. Answered with hasMore.
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit <= 0 || limit > 500 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit 는 1~500 이어야 합니다."})
			return
		}
		// before= is a turn id (the oldest on the page below) or an RFC3339 time.
		var before time.Time
		var beforeID string
		if b := r.URL.Query().Get("before"); b != "" {
			if turnID.MatchString(b) {
				beforeID = b
			} else if t, err := time.Parse(time.RFC3339Nano, b); err == nil {
				before = t
			} else {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before 는 턴 id 나 RFC3339 시각이어야 합니다."})
				return
			}
		}
		turns, more, err := s.views.Chat.Window(r.Context(), s.views.TZ, thread, before, beforeID, limit)
		if err != nil {
			s.log.Error("스레드를 불러오지 못했습니다", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "스레드를 불러오지 못했습니다."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"turns": turns, "hasMore": more})
		return
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "since 는 RFC3339 시각이어야 합니다."})
			return
		}
		since = t
	}
	turns, err := s.views.Chat.All(r.Context(), s.views.TZ, thread, since)
	if err != nil {
		s.log.Error("스레드를 불러오지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "스레드를 불러오지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
}

func (s *Server) appendTurn(w http.ResponseWriter, r *http.Request) {
	var in turn.Turn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
		return
	}
	// 빈 턴은 화면에 빈 말풍선으로 나타난다. 에이전트도 걸러내지만, 여기서
	// 막아야 다른 호출자가 같은 실수를 반복하지 않는다.
	if !threadName.MatchString(in.Thread) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "thread 이름이 올바르지 않습니다."})
		return
	}
	if asGuest(r) && in.Thread != guestThread {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "손님은 손님 대화에만 쓸 수 있습니다."})
		return
	}
	if empty(in) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "내용이 없는 턴입니다."})
		return
	}
	saved, err := s.views.Chat.Append(r.Context(), in, s.views.TZ, s.keepTurn(asGuest(r)))
	if err != nil {
		s.log.Error("턴을 저장하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "턴을 저장하지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// empty is a turn with nothing to show: no words, no photos, no card. A
// checklist card is its list (kept by mind, named in the origin) - with or
// without a line above it. Refusing it as empty let it reach the screen
// unsaved and vanish on the next read (2026-10-21, "다른 탭 갔다 오면 사라져").
func empty(t turn.Turn) bool {
	if t.Origin != nil && t.Origin.Checklist != "" {
		return false
	}
	return t.Text == "" && len(t.Paragraphs) == 0 && t.ProposalID == "" && len(t.Images) == 0
}

// updateTurn rewrites what a turn with an origin says and moves it to now.
func (s *Server) updateTurn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Thread     string   `json:"thread"`
		Paragraphs []string `json:"paragraphs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
		return
	}
	if !threadName.MatchString(in.Thread) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "thread 이름이 올바르지 않습니다."})
		return
	}
	if asGuest(r) && in.Thread != guestThread {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "손님은 손님 대화에만 쓸 수 있습니다."})
		return
	}
	if len(in.Paragraphs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "내용이 없는 턴입니다."})
		return
	}
	saved, err := s.views.Chat.Update(r.Context(), in.Thread, r.PathValue("id"), in.Paragraphs, s.views.TZ, s.keepTurn(asGuest(r)))
	if errors.Is(err, store.ErrNoTurn) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 턴이 없습니다."})
		return
	}
	if err != nil {
		s.log.Error("턴을 갱신하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "턴을 갱신하지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// keepTurn leaves the record of a turn in the transaction that stores it.
func (s *Server) keepTurn(guest bool) store.After {
	return func(ctx context.Context, tx pgx.Tx, t turn.Turn) error {
		e, err := s.turnRecord(t, guest)
		if err != nil {
			return err
		}
		return s.store.PutIn(ctx, tx, e)
	}
}

// turnRecord is a turn as a record: who said it and when in the open, the rest
// sealed. A turn changed later is a record of its own.
func (s *Server) turnRecord(t turn.Turn, guest bool) (envelope.Envelope, error) {
	at, err := time.Parse(time.RFC3339Nano, t.AtISO)
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("턴의 시각을 읽지 못했습니다: %w", err)
	}
	plain, err := json.Marshal(t)
	if err != nil {
		return envelope.Envelope{}, err
	}
	sealed, err := s.views.Seal.Seal(plain)
	if err != nil {
		return envelope.Envelope{}, err
	}
	payload, err := json.Marshal(map[string]string{"thread": t.Thread, "sealed": base64.StdEncoding.EncodeToString(sealed)})
	if err != nil {
		return envelope.Envelope{}, err
	}
	actor := "operator"
	switch {
	case guest:
		actor = envelope.Guest
	case t.Role == turn.RoleAgent:
		actor = "agent"
	}
	return envelope.Envelope{
		Kind: kindTurn, Source: "chat", SourceKey: "turn:" + t.ID + ":" + t.AtISO, Actor: actor, Channel: "chat",
		Subject: "chat:" + t.Thread, OccurredAt: at, Tier: "durable", SchemaVersion: 1, Payload: payload,
	}, nil
}
