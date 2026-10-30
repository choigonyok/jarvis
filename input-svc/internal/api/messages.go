package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/input-svc/internal/envelope"
)

// What a person says in the conversation comes in here first, like everything
// else: it is a record before anything reads it, so an agent that is restarting
// finds it waiting. The words go in sealed, and the agent asks for them by the
// record's key rather than holding the key to everything.

const maxImages = 12

// said is what a person sent. Images and Ref name things the agent keeps.
type said struct {
	Text   string   `json:"text"`
	Images []string `json:"images,omitempty"`
	Ref    *struct {
		ID string `json:"id"`
	} `json:"ref,omitempty"`
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	var in said
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	switch {
	case in.Text == "" && len(in.Images) == 0:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "메시지가 비어 있습니다."})
		return
	case len(in.Images) > maxImages:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "사진은 한 번에 12장까지입니다."})
		return
	}
	sealed, err := s.views.Seal.Seal([]byte(in.Text))
	if err != nil {
		s.log.Error("메시지를 봉인하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 받지 못했습니다."})
		return
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 받지 못했습니다."})
		return
	}
	payload, err := json.Marshal(map[string]any{"sealed": base64.StdEncoding.EncodeToString(sealed), "images": in.Images, "ref": in.Ref})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 받지 못했습니다."})
		return
	}
	e := envelope.Envelope{Kind: record.KindMessage, Source: "web", SourceKey: "msg-" + hex.EncodeToString(raw), Actor: "operator", Channel: "console",
		Subject: "chat:", OccurredAt: s.now(), Tier: "raw", SchemaVersion: 1, Payload: payload}
	if asGuest(r) {
		e.Subject = "chat:" + guestThread
		if e, err = envelope.ForGuest(e); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
	}
	if _, _, err := s.store.Put(r.Context(), e); err != nil {
		s.log.Error("메시지를 기록하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 받지 못했습니다."})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"key": e.SourceKey})
}

// getMessage is what the agent asks for a record it took from the queue: only
// the one who said it, by its own token.
func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	var stored struct {
		said
		Sealed string `json:"sealed"`
	}
	plain, ok := s.opened(w, r, record.KindMessage, &stored, &stored.Sealed)
	if !ok {
		return
	}
	stored.said.Text = string(plain)
	writeJSON(w, http.StatusOK, stored.said)
}

// opened reads a sealed record of a person's into into, and opens sealed.
func (s *Server) opened(w http.ResponseWriter, r *http.Request, kind string, into any, sealed *string) ([]byte, bool) {
	raw, err := s.store.Sealed(r.Context(), asGuest(r), kind, r.PathValue("key"))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 기록이 없습니다."})
		return nil, false
	}
	if err == nil {
		err = json.Unmarshal(raw, into)
	}
	var cipher, plain []byte
	if err == nil {
		cipher, err = base64.StdEncoding.DecodeString(*sealed)
	}
	if err == nil {
		plain, err = s.views.Seal.Open(cipher)
	}
	if err != nil {
		s.log.Error("기록을 열지 못했습니다", "err", err, "kind", kind)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "기록을 읽지 못했습니다."})
		return nil, false
	}
	return plain, true
}

// A person's answer to a card comes in the same way: a record, read directly by
// whoever waits on the card - a stopped payment goes on whatever mind is doing,
// and mind only closes its ledger. The answer can hold what they typed on a
// form, so it is sealed too. A card with a secret field does not come here: a
// secret is a value, not a record.

// answerWait is how long a person's press waits to hear how it went. Past it
// the answer stays in the queue and the card moves when it is taken.
const answerWait = 10 * time.Second

// reply is how an answer went.
type reply struct {
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (s *Server) postAnswer(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
		return
	}
	sealed, err := s.views.Seal.Seal(body)
	if err != nil {
		s.log.Error("카드의 답을 봉인하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "답을 받지 못했습니다."})
		return
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "답을 받지 못했습니다."})
		return
	}
	id, key := r.PathValue("id"), "ans-"+hex.EncodeToString(raw)
	payload, err := json.Marshal(map[string]string{"card": id, "sealed": base64.StdEncoding.EncodeToString(sealed)})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "답을 받지 못했습니다."})
		return
	}
	e := envelope.Envelope{Kind: record.KindAnswer, Source: "web", SourceKey: key, Actor: "operator", Channel: "console",
		Subject: "card:" + id, OccurredAt: s.now(), Tier: "raw", SchemaVersion: 1, Payload: payload}
	if asGuest(r) {
		if e, err = envelope.ForGuest(e); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
	}
	heard := s.expect(key)
	defer s.forget(key)
	if _, _, err := s.store.Put(r.Context(), e); err != nil {
		s.log.Error("카드의 답을 기록하지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "답을 받지 못했습니다."})
		return
	}
	select {
	case got := <-heard:
		if got.Error == "" {
			w.WriteHeader(got.Status)
			return
		}
		writeJSON(w, got.Status, map[string]string{"error": got.Error})
	case <-time.After(answerWait):
		writeJSON(w, http.StatusAccepted, map[string]string{"key": key})
	case <-r.Context().Done():
	}
}

// getAnswer is the words of an answer, for the agent that took it.
func (s *Server) getAnswer(w http.ResponseWriter, r *http.Request) {
	var stored struct {
		Sealed string `json:"sealed"`
	}
	plain, ok := s.opened(w, r, record.KindAnswer, &stored, &stored.Sealed)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(plain)
}

// postReply is the agent saying how an answer went. Nobody may be waiting any
// more, and that is fine.
func (s *Server) postReply(w http.ResponseWriter, r *http.Request) {
	var in reply
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
		return
	}
	s.answers.Lock()
	heard := s.answers.waiting[r.PathValue("key")]
	s.answers.Unlock()
	if heard != nil {
		select {
		case heard <- in:
		default:
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) expect(key string) chan reply {
	heard := make(chan reply, 1)
	s.answers.Lock()
	defer s.answers.Unlock()
	if s.answers.waiting == nil {
		s.answers.waiting = map[string]chan reply{}
	}
	s.answers.waiting[key] = heard
	return heard
}

func (s *Server) forget(key string) {
	s.answers.Lock()
	defer s.answers.Unlock()
	delete(s.answers.waiting, key)
}
