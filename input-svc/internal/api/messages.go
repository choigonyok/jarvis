package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
	raw, err := s.store.Message(r.Context(), asGuest(r), r.PathValue("key"))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "그런 메시지가 없습니다."})
		return
	case err != nil:
		s.log.Error("메시지를 읽지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 읽지 못했습니다."})
		return
	}
	var stored struct {
		said
		Sealed string `json:"sealed"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 읽지 못했습니다."})
		return
	}
	cipher, err := base64.StdEncoding.DecodeString(stored.Sealed)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 읽지 못했습니다."})
		return
	}
	plain, err := s.views.Seal.Open(cipher)
	if err != nil {
		s.log.Error("메시지를 열지 못했습니다", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "메시지를 읽지 못했습니다."})
		return
	}
	stored.said.Text = string(plain)
	writeJSON(w, http.StatusOK, stored.said)
}
